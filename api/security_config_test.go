package main

import (
	"net/http/httptest"
	"testing"
)

func TestSecureSessionCookies(t *testing.T) {
	for _, tc := range []struct {
		origin, extra, flag string
		secure              bool
	}{
		{"http://localhost:3000", "", "false", false},
		{"http://127.0.0.1:3000", "", "", false},
		{"http://[::1]:3000", "", "", false},
		{"http://localhost:3000", "", "true", true},
		{"https://devicepulse.waybeyond.tech", "", "false", true},
		{"http://devicepulse.waybeyond.tech", "", "false", true},
		{"http://localhost:3000", "https://devicepulse.waybeyond.tech", "", true},
	} {
		t.Run(tc.origin+tc.extra+tc.flag, func(t *testing.T) {
			t.Setenv("DASHBOARD_ORIGIN", tc.origin)
			t.Setenv("DASHBOARD_EXTRA_ORIGINS", tc.extra)
			t.Setenv("COOKIE_SECURE", tc.flag)
			if got := secureSessionCookies(); got != tc.secure {
				t.Fatalf("Secure=%v, want %v", got, tc.secure)
			}
			w := httptest.NewRecorder()
			if err := setSessionCookie(w, DashboardUser{}); err != nil {
				t.Fatal(err)
			}
			cookie := w.Result().Cookies()[0]
			if cookie.Secure != tc.secure || !cookie.HttpOnly {
				t.Fatal("incorrect login cookie flags")
			}
			w = httptest.NewRecorder()
			clearSessionCookie(w)
			cookie = w.Result().Cookies()[0]
			if cookie.Secure != tc.secure || !cookie.HttpOnly || cookie.MaxAge != -1 {
				t.Fatal("incorrect logout cookie flags")
			}
		})
	}
}
