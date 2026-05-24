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

func (s *Store) DB() *sql.DB {
	return s.db
}

// migrate applies the schema. Idempotent — safe to run on every startup.
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

	CREATE INDEX IF NOT EXISTS idx_samples_ts     ON samples(ts);
	CREATE INDEX IF NOT EXISTS idx_samples_app_ts ON samples(app_class, ts);

	CREATE TABLE IF NOT EXISTS categories (
		app_class TEXT PRIMARY KEY,
		category  TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS tracked_keywords (
		id         INTEGER PRIMARY KEY,
		app_class  TEXT    NOT NULL,
		keyword    TEXT    NOT NULL,
		label      TEXT,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
		UNIQUE(app_class, keyword)
	);

	CREATE INDEX IF NOT EXISTS idx_keywords_app ON tracked_keywords(app_class);

	CREATE TABLE IF NOT EXISTS daily_rollups (
		day       TEXT    NOT NULL,
		app_class TEXT    NOT NULL,
		seconds   INTEGER NOT NULL,
		PRIMARY KEY (day, app_class)
	);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	return nil
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

// Keyword represents a tracking rule: match titles containing keyword
// within a given app_class.
type Keyword struct {
	ID        int       `json:"id"`
	AppClass  string    `json:"app_class"`
	Keyword   string    `json:"keyword"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) AddKeyword(ctx context.Context, appClass, keyword, label string) error {
	const q = `
	INSERT INTO tracked_keywords (app_class, keyword, label)
	VALUES (?, ?, ?)
	ON CONFLICT(app_class, keyword) DO UPDATE SET label = excluded.label
	`
	_, err := s.db.ExecContext(ctx, q, appClass, keyword, nullableStr(label))
	return err
}

func (s *Store) DeleteKeyword(ctx context.Context, id int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tracked_keywords WHERE id = ?`, id)
	return err
}

func (s *Store) ListKeywords(ctx context.Context) ([]Keyword, error) {
	const q = `
	SELECT id, app_class, keyword, COALESCE(label, keyword), created_at
	FROM tracked_keywords
	ORDER BY app_class, keyword
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var kws []Keyword
	for rows.Next() {
		var kw Keyword
		var ts int64
		if err := rows.Scan(&kw.ID, &kw.AppClass, &kw.Keyword, &kw.Label, &ts); err != nil {
			return nil, err
		}
		kw.CreatedAt = time.Unix(ts, 0)
		kws = append(kws, kw)
	}
	return kws, rows.Err()
}

// nullableStr returns sql.NullString so that empty strings become SQL NULL.
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
