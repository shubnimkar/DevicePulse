package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devicepulse/agent/commander"
)

// The API key and device id are read by the collection loop, the sync engine, the
// policy poller, the updater and the command poller, and replaced when the server
// rotates the key (401 → re-register). Every access goes through these helpers.
var credMu sync.RWMutex

func currentAPIKey() string {
	credMu.RLock()
	defer credMu.RUnlock()
	return apiKey
}

func currentDeviceID() string {
	credMu.RLock()
	defer credMu.RUnlock()
	return deviceID
}

// setCredentials stores new credentials and hands them to the command poller.
func setCredentials(id, key string) {
	credMu.Lock()
	deviceID, apiKey = id, key
	credMu.Unlock()
	commander.SetCredentials(apiURL, key)
}

// registerUntilSuccess keeps trying to register with exponential backoff (5s up
// to 5 min) so an API outage at boot never stops local collection.
func registerUntilSuccess() {
	delay := 5 * time.Second
	for {
		time.Sleep(delay)
		if err := registerDevice(); err == nil {
			log.Printf("Registered as device_id=%s", currentDeviceID())
			return
		} else {
			log.Printf("Device registration retry failed: %v", err)
		}
		if delay *= 2; delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
}

// reloadCredentialsFromCache re-reads registration.json (written by the root
// service). The per-user window service uses it after a 401, because the root
// service may have rotated the key since this process started. It reports
// whether the credentials changed.
func reloadCredentialsFromCache() bool {
	data, err := os.ReadFile(filepath.Join(dataDir, "registration.json"))
	if err != nil {
		return false
	}
	var reg map[string]string
	if json.Unmarshal(data, &reg) != nil || reg["device_id"] == "" || reg["api_key"] == "" {
		return false
	}
	if reg["api_key"] == currentAPIKey() {
		return false
	}
	setCredentials(reg["device_id"], reg["api_key"])
	log.Printf("Reloaded rotated credentials from the registration cache")
	return true
}

// checkAPIScheme warns (or fails when requireHTTPS) if the API key would travel
// over plain http to a non-loopback host.
func checkAPIScheme(raw string, requireHTTPS bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid API URL %q", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	msg := fmt.Sprintf("API URL %s is not https: the API key would be sent unencrypted", raw)
	if requireHTTPS {
		return fmt.Errorf("%s (DEVICEPULSE_REQUIRE_HTTPS is set)", msg)
	}
	log.Printf("WARNING: %s. Set DEVICEPULSE_REQUIRE_HTTPS=true to refuse this.", msg)
	return nil
}

func requireHTTPSFromEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("DEVICEPULSE_REQUIRE_HTTPS")), "true")
}
