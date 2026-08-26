# Backend Audit — `api/`

**Scope:** `/Users/shubhamnimkar/panda/DevicePluse/api` (`main.go` ~4.7k lines, 2 test files, `cmd/backfill_browser_history_s3`, Dockerfile), cross-checked against `agent/` collector semantics where behavior depends on it.
**Date:** 2026-08-26 · **Go:** module `go 1.24` (Dockerfile builds on golang:1.25)

> **✅ Status: all findings implemented (2026-08-26).**
> `go build` / `go vet` clean, 13 tests passing (8 new regression tests added in
> `audit_fixes_test.go`). Behavioral notes for operators live in
> `README.md → Security Hardening`. Known residual: focus totals rebuilt from
> pre-fix telemetry metadata may stay inflated until those docs expire via the
> retention TTL (~30 days).

## Verification performed

| Check | Result |
|---|---|
| `go build ./...` | ✅ pass |
| `go vet ./...` | ✅ clean |
| `go test ./...` | ✅ ok (5 tests) |
| `gofmt -l` | ⚠️ `main.go` unformatted (alignment only) |
| `govulncheck` | not installed (recommend adding to CI) |

---

## Findings summary

| # | Severity | Area | Finding |
|---|---|---|---|
| 1 | **High (data integrity)** | Focus cache | Cumulative focus totals re-added every sync → quadratic over-counting |
| 2 | **High (security)** | Auth | Unauthenticated `/devices/register` returns existing `api_key` on hostname-only match; no rate limiting |
| 3 | **High (perf/DoS)** | History | `/devices/{id}/history` hydrates *every* archived doc via 1 S3 GET each (N+1, unbounded) |
| 4 | **High (perf/DoS)** | Browser history | Unbounded `from`/`to` range → day-by-day S3 List+Get storm (~47k days possible) |
| 5 | **Medium (security)** | HTTP server | `http.ListenAndServe` with zero timeouts (Slowloris); no body limit on non-ingest handlers |
| 6 | **Medium (security)** | Login | No rate limiting / lockout; timing-based user enumeration |
| 7 | **Medium (security)** | Secrets | Device `api_key`s stored in plaintext in MongoDB |
| 8 | **Medium (security)** | Sessions | Password reset doesn't invalidate outstanding session cookies (valid ≤24h) |
| 9 | **Medium (security)** | CORS | Any `http://localhost:*` origin allowed **with credentials** |
| 10 | **Medium (robustness)** | Registration | Duplicate-key race surfaces as 500 instead of returning existing creds |
| 11 | **Medium (robustness)** | Activity archive | S3 read-modify-write without concurrency control → lost sessions |
| 12 | **Medium (ops)** | Docker | Runtime image is full `golang:1.25-bookworm` running as root |
| 13 | **Medium (logic)** | Updates | Equality-only version comparison → can downgrade newer agents |
| 14 | **Medium (perf)** | Startup | Full telemetry scan per device on boot; `ensureIndexes` errors swallowed + async |
| 15 | Low | Policy | `normalizePolicy` blindly merges unknown keys into persisted config |
| 16 | Low | Docs drift | `requireAdmin` dead code; `DASHBOARD_TOKEN` documented but not implemented; README/env docs stale |
| 17 | Low | Hygiene | `gofmt`; redundant `Body.Close()`; release rows grow unbounded; outdated `x/crypto`; no security headers |

---

## High severity

### 1. Focus totals are over-counted every sync (`/focus/{id}`, focus cache)
- Agent (`agent/collector/active_window.go:57-61`): `app_summaries` = delta since last Collect (**reset each cycle**); `cumulative_summaries` = totals since agent start (**never reset**).
- API ingest (`main.go:2870-2884`): prefers `cumulative_summaries` and calls `applyFocusSummaries`, which **adds** (`+=`) into `globalFocusCache` — a device syncing every 10s adds its full since-start total again every cycle. Totals grow quadratically.
- Startup rebuild (`buildFocusCacheFromMongo`, `main.go:129-178`) sums `cumulative_summaries` across *all* retained telemetry docs → same inflation.
- **Fix:** feed the cache from per-cycle `app_summaries` only — both in ingest and startup rebuild — or treat cumulative values as replace-not-add. Add a regression test asserting two identical ingests don't double the total.

### 2. Device registration: credential recovery by hostname, unthrottled
`registerHandler` (`main.go:2213-2317`) is unauthenticated. Dedup priority uuid → mac → **hostname**; on match it returns the existing `api_key`. Anyone who can guess the hostname of a device that registered without uuid/mac receives valid credentials for it. No rate limit → unlimited device-farming/DoS of the collection.
- **Fix:** return existing creds only on uuid/mac match (hostname may seed a *new* registration), add per-IP rate limiting, consider an enrollment token.

### 3. `historyHandler` N+1 S3 hydration, unbounded
`main.go:3238-3301`: `limit` defaults to no limit; each archived doc triggers one `GetObject` sequentially inside a single 30s context. Long-lived device ⇒ thousands of S3 reads, guaranteed timeouts, triggerable by any viewer.
- **Fix:** hard cap results, paginate, hydrate concurrently with a bounded pool, or keep recent payloads in Mongo and hydrate older ones on demand.

### 4. `browserHistoryHandler` unbounded date range
`ReadEntries` (`main.go:1340-1419`) iterates `from`→`to` one calendar day at a time, doing paginated ListObjectsV2 plus a GetObject per object per day. `from=1970-01-01&to=2100-01-01` is accepted (`parseDateQuery` has no clamp).
- **Fix:** clamp the range to a retention/max window (e.g. ≤90 days), cap total days scanned, reject absurd ranges with 400.

---

## Medium severity

### 5. Server hardening gaps
- `http.ListenAndServe(":"+port, rootMux)` (`main.go:4585`) — no `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout` → Slowloris-prone. Use an explicit `http.Server`.
- Only `/ingest` caps the body (5MB, `main.go:2715`). Every other JSON handler decodes unlimited bodies. Apply a global `MaxBytesReader` middleware (≈1MB) with per-route overrides.
- No security headers (`X-Content-Type-Options: nosniff`, etc.).

### 6. Login brute force / enumeration
`authLoginHandler` (`main.go:2409-2440`): no throttling or lockout; unknown email skips bcrypt entirely → timing-based account enumeration. Add per-IP+per-account backoff; always run a dummy bcrypt compare on miss.

### 7. Plaintext device API keys
Devices store `api_key` verbatim; lookup is exact-match (`resolveAPIKey`, `main.go:1887-1902`). A MongoDB leak exposes every endpoint credential. Store SHA-256(key) (unique-indexed) and compare hashes.

### 8. Session lifecycle
Good: HMAC token, and `requireAuth` reloads the user each request enforcing `status:"active"` + DB role (`main.go:1836-1872`). Gap: nothing invalidates outstanding tokens after **password reset**; logout is client-side only. Add a per-user `token_epoch` bumped on password change and verify it in `requireAuth`.

### 9. CORS
`corsMiddleware` (`main.go:1638-1660`) reflects any origin starting `http://localhost:` **with credentials enabled** — any page served on localhost can make credentialed calls against the API. Restrict to `DASHBOARD_ORIGIN` (+ explicit dev origins); omit credentials otherwise.

### 10. Registration duplicate-key race
Two simultaneous first-registrations for the same uuid both mint the same derived `device_id`; the second insert hits the unique index → 500. Catch `IsDuplicateKeyError`, re-fetch, return existing creds.

### 11. DailyActivityArchive read-modify-write race
`Archive` (`main.go:512-587`) GETs the day object, merges, PUTs back — no If-Match/versioning. Concurrent ingests (queue drain + live sync) can last-write-wins and lose sessions. Use conditional PUTs with retry, or merge in Mongo and write S3 once.

### 12. Container posture
Runtime stage is full `golang:1.25-bookworm` as root (`api/Dockerfile`). Add a non-root `USER`; keep the toolchain only because in-container agent builds are intended (`AGENT_BUILD_ROOT=/workspace`), but still drop privileges.

### 13. Update downgrades
`updateCheckHandler` (`main.go:3557`) treats latest ≠ current as update-available without comparing versions — a device on a newer build is told to install an older one. Use `compareAgentVersions(latest.Version, currentVersion) > 0`.

### 14. Startup work
- `buildFocusCacheFromMongo` does `Distinct` + full scan per device every boot; cost grows with fleet × retention.
- `ensureIndexes()` runs in a goroutine and discards all results/errors (`main.go:4559`, `4645-4717`). Run synchronously before listening; log/fail loudly on error.

---

## Low severity

15. **Policy schema:** `normalizePolicy` copies unknown keys into the persisted config document (even `_id`, which would break `$set` persistence silently). Whitelist known keys.
16. **Dead code / doc drift:** `requireAdmin` (`main.go:1904-1920`) is never routed — routes use dashboard roles (`POST /policy`=manager, releases=admin). `SECURITY.md` / `.env.example` (ADMIN_SECRET semantics, `DASHBOARD_TOKEN`) and README (Go 1.23 vs go.mod 1.24) don't match the code; `ACTIVITY_S3_*` env vars are undocumented. If kept as break-glass, compare the admin secret via `hmac.Equal` (currently plain `!=`).
17. **Hygiene:** run `gofmt -w main.go`; drop the redundant `defer r.Body.Close()` after `io.ReadAll` (`main.go:2732`); rollout/activate insert duplicate release rows forever; bump `golang.org/x/crypto` (v0.26.0 → current) and add `govulncheck` to CI; prune stale `FocusCache` entries for devices that stopped reporting.

---

## What’s done well 👍

- Parameterized BSON everywhere — no injection-class issues found; S3 key segments sanitized.
- Ingest binds `device_id` server-side from the authenticated key; 5MB body cap.
- Command pipeline: per-type role matrix, atomic claim (`FindOneAndUpdate`), ownership-checked results, TTL expiry.
- Sessions: bcrypt, HttpOnly+SameSite cookie, DB-backed role/status re-check per request, `hmac.Equal` signature compare, bootstrap lock doc + stale-lock cleanup against first-admin races.
- Revocation tombstones before deletion block replay of queued telemetry; unique/sparse/TTL indexes on hot paths; projections exclude `api_key`.
- `crypto/rand` API keys with fail-fast; generic auth error messages.

## Suggested priority order

1. Fix focus-cache double counting (#1) — silent data corruption today.
2. Clamp history/browser-history read paths (#3, #4) — cheapest DoS fix.
3. Registration hardening (#2, #10) + login rate limiting (#6).
4. Server timeouts/body limits (#5), session epoch (#8), hashed API keys (#7).
5. Version-compare fix (#13), CORS tightening (#9), container user (#12).
6. Cleanup batch (#14–17) incl. `gofmt -w` and docs sync.

