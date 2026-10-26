package main

import (
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestCompareAgentVersionsPrerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3-beta", "1.2.3", -1}, // a prerelease never outranks its release
		{"1.2.3", "1.2.3-beta", 1},  // so a stable build upgrades a prerelease
		{"1.2.3-beta", "1.2.3-beta", 0},
		{"1.2.3-rc.2", "1.2.3-rc.1", 1},
		{"1.2.3+build5", "1.2.3", 0}, // build metadata ignored
		{"0.0.1-dev", "1.0.0", -1},   // default dev agent still gets real releases
		{"1.2", "1.2.0", 0},
	}
	for _, c := range cases {
		if got := compareAgentVersions(c.a, c.b); got != c.want {
			t.Errorf("compareAgentVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRevocationIgnoresHostname(t *testing.T) {
	// A hostname alone must not match any revocation: "ubuntu" is not an identity.
	if got := revokedDeviceFilter("", "", "", "ubuntu"); got["_id"] != "__never__" {
		t.Fatalf("hostname-only filter should match nothing, got %#v", got)
	}
	filters := deviceIdentityFilters("device-1", "uuid-1", "aabbcc", "ubuntu")
	if len(filters) != 3 {
		t.Fatalf("expected device_id, hardware_uuid and mac filters only, got %#v", filters)
	}
	for _, f := range filters {
		if _, ok := f.(bson.M)["hostname"]; ok {
			t.Fatalf("hostname leaked into revocation filters: %#v", filters)
		}
	}
}

func TestClientIPTrustedProxies(t *testing.T) {
	req := func(peer, xff string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "http://api/auth/login", nil)
		r.RemoteAddr = peer + ":4000"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.1.5")
	// Proxy appended the real client; a client-supplied left entry is ignored.
	if got := clientIP(req("10.0.0.1", "6.6.6.6, 203.0.113.9, 10.0.0.2")); got != "203.0.113.9" {
		t.Fatalf("expected right-most untrusted address, got %q", got)
	}
	// A direct caller cannot spoof the header.
	if got := clientIP(req("198.51.100.5", "6.6.6.6")); got != "198.51.100.5" {
		t.Fatalf("untrusted peer must not control X-Forwarded-For, got %q", got)
	}
	if got := clientIP(req("192.168.1.5", "203.0.113.7")); got != "203.0.113.7" {
		t.Fatalf("single-IP proxy entry not honoured, got %q", got)
	}

	// Unset keeps the legacy behaviour so existing deployments are unchanged.
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	if got := clientIP(req("10.0.0.1", "203.0.113.7, 10.0.0.1")); got != "203.0.113.7" {
		t.Fatalf("legacy behaviour changed, got %q", got)
	}
}

func TestQuarantineWindowMinutes(t *testing.T) {
	t.Setenv("QUARANTINE_MAX_HOURS", "")
	for params, want := range map[float64]float64{0: 24 * 60, 0.5: 24 * 60, 90: 90, 1e9: 30 * 24 * 60} {
		if got := quarantineWindowMinutes(map[string]interface{}{"duration_minutes": params}); got != want {
			t.Errorf("duration_minutes=%v -> %v, want %v", params, got, want)
		}
	}
	if got := quarantineWindowMinutes(nil); got != 24*60 {
		t.Errorf("default = %v", got)
	}
	t.Setenv("QUARANTINE_MAX_HOURS", "2")
	if got := quarantineWindowMinutes(map[string]interface{}{}); got != 120 {
		t.Errorf("env default = %v", got)
	}
}

func TestRateLimiterBlockedHitReset(t *testing.T) {
	rl := &rateLimiter{limit: 2, window: time.Minute, hits: map[string]*rateWindow{}}
	if rl.Blocked("k") {
		t.Fatal("fresh key must not be blocked")
	}
	rl.Hit("k")
	if rl.Blocked("k") {
		t.Fatal("one failure of two must not block")
	}
	rl.Hit("k")
	if !rl.Blocked("k") {
		t.Fatal("key must be blocked after reaching the limit")
	}
	if rl.Blocked("other") {
		t.Fatal("other keys must be independent")
	}
	rl.Reset("k")
	if rl.Blocked("k") {
		t.Fatal("successful sign-in must clear the failure count")
	}
	var nilLimiter *rateLimiter
	if nilLimiter.Blocked("k") {
		t.Fatal("nil limiter must never block")
	}
}

func TestValidIdentityField(t *testing.T) {
	long := make([]byte, 257)
	for i := range long {
		long[i] = 'a'
	}
	for s, want := range map[string]bool{
		"":                                     true, // optional fields may be empty
		"MacBook-Pro.local":                    true,
		"A1B2C3D4-0000-1111-2222-333344445555": true,
		"büro-laptop":                          true,
		"bad\nhost":                            false,
		"bad\x00host":                          false,
		string(long):                           false,
	} {
		if got := validIdentityField(s); got != want {
			t.Errorf("validIdentityField(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestBrowserArchiveSkipsRecentSnapshot(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"BrowserHistory": map[string]interface{}{
				"new_history_entries": []interface{}{
					map[string]interface{}{"url": "https://new.example/a", "title": "A", "browser": "Chrome", "last_visit_time": 1.0},
				},
				"top_recent_urls": []interface{}{
					map[string]interface{}{"url": "https://old.example/b", "title": "B", "browser": "Chrome", "last_visit_time": 2.0},
				},
			},
		},
	}
	_, entries := extractNewBrowserHistoryEntries(payload)
	if len(entries) != 1 || entries[0].(map[string]interface{})["title"] != "A" {
		t.Fatalf("only new entries may be archived, got %#v", entries)
	}

	dropBrowserRecentSnapshot(payload)
	bh := payload["data"].(map[string]interface{})["BrowserHistory"].(map[string]interface{})
	if _, ok := bh["top_recent_urls"]; ok {
		t.Fatal("recent snapshot should be dropped from a non-archived payload")
	}
	if _, ok := bh["new_history_entries"]; !ok {
		t.Fatal("new entries must be kept")
	}
}
