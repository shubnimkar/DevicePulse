package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Integration tests for release gating and rollback. They need a real MongoDB:
//
//	TEST_MONGO_URI=mongodb://127.0.0.1:27017 go test -run ReleaseControl ./...
//
// They use a throwaway database and skip when TEST_MONGO_URI is unset.
func useTestMongo(t *testing.T) {
	t.Helper()
	uri := strings.TrimSpace(os.Getenv("TEST_MONGO_URI"))
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB unreachable: %v", err)
	}
	oldClient, oldDB := mongoClient, dbName
	mongoClient, dbName = client, fmt.Sprintf("devicepulse_test_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.Database(dbName).Drop(context.Background())
		_ = client.Disconnect(context.Background())
		mongoClient, dbName = oldClient, oldDB
	})
}

func call(h http.HandlerFunc, method, target, body, apiKey string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if apiKey != "" {
		r.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func checkUpdate(t *testing.T, version string) (bool, string) {
	t.Helper()
	w := call(updateCheckHandler, http.MethodGet, "/update/check?version="+version+"&os=linux&arch=amd64", "", "agent-key")
	if w.Code != 200 {
		t.Fatalf("update check %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Available bool   `json:"update_available"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Available, out.Version
}

func seedDevice(t *testing.T, agentVersion string) {
	t.Helper()
	_, err := mongoClient.Database(dbName).Collection("devices").UpdateOne(context.Background(),
		bson.M{"device_id": "d1"},
		bson.M{"$set": bson.M{
			"device_id": "d1", "api_key_hash": hashAPIKey("agent-key"), "status": "active",
			"agent_os": "linux", "agent_arch": "amd64", "agent_version": agentVersion,
		}},
		options.Update().SetUpsert(true))
	if err != nil {
		t.Fatal(err)
	}
}

func publish(t *testing.T, version string) {
	t.Helper()
	body := fmt.Sprintf(`{"version":%q,"os":"linux","arch":"amd64","download_url":"https://example.test/a","checksum_sha256":"abc"}`, version)
	if w := call(releasePublishHandler, http.MethodPost, "/update/release", body, ""); w.Code != http.StatusCreated {
		t.Fatalf("publish %s: %d %s", version, w.Code, w.Body.String())
	}
	time.Sleep(5 * time.Millisecond) // published_at has millisecond resolution
}

func activate(t *testing.T, version string) {
	t.Helper()
	body := fmt.Sprintf(`{"os":"linux","arch":"amd64","version":%q}`, version)
	if w := call(releaseActivateHandler, http.MethodPost, "/update/release/activate", body, ""); w.Code != 200 {
		t.Fatalf("activate %s: %d %s", version, w.Code, w.Body.String())
	}
	time.Sleep(5 * time.Millisecond)
}

func TestReleaseControlGatesNewReleases(t *testing.T) {
	useTestMongo(t)
	seedDevice(t, "1.0.0")

	// Default (flag off): publish is live immediately, exactly as before.
	t.Setenv("RELEASES_REQUIRE_ACTIVATION", "")
	publish(t, "1.0.0")
	publish(t, "1.1.0")
	if ok, v := checkUpdate(t, "1.0.0"); !ok || v != "1.1.0" {
		t.Fatalf("flag off: expected immediate update to 1.1.0, got %v %q", ok, v)
	}

	// Flag on: a new release is held back until activated.
	t.Setenv("RELEASES_REQUIRE_ACTIVATION", "true")
	publish(t, "1.2.0")
	if ok, v := checkUpdate(t, "1.1.0"); ok {
		t.Fatalf("pending release leaked to agents: %q", v)
	}
	list := call(releaseListHandler, http.MethodGet, "/update/releases", "", "")
	var resp struct {
		Releases           []AgentRelease `json:"releases"`
		AllReleases        []AgentRelease `json:"all_releases"`
		ActivationRequired bool           `json:"activation_required"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.ActivationRequired || len(resp.Releases) != 1 || resp.Releases[0].Version != "1.1.0" {
		t.Fatalf("active release must stay 1.1.0 while 1.2.0 is pending: %+v", resp)
	}
	pending := 0
	for _, r := range resp.AllReleases {
		if r.PendingActivation {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("expected exactly one pending release in all_releases, got %d", pending)
	}

	// Activation releases it.
	activate(t, "1.2.0")
	if ok, v := checkUpdate(t, "1.1.0"); !ok || v != "1.2.0" {
		t.Fatalf("after activation expected update to 1.2.0, got %v %q", ok, v)
	}
}

func TestReleaseControlRollbackOnlyForTargetedDevices(t *testing.T) {
	useTestMongo(t)
	t.Setenv("RELEASES_REQUIRE_ACTIVATION", "")
	publish(t, "1.0.0")
	publish(t, "1.1.0")
	seedDevice(t, "1.1.0")

	// Admin re-activates 1.0.0 (rollback). A plain activation never downgrades.
	activate(t, "1.0.0")
	if ok, v := checkUpdate(t, "1.1.0"); ok {
		t.Fatalf("downgrade offered without an explicit rollout: %q", v)
	}

	// Explicit rollout of the ACTIVE release marks the device and allows it.
	w := call(releaseRolloutLatestHandler, http.MethodPost, "/update/release/rollout-latest", `{"use_active":true}`, "")
	if w.Code != 200 {
		t.Fatalf("rollout: %d %s", w.Code, w.Body.String())
	}
	if ok, v := checkUpdate(t, "1.1.0"); !ok || v != "1.0.0" {
		t.Fatalf("targeted rollback expected to 1.0.0, got %v %q", ok, v)
	}

	// Once the device runs 1.0.0 it reports up to date and the target is cleared.
	seedDevice(t, "1.0.0")
	if ok, _ := checkUpdate(t, "1.0.0"); ok {
		t.Fatal("device already on the active release must not update")
	}
	var d bson.M
	if err := mongoClient.Database(dbName).Collection("devices").FindOne(context.Background(), bson.M{"device_id": "d1"}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if _, still := d["agent_target_version"]; still {
		t.Fatalf("rollback target not cleared: %#v", d)
	}
}

func TestReleaseSignatureGatesUpdates(t *testing.T) {
	useTestMongo(t)
	t.Setenv("RELEASES_REQUIRE_ACTIVATION", "")
	t.Setenv("UPDATE_REQUIRE_SIGNATURE", "true")
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	t.Setenv("UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
	seedDevice(t, "1.0.0")
	publish(t, "1.1.0") // checksum "abc" from the helper

	// Unsigned: never offered while signatures are required.
	if ok, v := checkUpdate(t, "1.0.0"); ok {
		t.Fatalf("unsigned release offered: %q", v)
	}

	// A signature that does not verify is rejected and changes nothing.
	bad := base64.StdEncoding.EncodeToString(make([]byte, 64))
	body := fmt.Sprintf(`{"os":"linux","arch":"amd64","version":"1.1.0","signature":%q}`, bad)
	if w := call(releaseSignatureHandler, http.MethodPost, "/update/release/signature", body, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("forged signature: %d %s", w.Code, w.Body.String())
	}
	if ok, _ := checkUpdate(t, "1.0.0"); ok {
		t.Fatal("rejected signature must not release the update")
	}

	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, releaseSigningMessage("1.1.0", "linux", "amd64", "abc")))
	body = fmt.Sprintf(`{"os":"linux","arch":"amd64","version":"1.1.0","signature":%q}`, good)
	if w := call(releaseSignatureHandler, http.MethodPost, "/update/release/signature", body, ""); w.Code != 200 {
		t.Fatalf("valid signature: %d %s", w.Code, w.Body.String())
	}
	w := call(updateCheckHandler, http.MethodGet, "/update/check?version=1.0.0&os=linux&arch=amd64", "", "agent-key")
	var out struct {
		Available bool   `json:"update_available"`
		Version   string `json:"version"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || out.Version != "1.1.0" || out.Signature != good {
		t.Fatalf("signed release must be offered with its signature: %+v", out)
	}
}

func TestReleaseControlDefaultRolloutUnchanged(t *testing.T) {
	useTestMongo(t)
	t.Setenv("RELEASES_REQUIRE_ACTIVATION", "")
	publish(t, "1.0.0")
	publish(t, "1.1.0")
	seedDevice(t, "1.0.0")

	// Empty body = original behaviour: roll out the highest version.
	w := call(releaseRolloutLatestHandler, http.MethodPost, "/update/release/rollout-latest", "", "")
	if w.Code != 200 {
		t.Fatalf("rollout: %d %s", w.Code, w.Body.String())
	}
	if ok, v := checkUpdate(t, "1.0.0"); !ok || v != "1.1.0" {
		t.Fatalf("expected rollout to 1.1.0, got %v %q", ok, v)
	}
}
