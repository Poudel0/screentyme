package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDB creates an in-memory SQLite DB with the minimal schema analytics needs.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE samples (
			id        INTEGER PRIMARY KEY,
			ts        INTEGER NOT NULL,
			app_class TEXT    NOT NULL,
			title     TEXT
		);
		CREATE TABLE tracked_keywords (
			id         INTEGER PRIMARY KEY,
			app_class  TEXT NOT NULL,
			keyword    TEXT NOT NULL,
			label      TEXT,
			created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
			UNIQUE(app_class, keyword)
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// insertSamples inserts a contiguous run of samples spaced 5 s apart,
// starting at base, for the given app_class and cleaned_title.
func insertSamples(t *testing.T, db *sql.DB, base time.Time, appClass, cleanedTitle string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		ts := base.Add(time.Duration(i) * 5 * time.Second).Unix()
		_, err := db.Exec(
			`INSERT INTO samples (ts, app_class, title) VALUES (?, ?, ?)`,
			ts, appClass, cleanedTitle,
		)
		if err != nil {
			t.Fatalf("insert sample: %v", err)
		}
	}
}

func TestTodayByApp(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	// Place samples within today's local midnight boundary.
	todayMidnight := time.Now().Local().Truncate(24 * time.Hour)
	base := todayMidnight.Add(2 * time.Hour)

	// 12 samples × 5 s apart → session spans (12-1)*5 + 5 = 60 s
	insertSamples(t, db, base, "chromium", "YouTube", 12)
	// 6 samples → 30 s
	insertSamples(t, db, base.Add(10*time.Minute), "code", "main.go - screentyme", 6)

	results, err := a.TodayByApp(context.Background())
	if err != nil {
		t.Fatalf("TodayByApp: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(results), results)
	}

	// First result should be chromium (longer).
	if results[0].AppClass != "chromium" {
		t.Errorf("first app = %q, want chromium", results[0].AppClass)
	}
	if results[0].TotalSeconds <= results[1].TotalSeconds {
		t.Errorf("chromium (%ds) should be longer than code (%ds)",
			results[0].TotalSeconds, results[1].TotalSeconds)
	}
}

func TestTodayByApp_ExcludesOtherDays(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	// 25 h ago is unambiguously yesterday regardless of local timezone offset.
	yesterday := time.Now().Add(-25 * time.Hour)
	insertSamples(t, db, yesterday, "chromium", "YouTube", 20)

	results, err := a.TodayByApp(context.Background())
	if err != nil {
		t.Fatalf("TodayByApp: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results for yesterday's samples, want 0", len(results))
	}
}

func TestRecentByKeyword_MatchedAndUnmatched(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	base := time.Now().Local().Truncate(24 * time.Hour).Add(2 * time.Hour)

	insertSamples(t, db, base, "chromium", "YouTube - some video", 12)
	insertSamples(t, db, base.Add(10*time.Minute), "chromium", "Home / X", 6)

	// Register one keyword — "youtube" matches "YouTube - some video".
	_, err := db.Exec(`INSERT INTO tracked_keywords (app_class, keyword, label) VALUES ('chromium','youtube','YouTube')`)
	if err != nil {
		t.Fatalf("insert keyword: %v", err)
	}

	results, err := a.RecentByKeyword(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecentByKeyword: %v", err)
	}

	var matched, unmatched int
	for _, r := range results {
		if r.AppClass != "chromium" {
			t.Errorf("unexpected app_class %q", r.AppClass)
		}
		if r.Keyword == "youtube" {
			matched++
		} else if r.Keyword == "" {
			unmatched++
			if r.Title == "" {
				t.Error("unmatched row missing title")
			}
		}
	}
	if matched != 1 {
		t.Errorf("got %d matched rows, want 1", matched)
	}
	if unmatched != 1 {
		t.Errorf("got %d unmatched rows, want 1", unmatched)
	}
}

func TestRecentByKeyword_DaysFilter(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	now := time.Now()
	// 10 days ago — should be excluded from a 7-day window.
	old := now.Add(-10 * 24 * time.Hour)
	insertSamples(t, db, old, "chromium", "YouTube", 20)

	results, err := a.RecentByKeyword(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecentByKeyword: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results for 10-day-old samples in 7-day window, want 0", len(results))
	}
}

func TestRecentByKeyword_NilSamplesReturnsEmpty(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	results, err := a.RecentByKeyword(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error on empty table: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results on empty table, want 0", len(results))
	}
}

func TestRecentByKeyword_LabelFallsBackToKeyword(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	base := time.Now().Local().Truncate(24*time.Hour).Add(2 * time.Hour)
	insertSamples(t, db, base, "chromium", "VCT highlights", 12)

	// Keyword with no label — label should default to keyword string.
	_, err := db.Exec(`INSERT INTO tracked_keywords (app_class, keyword) VALUES ('chromium','vct')`)
	if err != nil {
		t.Fatalf("insert keyword: %v", err)
	}

	results, err := a.RecentByKeyword(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecentByKeyword: %v", err)
	}

	found := false
	for _, r := range results {
		if r.Keyword == "vct" {
			found = true
			if r.Label != "vct" {
				t.Errorf("label = %q, want %q (fallback to keyword)", r.Label, "vct")
			}
		}
	}
	if !found {
		t.Error("no row with keyword=vct in results")
	}
}

func TestAnalyzerNew(t *testing.T) {
	db := openTestDB(t)
	a := New(db)
	if a == nil {
		t.Fatal("New returned nil")
	}
	if a.db != db {
		t.Fatal("analyzer db not set correctly")
	}
}

// Verify session boundary: two bursts separated by >30 s become two sessions.
func TestTodayByApp_SessionCount(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	base := time.Now().Local().Truncate(24 * time.Hour).Add(2 * time.Hour)

	// Burst 1: 4 samples
	insertSamples(t, db, base, "chromium", "YouTube", 4)
	// Burst 2: 4 samples, 2 min gap (> 30 s)
	insertSamples(t, db, base.Add(2*time.Minute), "chromium", "YouTube", 4)

	results, err := a.TodayByApp(context.Background())
	if err != nil {
		t.Fatalf("TodayByApp: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("got no results")
	}
	if results[0].SessionCount != 2 {
		t.Errorf("session_count = %d, want 2", results[0].SessionCount)
	}
	fmt.Printf("sessions=%d total=%ds longest=%ds\n",
		results[0].SessionCount, results[0].TotalSeconds, results[0].LongestSession)
}

func TestHistory(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	today := time.Now().Local().Truncate(24 * time.Hour).Add(2 * time.Hour)
	twoDaysAgo := today.Add(-2 * 24 * time.Hour)

	insertSamples(t, db, today, "chromium", "YouTube", 12)              // 60 s today
	insertSamples(t, db, twoDaysAgo, "code", "main.go - screentyme", 6) // 30 s 2 days ago

	results, err := a.History(context.Background(), 7)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(results), results)
	}
	// Newest day first.
	if results[0].Day < results[1].Day {
		t.Errorf("rows not ordered newest first: %v", results)
	}
}

func TestHistory_ExcludesOutsideWindow(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	old := time.Now().Add(-10 * 24 * time.Hour)
	insertSamples(t, db, old, "chromium", "YouTube", 20)

	results, err := a.History(context.Background(), 7)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d rows for out-of-window data, want 0", len(results))
	}
}

func TestAppDetailFor(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	base := time.Now().Local().Truncate(24 * time.Hour).Add(2 * time.Hour)
	insertSamples(t, db, base, "chromium", "YouTube - some video", 12)
	insertSamples(t, db, base.Add(5*time.Minute), "chromium", "Home / X", 6)
	insertSamples(t, db, base, "code", "main.go", 6) // different app, should be excluded

	_, err := db.Exec(`INSERT INTO tracked_keywords (app_class, keyword, label) VALUES ('chromium','youtube','YouTube')`)
	if err != nil {
		t.Fatalf("insert keyword: %v", err)
	}

	d, err := a.AppDetailFor(context.Background(), "chromium", 7)
	if err != nil {
		t.Fatalf("AppDetailFor: %v", err)
	}
	if d.AppClass != "chromium" {
		t.Errorf("app_class = %q, want chromium", d.AppClass)
	}
	if d.TotalSeconds <= 0 {
		t.Errorf("total_seconds = %d, want > 0", d.TotalSeconds)
	}
	if len(d.TopTitles) < 2 {
		t.Errorf("top_titles = %d, want at least 2", len(d.TopTitles))
	}
	if len(d.ByKeyword) != 1 || d.ByKeyword[0].Keyword != "youtube" {
		t.Errorf("by_keyword = %+v, want one entry for youtube", d.ByKeyword)
	}
	if len(d.ByDay) == 0 {
		t.Error("by_day empty")
	}
}

func TestAppDetailFor_NoData(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	d, err := a.AppDetailFor(context.Background(), "chromium", 7)
	if err != nil {
		t.Fatalf("AppDetailFor: %v", err)
	}
	if d.TotalSeconds != 0 || d.SessionCount != 0 {
		t.Errorf("expected zero totals on empty db, got %+v", d)
	}
}

func TestKeywordTotals(t *testing.T) {
	db := openTestDB(t)
	a := New(db)

	base := time.Now().Local().Truncate(24 * time.Hour).Add(2 * time.Hour)
	insertSamples(t, db, base, "chromium", "YouTube highlights", 12)

	_, err := db.Exec(`INSERT INTO tracked_keywords (app_class, keyword, label) VALUES
		('chromium','youtube','YouTube'),
		('chromium','vct','VCT')`)
	if err != nil {
		t.Fatalf("insert keywords: %v", err)
	}

	results, err := a.KeywordTotals(context.Background(), 7)
	if err != nil {
		t.Fatalf("KeywordTotals: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(results), results)
	}
	// First is most active (youtube), then vct (zero).
	if results[0].Keyword != "youtube" || results[0].TotalSeconds == 0 {
		t.Errorf("first row should be active youtube, got %+v", results[0])
	}
	if results[1].Keyword != "vct" || results[1].TotalSeconds != 0 {
		t.Errorf("second row should be inactive vct, got %+v", results[1])
	}
}
