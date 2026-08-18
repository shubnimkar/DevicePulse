'use client';

import { useEffect, useState, useCallback } from 'react';
import { Device, AppFocusSummary, FocusCacheData, DeviceTab, EnterprisePolicy } from '@/types';
import { API, readHeaders, adminHeaders, isOnline, getOSIcon, timeAgo } from '@/lib/utils';
import DeviceCard from '@/components/DeviceCard';

type PageView = 'dashboard' | 'inventory' | 'settings';
type StatusFilter = 'all' | 'online' | 'critical' | 'warning' | 'offline';
type CollectorPolicyKey =
  | 'collect_system_info'
  | 'collect_hardware_stats'
  | 'collect_processes'
  | 'collect_active_window'
  | 'collect_services'
  | 'collect_network_ports'
  | 'collect_installed_apps'
  | 'collect_os_updates'
  | 'collect_usb_devices';

const DEFAULT_POLICY: EnterprisePolicy = {
  sync_interval_seconds: 10,
  telemetry_retention_days: 30,
  delta_upload_enabled: true,
  cache_unchanged_collector_data: true,
  browser_history_mode: 'disabled',
  browser_history_limit: 10,
  collect_system_info: true,
  collect_hardware_stats: true,
  collect_processes: true,
  collect_browser_history: false,
  collect_active_window: true,
  collect_services: true,
  collect_network_ports: true,
  collect_installed_apps: true,
  collect_os_updates: true,
  collect_usb_devices: true,
};

// ── SVG icons ────────────────────────────────────────────────────────────────
const IconGrid = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="currentColor">
    <rect x="1" y="1" width="6" height="6" rx="1"/><rect x="9" y="1" width="6" height="6" rx="1"/>
    <rect x="1" y="9" width="6" height="6" rx="1"/><rect x="9" y="9" width="6" height="6" rx="1"/>
  </svg>
);
const IconList = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="currentColor">
    <rect x="1" y="2" width="14" height="2.5" rx="1"/><rect x="1" y="6.75" width="14" height="2.5" rx="1"/>
    <rect x="1" y="11.5" width="14" height="2.5" rx="1"/>
  </svg>
);
const IconSearch = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <circle cx="6.5" cy="6.5" r="5"/><path d="M10.5 10.5L14 14"/>
  </svg>
);
const IconFilter = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
    <path d="M1 3h14M3 8h10M6 13h4"/>
  </svg>
);
const IconExport = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <path d="M8 1v9M5 7l3 3 3-3M2 11v2a1 1 0 001 1h10a1 1 0 001-1v-2"/>
  </svg>
);
const IconPlus = () => (
  <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2">
    <path d="M8 2v12M2 8h12"/>
  </svg>
);
const IconLogo = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
    <circle cx="8" cy="8" r="6" stroke="white" strokeWidth="1.5"/>
    <circle cx="8" cy="8" r="2.5" fill="white"/>
    <path d="M8 2v2M8 12v2M2 8h2M12 8h2" stroke="white" strokeWidth="1.5" strokeLinecap="round"/>
  </svg>
);
const IconSettings = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <circle cx="8" cy="8" r="2.25"/>
    <path d="M13 8a5.3 5.3 0 00-.07-.86l1.42-1.08-1.35-2.33-1.66.68a5.1 5.1 0 00-1.48-.86L9.62 1.8H6.38l-.24 1.75a5.1 5.1 0 00-1.48.86L3 3.73 1.65 6.06l1.42 1.08A5.3 5.3 0 003 8c0 .3.02.58.07.86L1.65 9.94 3 12.27l1.66-.68c.44.37.94.66 1.48.86l.24 1.75h3.24l.24-1.75c.54-.2 1.04-.49 1.48-.86l1.66.68 1.35-2.33-1.42-1.08c.05-.28.07-.57.07-.86z"/>
  </svg>
);

export default function Home() {
  const [devices, setDevices]         = useState<Device[]>([]);
  const [loading, setLoading]         = useState(true);
  const [syncInterval, setSyncInterval] = useState(10);
  const [isUpdating, setIsUpdating]   = useState(false);
  const [policy, setPolicy]           = useState<EnterprisePolicy>(DEFAULT_POLICY);
  const [policySavedAt, setPolicySavedAt] = useState<string>('');
  const [activeTab, setActiveTab]     = useState<Record<string, DeviceTab>>({});
  const [focusCache, setFocusCache]   = useState<Record<string, AppFocusSummary[]>>({});
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [currentPage, setCurrentPage] = useState<PageView>('inventory');

  // ── Data fetching ─────────────────────────────────────────────────────────

  const fetchAll = useCallback(async () => {
    try {
      const devRes = await fetch(`${API}/devices`, { headers: readHeaders() });
      if (devRes.ok) {
        const data: Device[] = await devRes.json();
        const devList = data ?? [];
        setDevices(devList);
        const focusResults = await Promise.allSettled(
          devList.map(d =>
            fetch(`${API}/focus/${d.device_id}`, { headers: readHeaders() }).then(r =>
              r.ok ? (r.json() as Promise<FocusCacheData>) : null
            )
          )
        );
        const cache: Record<string, AppFocusSummary[]> = {};
        focusResults.forEach((result, i) => {
          if (result.status === 'fulfilled' && result.value)
            cache[devList[i].device_id] = result.value.app_summaries ?? [];
        });
        setFocusCache(cache);
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
        const nextPolicy = { ...DEFAULT_POLICY, ...d };
        setPolicy(nextPolicy);
        if (nextPolicy.sync_interval_seconds) setSyncInterval(nextPolicy.sync_interval_seconds);
      }
    } catch {}
  }, []);

  useEffect(() => {
    const t = window.setTimeout(() => { void fetchAll(); void fetchPolicy(); }, 0);
    const id = setInterval(fetchAll, 5000);
    return () => { window.clearTimeout(t); clearInterval(id); };
  }, [fetchAll, fetchPolicy]);

  // ── Actions ───────────────────────────────────────────────────────────────

  const savePolicy = async (nextPolicy: EnterprisePolicy) => {
    setIsUpdating(true);
    setPolicy(nextPolicy);
    setSyncInterval(nextPolicy.sync_interval_seconds);
    try {
      const res = await fetch(`${API}/policy`, {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify(nextPolicy),
      });
      if (res.ok) setPolicySavedAt(new Date().toLocaleTimeString());
    } catch {} finally { setIsUpdating(false); }
  };

  const handlePolicyUpdate = async (val: number) => {
    const nextPolicy = { ...policy, sync_interval_seconds: val };
    await savePolicy(nextPolicy);
  };

  const updatePolicyField = <K extends keyof EnterprisePolicy>(key: K, value: EnterprisePolicy[K]) => {
    const nextPolicy = { ...policy, [key]: value };
    if (key === 'browser_history_mode') {
      nextPolicy.collect_browser_history = value !== 'disabled';
    }
    void savePolicy(nextPolicy);
  };

  const deleteDevice = async (deviceId: string) => {
    if (!confirm(`Remove device ${deviceId}?`)) return;
    try {
      await fetch(`${API}/devices/${deviceId}`, { method: 'DELETE', headers: readHeaders() });
      setDevices(d => d.filter(x => x.device_id !== deviceId));
    } catch {}
  };

  const getTab = (id: string): DeviceTab => activeTab[id] ?? 'overview';
  const setTab = (id: string, tab: DeviceTab) =>
    setActiveTab(prev => ({ ...prev, [id]: tab }));

  // ── Computed stats ────────────────────────────────────────────────────────

  const onlineCount  = devices.filter(d => isOnline(d.last_seen)).length;
  const offlineCount = devices.length - onlineCount;

  const fleetLoad = devices.reduce(
    (acc, d) => {
      const hw = d.data?.HardwareStats;
      if (hw?.cpu?.usage_percent  !== undefined) acc.cpu.push(hw.cpu.usage_percent);
      if (hw?.ram?.used_percent   !== undefined) acc.ram.push(hw.ram.used_percent);
      if (hw?.disks?.[0]?.used_percent !== undefined) acc.disk.push(hw.disks[0].used_percent);
      return acc;
    },
    { cpu: [] as number[], ram: [] as number[], disk: [] as number[] }
  );
  const avg = (arr: number[]) => arr.length ? Math.round(arr.reduce((s, v) => s + v, 0) / arr.length) : 0;
  const avgCpu  = avg(fleetLoad.cpu);
  const avgRam  = avg(fleetLoad.ram);
  const avgDisk = avg(fleetLoad.disk);

  const deviceRisk = (device: Device) => {
    const hw = device.data?.HardwareStats;
    return Math.max(hw?.cpu?.usage_percent ?? 0, hw?.ram?.used_percent ?? 0, hw?.disks?.[0]?.used_percent ?? 0);
  };
  const getDeviceState = (device: Device): 'online' | 'critical' | 'warning' | 'offline' => {
    if (!isOnline(device.last_seen)) return 'offline';
    const risk = deviceRisk(device);
    if (risk >= 90) return 'critical';
    if (risk >= 70) return 'warning';
    return 'online';
  };

  const criticalCount = devices.filter(d => getDeviceState(d) === 'critical').length;
  const warningCount  = devices.filter(d => getDeviceState(d) === 'warning').length;
  const attentionDevice = [...devices]
    .sort((a, b) => deviceRisk(b) - deviceRisk(a))
    .find(d => deviceRisk(d) > 0);

  const normalizedQuery = searchQuery.trim().toLowerCase();
  const filteredDevices = devices.filter(device => {
    const state = getDeviceState(device);
    if (statusFilter !== 'all' && state !== statusFilter) return false;
    if (!normalizedQuery) return true;
    const sys = device.data?.SystemInfo;
    return [device.device_id, device.hostname, sys?.hostname, sys?.os, sys?.platform, sys?.platform_version]
      .filter(Boolean).join(' ').toLowerCase().includes(normalizedQuery);
  });

  const filterOptions: { key: StatusFilter; label: string; count: number }[] = [
    { key: 'all',      label: 'All Devices', count: devices.length },
    { key: 'online',   label: 'Online',      count: onlineCount },
    { key: 'critical', label: 'Critical',    count: criticalCount },
    { key: 'warning',  label: 'Warning',     count: warningCount },
    { key: 'offline',  label: 'Offline',     count: offlineCount },
  ];

  const viewDeviceDetails = (deviceId: string) => {
    setCurrentPage('dashboard');
    setTab(deviceId, 'overview');
    window.setTimeout(() => {
      document.getElementById(`device-${deviceId}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }, 0);
  };

  const collectorControls: { key: CollectorPolicyKey; label: string; meta: string }[] = [
    { key: 'collect_system_info', label: 'System info', meta: 'Hostname, OS, architecture, kernel' },
    { key: 'collect_hardware_stats', label: 'Hardware stats', meta: 'CPU, RAM, disk, network and battery' },
    { key: 'collect_processes', label: 'Processes', meta: 'Active process names with CPU and memory' },
    { key: 'collect_active_window', label: 'Active window', meta: 'Foreground app and focus duration' },
    { key: 'collect_services', label: 'Services', meta: 'Running and stopped system services' },
    { key: 'collect_network_ports', label: 'Network ports', meta: 'Open TCP/UDP ports and owning process' },
    { key: 'collect_installed_apps', label: 'Installed apps', meta: 'Application inventory and versions' },
    { key: 'collect_os_updates', label: 'OS updates', meta: 'Update status and pending count' },
    { key: 'collect_usb_devices', label: 'USB devices', meta: 'Connected USB device inventory' },
  ];

  // ── Render ────────────────────────────────────────────────────────────────

  return (
    <div className="app-shell">

      {/* ── Top header bar ── */}
      <header className="app-header">
        <div className="header-brand">
          <div className="header-logo"><IconLogo /></div>
          <span className="brand-name">DevicePulse</span>
        </div>

        <div className="header-center">
          <div className="header-stat">
            <span className="dot" />
            <strong>{onlineCount}</strong>
            <span>online</span>
          </div>
          <div className="header-stat">
            <strong>{devices.length}</strong>
            <span>total</span>
          </div>
          <div className="header-stat">
            <strong>CPU {avgCpu}%</strong>
          </div>
          <div className="header-stat">
            <strong>MEM {avgRam}%</strong>
          </div>
        </div>

        <div className="header-right">
          {!loading && (
            <span className="live-badge">
              <span className="dot" />
              Live
            </span>
          )}
        </div>
      </header>

      {/* ── Sidebar ── */}
      <aside className="app-sidebar">
        <button
          type="button"
          className="add-device-btn"
          onClick={() => window.alert('Start the DevicePulse agent on a machine to add it to inventory.')}
        >
          <IconPlus />
          Add Device
        </button>

        <div className="sidebar-section">
          <div className="sidebar-label">Navigation</div>
          <nav className="sidebar-nav" aria-label="Dashboard pages">
            <button
              type="button"
              className={`nav-item ${currentPage === 'dashboard' ? 'active' : ''}`}
              onClick={() => setCurrentPage('dashboard')}
              aria-current={currentPage === 'dashboard' ? 'page' : undefined}
            >
              <IconGrid />
              Dashboard
            </button>
            <button
              type="button"
              className={`nav-item ${currentPage === 'inventory' ? 'active' : ''}`}
              onClick={() => setCurrentPage('inventory')}
              aria-current={currentPage === 'inventory' ? 'page' : undefined}
            >
              <IconList />
              Device Inventory
            </button>
            <button
              type="button"
              className={`nav-item ${currentPage === 'settings' ? 'active' : ''}`}
              onClick={() => setCurrentPage('settings')}
              aria-current={currentPage === 'settings' ? 'page' : undefined}
            >
              <IconSettings />
              Settings
            </button>
          </nav>
        </div>

        <div className="sidebar-section">
          <div className="sidebar-label">Fleet — {filteredDevices.length}/{devices.length}</div>
          <div className="fleet-widget">
            <input
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              placeholder="Search devices…"
              className="fleet-search"
              aria-label="Search devices"
            />
            <div className="fleet-filter-row" role="group" aria-label="Status filter">
              {(['all', 'online', 'offline'] as const).map(f => (
                <button
                  key={f}
                  type="button"
                  className={`fleet-filter-btn ${statusFilter === f ? 'active' : ''}`}
                  onClick={() => setStatusFilter(f)}
                >
                  {f}
                </button>
              ))}
            </div>
            <div className="fleet-metrics">
              <div className="fleet-metric"><span>CPU</span><strong>{avgCpu}%</strong></div>
              <div className="fleet-metric"><span>Mem</span><strong>{avgRam}%</strong></div>
              <div className="fleet-metric"><span>Disk</span><strong>{avgDisk}%</strong></div>
            </div>
            {attentionDevice && (
              <div className="attention-strip">
                <div className="kicker">Needs Attention</div>
                <strong>{attentionDevice.data?.SystemInfo?.hostname || attentionDevice.hostname || attentionDevice.device_id}</strong>
                <span>Peak load {deviceRisk(attentionDevice).toFixed(0)}%</span>
              </div>
            )}
          </div>
        </div>

        <div className="sidebar-section">
          <div className="sidebar-label">Global Policy</div>
          <div className="policy-widget">
            <div className="policy-slider-row">
              <span>Sync Interval</span>
              <strong>{syncInterval}s</strong>
            </div>
            <input
              type="range"
              min="2"
              max="60"
              value={syncInterval}
              onChange={e => setSyncInterval(Number(e.target.value))}
              onMouseUp={e => handlePolicyUpdate(Number((e.target as HTMLInputElement).value))}
              disabled={isUpdating}
              className="policy-slider"
              aria-label="Sync interval in seconds"
            />
            <div className="slider-range">
              <span>2s (fast)</span>
              <span>60s (eco)</span>
            </div>
          </div>
        </div>
      </aside>

      {/* ── Main content ── */}
      <main className="app-main">
        <div className="main-inner">

          {/* Page header */}
          <div className="page-header">
            <div>
              <h1>
                {currentPage === 'settings'
                  ? 'Settings'
                  : currentPage === 'inventory'
                    ? 'Device Inventory'
                    : 'Telemetry Dashboard'}
              </h1>
              <p>
                {currentPage === 'settings'
                  ? 'Enterprise data collection, retention and upload controls'
                  : currentPage === 'inventory'
                  ? `${devices.length.toLocaleString()} device${devices.length !== 1 ? 's' : ''} registered`
                  : 'Live endpoint telemetry grouped by device'}
              </p>
            </div>
            {currentPage !== 'settings' && (
              <div className="toolbar">
                <div className="search-box">
                  <IconSearch />
                  <input
                    value={searchQuery}
                    onChange={e => setSearchQuery(e.target.value)}
                    placeholder="Search by ID, name or OS…"
                    aria-label="Search devices"
                  />
                </div>
                <button type="button" className="btn" onClick={() => setStatusFilter(statusFilter === 'all' ? 'offline' : 'all')}>
                  <IconFilter />
                  Filters
                </button>
                <button type="button" className="btn" onClick={() => window.print()}>
                  <IconExport />
                  Export
                </button>
              </div>
            )}
          </div>

          {/* Status filter tabs */}
          {currentPage !== 'settings' && (
            <div className="filter-tabs" role="group" aria-label="Filter by status">
              {filterOptions.map(opt => (
                <button
                  key={opt.key}
                  type="button"
                  className={`filter-tab tab-${opt.key} ${statusFilter === opt.key ? 'active' : ''}`}
                  onClick={() => setStatusFilter(opt.key)}
                >
                  {opt.label}
                  <span className="tab-count">{opt.count.toLocaleString()}</span>
                </button>
              ))}
            </div>
          )}

          {/* Content */}
          {currentPage === 'settings' ? (
            <div className="settings-layout">
              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Retention</h2>
                    <p>Controls how long new server telemetry remains queryable.</p>
                  </div>
                  {policySavedAt && <span className="save-state">Saved {policySavedAt}</span>}
                </div>
                <div className="settings-grid two-col">
                  <label className="setting-field">
                    <span>Telemetry Retention</span>
                    <div className="number-control">
                      <input
                        type="number"
                        min="1"
                        max="3650"
                        value={policy.telemetry_retention_days}
                        onChange={e => setPolicy(p => ({ ...p, telemetry_retention_days: Number(e.target.value) }))}
                        onBlur={e => updatePolicyField('telemetry_retention_days', Number(e.target.value))}
                      />
                      <span>days</span>
                    </div>
                  </label>
                  <label className="setting-field">
                    <span>Sync Interval</span>
                    <div className="number-control">
                      <input
                        type="number"
                        min="2"
                        max="3600"
                        value={policy.sync_interval_seconds}
                        onChange={e => {
                          const next = Number(e.target.value);
                          setPolicy(p => ({ ...p, sync_interval_seconds: next }));
                          setSyncInterval(next);
                        }}
                        onBlur={e => updatePolicyField('sync_interval_seconds', Number(e.target.value))}
                      />
                      <span>seconds</span>
                    </div>
                  </label>
                </div>
              </section>

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Data Caching</h2>
                    <p>Reduces repeated uploads by sending only data that changed or has not been sent before.</p>
                  </div>
                </div>
                <div className="toggle-list">
                  <label className="toggle-row">
                    <span>
                      <strong>Delta upload</strong>
                      <small>Upload changed collector payloads instead of full repeated snapshots.</small>
                    </span>
                    <input
                      type="checkbox"
                      checked={policy.delta_upload_enabled}
                      onChange={e => updatePolicyField('delta_upload_enabled', e.target.checked)}
                    />
                  </label>
                  <label className="toggle-row">
                    <span>
                      <strong>Cache unchanged collector data</strong>
                      <small>Keep local fingerprints so unchanged apps, services, ports and browser entries are skipped.</small>
                    </span>
                    <input
                      type="checkbox"
                      checked={policy.cache_unchanged_collector_data}
                      onChange={e => updatePolicyField('cache_unchanged_collector_data', e.target.checked)}
                    />
                  </label>
                </div>
              </section>

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Browser History</h2>
                    <p>Use the least invasive mode that still supports your reporting needs.</p>
                  </div>
                </div>
                <div className="segmented-control" role="group" aria-label="Browser history mode">
                  {([
                    ['disabled', 'Disabled'],
                    ['domain_only', 'Domain only'],
                    ['full_url', 'Full URL'],
                  ] as const).map(([value, label]) => (
                    <button
                      key={value}
                      type="button"
                      className={policy.browser_history_mode === value ? 'active' : ''}
                      onClick={() => updatePolicyField('browser_history_mode', value)}
                    >
                      {label}
                    </button>
                  ))}
                </div>
                <label className="setting-field compact-field">
                  <span>Browser Entry Limit</span>
                  <div className="number-control">
                    <input
                      type="number"
                      min="0"
                      max="1000"
                      value={policy.browser_history_limit}
                      disabled={policy.browser_history_mode === 'disabled'}
                      onChange={e => setPolicy(p => ({ ...p, browser_history_limit: Number(e.target.value) }))}
                      onBlur={e => updatePolicyField('browser_history_limit', Number(e.target.value))}
                    />
                    <span>entries</span>
                  </div>
                </label>
              </section>

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Collectors</h2>
                    <p>Enable only the telemetry categories required by your environment.</p>
                  </div>
                </div>
                <div className="collector-grid">
                  {collectorControls.map(item => (
                    <label key={item.key} className="collector-toggle">
                      <span>
                        <strong>{item.label}</strong>
                        <small>{item.meta}</small>
                      </span>
                      <input
                        type="checkbox"
                        checked={Boolean(policy[item.key])}
                        onChange={e => updatePolicyField(item.key, e.target.checked)}
                      />
                    </label>
                  ))}
                </div>
              </section>
            </div>
          ) : loading ? (
            <div className="loading" role="status" aria-label="Loading">
              Connecting to telemetry stream…
            </div>
          ) : devices.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon">📡</span>
              <div className="empty-title">No devices connected</div>
              <div className="empty-sub">Start the DevicePulse agent on a machine to begin.</div>
            </div>
          ) : filteredDevices.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon">🔍</span>
              <div className="empty-title">No matching devices</div>
              <div className="empty-sub">Adjust your search or status filter.</div>
            </div>
          ) : currentPage === 'inventory' ? (
            /* ── Inventory table ── */
            <div>
              <div className="table-wrap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Device</th>
                      <th>Status</th>
                      <th>Platform</th>
                      <th>Connection</th>
                      <th>Last Seen</th>
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredDevices.map(device => {
                      const sys    = device.data?.SystemInfo;
                      const state  = getDeviceState(device);
                      const online = isOnline(device.last_seen);
                      const name   = sys?.hostname || device.hostname || device.device_id;
                      const risk   = deviceRisk(device);
                      const hw     = device.data?.HardwareStats;

                      const stateLabel = state === 'critical' ? 'Critical'
                        : state === 'warning' ? 'Warning'
                        : online ? 'Online' : 'Offline';

                      const platform = sys?.platform
                        ? `${getOSIcon(sys.os)} ${sys.platform}${sys.architecture ? `, ${sys.architecture}` : ''}`
                        : '—';

                      return (
                        <tr key={device.device_id} className={`row-${state}`}>
                          <td>
                            <div className="device-cell">
                              <div className="device-cell-text">
                                <strong>{name}</strong>
                                <span>{device.device_id}</span>
                              </div>
                            </div>
                          </td>
                          <td>
                            <span className={`status-badge badge-${state}`}>
                              <span className="dot" />
                              {stateLabel}
                            </span>
                          </td>
                          <td>
                            <div className="cell-stack">
                              <strong>{platform}</strong>
                              <span>{sys?.platform_version ?? '—'}</span>
                            </div>
                          </td>
                          <td>
                            <div className="cell-stack">
                              <strong style={{ color: online ? 'var(--text-1)' : 'var(--red)' }}>
                                {online ? `${risk.toFixed(0)}% peak load` : 'Connection lost'}
                              </strong>
                              <span>{hw?.uptime_human || sys?.os || '—'}</span>
                            </div>
                          </td>
                          <td>
                            <span className="mono" style={{ fontSize: '0.8125rem', color: 'var(--text-2)' }}>
                              {timeAgo(device.last_seen)}
                            </span>
                          </td>
                          <td>
                            <div className="row-actions">
                              <button
                                type="button"
                                className="action-btn"
                                onClick={() => viewDeviceDetails(device.device_id)}
                              >
                                Inspect
                              </button>
                              <button
                                type="button"
                                className="action-btn danger"
                                onClick={() => deleteDevice(device.device_id)}
                                aria-label={`Remove ${name}`}
                              >
                                ×
                              </button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                <div className="table-footer">
                  <span>
                    Showing {filteredDevices.length ? '1' : '0'}–{filteredDevices.length} of {devices.length.toLocaleString()} devices
                  </span>
                  <div className="table-footer-pages">
                    <button type="button" className="page-btn" aria-label="Previous page">‹</button>
                    <span>Page 1 / 1</span>
                    <button type="button" className="page-btn" aria-label="Next page">›</button>
                  </div>
                </div>
              </div>
            </div>
          ) : (
            /* ── Device telemetry cards ── */
            <div className="device-grid">
              {filteredDevices.map((device, idx) => (
                <div key={device.device_id ?? idx} id={`device-${device.device_id}`}>
                  <DeviceCard
                    device={device}
                    tab={getTab(device.device_id)}
                    onTabChange={tab => setTab(device.device_id, tab)}
                    onDelete={() => deleteDevice(device.device_id)}
                    cachedFocus={focusCache[device.device_id] ?? []}
                  />
                </div>
              ))}
            </div>
          )}

        </div>
      </main>

    </div>
  );
}
