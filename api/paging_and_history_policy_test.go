package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func historyPayload() map[string]interface{} {
	return map[string]interface{}{
		"data": map[string]interface{}{
			"SystemInfo": map[string]interface{}{"hostname": "keep-me"},
			"BrowserHistory": map[string]interface{}{
				"sync_type": "incremental",
				"new_history_entries": []interface{}{
					map[string]interface{}{"url": "https://mail.example.com:8443/inbox?token=secret#frag", "title": "Inbox (3)", "browser": "Chrome"},
					map[string]interface{}{"url": "file:///home/alice/taxes.pdf", "title": "taxes.pdf", "browser": "Chrome"},
					map[string]interface{}{"url": "about:blank", "title": "", "browser": "Firefox"},
					map[string]interface{}{"url": "http://intranet/hr/salaries", "title": "Salaries", "browser": "Edge"},
				},
				"top_recent_urls": []interface{}{
					map[string]interface{}{"url": "https://news.example.org/a/b?c=d", "title": "Headline", "browser": "Firefox"},
				},
			},
		},
	}
}

func historyOf(p map[string]interface{}) map[string]interface{} {
	h, _ := p["data"].(map[string]interface{})["BrowserHistory"].(map[string]interface{})
	return h
}

func TestEnforceBrowserHistoryDomainOnly(t *testing.T) {
	p := historyPayload()
	enforceBrowserHistoryPolicy(p, "domain_only", true)
	h := historyOf(p)

	entries := h["new_history_entries"].([]interface{})
	if len(entries) != 2 {
		t.Fatalf("entries without a host must be dropped, got %#v", entries)
	}
	first := entries[0].(map[string]interface{})
	if first["url"] != "https://mail.example.com:8443" || first["title"] != "" {
		t.Fatalf("path, query, fragment and title must be stripped: %#v", first)
	}
	if second := entries[1].(map[string]interface{}); second["url"] != "http://intranet" {
		t.Fatalf("unexpected second entry: %#v", second)
	}
	recent := h["top_recent_urls"].([]interface{})
	if len(recent) != 1 || recent[0].(map[string]interface{})["url"] != "https://news.example.org" {
		t.Fatalf("recent snapshot must be reduced too: %#v", recent)
	}
	if h["sync_type"] != "incremental" {
		t.Fatal("non-URL fields must survive")
	}
	if p["data"].(map[string]interface{})["SystemInfo"] == nil {
		t.Fatal("other collectors must be untouched")
	}
}

func TestEnforceBrowserHistoryDisabledOrOff(t *testing.T) {
	for name, args := range map[string]struct {
		mode    string
		collect bool
	}{"mode disabled": {"disabled", true}, "collection off": {"full_url", false}} {
		p := historyPayload()
		enforceBrowserHistoryPolicy(p, args.mode, args.collect)
		if historyOf(p) != nil {
			t.Errorf("%s: browser history must be dropped", name)
		}
		if p["data"].(map[string]interface{})["SystemInfo"] == nil {
			t.Errorf("%s: other collectors must be untouched", name)
		}
	}
}

func TestEnforceBrowserHistoryFullURLUnchanged(t *testing.T) {
	p := historyPayload()
	enforceBrowserHistoryPolicy(p, "full_url", true)
	entries := historyOf(p)["new_history_entries"].([]interface{})
	if len(entries) != 4 || entries[0].(map[string]interface{})["url"] != "https://mail.example.com:8443/inbox?token=secret#frag" {
		t.Fatalf("full_url mode must not modify entries: %#v", entries)
	}
	// Payloads without browser history are a no-op.
	enforceBrowserHistoryPolicy(map[string]interface{}{"data": map[string]interface{}{}}, "domain_only", true)
	enforceBrowserHistoryPolicy(map[string]interface{}{}, "disabled", false)
}

func TestParsePageLimit(t *testing.T) {
	cases := []struct {
		query     string
		wantLimit int
		wantPaged bool
		wantErr   bool
	}{
		{"", 0, false, false}, // legacy: whole list as an array
		{"limit=50", 50, true, false},
		{"limit=500", 500, true, false},
		{"limit=0", 0, false, true},
		{"limit=501", 0, false, true},
		{"limit=-1", 0, false, true},
		{"limit=abc", 0, false, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/devices?"+c.query, nil)
		limit, paged, err := parsePageLimit(r)
		if limit != c.wantLimit || paged != c.wantPaged || (err != nil) != c.wantErr {
			t.Errorf("%q: got (%d,%v,%v), want (%d,%v,err=%v)", c.query, limit, paged, err, c.wantLimit, c.wantPaged, c.wantErr)
		}
	}
}

type page struct {
	Items []map[string]interface{} `json:"items"`
	Total float64                  `json:"total"`
	Next  string                   `json:"next"`
}

func getPage(t *testing.T, h http.HandlerFunc, target string) page {
	t.Helper()
	w := call(h, http.MethodGet, target, "", "")
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", target, w.Code, w.Body.String())
	}
	var p page
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("%s: %v (%s)", target, err, w.Body.String())
	}
	return p
}

func TestDevicesPagingWalksEveryDeviceOnce(t *testing.T) {
	useTestMongo(t)
	for i := 0; i < 5; i++ {
		if _, err := mongoClient.Database(dbName).Collection("devices").InsertOne(context.Background(),
			bson.M{"device_id": fmt.Sprintf("dev-%d", i), "api_key_hash": "secret-hash", "hostname": "h"}); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		target := "/devices?limit=2"
		if cursor != "" {
			target += "&after=" + cursor
		}
		p := getPage(t, devicesHandler, target)
		pages++
		if p.Total != 5 {
			t.Fatalf("total = %v", p.Total)
		}
		for _, d := range p.Items {
			id := d["device_id"].(string)
			if seen[id] {
				t.Fatalf("%s returned twice", id)
			}
			seen[id] = true
			if _, leaked := d["api_key_hash"]; leaked {
				t.Fatal("api_key_hash leaked in a paged response")
			}
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("saw %d devices in %d pages, want 5 in 3", len(seen), pages)
	}
}

func TestDevicesLegacyAndInvalidRequests(t *testing.T) {
	useTestMongo(t)
	for i := 0; i < 3; i++ {
		_, _ = mongoClient.Database(dbName).Collection("devices").InsertOne(context.Background(), bson.M{"device_id": fmt.Sprintf("d%d", i)})
	}
	// No limit: the original bare array, so a dashboard that is not updated yet keeps working.
	w := call(devicesHandler, http.MethodGet, "/devices", "", "")
	var all []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &all); err != nil || len(all) != 3 {
		t.Fatalf("legacy response must be an array of every device: %v %s", err, w.Body.String())
	}
	for _, target := range []string{"/devices?limit=0", "/devices?limit=9999", "/devices?limit=2&after=not-an-id"} {
		if w := call(devicesHandler, http.MethodGet, target, "", ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", target, w.Code)
		}
	}
}

func TestUsersPagingKeepsNewestFirstWithoutRepeats(t *testing.T) {
	useTestMongo(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		u := DashboardUser{Email: fmt.Sprintf("u%d@example.com", i), Name: "u", Role: RoleViewer, Status: "active",
			CreatedAt: base.Add(time.Duration(i) * time.Minute), UpdatedAt: base}
		if _, err := mongoClient.Database(dbName).Collection("users").InsertOne(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	var order []string
	offset := ""
	for guard := 0; guard < 10; guard++ {
		target := "/users?limit=2"
		if offset != "" {
			target += "&offset=" + offset
		}
		p := getPage(t, usersHandler, target)
		for _, u := range p.Items {
			order = append(order, u["email"].(string))
		}
		if p.Next == "" {
			break
		}
		offset = p.Next
	}
	want := []string{"u4@example.com", "u3@example.com", "u2@example.com", "u1@example.com", "u0@example.com"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want newest first %v", order, want)
	}

	// Legacy call unchanged.
	w := call(usersHandler, http.MethodGet, "/users", "", "")
	var all []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &all); err != nil || len(all) != 5 {
		t.Fatalf("legacy users response must be an array: %v", err)
	}
	if w := call(usersHandler, http.MethodGet, "/users?limit=2&offset=-3", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("negative offset: %d", w.Code)
	}
}

func TestIngestEnforcesDomainOnlyBeforeStoring(t *testing.T) {
	useTestMongo(t)
	seedDevice(t, "1.0.0") // device d1, API key "agent-key"

	globalPolicy.mu.Lock()
	saved := globalPolicy.config
	cfg := defaultPolicy()
	cfg["browser_history_mode"] = "domain_only"
	globalPolicy.config = cfg
	globalPolicy.mu.Unlock()
	t.Cleanup(func() { globalPolicy.mu.Lock(); globalPolicy.config = saved; globalPolicy.mu.Unlock() })

	body := fmt.Sprintf(`{"event_id":"e1","timestamp":%q,"data":{"BrowserHistory":{
		"sync_type":"incremental",
		"new_history_entries":[{"url":"https://bank.example.com/accounts/123?session=abc","title":"My Accounts","browser":"Chrome","last_visit_time":1700000000000000000}],
		"top_recent_urls":[{"url":"https://bank.example.com/accounts/123?session=abc","title":"My Accounts","browser":"Chrome","last_visit_time":1700000000000000000}]}}}`,
		time.Now().UTC().Format(time.RFC3339))
	if w := call(ingestHandler, http.MethodPost, "/ingest", body, "agent-key"); w.Code != 200 {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}

	ctx := context.Background()
	db := mongoClient.Database(dbName)
	var tel, dev bson.M
	if err := db.Collection("telemetry").FindOne(ctx, bson.M{"event_id": "e1"}).Decode(&tel); err != nil {
		t.Fatal(err)
	}
	if err := db.Collection("devices").FindOne(ctx, bson.M{"device_id": "d1"}).Decode(&dev); err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]bson.M{"telemetry": tel, "device": dev} {
		raw, _ := json.Marshal(doc)
		for _, leak := range []string{"/accounts/123", "session=abc", "My Accounts"} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("%s document still contains %q: %s", name, leak, raw)
			}
		}
		if !strings.Contains(string(raw), "https://bank.example.com") {
			t.Errorf("%s document lost the domain: %s", name, raw)
		}
	}
}
