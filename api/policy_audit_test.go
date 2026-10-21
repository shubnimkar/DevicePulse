package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolicyAuditVisibleToDashboardOnly(t *testing.T) {
	globalPolicy.mu.Lock()
	globalPolicy.config = defaultPolicy()
	globalPolicy.updatedBy, globalPolicy.updatedAt = "", ""
	globalPolicy.mu.Unlock()

	post := httptest.NewRequest(http.MethodPost, "/policy", strings.NewReader(`{"sync_interval_seconds":30}`))
	post = post.WithContext(context.WithValue(post.Context(), dashboardUserContextKey, SessionClaims{Email: "admin@example.com", Role: RoleAdmin}))
	policyHandler(httptest.NewRecorder(), post)

	get := func(agent bool) map[string]interface{} {
		req := httptest.NewRequest(http.MethodGet, "/policy", nil)
		if agent {
			req.Header.Set("X-API-Key", "k")
		}
		rec := httptest.NewRecorder()
		policyHandler(rec, req)
		var out map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	if got := get(false)["updated_by"]; got != "admin@example.com" {
		t.Fatalf("dashboard GET updated_by = %v", got)
	}
	if _, leaked := get(true)["updated_by"]; leaked {
		t.Fatal("agent GET must not expose updated_by")
	}
}
