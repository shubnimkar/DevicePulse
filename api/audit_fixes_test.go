package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// ── Focus-cache fix (#1): cycle deltas only ───────────────────────────────────

func TestApplyFocusSummariesAccumulatesCycleDeltas(t *testing.T) {
	fc := &FocusCache{data: map[string]map[string]*AppFocusEntry{}}
	summary := func(seconds float64) []interface{} {
		return []interface{}{
			map[string]interface{}{"app_name": "Chrome", "total_focus_seconds": seconds, "session_count": 1.0},
		}
	}
	// Two sync cycles, each reporting a 10s delta → 20s total, never doubled.
	fc.applyFocusSummaries("dev-1", summary(10))
	fc.applyFocusSummaries("dev-1", summary(10))
	snap := fc.snapshot("dev-1")
	if len(snap) != 1 || snap[0].TotalFocusS != 20 {
		t.Fatalf("expected 20s accumulated total, got %+v", snap)
	}
}

func TestExtractFocusSummariesIgnoresCumulative(t *testing.T) {
	doc := bson.M{
		"data": bson.M{
			"ActiveWindowTracker": bson.M{
				"app_summaries":        bson.A{bson.M{"app_name": "Code", "total_focus_seconds": 5.0}},
				"cumulative_summaries": bson.A{bson.M{"app_name": "Code", "total_focus_seconds": 9999.0}},
			},
		},
	}
	got := extractFocusSummaries(doc)
	if len(got) != 1 {
		t.Fatalf("expected exactly the per-cycle delta entry, got %#v", got)
	}
	m := got[0].(bson.M)
	if seconds, _ := toFloat(m["total_focus_seconds"]); seconds != 5.0 {
		t.Fatalf("expected cumulative totals to be ignored, got %v", seconds)
	}
}

func TestPayloadFocusSummariesPersistCycleDeltasOnly(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"ActiveWindowTracker": map[string]interface{}{
				"app_summaries":        []interface{}{map[string]interface{}{"app_name": "Code", "total_focus_seconds": 7.0}},
				"cumulative_summaries": []interface{}{map[string]interface{}{"app_name": "Code", "total_focus_seconds": 4242.0}},
			},
		},
	}
	got := focusSummariesFromPayload(payload)
	if len(got) != 1 {
		t.Fatalf("expected one delta entry, got %#v", got)
	}
	m := got[0].(map[string]interface{})
	if seconds, _ := toFloat(m["total_focus_seconds"]); seconds != 7.0 {
		t.Fatalf("expected persisted metadata to carry cycle deltas, got %v", seconds)
	}
}

// ── Policy whitelist (#15) ─────────────────────────────────────────────────────

func TestNormalizePolicyDropsUnknownKeys(t *testing.T) {
	got := normalizePolicy(map[string]interface{}{
		"sync_interval_seconds": 42.0,
		"_id":                   "global_policy",
		"$where":                "sleep(1000)",
		"sneaker_key":           true,
	})
	if got["sync_interval_seconds"] != 42.0 {
		t.Fatalf("expected known key to survive, got %#v", got["sync_interval_seconds"])
	}
	for _, bad := range []string{"_id", "$where", "sneaker_key"} {
		if _, exists := got[bad]; exists {
			t.Fatalf("unknown/malicious key %q must be dropped, got %#v", bad, got)
		}
	}
}

// ── Downgrade guard (#13) ──────────────────────────────────────────────────────

func TestCompareAgentVersionsGuardsDowngrades(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},   // equal → no update
		{"1.2.4", "1.2.3", 1},   // newer latest → update
		{"1.2.3", "1.9.9", -1},  // older latest than running agent → no downgrade
		{"2.0.0", "10.0.0", -1}, // numeric compare, not lexicographic
	}
	for _, c := range cases {
		if got := compareAgentVersions(c.a, c.b); got != c.want {
			t.Errorf("compareAgentVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// ── API-key hashing (#7) ───────────────────────────────────────────────────────

func TestHashAPIKeyDeterministicAndDistinct(t *testing.T) {
	h1 := hashAPIKey("abcdef123456")
	h2 := hashAPIKey("abcdef123456")
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("hash must be deterministic sha256 hex, got %q vs %q", h1, h2)
	}
	if hashAPIKey("different") == h1 {
		t.Fatal("distinct keys must hash differently")
	}
}

// ── Rate limiting (#6) — pure logic, no sleeps ────────────────────────────────

func TestRateLimiterWindowAllowsBurstThenBlocks(t *testing.T) {
	rl := &rateLimiter{limit: 3, window: time.Minute, hits: map[string]*rateWindow{}}
	for i := 0; i < 3; i++ {
		if !rl.Allow("ip-1") {
			t.Fatalf("request %d within limit must pass", i+1)
		}
	}
	if rl.Allow("ip-1") {
		t.Fatal("4th request in window must be rejected")
	}
	if !rl.Allow("ip-2") {
		t.Fatal("other keys must have independent windows")
	}
}

func TestClientIPPrefersForwardedFor(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "http://api/auth/login", nil)
	r.RemoteAddr = "10.0.0.1:55555"
	if got := clientIP(r); got != "10.0.0.1" {
		t.Fatalf("expected remote addr host, got %q", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("expected left-most forwarded IP, got %q", got)
	}
}

// ── CORS (#9) — exact-match origins only ───────────────────────────────────────

func TestAllowedOriginsExactMatchOnly(t *testing.T) {
	t.Setenv("DASHBOARD_ORIGIN", "")
	t.Setenv("DASHBOARD_EXTRA_ORIGINS", "")
	set := allowedOrigins()
	for origin := range set {
		if strings.Contains(origin, "*") {
			t.Fatalf("no wildcard origins allowed: %q", origin)
		}
	}
	if _, ok := set["http://localhost:3000"]; !ok {
		t.Fatalf("default dev origin missing: %#v", set)
	}
	// An arbitrary localhost port must NOT be implicitly trusted.
	if _, ok := set["http://localhost:6666"]; ok {
		t.Fatalf("arbitrary localhost origin leaked into allowlist: %#v", set)
	}
}
