package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Poudel0/screentyme/internal/sampler"
)

// Store wraps the SQLite database. Safe for concurrent use — database/sql
// handles connection pooling, and we set max-open-conns to 1 below because
// SQLite serializes writes anyway.
type Store struct {
	db *sql.DB
}

// DefaultPath returns the XDG-compliant path for the database file.
// Honors $XDG_DATA_HOME, falls back to ~/.local/share/screentyme/screentyme.db.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	dir = filepath.Join(dir, "screentyme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return filepath.Join(dir, "screentyme.db"), nil
}

// Open opens (and creates if needed) the SQLite database at path,
// runs migrations, and returns a ready-to-use Store.
func Open(path string) (*Store, error) {
	// _journal=WAL: much better concurrency, survives crashes cleanly.
	// _busy_timeout=5000: wait up to 5s on lock contention instead of erroring.
	// _foreign_keys=on: SQLite ships with FKs *off* by default. Always enable.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite serializes writes through a single global lock.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// migrate applies the schema. Idempotent — safe to run on every startup.
// When you add a v2 schema change, do it here, with a check for the new
// column/table before adding it. We're not pulling in a migration library
// for two tables.
func (s *Store) migrate() error {
	const schema = `
	CREATE TABLE IF NOT EXISTS samples (
		id              INTEGER PRIMARY KEY,
		ts              INTEGER NOT NULL,
		app_class       TEXT    NOT NULL,
		title           TEXT,
		pid             INTEGER,
		workspace       INTEGER,
		monitor         INTEGER,
		content_type    TEXT,
		inhibiting_idle INTEGER NOT NULL DEFAULT 0,
		fullscreen      INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_samples_ts ON samples(ts);
	CREATE INDEX IF NOT EXISTS idx_samples_app_ts ON samples(app_class, ts);

	CREATE TABLE IF NOT EXISTS categories (
		app_class TEXT PRIMARY KEY,
		category  TEXT NOT NULL
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

// Insert writes one sample. Called once per tick.
func (s *Store) Insert(ctx context.Context, smp sampler.Sample) error {
	const q = `
	INSERT INTO samples
		(ts, app_class, title, pid, workspace, monitor, content_type, inhibiting_idle, fullscreen)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, q,
		smp.Timestamp.Unix(),
		smp.AppClass,
		nullableStr(smp.Title),
		smp.PID,
		smp.Workspace,
		smp.Monitor,
		nullableStr(smp.ContentType),
		boolToInt(smp.InhibitingIdle),
		smp.Fullscreen,
	)
	if err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}
	return nil
}

// nullableStr returns sql.NullString so that empty strings become SQL NULL.
// Helps your aggregation queries — `WHERE title IS NULL` reads better than
// `WHERE title = ”`, and it shrinks the file a bit.
func nullableStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var ErrNotFound = errors.New("not found")

// Health is a tiny sanity probe — call it from a /healthz endpoint later.
func (s *Store) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.db.PingContext(ctx)
}
