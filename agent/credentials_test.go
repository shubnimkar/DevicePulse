package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckAPIScheme(t *testing.T) {
	cases := []struct {
		url     string
		require bool
		wantErr bool
	}{
		{"https://api.example.com/api", true, false},
		{"http://localhost:8000", true, false},
		{"http://127.0.0.1:8000", true, false},
		{"http://[::1]:8000", true, false},
		{"http://api.example.com", false, false}, // warns only
		{"http://api.example.com", true, true},   // refused when required
		{"not a url", false, true},
	}
	for _, c := range cases {
		err := checkAPIScheme(c.url, c.require)
		if (err != nil) != c.wantErr {
			t.Errorf("checkAPIScheme(%q, require=%v) err=%v, wantErr=%v", c.url, c.require, err, c.wantErr)
		}
	}
}

func TestReloadCredentialsFromCacheOnlyWhenKeyChanged(t *testing.T) {
	dir := t.TempDir()
	oldDir, oldURL := dataDir, apiURL
	dataDir, apiURL = dir, "http://localhost:8000"
	t.Cleanup(func() { dataDir, apiURL = oldDir, oldURL; setCredentials("", "") })

	setCredentials("dev-1", "old-key")
	write := func(id, key string) {
		data, _ := json.Marshal(map[string]string{"device_id": id, "api_key": key})
		if err := os.WriteFile(filepath.Join(dir, "registration.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	write("dev-1", "old-key")
	if reloadCredentialsFromCache() {
		t.Fatal("unchanged key must not report a reload")
	}
	write("dev-1", "rotated-key")
	if !reloadCredentialsFromCache() || currentAPIKey() != "rotated-key" {
		t.Fatalf("rotated key not picked up, have %q", currentAPIKey())
	}
	write("", "")
	if reloadCredentialsFromCache() || currentAPIKey() != "rotated-key" {
		t.Fatal("an empty cache must never wipe working credentials")
	}
}
