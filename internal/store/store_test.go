package store

import (
	"context"
	"testing"
	"time"

	"github.com/Poudel0/screentyme/internal/sampler"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestInsert(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	smp := sampler.Sample{
		Timestamp: time.Now(),
		AppClass:  "chromium",
		Title:     "YouTube",
		PID:       1234,
		Workspace: 1,
		Monitor:   0,
	}
	if err := st.Insert(ctx, smp); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows, want 1", count)
	}
}

func TestInsertStoresTitle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	smp := sampler.Sample{
		Timestamp: time.Now(),
		AppClass:  "code",
		Title:     "main.go - screentyme",
	}
	if err := st.Insert(ctx, smp); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var title string
	err := st.db.QueryRowContext(ctx, `SELECT title FROM samples`).Scan(&title)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if title != "main.go - screentyme" {
		t.Errorf("title = %q, want %q", title, "main.go - screentyme")
	}
}

func TestKeywordCRUD(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.AddKeyword(ctx, "chromium", "youtube", "YouTube"); err != nil {
		t.Fatalf("add keyword: %v", err)
	}
	if err := st.AddKeyword(ctx, "chromium", "vct", "VCT"); err != nil {
		t.Fatalf("add keyword: %v", err)
	}

	kws, err := st.ListKeywords(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(kws) != 2 {
		t.Fatalf("got %d keywords, want 2", len(kws))
	}
	if kws[0].Keyword != "vct" || kws[1].Keyword != "youtube" {
		t.Errorf("unexpected order: %v", kws)
	}

	if err := st.DeleteKeyword(ctx, kws[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	kws, err = st.ListKeywords(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(kws) != 1 {
		t.Fatalf("got %d keywords after delete, want 1", len(kws))
	}
}

func TestAddKeywordUpsert(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.AddKeyword(ctx, "chromium", "youtube", "old label"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Upsert: same (app_class, keyword) should update label.
	if err := st.AddKeyword(ctx, "chromium", "youtube", "YouTube"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	kws, _ := st.ListKeywords(ctx)
	if len(kws) != 1 {
		t.Fatalf("got %d rows after upsert, want 1", len(kws))
	}
	if kws[0].Label != "YouTube" {
		t.Errorf("label = %q, want %q", kws[0].Label, "YouTube")
	}
}

func TestHealth(t *testing.T) {
	st := openTestStore(t)
	if err := st.Health(context.Background()); err != nil {
		t.Errorf("health check failed: %v", err)
	}
}
