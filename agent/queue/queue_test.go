package queue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	q, err := NewQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func count(t *testing.T, q *Queue) int {
	t.Helper()
	var n int
	if err := q.db.QueryRow("SELECT COUNT(*) FROM telemetry").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneKeepsNewestWithinSizeCap(t *testing.T) {
	q := newTestQueue(t)
	for i := 0; i < 10; i++ {
		if err := q.Push(map[string]interface{}{"i": i, "pad": strings.Repeat("x", 1000)}); err != nil {
			t.Fatal(err)
		}
	}
	// ~1.02 KB per row: a 3.5 KB cap keeps the newest 3.
	if err := q.pruneTo(3500, 24*3600*1e9); err != nil {
		t.Fatal(err)
	}
	if got := count(t, q); got != 3 {
		t.Fatalf("expected 3 newest rows to remain, got %d", got)
	}
	items, err := q.PopBatch(10)
	if err != nil || len(items) != 3 || !strings.Contains(items[0].Payload, `"i":9`) {
		t.Fatalf("newest row must survive: %v %+v", err, items)
	}
}

func TestPruneDropsExpiredRows(t *testing.T) {
	q := newTestQueue(t)
	for i := 0; i < 4; i++ {
		if err := q.Push(map[string]interface{}{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec("UPDATE telemetry SET created_at = datetime('now', '-10 days') WHERE id <= 2"); err != nil {
		t.Fatal(err)
	}
	if err := q.pruneTo(1<<30, 7*24*3600*1e9); err != nil {
		t.Fatal(err)
	}
	if got := count(t, q); got != 2 {
		t.Fatalf("expected the 2 recent rows to remain, got %d", got)
	}
}

func TestPruneNoopUnderCap(t *testing.T) {
	q := newTestQueue(t)
	_ = q.Push(map[string]interface{}{"a": 1})
	if err := q.Prune(); err != nil {
		t.Fatal(err)
	}
	if count(t, q) != 1 {
		t.Fatal("small queue must be untouched")
	}
}

func TestNewQueueRemovesPlantedSymlinks(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "q.db")
	if err := os.Symlink(victim, dbPath+"-shm"); err != nil {
		t.Fatal(err)
	}
	q, err := NewQueue(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = q.Push(map[string]interface{}{"a": 1})
	if info, _ := os.Stat(victim); info.Mode().Perm() != 0600 {
		t.Fatalf("victim permissions changed: %v", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(victim); string(data) != "x" {
		t.Fatalf("victim contents changed: %q", data)
	}
}
