# Security Guide

## Secrets Management

### API Secrets (`api/.env`)

```bash
# Required
MONGO_URI=mongodb+srv://<user>:<password>@<cluster>.mongodb.net/

# Required for admin endpoints
ADMIN_SECRET=<random-32-char-hex>

# Optional
PORT=8000
```

**Generate a secure admin secret:**
```bash
openssl rand -hex 32
```

**Never commit** `api/.env` — it's already in `.gitignore`. Use `api/.env.example` as a template.

---

### Dashboard Environment (`dashboard/.env.local`)

```bash
NEXT_PUBLIC_API_URL=http://localhost:8000
NEXT_PUBLIC_DASHBOARD_TOKEN=<same-value-as-api-DASHBOARD_TOKEN-if-configured>
NEXT_PUBLIC_ADMIN_SECRET=<same-value-as-api-ADMIN_SECRET-for-policy-updates>
```

For production builds:
```bash
NEXT_PUBLIC_API_URL=https://your-api-domain.com npm run build
```

Values prefixed with `NEXT_PUBLIC_` are embedded in browser-side JavaScript. For a production admin console, restrict access to trusted admins with network controls, authentication, or a server-side dashboard/proxy before exposing privileged operations.

---

### Agent Configuration

The agent stores credentials in `agent/data/registration.json` after its first registration. This file contains:
- `device_id` — unique device identifier
- `api_key` — authentication token for API calls
- `hardware_uuid` — hardware fingerprint (dedup key)
- `mac_address` — primary network interface MAC

**File permissions:** The agent writes `registration.json` with mode `0600` (owner read/write only).

**Never commit** `agent/data/` — it's in `.gitignore` and contains telemetry + credentials.

---

## Admin Endpoints

The following endpoints require the `X-Admin-Secret` header:

| Endpoint | Method | Purpose |
|---|---|---|
| `/policy` | POST | Update global policy (sync interval) |
| `/update/release` | POST | Publish a new agent binary release |

**If `ADMIN_SECRET` is not set**, these endpoints return `503 Service Unavailable` to prevent accidental open access.

**Example authenticated request:**
```bash
curl -X POST http://localhost:8000/policy \
  -H "Content-Type: application/json" \
  -H "X-Admin-Secret: your-secret-here" \
  -d '{"sync_interval_seconds": 15}'
```

---

## MongoDB Connection Security

### Local Development
```bash
MONGO_URI=mongodb://localhost:27017
```

### Production (MongoDB Atlas)
```bash
MONGO_URI=mongodb+srv://<user>:<password>@<cluster>.mongodb.net/?retryWrites=true&w=majority
```

**Atlas security checklist:**
- ✅ Use a dedicated database user (not the Atlas admin account)
- ✅ Restrict IP allowlist to your API server's public IP
- ✅ Enable TLS/SSL (Atlas default)
- ✅ Rotate passwords quarterly
- ✅ Use a strong password (24+ chars, random)

---

## API Key Generation

Device API keys are generated using `crypto/rand` (cryptographically secure randomness) and encoded as 48-character hex strings.

**Old versions** (before this security patch) used time-based pseudo-random generation. If you deployed an API before this fix, consider rotating all device keys by:
1. Clearing the `devices` collection
2. Forcing all agents to re-register

---

## Data Retention

The API stores:
- **Telemetry metadata**: one MongoDB document per agent sync cycle, with `telemetry_expires_at`
- **Full telemetry payloads**: S3 objects when `TELEMETRY_S3_BUCKET` or `S3_BUCKET` is configured
- **Full browser history**: S3 objects when `BROWSER_HISTORY_S3_BUCKET` or `S3_BUCKET` is configured
- **Focus cache**: in-memory, rebuilt from retained telemetry metadata on startup

Telemetry retention is controlled by the dashboard policy (`telemetry_retention_days`, default 30 days). MongoDB cleanup is enforced by a TTL index on `telemetry_expires_at`. S3 archive retention is controlled by bucket lifecycle rules.

Deleting a device creates a revocation tombstone before removing server data, so the same hardware UUID/MAC cannot re-register and resend queued local telemetry unless the revocation is removed manually.

---

## Exposed Credentials (Incident Response)

**If you accidentally commit a secret:**

1. **Rotate immediately** — don't wait for the PR to merge or branch to be deleted. Treat the secret as compromised.
2. **MongoDB Atlas password**: Database Access → Edit User → Reset Password → update `.env`
3. **Admin secret**: Generate a new one with `openssl rand -hex 32` → update `.env` → restart API
4. **Device API keys**: If the devices collection was exposed, clear it and force agents to re-register

**Git history cleanup** (optional):
```bash
# Remove .env from history (use BFG Repo-Cleaner or git-filter-repo)
git filter-repo --invert-paths --path api/.env
```

---

## Production Deployment Checklist

- [ ] Set `ADMIN_SECRET` in production `.env`
- [ ] Restrict MongoDB Atlas IP allowlist to API server only
- [ ] Run API behind a reverse proxy (nginx) with TLS
- [ ] Use Let's Encrypt for free TLS certificates (`certbot --nginx`)
- [ ] Set `NEXT_PUBLIC_API_URL` to the production HTTPS domain
- [ ] Set up log aggregation (API logs contain auth failures)
- [ ] Back up MongoDB regularly (Atlas automated backups)

---

## Vulnerability Disclosure

**Found a security issue?** Email: security@devicepulse.io (replace with your actual contact)

**Do NOT** open a public GitHub issue for security vulnerabilities.
