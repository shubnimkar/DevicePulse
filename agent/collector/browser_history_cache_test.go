package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedScanReusesUntilDatabaseChanges(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "History")
	if err := os.WriteFile(db, []byte("v1"), 0600); err != nil {
		t.Fatal(err)
	}
	scans := 0
	scan := func() []HistoryEntry {
		scans++
		return []HistoryEntry{{URL: "https://example.test", Browser: "Chrome"}}
	}

	first := cachedScan(db, scan)
	second := cachedScan(db, scan)
	if scans != 1 {
		t.Fatalf("unchanged database was copied/scanned %d times", scans)
	}
	// Callers mutate entries in place (domain-only mode); the cache must be unaffected.
	second[0].URL = "mutated"
	if third := cachedScan(db, scan); third[0].URL != "https://example.test" || first[0].URL != "https://example.test" {
		t.Fatal("cache returned aliased entries")
	}

	if err := os.WriteFile(db, []byte("v2-longer"), 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(db, future, future)
	cachedScan(db, scan)
	if scans != 2 {
		t.Fatalf("changed database must be rescanned, scans=%d", scans)
	}

	// A write-ahead file change alone also invalidates the cache.
	if err := os.WriteFile(db+"-wal", []byte("w"), 0600); err != nil {
		t.Fatal(err)
	}
	cachedScan(db, scan)
	if scans != 3 {
		t.Fatalf("WAL change must trigger a rescan, scans=%d", scans)
	}
}

func TestCachedScanDoesNotCacheMissingDatabase(t *testing.T) {
	scans := 0
	for i := 0; i < 2; i++ {
		cachedScan(filepath.Join(t.TempDir(), "absent"), func() []HistoryEntry { scans++; return nil })
	}
	if scans != 2 {
		t.Fatalf("missing databases must not be cached, scans=%d", scans)
	}
}

func TestServicesAndPortsReuseResultsWithinTTL(t *testing.T) {
	p := &NetworkPorts{}
	a, _ := p.Collect()
	b, _ := p.Collect()
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("expected a payload")
	}
	p.mu.Lock()
	cachedAt := p.at
	p.mu.Unlock()
	if _, _ = p.Collect(); !p.at.Equal(cachedAt) {
		t.Fatal("second call within the TTL must not recollect")
	}
}
