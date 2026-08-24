# DevicePulse

Enterprise endpoint telemetry platform. Deploy a lightweight Go agent on any machine and get live system data — hardware stats, processes, browser history, active-window focus tracking, installed apps, open ports, services, USB devices, and OS updates — streamed to a central API and visualised in a real-time dashboard.

```
┌─────────────┐   register / ingest   ┌─────────────────┐   polling   ┌──────────────────┐
│  Agent (Go) │ ───────────────────►  │   API (Go + DB) │ ◄────────── │ Dashboard (Next) │
│  runs on    │ ◄─────────────────── │   MongoDB Atlas  │             │  localhost:3000   │
│  endpoint   │   policy + updates    └─────────────────┘             └──────────────────┘
└─────────────┘
```

---

## Components

| Component | Stack | Default port |
|-----------|-------|--------------|
| `agent/`  | Go 1.25, gopsutil, modernc/sqlite | — |
| `api/`    | Go 1.23, stdlib net/http, MongoDB driver | 8000 |
| `dashboard/` | Next.js 16, React 19, TypeScript, Tailwind 4 | 3000 |

---

## Quick Start

### 1. Prerequisites

- Go 1.25+ for the agent; Go 1.23+ for the API
- Node.js 18+
- A MongoDB instance (local or Atlas)

### 2. Start the API

```bash
cd api
cp .env.example .env          # or create api/.env manually
# Set MONGO_URI in api/.env
go run .
# Listening on :8000
```

**`api/.env`**
```
MONGO_URI=mongodb://localhost:27017
SESSION_SECRET=replace-with-a-strong-random-secret
DASHBOARD_ORIGIN=http://localhost:3000
```

### 3. Start the Agent

```bash
cd agent
go run .
# Registers with the API, begins collecting, syncs every 10s
```

On first run the agent collects the hardware fingerprint (hardware UUID + MAC address), calls `POST /devices/register`, and writes credentials to `agent/data/registration.json`. Subsequent restarts reuse those credentials. If the binary is moved to a different machine the fingerprint check fails and the new device registers fresh.

### 4. Start the Dashboard

```bash
cd dashboard
npm install
npm run dev
# Open http://localhost:3000
```

---

## Agent

### What it collects

| Collector | Data | Interval |
|-----------|------|----------|
| `SystemInfo` | Hostname, OS, architecture, kernel | Every sync |
| `HardwareStats` | CPU %, RAM, disk usage, network I/O, battery | Every sync |
| `ProcessMonitor` | Top processes (CPU + memory %) | Every sync |
| `BrowserHistory` | Incremental recent browser visits across Chrome, Edge, Firefox, Safari | Every sync |
| `ActiveWindowTracker` | Current foreground app, per-app focus duration | Every sync |
| `Services` | Running/stopped system services | Every sync |
| `NetworkPorts` | Open TCP/UDP ports with process names | Every sync |
| `InstalledApps` | Installed applications list | Every 60s (cached) |
| `OSUpdates` | Last update time, pending update count | Every 60s (cached) |
| `USBEvents` | Connected USB devices | Every sync |

### Offline queue

Telemetry is written to a local SQLite database (`agent/data/devicepulse.db`) before being uploaded. If the API is unreachable, data queues locally and is flushed in batches of 50 when connectivity is restored.

### Device fingerprinting

On registration the agent collects:

- **Hardware UUID** — read from SMBIOS/DMI (Linux/Windows) or IOKit (macOS) via `gopsutil`. Survives OS reinstalls on the same hardware.
- **MAC address** — primary non-loopback, non-virtual network interface. Normalised to lower-case with no separators.

The API deduplicates registrations in priority order: `hardware_uuid → mac_address → hostname`. A device that reinstalls its OS gets back the same `device_id` and `api_key` automatically.

The cached `registration.json` is validated against the current hardware fingerprint on every startup. If the binary is copied to a different machine the mismatch triggers a fresh registration.

### Auto-update

The agent polls `GET /update/check` every hour (with a 30-second startup delay). When the server signals a new version the agent:

1. Downloads the new binary from the provided URL.
2. Verifies the SHA-256 checksum — aborts if it does not match.
3. Atomically replaces the running executable (`os.Rename`).
4. Re-execs into the new binary via `syscall.Exec` — zero downtime.

### Platform support

| Feature | macOS | Linux | Windows |
|---------|-------|-------|---------|
| Active window | `osascript` | `xdotool` | PowerShell |
| Browser history | ✓ | ✓ | ✓ |
| Hardware stats | ✓ | ✓ | ✓ |
| Services | ✓ | ✓ | ✓ |

> Safari history requires Full Disk Access for the agent process.  
> Linux active window tracking requires `xdotool` to be installed.  
> macOS active window tracking requires Automation permission for the terminal / app running the agent.

### Build binary

```bash
cd agent

# Local development (hits http://localhost:8000)
go build -o agent_bin .

# Production build — bake in the remote API URL and version
go build \
  -ldflags "-X main.defaultAPIURL=https://your-ec2-domain.com -X main.agentVersion=1.0.0" \
  -o agent_bin .

./agent_bin
```

**Runtime override (no rebuild needed):**
```bash
DEVICEPULSE_API_URL=https://your-ec2-domain.com ./agent_bin
```

---

## API

Listens on `:8000`. All endpoints support CORS.

In production the API sits behind nginx on EC2, which handles TLS termination:

```
Agent ──HTTPS──► nginx (EC2, port 443) ──HTTP──► Go API (localhost:8000)
```

Get a free TLS certificate with Certbot:
```bash
sudo certbot --nginx -d your-ec2-domain.com
```

### Authentication

Agents authenticate via the `X-API-Key` header using the key returned at registration.

Dashboard users authenticate with an HTTP-only `devicepulse_session` cookie. On a fresh database, the dashboard registration page creates the first `admin` user. After that, registration is closed and only an `admin` can create additional dashboard users from the Access page.

Dashboard roles:

| Role | Access |
|------|--------|
| `admin` | Full access, including user creation and device deletion |
| `manager` | View telemetry and update policy/settings |
| `viewer` | Read-only telemetry and settings access |

### Console password reset

Dashboard password resets are intentionally console-only. Run this from `api/` with `MONGO_URI` available in `.env` or the environment:

```bash
go run . reset-password --email admin@example.com --password 'new-password-min-8-chars'
```

The command can reset an admin's own password or any other dashboard user's password. It updates the stored bcrypt hash directly and reactivates the user account.

### Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/devices/register` | — | Register a device (deduplicates by hardware UUID → MAC → hostname) |
| `GET` | `/health` | — | API health check |
| `GET` | `/auth/bootstrap` | — | Check whether first-admin bootstrap is required |
| `POST` | `/auth/register` | — | Create the first dashboard admin only |
| `POST` | `/auth/login` | — | Login dashboard user and set session cookie |
| `POST` | `/auth/logout` | Dashboard session | Clear dashboard session cookie |
| `GET` | `/auth/me` | Dashboard session | Return current dashboard user |
| `GET` | `/users` | `admin` | List dashboard users |
| `POST` | `/users` | `admin` | Create a dashboard user |
| `POST` | `/users/{id}/password` | `admin` | Reset a dashboard user's password |
| `POST` | `/users/{id}/role` | `admin` | Change a dashboard user's role |
| `GET` | `/devices` | `viewer`+ | List all registered devices |
| `DELETE` | `/devices/{id}` | `admin` | Remove a device |
| `GET` | `/devices/{id}/history` | `viewer`+ | Last 100 telemetry snapshots for a device |
| `GET` | `/devices/{id}/browser-history?from=YYYY-MM-DD&to=YYYY-MM-DD` | `viewer`+ | Browser history entries archived in S3 for a date range |
| `POST` | `/ingest` | ✓ X-API-Key | Accept a telemetry payload |
| `GET` | `/policy` | `viewer`+ | Read the current global policy |
| `POST` | `/policy` | `manager`+ | Update the global policy (e.g. `sync_interval_seconds`) |
| `GET` | `/focus/{device_id}` | `viewer`+ | Cumulative per-app focus totals |
| `GET` | `/update/check` | ✓ X-API-Key | Check for a new agent binary (used by auto-updater) |
| `POST` | `/update/release` | `admin` | Publish a new agent release |

### Publishing an agent release

After building a new agent binary, compute its SHA-256 and upload it somewhere reachable (S3, GitHub Releases, etc.), then notify the API:

```bash
# Compute checksum
shasum -a 256 agent_bin
# a1b2c3... agent_bin

curl -X POST https://your-ec2-domain.com/update/release \
  -H "Content-Type: application/json" \
  -d '{
    "version":          "1.1.0",
    "os":               "darwin",
    "arch":             "arm64",
    "download_url":     "https://your-bucket.s3.amazonaws.com/agent-1.1.0-darwin-arm64",
    "checksum_sha256":  "a1b2c3..."
  }'
```

Repeat for each OS/arch combination you support. Agents will pick up the update within one hour.

### MongoDB collections

| Collection | Contents |
|------------|----------|
| `devices` | Registered devices (hardware_uuid, mac_address, hostname) + latest telemetry snapshot |
| `telemetry` | Every ingest payload (time-series) |
| `agent_releases` | Published agent binaries with version, OS, arch, URL, checksum |

### Build binary

```bash
cd api
go build -o api_bin .
./api_bin
```

---

## Dashboard

Single-page Next.js app that polls the API every 5 seconds.

### Tabs (per device)

| Tab | Shows |
|-----|-------|
| 📊 Hardware | CPU / RAM / Disk / Network gauges + battery indicator |
| ⚙️ Processes | Top processes with CPU and memory bars |
| 🌐 Browser | Recent browser history with favicons, domain, and visit time |
| 🔧 Services | Running/stopped services with filter tabs |
| 🔌 Ports | Open ports table (protocol, address, state, process, PID) |
| 📦 Apps | Installed apps with search |
| 🛡 Security | OS update status + connected USB devices |
| 🎯 Focus | Per-app focus time with cumulative bars |
| 🖥 System | Hostname, OS, architecture, kernel |

### Sidebar panels

- **Global Policy** — slider to adjust agent sync interval (2–60 s) in real time

### Build for production

```bash
cd dashboard
npm run build
npm start
```

---

## Project Structure

```
DevicePulse/
├── agent/
│   ├── main.go                  # Entry point: registration, sync engine, collection loop, update poller
│   ├── updater/
│   │   └── updater.go           # Auto-update: version check, download, checksum verify, re-exec
│   ├── collector/
│   │   ├── collector.go         # Collector interface
│   │   ├── system_info.go       # SystemInfo + GetHardwareFingerprint (UUID, MAC)
│   │   ├── hardware_stats.go
│   │   ├── process_monitor.go
│   │   ├── browser_history.go
│   │   ├── active_window.go
│   │   ├── services.go
│   │   ├── network_ports.go
│   │   ├── installed_apps.go
│   │   ├── os_updates.go
│   │   └── usb_events.go
│   ├── queue/
│   │   └── queue.go             # SQLite offline queue
│   └── data/
│       ├── devicepulse.db       # Local telemetry queue (auto-created)
│       └── registration.json    # Device credentials + hardware fingerprint (auto-created)
├── api/
│   ├── main.go                  # HTTP server, MongoDB handlers, focus cache, update endpoints
│   └── .env                     # MONGO_URI
└── dashboard/
    └── src/app/
        └── page.tsx             # Full dashboard UI
```

---

## Environment Variables

| Variable | Component | Default | Description |
|----------|-----------|---------|-------------|
| `MONGO_URI` | API | — | MongoDB connection string (required) |
| `ADMIN_SECRET` | API | — | Admin secret for privileged endpoints such as policy updates and release publishing |
| `DASHBOARD_TOKEN` | API | — | Optional token required by read endpoints when configured |
| `BROWSER_HISTORY_S3_BUCKET` | API | — | S3 bucket for browser-history archive objects; disables S3 archive when unset |
| `BROWSER_HISTORY_S3_PREFIX` | API | `browser-history` | S3 key prefix for browser-history archive objects |
| `BROWSER_HISTORY_S3_ENDPOINT` | API | — | Optional S3-compatible endpoint, for example MinIO |
| `BROWSER_HISTORY_S3_PATH_STYLE` | API | `false` | Force path-style S3 requests; automatically enabled when endpoint is set |
| `DEVICEPULSE_API_URL` | Agent | `http://localhost:8000` | API endpoint override at runtime |
| `NEXT_PUBLIC_API_URL` | Dashboard | `http://localhost:8000` | API URL used by the browser dashboard |
| `NEXT_PUBLIC_DASHBOARD_TOKEN` | Dashboard | — | Dashboard token sent to read endpoints when `DASHBOARD_TOKEN` is configured |
| `NEXT_PUBLIC_ADMIN_SECRET` | Dashboard | — | Admin secret sent for policy updates in simple dashboard deployments |

The agent API URL can also be baked in at build time:
```bash
go build -ldflags "-X main.defaultAPIURL=https://your-ec2-domain.com" -o agent_bin .
```

---

## Deployment (EC2 + nginx)

```
                      ┌──────── EC2 ────────┐
Agents ──HTTPS:443──► │  nginx (TLS)        │
                      │    │                │
                      │    ▼                │
                      │  Go API :8000       │
                      │    │                │
                      │    ▼                │
                      │  MongoDB Atlas      │
                      └─────────────────────┘
                              ▲
                      Dashboard (Next.js)
                      polls API directly
```

1. Launch an EC2 instance (Ubuntu 22.04 recommended).
2. Install Go, build the API binary, run it as a systemd service on `:8000`.
3. Install nginx, point it at `localhost:8000`.
4. Run `sudo certbot --nginx -d your-domain.com` for free TLS.
5. Build agent binaries with `defaultAPIURL` set to `https://your-domain.com`.
6. Distribute agent binaries to endpoint machines.

---

## License

MIT
