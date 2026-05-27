package sampler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Sample is what the sampler emits on each tick.
type Sample struct {
	Timestamp      time.Time
	AppClass       string
	Title          string
	PID            int
	Workspace      int
	Monitor        int
	ContentType    string
	InhibitingIdle bool
	Fullscreen     int // 0 = none, 1 = maximize, 2 = fullscreen (Hyprland convention)
}

// hyprWindow mirrors only the fields we want from `hyprctl activewindow -j`.
type hyprWindow struct {
	Class          string        `json:"class"`
	Title          string        `json:"title"`
	PID            int           `json:"pid"`
	Monitor        int           `json:"monitor"`
	ContentType    string        `json:"contentType"`
	InhibitingIdle bool          `json:"inhibitingIdle"`
	Fullscreen     int           `json:"fullscreen"`
	Workspace      hyprWorkspace `json:"workspace"`
}

type hyprWorkspace struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Sampler polls Hyprland on a fixed interval and emits Samples on out.
type Sampler struct {
	interval time.Duration
	out      chan<- Sample
}

func New(interval time.Duration, out chan<- Sample) *Sampler {
	return &Sampler{
		interval: interval,
		out:      out,
	}
}

// Run blocks until ctx is cancelled. Errors from individual ticks are logged
// and ignored — a transient hyprctl failure shouldn't kill the daemon.
func (s *Sampler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			sample, err := queryActiveWindow()
			if err != nil {
				log.Printf("sampler: query failed: %v", err)
				continue
			}
			if sample == nil {
				continue // empty workspace, nothing focused
			}
			// Non-blocking-ish send: if the consumer is slow, we'd rather
			// drop a sample than block the ticker. For v1 we just send;
			// revisit if you ever see the channel back up.
			s.out <- *sample
		}
	}
}

// queryActiveWindow shells out to hyprctl and returns the focused window.
func queryActiveWindow() (*Sample, error) {
	cmd := exec.Command("hyprctl", "activewindow", "-j")
	cmd.Env = hyprctlEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("exec hyprctl: %w", err)
	}

	// Empty workspace: hyprctl emits "{}" (or close to it).
	if len(out) <= 2 {
		return nil, nil
	}

	var w hyprWindow
	if err := json.Unmarshal(out, &w); err != nil {
		return nil, fmt.Errorf("parse hyprctl output: %w", err)
	}

	return &Sample{
		Timestamp:      time.Now(),
		AppClass:       w.Class,
		Title:          CleanTitle(w.Title),
		PID:            w.PID,
		Workspace:      w.Workspace.ID,
		Monitor:        w.Monitor,
		ContentType:    w.ContentType,
		InhibitingIdle: w.InhibitingIdle,
		Fullscreen:     w.Fullscreen,
	}, nil
}

// hyprctlEnv returns the process environment for hyprctl, ensuring
// HYPRLAND_INSTANCE_SIGNATURE points at a live Hyprland instance.
//
// When the daemon is launched as a systemd user service it may inherit no
// signature at all, or a stale one from a previous session — Hyprland mints a
// fresh signature on every restart. hyprctl can't locate its IPC socket
// without the correct value, so we resolve it from the runtime dir each call
// and only fall back to the inherited env when discovery turns up nothing.
func hyprctlEnv() []string {
	env := os.Environ()
	if sig := discoverSignature(); sig != "" {
		return append(env, "HYPRLAND_INSTANCE_SIGNATURE="+sig)
	}
	return env
}

// discoverSignature finds the signature of the currently running Hyprland
// instance by scanning $XDG_RUNTIME_DIR/hypr for an instance directory with a
// live control socket, preferring the most recently created one if several
// exist. Returns "" if none is found.
func discoverSignature() string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return ""
	}
	hyprDir := filepath.Join(runtimeDir, "hypr")
	entries, err := os.ReadDir(hyprDir)
	if err != nil {
		return ""
	}

	var newest string
	var newestMod time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// A live instance exposes a control socket; dead ones don't.
		if _, err := os.Stat(filepath.Join(hyprDir, e.Name(), ".socket.sock")); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestMod) {
			newestMod = info.ModTime()
			newest = e.Name()
		}
	}
	return newest
}
