package main

import (
	"net"
	"net/url"
	"os"
	"strings"
)

// Only a loopback HTTP dashboard may use non-Secure cookies. A stale
// COOKIE_SECURE=false setting must not weaken a production dashboard.
func secureSessionCookies() bool {
	if strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true") {
		return true
	}
	for origin := range allowedOrigins() {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" {
			return true
		}
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return true
		}
	}
	return false
}
