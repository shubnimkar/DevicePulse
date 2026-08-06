'use client';

import { AppFocusSummary, DeviceData } from '@/types';
import { formatBytes, formatDuration, metricColor } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';

interface Props {
  data?: DeviceData;
  cachedFocus: AppFocusSummary[];
}

export default function OverviewTab({ data, cachedFocus }: Props) {
  const hw = data?.HardwareStats;
  const cpu = hw?.cpu;
  const ram = hw?.ram;
  const disk = hw?.disks?.[0];
  const battery = hw?.battery;

  const procs = data?.ProcessMonitor?.top_processes ?? [];
  const services = data?.Services?.services ?? [];
  const runningServices = services.filter(s => s.status === 'running').length;
  const ports = data?.NetworkPorts?.open_ports ?? [];
  const usbDevices = data?.USBEvents?.usb_devices ?? [];
  const osUpd = data?.OSUpdates?.os_updates;
  const activeWin = data?.ActiveWindowTracker;

  // Merge focus summaries
  const liveSummaries = activeWin?.cumulative_summaries ?? activeWin?.app_summaries ?? [];
  const merged = new Map<string, AppFocusSummary>();
  for (const e of liveSummaries) merged.set(e.app_name, { ...e });
  for (const e of cachedFocus) {
    const existing = merged.get(e.app_name);
    if (!existing || e.total_focus_seconds > existing.total_focus_seconds)
      merged.set(e.app_name, { ...e });
  }
  const topApps = Array.from(merged.values())
    .sort((a, b) => b.total_focus_seconds - a.total_focus_seconds)
    .slice(0, 4);

  const totalFocus = topApps.reduce((s, a) => s + a.total_focus_seconds, 0);

  return (
    <div className="overview-grid">
      {/* Resource gauges */}
      <div className="overview-section">
        <h4 className="overview-section-title">Resources</h4>
        <div className="resource-cards">
          {/* CPU */}
          <div className="resource-card">
            <div className="resource-header">
              <span className="resource-icon">⚡</span>
              <span className="resource-label">CPU</span>
              <span className="resource-value" style={{ color: metricColor(cpu?.usage_percent ?? 0) }}>
                {cpu?.usage_percent?.toFixed(1) ?? '—'}%
              </span>
            </div>
            <GaugeBar value={cpu?.usage_percent ?? 0} color={metricColor(cpu?.usage_percent ?? 0)} height={6} />
            <div className="resource-sub">{cpu?.core_count ?? '?'} logical cores</div>
          </div>

          {/* RAM */}
          <div className="resource-card">
            <div className="resource-header">
              <span className="resource-icon">🧠</span>
              <span className="resource-label">Memory</span>
              <span className="resource-value" style={{ color: metricColor(ram?.used_percent ?? 0) }}>
                {ram?.used_percent?.toFixed(1) ?? '—'}%
              </span>
            </div>
            <GaugeBar value={ram?.used_percent ?? 0} color={metricColor(ram?.used_percent ?? 0)} height={6} />
            <div className="resource-sub">
              {ram ? `${ram.used_gb.toFixed(1)} / ${ram.total_gb.toFixed(1)} GB` : '—'}
            </div>
          </div>

          {/* Disk */}
          <div className="resource-card">
            <div className="resource-header">
              <span className="resource-icon">💾</span>
              <span className="resource-label">Disk</span>
              <span className="resource-value" style={{ color: metricColor(disk?.used_percent ?? 0) }}>
                {disk?.used_percent?.toFixed(1) ?? '—'}%
              </span>
            </div>
            <GaugeBar value={disk?.used_percent ?? 0} color={metricColor(disk?.used_percent ?? 0)} height={6} />
            <div className="resource-sub">
              {disk ? `${disk.used_gb.toFixed(1)} / ${disk.total_gb.toFixed(1)} GB` : '—'}
            </div>
          </div>

          {/* Network */}
          <div className="resource-card">
            <div className="resource-header">
              <span className="resource-icon">📡</span>
              <span className="resource-label">Network</span>
            </div>
            <div className="net-io-row">
              <span className="net-arrow net-up">↑</span>
              <span className="net-val">{hw?.network?.[0] ? formatBytes(hw.network[0].bytes_sent) : '—'}</span>
              <span className="net-arrow net-down">↓</span>
              <span className="net-val">{hw?.network?.[0] ? formatBytes(hw.network[0].bytes_recv) : '—'}</span>
            </div>
            {hw?.uptime_human && <div className="resource-sub">Up {hw.uptime_human}</div>}
          </div>

          {/* Battery — only if available */}
          {battery?.available && (
            <div className="resource-card">
              <div className="resource-header">
                <span className="resource-icon">🔋</span>
                <span className="resource-label">Battery</span>
                <span
                  className="resource-value"
                  style={{
                    color:
                      battery.state === 'charging' || battery.state === 'full'
                        ? 'var(--success-color)'
                        : battery.percent <= 20
                        ? 'var(--danger-color)'
                        : 'var(--warning-color)',
                  }}
                >
                  {battery.percent.toFixed(0)}%
                </span>
              </div>
              <GaugeBar
                value={battery.percent}
                color={
                  battery.state === 'charging' || battery.state === 'full'
                    ? 'var(--success-color)'
                    : battery.percent <= 20
                    ? 'var(--danger-color)'
                    : 'var(--warning-color)'
                }
                height={6}
              />
              <div className="resource-sub" style={{ textTransform: 'capitalize' }}>{battery.state}</div>
            </div>
          )}
        </div>
      </div>

      {/* Quick stats row */}
      <div className="overview-section">
        <h4 className="overview-section-title">Inventory</h4>
        <div className="quick-stats">
          <div className="quick-stat">
            <span className="qs-number">{runningServices}</span>
            <span className="qs-label">Services Running</span>
          </div>
          <div className="quick-stat">
            <span className="qs-number">{ports.length}</span>
            <span className="qs-label">Open Ports</span>
          </div>
          <div className="quick-stat">
            <span className="qs-number">{data?.InstalledApps?.count ?? 0}</span>
            <span className="qs-label">Installed Apps</span>
          </div>
          <div className="quick-stat">
            <span className="qs-number">{usbDevices.length}</span>
            <span className="qs-label">USB Devices</span>
          </div>
          {osUpd && (
            <div className={`quick-stat ${osUpd.pending_count > 0 ? 'qs-warn' : ''}`}>
              <span className="qs-number">{osUpd.pending_count}</span>
              <span className="qs-label">Pending Updates</span>
            </div>
          )}
        </div>
      </div>

      {/* Top Processes */}
      {procs.length > 0 && (
        <div className="overview-section">
          <h4 className="overview-section-title">Top Processes</h4>
          <div className="mini-proc-list">
            {procs.slice(0, 5).map((p, i) => (
              <div key={i} className="mini-proc-item">
                <span className="mini-proc-name">{p.name}</span>
                <div className="mini-proc-bars">
                  <div className="mini-proc-bar-wrap">
                    <div
                      className="mini-proc-bar"
                      style={{
                        width: `${Math.min(p.cpu, 100)}%`,
                        background: 'var(--warning-color)',
                      }}
                    />
                  </div>
                  <span className="mini-proc-pct cpu-pct">{p.cpu.toFixed(1)}%</span>
                  <div className="mini-proc-bar-wrap">
                    <div
                      className="mini-proc-bar"
                      style={{
                        width: `${Math.min(p.memory, 100)}%`,
                        background: 'var(--accent-cyan)',
                      }}
                    />
                  </div>
                  <span className="mini-proc-pct mem-pct">{p.memory.toFixed(1)}%</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Focus Summary */}
      {topApps.length > 0 && (
        <div className="overview-section">
          <h4 className="overview-section-title">
            App Focus
            {activeWin?.current_app && (
              <span className="focus-live-badge">
                <span className="focus-live-dot" />
                {activeWin.current_app}
              </span>
            )}
          </h4>
          <div className="mini-focus-list">
            {topApps.map((app, i) => {
              const pct = totalFocus > 0 ? (app.total_focus_seconds / totalFocus) * 100 : 0;
              const isActive = app.app_name === activeWin?.current_app;
              return (
                <div key={i} className={`mini-focus-item${isActive ? ' focus-active' : ''}`}>
                  <span className="mini-focus-name">
                    {isActive && <span className="aw-live-dot" />}
                    {app.app_name}
                  </span>
                  <div className="mini-proc-bar-wrap" style={{ flex: 1 }}>
                    <div
                      className="mini-proc-bar"
                      style={{
                        width: `${pct}%`,
                        background: isActive ? 'var(--success-color)' : 'var(--accent-purple)',
                      }}
                    />
                  </div>
                  <span className="mini-focus-dur">{formatDuration(app.total_focus_seconds)}</span>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
