'use client';

import { useEffect, useState, useCallback } from 'react';

const API = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080';
const DASHBOARD_TOKEN = process.env.NEXT_PUBLIC_DASHBOARD_TOKEN ?? '';

// ─── Types ────────────────────────────────────────────────────────────────────

interface ProcessData {
  pid: number;
  name: string;
  cpu: number;
  memory: number;
}

interface HistoryEntry {
  url: string;
  title: string;
  last_visit_time: number;
  browser: string;
}

interface SystemInfo {
  hostname: string;
  os: string;
  architecture: string;
  num_cpus: number;
  platform: string;
  platform_version: string;
  kernel_version: string;
}

interface CPUStat {
  usage_percent: number;
  core_count: number;
}

interface RAMStat {
  total_gb: number;
  used_gb: number;
  free_gb: number;
  used_percent: number;
}

interface DiskStat {
  mount: string;
  total_gb: number;
  used_gb: number;
  free_gb: number;
  used_percent: number;
}

interface NetStat {
  interface: string;
  bytes_sent: number;
  bytes_recv: number;
}

interface BatteryStat {
  available: boolean;
  percent: number;
  plugged: boolean;
  charging: boolean;
  charge_rate_w: number;
  state: 'charging' | 'discharging' | 'full' | 'empty' | 'idle' | 'unknown';
}

interface HardwareStats {
  cpu: CPUStat;
  ram: RAMStat;
  disks: DiskStat[];
  network: NetStat[];
  battery?: BatteryStat;
  uptime_human: string;
}

// ── New collector types ───────────────────────────────────────────────────────

interface ServiceEntry {
  name: string;
  status: 'running' | 'stopped' | 'unknown';
  pid?: string;
}

interface PortEntry {
  protocol: string;
  local_addr: string;
  state?: string;
  pid?: number;
  process?: string;
}

interface AppEntry {
  name: string;
  version?: string;
  bundle_id?: string;
  path?: string;
  source: string;
}

interface USBDevice {
  name: string;
  vendor_id?: string;
  product_id?: string;
  manufacturer?: string;
  serial_number?: string;
  speed?: string;
}

interface OSUpdateInfo {
  last_update_time?: string;
  last_update_raw?: string;
  pending_updates?: string[];
  pending_count: number;
  source: string;
}

interface AppFocusSummary {
  app_name: string;
  total_focus_seconds: number;
  session_count: number;
}

interface ActiveWindowData {
  current_app: string;
  app_summaries: AppFocusSummary[];        // last sync window (~10s)
  cumulative_summaries: AppFocusSummary[]; // since agent start
}

// Persistent cached focus totals from the API focus cache
interface FocusCacheData {
  device_id: string;
  app_summaries: AppFocusSummary[];
}

interface DeviceData {
  SystemInfo?: SystemInfo;
  ProcessMonitor?: { top_processes: ProcessData[] };
  BrowserHistory?: { top_recent_urls: HistoryEntry[] };
  HardwareStats?: HardwareStats;
  Services?: { services: ServiceEntry[]; source?: string };
  NetworkPorts?: { open_ports: PortEntry[]; source?: string };
  InstalledApps?: { installed_apps: AppEntry[]; count: number; source?: string };
  USBEvents?: { usb_devices: USBDevice[]; count: number; source?: string };
  OSUpdates?: { os_updates: OSUpdateInfo };
  ActiveWindowTracker?: ActiveWindowData;
}

interface Device {
  device_id: string;
  hostname?: string;
  timestamp?: string;
  last_seen?: string;
  data?: DeviceData;
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// authHeaders returns request init options with the dashboard read token set.
// Falls back gracefully when NEXT_PUBLIC_DASHBOARD_TOKEN is not configured.
function readHeaders(): HeadersInit {
  return DASHBOARD_TOKEN ? { 'X-Dashboard-Token': DASHBOARD_TOKEN } : {};
}

function getDomain(url: string): string {
  try { return new URL(url).hostname; } catch { return url; }
}

function formatVisitTime(nanos: number): string {
  if (!nanos) return '';
  const ms = nanos / 1_000_000;
  const diff = Date.now() - ms;
  if (diff < 60_000)    return 'just now';
  if (diff < 3600_000)  return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86400_000) return `${Math.floor(diff / 3600_000)}h ago`;
  return new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function isOnline(lastSeen?: string): boolean {
  if (!lastSeen) return false;
  return Date.now() - new Date(lastSeen).getTime() < 60_000;
}

function formatBytes(bytes: number): string {
  if (bytes >= 1e9) return (bytes / 1e9).toFixed(1) + ' GB';
  if (bytes >= 1e6) return (bytes / 1e6).toFixed(1) + ' MB';
  if (bytes >= 1e3) return (bytes / 1e3).toFixed(1) + ' KB';
  return bytes + ' B';
}

function browserEmoji(browser: string): string {
  const b = (browser || '').toLowerCase();
  if (b.includes('chrome'))  return '🟡';
  if (b.includes('firefox')) return '🦊';
  if (b.includes('safari'))  return '🧭';
  if (b.includes('edge'))    return '🌊';
  return '🌐';
}

function browserClass(browser: string): string {
  const b = (browser || '').toLowerCase();
  if (b.includes('chrome'))  return 'browser-chrome';
  if (b.includes('firefox')) return 'browser-firefox';
  if (b.includes('safari'))  return 'browser-safari';
  if (b.includes('edge'))    return 'browser-edge';
  return 'browser-unknown';
}

function faviconUrl(url: string): string {
  return `https://www.google.com/s2/favicons?domain=${getDomain(url)}&sz=32`;
}

function cpuColor(pct: number): string {
  if (pct > 80) return 'var(--danger-color)';
  if (pct > 50) return 'var(--warning-color)';
  return 'var(--success-color)';
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function GaugeBar({ value, color }: { value: number; color: string }) {
  return (
    <div className="gauge-wrap">
      <div className="gauge-bar" style={{ width: `${Math.min(value, 100)}%`, background: color }} />
    </div>
  );
}

function StatBlock({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}</div>
      {sub && <div className="metric-sub">{sub}</div>}
    </div>
  );
}

// ─── Battery Card ─────────────────────────────────────────────────────────────

function BatteryCard({ battery }: { battery?: BatteryStat }) {
  if (!battery?.available) return null;

  const pct   = Math.min(Math.max(battery.percent ?? 0, 0), 100);
  const state = battery.state ?? 'unknown';
  // Guard against NaN coming from the agent during macOS state transitions
  const rateW = Number.isFinite(battery.charge_rate_w) ? battery.charge_rate_w : 0;

  // Color: green when plugged/charging/full/idle, red when critically low, else amber
  const color =
    state === 'charging' || state === 'full' || state === 'idle'
      ? 'var(--success-color)'
      : pct <= 20
        ? 'var(--danger-color)'
        : pct <= 40
          ? 'var(--warning-color)'
          : 'var(--success-color)';

  // Icon
  const icon =
    state === 'charging' ? '⚡'
    : state === 'idle'   ? '🔌'
    : state === 'full'   ? '🔋'
    : pct <= 20          ? '🪫'
    : '🔋';

  // Human label
  const stateLabel: Record<string, string> = {
    charging:    'Charging',
    full:        'Full',
    idle:        'Plugged In', // capacity-saving, not actively charging
    discharging: 'Discharging',
    empty:       'Empty',
    unknown:     'Unknown',
  };

  return (
    <div className="hw-card battery-card">
      <div className="hw-card-title">Battery</div>

      {/* Percentage + icon */}
      <div className="battery-top">
        <span className="hw-big-value" style={{ color }}>{pct.toFixed(0)}%</span>
        <span className="battery-icon">{icon}</span>
      </div>

      {/* Visual battery bar */}
      <div className="battery-shell">
        <div className="battery-fill" style={{ width: `${pct}%`, background: color }} />
        <div className="battery-nub" />
      </div>

      {/* State badge */}
      <div className="battery-state" style={{ color }}>
        {stateLabel[state] ?? state}
      </div>

      {/* Charge rate — only show when non-zero and finite */}
      {rateW !== 0 && (
        <div className="hw-sub">
          {rateW > 0 ? '+' : ''}{rateW.toFixed(1)} W
        </div>
      )}
    </div>
  );
}

// ─── HardwareStats Section ────────────────────────────────────────────────────

function HardwareSection({ hw }: { hw?: HardwareStats }) {
  if (!hw) return <div className="no-data">No hardware data yet.</div>;

  const cpu  = hw.cpu;
  const ram  = hw.ram;
  const disk = hw.disks?.[0];
  const net  = hw.network?.[0];

  return (
    <div className="hw-grid">
      {/* CPU */}
      <div className="hw-card">
        <div className="hw-card-title">CPU</div>
        <div className="hw-big-value" style={{ color: cpuColor(cpu?.usage_percent ?? 0) }}>
          {cpu?.usage_percent?.toFixed(1) ?? '—'}%
        </div>
        <GaugeBar value={cpu?.usage_percent ?? 0} color={cpuColor(cpu?.usage_percent ?? 0)} />
        <div className="hw-sub">{cpu?.core_count ?? '?'} logical cores</div>
      </div>

      {/* RAM */}
      <div className="hw-card">
        <div className="hw-card-title">Memory</div>
        <div className="hw-big-value" style={{ color: cpuColor(ram?.used_percent ?? 0) }}>
          {ram?.used_percent?.toFixed(1) ?? '—'}%
        </div>
        <GaugeBar value={ram?.used_percent ?? 0} color={cpuColor(ram?.used_percent ?? 0)} />
        <div className="hw-sub">
          {ram?.used_gb?.toFixed(1) ?? '?'} / {ram?.total_gb?.toFixed(1) ?? '?'} GB
        </div>
      </div>

      {/* Disk */}
      <div className="hw-card">
        <div className="hw-card-title">Disk {disk?.mount}</div>
        <div className="hw-big-value" style={{ color: cpuColor(disk?.used_percent ?? 0) }}>
          {disk?.used_percent?.toFixed(1) ?? '—'}%
        </div>
        <GaugeBar value={disk?.used_percent ?? 0} color={cpuColor(disk?.used_percent ?? 0)} />
        <div className="hw-sub">
          {disk ? `${disk.used_gb.toFixed(1)} / ${disk.total_gb.toFixed(1)} GB` : 'No disk'}
        </div>
      </div>

      {/* Network */}
      <div className="hw-card">
        <div className="hw-card-title">Network {net?.interface}</div>
        <div className="hw-net-row">
          <span className="hw-net-label">↑</span>
          <span className="hw-net-val">{net ? formatBytes(net.bytes_sent) : '—'}</span>
        </div>
        <div className="hw-net-row">
          <span className="hw-net-label">↓</span>
          <span className="hw-net-val">{net ? formatBytes(net.bytes_recv) : '—'}</span>
        </div>
        {hw.uptime_human && (
          <div className="hw-sub">Up {hw.uptime_human}</div>
        )}
      </div>

      {/* Battery — only shown when available */}
      <BatteryCard battery={hw.battery} />
    </div>
  );
}

// ─── Services Section ─────────────────────────────────────────────────────────

function ServicesSection({ data }: { data?: { services: ServiceEntry[] | null; source?: string } }) {
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all');
  const services = data?.services ?? [];
  if (!services.length) return <div className="no-data">No service data yet.</div>;

  const filtered = filter === 'all' ? services : services.filter(s => s.status === filter);
  const runCount = services.filter(s => s.status === 'running').length;
  const stopCount = services.filter(s => s.status === 'stopped').length;

  return (
    <div>
      <div className="collector-header">
        <div className="collector-counts">
          <span className="count-pill count-running">▶ {runCount} running</span>
          <span className="count-pill count-stopped">■ {stopCount} stopped</span>
          {data?.source && <span className="count-pill count-source">{data.source}</span>}
        </div>
        <div className="filter-tabs">
          {(['all', 'running', 'stopped'] as const).map(f => (
            <button key={f} className={`filter-btn ${filter === f ? 'filter-active' : ''}`}
              onClick={() => setFilter(f)}>{f}</button>
          ))}
        </div>
      </div>
      <div className="service-grid">
        {filtered.map((s, i) => (
          <div key={i} className={`service-item ${s.status}`}>
            <div className={`service-dot ${s.status}`} />
            <div className="service-name" title={s.name}>{s.name}</div>
            {s.pid && <div className="service-pid">PID {s.pid}</div>}
          </div>
        ))}
      </div>
    </div>
  );
}

// ─── Network Ports Section ────────────────────────────────────────────────────

function NetworkPortsSection({ data }: { data?: { open_ports: PortEntry[] | null; source?: string } }) {
  const ports = data?.open_ports ?? [];
  if (!ports.length) return <div className="no-data">No open ports detected.</div>;

  return (
    <div>
      <div className="collector-header">
        <span className="count-pill count-running">🔌 {ports.length} open ports</span>
        {data?.source && <span className="count-pill count-source">{data.source}</span>}
      </div>
      <table className="ports-table">
        <thead>
          <tr>
            <th>Protocol</th>
            <th>Address</th>
            <th>State</th>
            <th>Process</th>
            <th>PID</th>
          </tr>
        </thead>
        <tbody>
          {ports.map((p, i) => (
            <tr key={i}>
              <td><span className={`proto-badge proto-${p.protocol.replace('4','').replace('6','')}`}>{p.protocol.toUpperCase()}</span></td>
              <td><span className="mono-text">{p.local_addr}</span></td>
              <td><span className="state-text">{p.state ?? '—'}</span></td>
              <td><span className="process-badge">{p.process ?? '—'}</span></td>
              <td><span className="mono-text muted">{p.pid ?? '—'}</span></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ─── Installed Apps Section ───────────────────────────────────────────────────

function InstalledAppsSection({ data }: { data?: { installed_apps: AppEntry[] | null; count: number; source?: string } }) {
  const [search, setSearch] = useState('');
  const allApps = data?.installed_apps ?? [];
  if (!allApps.length) return <div className="no-data">No installed apps data yet.</div>;

  const filtered = search
    ? allApps.filter(a => a.name.toLowerCase().includes(search.toLowerCase()))
    : allApps;

  return (
    <div>
      <div className="collector-header">
        <span className="count-pill count-running">📦 {data?.count ?? allApps.length} apps</span>
        {data?.source && <span className="count-pill count-source">{data.source}</span>}
        <input
          className="app-search"
          placeholder="Search…"
          value={search}
          onChange={e => setSearch(e.target.value)}
        />
      </div>
      <div className="apps-grid">
        {filtered.slice(0, 60).map((app, i) => (
          <div key={i} className="app-item" title={app.path}>
            <div className="app-name">{app.name}</div>
            <div className="app-version">{app.version ?? '—'}</div>
          </div>
        ))}
        {filtered.length > 60 && (
          <div className="no-data" style={{ gridColumn: '1/-1', padding: '0.75rem' }}>
            +{filtered.length - 60} more
          </div>
        )}
      </div>
    </div>
  );
}

// ─── Security Section (USB + OS Updates) ─────────────────────────────────────

function SecuritySection({
  usb,
  osUpd,
}: {
  usb?: { usb_devices: USBDevice[] | null; count: number; source?: string };
  osUpd?: { os_updates: OSUpdateInfo };
}) {
  const upd = osUpd?.os_updates;
  const usbDevices = usb?.usb_devices ?? [];

  return (
    <div className="security-grid">
      {/* OS Updates */}
      <div className="security-card">
        <div className="security-card-title">🔄 OS Updates</div>
        {upd ? (
          <>
            <div className="security-row">
              <span className="security-label">Source</span>
              <span className="security-value mono">{upd.source}</span>
            </div>
            <div className="security-row">
              <span className="security-label">Last Updated</span>
              <span className="security-value">
                {upd.last_update_time
                  ? new Date(upd.last_update_time).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
                  : upd.last_update_raw ?? '—'}
              </span>
            </div>
            <div className="security-row">
              <span className="security-label">Pending</span>
              <span className={`security-value ${upd.pending_count > 0 ? 'text-warning' : 'text-success'}`}>
                {upd.pending_count} update{upd.pending_count !== 1 ? 's' : ''}
              </span>
            </div>
            {upd.pending_updates && upd.pending_updates.length > 0 && (
              <div className="pending-list">
                {upd.pending_updates.slice(0, 5).map((u, i) => (
                  <div key={i} className="pending-item">⬆ {u}</div>
                ))}
                {upd.pending_updates.length > 5 && (
                  <div className="pending-item muted">+{upd.pending_updates.length - 5} more</div>
                )}
              </div>
            )}
          </>
        ) : (
          <div className="no-data" style={{ padding: '1rem 0' }}>No update data yet.</div>
        )}
      </div>

      {/* USB Devices */}
      <div className="security-card">
        <div className="security-card-title">🔌 USB Devices ({usb?.count ?? 0})</div>
        {usbDevices.length ? (
          <ul className="usb-list">
            {usbDevices.map((d, i) => (
              <li key={i} className="usb-item">
                <div className="usb-name">{d.name || 'Unknown Device'}</div>
                <div className="usb-meta">
                  {d.manufacturer && <span>{d.manufacturer}</span>}
                  {d.vendor_id && <span className="mono">{d.vendor_id}:{d.product_id}</span>}
                  {d.speed && <span className="usb-speed">{d.speed}</span>}
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className="no-data" style={{ padding: '1rem 0' }}>No USB devices connected.</div>
        )}
      </div>
    </div>
  );
}

// ─── Active Window Section ────────────────────────────────────────────────────

function ActiveWindowSection({ data, cachedSummaries }: { data?: ActiveWindowData; cachedSummaries?: AppFocusSummary[] }) {
  if (!data && (!cachedSummaries || cachedSummaries.length === 0)) {
    return <div className="no-data">No active window data yet.</div>;
  }

  // Merge strategy:
  // - Always show live cumulative_summaries from the agent (covers the current
  //   session in full, including data not yet pushed to the API cache).
  // - Layer the persistent API cache on top so historical data from previous
  //   sessions is included. When both sources have an entry for the same app
  //   we take the larger value to avoid double-counting (the API cache already
  //   contains data the agent has previously reported).
  const liveSummaries = data?.cumulative_summaries ?? data?.app_summaries ?? [];

  const merged = new Map<string, AppFocusSummary>();

  // Seed from live data first.
  for (const entry of liveSummaries) {
    merged.set(entry.app_name, { ...entry });
  }

  // Merge API cache: add time that isn't already captured in the live data.
  // If the live total for an app is higher (current session > cached history)
  // keep the live value; otherwise use the cached value so we don't lose
  // historical sessions that the agent no longer holds in memory.
  for (const entry of (cachedSummaries ?? [])) {
    const existing = merged.get(entry.app_name);
    if (!existing) {
      merged.set(entry.app_name, { ...entry });
    } else if (entry.total_focus_seconds > existing.total_focus_seconds) {
      merged.set(entry.app_name, { ...entry });
    }
  }

  const summaries = Array.from(merged.values())
    .sort((a, b) => b.total_focus_seconds - a.total_focus_seconds);

  const totalSeconds = summaries.reduce((s, a) => s + a.total_focus_seconds, 0);
  const currentApp   = data?.current_app ?? '';

  function fmtDuration(secs: number): string {
    if (secs < 60)   return `${secs.toFixed(0)}s`;
    if (secs < 3600) return `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s`;
    return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
  }

  return (
    <div>
      {/* Current app banner */}
      {currentApp && (
        <div className="aw-current">
          <span className="aw-current-dot" />
          <span className="aw-current-label">Currently in focus:</span>
          <span className="aw-current-app">{currentApp}</span>
        </div>
      )}

      {summaries.length === 0 ? (
        <div className="no-data">No focus sessions recorded yet.</div>
      ) : (
        <div className="aw-list">
          {summaries.map((app, i) => {
            const pct = totalSeconds > 0 ? (app.total_focus_seconds / totalSeconds) * 100 : 0;
            const isActive = app.app_name === currentApp;
            return (
              <div key={i} className={`aw-item ${isActive ? 'aw-item-active' : ''}`}>
                <div className="aw-row">
                  <span className="aw-app-name" title={app.app_name}>
                    {isActive && <span className="aw-live-dot" />}
                    {app.app_name}
                  </span>
                  <span className="aw-duration">{fmtDuration(app.total_focus_seconds)}</span>
                  <span className="aw-sessions">{app.session_count} session{app.session_count !== 1 ? 's' : ''}</span>
                </div>
                <div className="gauge-wrap">
                  <div
                    className="gauge-bar"
                    style={{
                      width: `${pct}%`,
                      background: isActive ? 'var(--success-color)' : 'var(--accent-color)',
                    }}
                  />
                </div>
                <div className="aw-pct">{pct.toFixed(1)}% of tracked time</div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─── Main Component ───────────────────────────────────────────────────────────

export default function Home() {
  const [devices, setDevices]         = useState<Device[]>([]);
  const [loading, setLoading]         = useState(true);
  const [syncInterval, setSyncInterval] = useState(10);
  const [isUpdating, setIsUpdating]   = useState(false);
  const [activeTab, setActiveTab]     = useState<Record<string, string>>({});
  const [focusCache, setFocusCache]   = useState<Record<string, AppFocusSummary[]>>({});

  // ── Data fetching ────────────────────────────────────────────────────────────

  const fetchAll = useCallback(async () => {
    try {
      const devRes = await fetch(`${API}/devices`, { headers: readHeaders() });

      if (devRes.ok) {
        const data: Device[] = await devRes.json();
        const devList = data ?? [];
        setDevices(devList);

        // Fetch focus cache for every device in parallel
        const focusResults = await Promise.allSettled(
          devList.map(d => fetch(`${API}/focus/${d.device_id}`, { headers: readHeaders() }).then(r => r.ok ? r.json() as Promise<FocusCacheData> : null))
        );
        const newFocusCache: Record<string, AppFocusSummary[]> = {};
        focusResults.forEach((result, i) => {
          if (result.status === 'fulfilled' && result.value) {
            newFocusCache[devList[i].device_id] = result.value.app_summaries ?? [];
          }
        });
        setFocusCache(newFocusCache);
      }
    } catch (e) {
      console.error('Fetch error:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchPolicy = useCallback(async () => {
    try {
      const res = await fetch(`${API}/policy`, { headers: readHeaders() });
      if (res.ok) {
        const d = await res.json();
        if (d.sync_interval_seconds) setSyncInterval(d.sync_interval_seconds);
      }
    } catch {}
  }, []);

  useEffect(() => {
    fetchAll();
    fetchPolicy();
    const id = setInterval(fetchAll, 5000);
    return () => clearInterval(id);
  }, [fetchAll, fetchPolicy]);

  // ── Policy ───────────────────────────────────────────────────────────────────

  const handlePolicyUpdate = async (val: number) => {
    setSyncInterval(val);
    setIsUpdating(true);
    try {
      await fetch(`${API}/policy`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sync_interval_seconds: val }),
      });
    } catch {}
    finally { setIsUpdating(false); }
  };

  const deleteDevice = async (deviceId: string) => {
    if (!confirm(`Remove device ${deviceId}?`)) return;
    try {
      await fetch(`${API}/devices/${deviceId}`, { method: 'DELETE' });
      setDevices(d => d.filter(x => x.device_id !== deviceId));
    } catch {}
  };

  // ── Tab helpers ──────────────────────────────────────────────────────────────

  const getTab = (deviceId: string) => activeTab[deviceId] ?? 'hardware';
  const setTab = (deviceId: string, tab: string) =>
    setActiveTab(prev => ({ ...prev, [deviceId]: tab }));

  const onlineCount = devices.filter(d => isOnline(d.last_seen)).length;

  // ── Render ───────────────────────────────────────────────────────────────────

  return (
    <main className="dashboard-container">
      {/* Header */}
      <header className="header">
        <div>
          <h1>DevicePulse</h1>
          <p>Enterprise Endpoint Telemetry</p>
        </div>
        <div className="header-right">
          <div className="status-badge">
            <div className="status-dot" />
            {onlineCount} / {devices.length} Online
          </div>
          {isUpdating && (
            <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>Updating…</span>
          )}
        </div>
      </header>

      <div className="dashboard-layout">
        {/* Sidebar */}
        <aside>
          {/* Policy Card */}
          <div className="policy-card glass-card">
            <h3>Global Policy</h3>
            <p className="policy-desc">Control agent sync behavior in real time.</p>
            <div className="slider-container">
              <div className="slider-header">
                <label>Sync Interval</label>
                <span className="slider-value">{syncInterval}s</span>
              </div>
              <input
                type="range" min="2" max="60" value={syncInterval}
                onChange={e => setSyncInterval(Number(e.target.value))}
                onMouseUp={e => handlePolicyUpdate(Number((e.target as HTMLInputElement).value))}
                disabled={isUpdating}
                className="policy-slider"
              />
              <div className="slider-labels">
                <span>Fast (2s)</span>
                <span>Eco (60s)</span>
              </div>
            </div>
          </div>

        </aside>

        {/* Main content */}
        <section>
          {loading ? (
            <div className="loading">Initializing Telemetry Stream…</div>
          ) : devices.length === 0 ? (
            <div className="empty-state">
              No devices connected yet.<br />
              <span style={{ fontSize: '0.9rem', color: 'var(--text-muted)', marginTop: '0.5rem', display: 'block' }}>
                Start the agent to see live data.
              </span>
            </div>
          ) : (
            <div className="devices-grid">
              {devices.map((device, idx) => {
                const sys     = device.data?.SystemInfo;
                const procs   = device.data?.ProcessMonitor?.top_processes ?? [];
                const history = device.data?.BrowserHistory?.top_recent_urls ?? [];
                const hw      = device.data?.HardwareStats;
                const services  = device.data?.Services;
                const netPorts  = device.data?.NetworkPorts;
                const apps      = device.data?.InstalledApps;
                const usb       = device.data?.USBEvents;
                const osUpd     = device.data?.OSUpdates;
                const activeWin = device.data?.ActiveWindowTracker;
                const cachedFocus = focusCache[device.device_id] ?? [];
                const online  = isOnline(device.last_seen);
                const tab     = getTab(device.device_id);
                const lastSeen = device.last_seen
                  ? new Date(device.last_seen).toLocaleTimeString()
                  : 'Unknown';

                return (
                  <div key={device.device_id ?? idx} className="device-card glass-card">
                    {/* Card header */}
                    <div className="device-header">
                      <div>
                        <div className="device-id">
                          <span className={`online-dot ${online ? 'online' : 'offline'}`} />
                          {sys?.hostname || device.hostname || device.device_id}
                        </div>
                        <div className="device-last-seen">
                          <span>🕐</span> Last seen {lastSeen}
                          {sys?.platform_version && (
                            <span className="device-os-badge">
                              {sys.os} {sys.platform_version}
                            </span>
                          )}
                        </div>
                      </div>
                      <span className={`status-pill ${online ? 'pill-online' : 'pill-offline'}`}>
                        {online ? 'Online' : 'Offline'}
                      </span>
                      <button
                        className="device-delete-btn"
                        title="Remove device"
                        onClick={() => deleteDevice(device.device_id)}
                      >
                        🗑
                      </button>
                    </div>

                    {/* Tab bar */}
                    <div className="tab-bar">
                      {['hardware', 'processes', 'browser', 'services', 'ports', 'apps', 'security', 'focus', 'sysinfo'].map(t => (
                        <button
                          key={t}
                          className={`tab-btn ${tab === t ? 'tab-active' : ''}`}
                          onClick={() => setTab(device.device_id, t)}
                        >
                          {t === 'hardware'   ? '📊 Hardware'
                           : t === 'processes' ? '⚙️ Processes'
                           : t === 'browser'   ? '🌐 Browser'
                           : t === 'services'  ? '🔧 Services'
                           : t === 'ports'     ? '🔌 Ports'
                           : t === 'apps'      ? '📦 Apps'
                           : t === 'security'  ? '🛡 Security'
                           : t === 'focus'     ? '🎯 Focus'
                           : '🖥 System'}
                        </button>
                      ))}
                    </div>

                    {/* Tab content */}
                    <div className="device-section">
                      {tab === 'hardware'  && <HardwareSection hw={hw} />}
                      {tab === 'services'  && <ServicesSection data={services} />}
                      {tab === 'ports'     && <NetworkPortsSection data={netPorts} />}
                      {tab === 'apps'      && <InstalledAppsSection data={apps} />}
                      {tab === 'security'  && <SecuritySection usb={usb} osUpd={osUpd} />}
                      {tab === 'focus'     && <ActiveWindowSection data={activeWin} cachedSummaries={cachedFocus} />}

                      {tab === 'processes' && (
                        procs.length > 0 ? (
                          <ul className="process-list">
                            {procs.map((p, i) => (
                              <li key={i} className="process-item">
                                <div className="process-left">
                                  <span className="process-name" title={p.name}>{p.name}</span>
                                  <span className="process-pid">PID {p.pid}</span>
                                  <div className="cpu-bar-wrap">
                                    <div className="cpu-bar" style={{ width: `${Math.min(p.cpu, 100)}%` }} />
                                  </div>
                                </div>
                                <div className="process-stats">
                                  <span className="process-cpu">{p.cpu.toFixed(1)}% CPU</span>
                                  <span className="process-mem">{p.memory.toFixed(1)}% Mem</span>
                                </div>
                              </li>
                            ))}
                          </ul>
                        ) : <div className="no-data">No process data.</div>
                      )}

                      {tab === 'browser' && (
                        history.length > 0 ? (
                          <ul className="history-list">
                            {history.map((h, i) => {
                              const domain = getDomain(h.url);
                              return (
                                <li key={i}>
                                  <a href={h.url} target="_blank" rel="noopener noreferrer" className="history-item">
                                    {/* eslint-disable-next-line @next/next/no-img-element */}
                                    <img className="history-favicon" src={faviconUrl(h.url)} alt=""
                                      width={20} height={20}
                                      onError={e => { (e.target as HTMLImageElement).style.display = 'none'; }} />
                                    <div className="history-content">
                                      <div className="history-title" title={h.title || h.url}>
                                        {h.title || domain}
                                      </div>
                                      <div className="history-meta">
                                        <span className="history-domain">{domain}</span>
                                        {h.last_visit_time > 0 && (
                                          <span className="history-time">{formatVisitTime(h.last_visit_time)}</span>
                                        )}
                                        <span className={`browser-badge ${browserClass(h.browser)}`}>
                                          {browserEmoji(h.browser)} {h.browser || 'Unknown'}
                                        </span>
                                      </div>
                                    </div>
                                  </a>
                                </li>
                              );
                            })}
                          </ul>
                        ) : <div className="no-data">No browser history.</div>
                      )}

                      {tab === 'sysinfo' && (
                        <div className="metric-grid">
                          <StatBlock label="Hostname"     value={sys?.hostname ?? '—'} />
                          <StatBlock label="OS"           value={sys?.os ?? '—'} sub={sys?.platform_version} />
                          <StatBlock label="Architecture" value={sys?.architecture ?? '—'} />
                          <StatBlock label="CPU Cores"    value={String(sys?.num_cpus ?? '—')} />
                          <StatBlock label="Platform"     value={sys?.platform ?? '—'} />
                          <StatBlock label="Kernel"       value={sys?.kernel_version ?? '—'} />
                        </div>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      </div>
    </main>
  );
}
