// Package commander implements remote-action command & control for the
// DevicePulse agent.
//
// Flow:
//  1. Poller goroutine calls GET <api>/commands/pending. The server atomically
//     claims each command (pending → delivered), so the root service and the
//     window_only user service can poll concurrently without double execution.
//  2. Each claimed command is dispatched to a handler by type.
//  3. The outcome is reported via POST <api>/commands/result with one of:
//     success | failed | unsupported (+ a human-readable detail string).
//
// Supported commands:
//
//	collect_now         — trigger an immediate telemetry collection cycle
//	restart_agent       — re-exec / relaunch the agent binary
//	lock_screen         — lock the interactive user session
//	quarantine_enable   — firewall-isolate the host except API/DNS traffic
//	quarantine_release  — remove the isolation rules
//	wipe_agent          — corporate wipe: purge local data, disable service,
//	                      remove the binary; server revokes the credential on
//	                      the success report.
package commander

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Command is the subset of the server's DeviceCommand document the agent needs.
type Command struct {
	ID     string                 `json:"id"`
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// Execution outcome statuses understood by POST /commands/result.
const (
	StatusSuccess     = "success"
	StatusFailed      = "failed"
	StatusUnsupported = "unsupported"
)

type resultRequest struct {
	CommandID string `json:"command_id"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
}

var (
	mu           sync.RWMutex
	apiEndpoint  string
	credentials  string // X-API-Key value
	collectNowCh chan struct{}
)

func init() {
	collectNowCh = make(chan struct{}, 1)
}

// SetCredentials updates the endpoint/key used by the poller. Called after
// registration and again if cached credentials are ever re-minted.
func SetCredentials(apiURL, apiKey string) {
	mu.Lock()
	defer mu.Unlock()
	apiEndpoint = apiURL
	credentials = apiKey
}

// CollectNowChan returns a channel that receives a token whenever a
// collect_now command arrives. Consumers select on it alongside their timer.
func CollectNowChan() <-chan struct{} { return collectNowCh }

// NotifyCollectNow schedules an immediate collection cycle (non-blocking).
func NotifyCollectNow() {
	select {
	case collectNowCh <- struct{}{}:
	default:
	}
}

// Poller claims and executes pending commands until the process exits.
func Poller(interval time.Duration) {
	time.Sleep(5 * time.Second) // let startup settle before first poll
	log.Printf("Commander: polling for remote commands every %v", interval)
	for {
		fetchAndExecute()
		time.Sleep(interval)
	}
}

func fetchAndExecute() {
	mu.RLock()
	endpoint, key := apiEndpoint, credentials
	mu.RUnlock()
	if endpoint == "" || key == "" {
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, endpoint+"/commands/pending", nil)
	if err != nil {
		return
	}
	req.Header.Set("X-API-Key", key)

	resp, err := client.Do(req)
	if err != nil {
		return // offline; retry next tick
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Commander: pending poll returned status %d", resp.StatusCode)
		return
	}

	var payload struct {
		Commands []Command `json:"commands"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		log.Printf("Commander: bad pending payload: %v", err)
		return
	}

	for _, cmd := range payload.Commands {
		status, detail := Execute(endpoint, cmd)
		log.Printf("Commander: %s [%s] → %s (%s)", cmd.Type, cmd.ID, status, detail)
		postResult(endpoint, key, resultRequest{
			CommandID: cmd.ID,
			Status:    status,
			Detail:    detail,
		})
	}
}

func postResult(endpoint, key string, res resultRequest) {
	body, _ := json.Marshal(res)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/commands/result", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Commander: failed to report result for %s: %v", res.CommandID, err)
		return
	}
	resp.Body.Close()
}

// Execute dispatches a single command and returns (status, detail).
// Destructive/disruptive commands run asynchronously AFTER their result has
// been posted (restart, wipe), so the server records the outcome even if the
// process does not survive the action.
func Execute(apiURL string, cmd Command) (string, string) {
	switch cmd.Type {
	case "collect_now":
		NotifyCollectNow()
		return StatusSuccess, "collection cycle triggered"

	case "restart_agent":
		go func() {
			time.Sleep(2 * time.Second) // allow the result POST to flush
			restartProcess()
		}()
		return StatusSuccess, "agent restart scheduled"

	case "lock_screen":
		return lockScreen()

	case "quarantine_enable":
		return quarantineEnable(apiURL)

	case "quarantine_release":
		return quarantineRelease()

	case "wipe_agent":
		go func() {
			time.Sleep(2 * time.Second)
			runWipe()
		}()
		return StatusSuccess, "corporate wipe started (data purge + service disable)"

	default:
		return StatusUnsupported, fmt.Sprintf("unknown command type %q", cmd.Type)
	}
}

// apiHost extracts the hostname from the API URL for firewall allow-listing.
func apiHost(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
