'use client';

import { AppFocusSummary, DeviceData } from '@/types';
import { formatBytes, formatDuration, metricColor } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';

interface Props {
  data?: DeviceData;
  cachedFocus: AppFocusSummary[];
}

export default function OverviewTab({ data, cachedFocus }: Props) {
  const hw      = data?.HardwareStats;
  const cpu     = hw?.cpu;
  const ram     = hw?.ram;
  const disk    = hw?.disks?.[0];
  const battery = hw?.battery;
  const procs   = data?.ProcessMonitor?.top_processes ?? [];
  const services = data?.Services?.services ?? [];
  const ports    = data?.NetworkPorts?.open_ports ?? [];
  const usb      = data?.USBEvents?.usb_devices ?? [];
  const osUpd    = data?.OSUpdates?.os_updates;
  const activeWin = data?.ActiveWindowTracker;

  // Merge focus summaries
  const liveSummaries = activeWin?.cumulative_summaries ?? activeWin?.app_summaries ?? [];
  const merged = new Map<string, AppFocusSummary>();
  for (const e of liveSummaries) merged.set(e.app_name, { ...e });
  for (const e of cachedFocus) {
    const ex = merged.get(e.app_name);
    if (!ex || e.total_focus_seconds > ex.total_focus_seconds) merged.set(e.app_name, { ...e });
  }
  const topApps = Array.from(merged.values())
    .sort((a, b) => b.total_focus_seconds - a.total_focus_seconds)
    .slice(0, 4);
  const totalFocus = topApps.reduce((s, a) => s + a.total_focus_seconds, 0);
  const runningServices = services.filter(s => s.status === 'running').length;

  return (
    <div className="overview-grid">
      {/* Resources */}
      <div>
        <div className="section-title">Resources</div>
        <div className="resource-row">
          <div className="resource-card">
            <div className="rc-header">
              <span className="rc-label">CPU</span>
              <span className="rc-value" style={{ color: metricColor(cpu?.usage_percent ?? 0) }}>
                {cpu?.usage_percent?.toFixed(1) ?? '—'}%
              </span>
            </div>
            <GaugeBar value={cpu?.usage_percent ?? 0} color={metricColor(cpu?.usage_percent ?? 0)} />
            <div className="rc-sub">{cpu?.core_count ?? '?'} cores</div>
          </div>

          <div className="resource-card">
            <div className="rc-header">
              <span className="rc-label">Memory</span>
              <span className="rc-value" style={{ color: metricColor(ram?.used_percent ?? 0) }}>
                {ram?.used_percent?.toFixed(1) ?? '—'}%
              </span>
            </div>
            <GaugeBar value={ram?.used_percent ?? 0} color={metricColor(ram?.used_percent ?? 0)} />
            <div className="rc-sub">{ram ? `${ram.used_gb.toFixed(1)} / ${ram.total_gb.toFixed(1)} GB` : '—'}</div>
          </div>

          <div className="resource-card">
            <div className="rc-header">
              <span className="rc-label">Disk</span>
              <span className="rc-value" style={{ color: metricColor(disk?.used_percent ?? 0) }}>
                {disk?.used_percent?.toFixed(1) ?? '—'}%
              </span>
            </div>
            <GaugeBar value={disk?.used_percent ?? 0} color={metricColor(disk?.used_percent ?? 0)} />
            <div className="rc-sub">{disk ? `${disk.used_gb.toFixed(1)} / ${disk.total_gb.toFixed(1)} GB` : '—'}</div>
          </div>

          <div className="resource-card">
            <div className="rc-header">
              <span className="rc-label">Network</span>
            </div>
            <div className="net-row">
              <span className="net-dir up">↑</span>
              <span className="net-val">{hw?.network?.[0] ? formatBytes(hw.network[0].bytes_sent) : '—'}</span>
            </div>
            <div className="net-row">
              <span className="net-dir down">↓</span>
              <span className="net-val">{hw?.network?.[0] ? formatBytes(hw.network[0].bytes_recv) : '—'}</span>
            </div>
            {hw?.uptime_human && <div className="rc-sub">Up {hw.uptime_human}</div>}
          </div>

          {battery?.available && (
            <div className="resource-card">
              <div className="rc-header">
                <span className="rc-label">Battery</span>
                <span className="rc-value" style={{ color: battery.state === 'charging' || battery.state === 'full' ? 'var(--green)' : battery.percent <= 20 ? 'var(--red)' : 'var(--yellow)' }}>
                  {battery.percent.toFixed(0)}%
                </span>
              </div>
              <GaugeBar
                value={battery.percent}
                color={battery.state === 'charging' || battery.state === 'full' ? 'var(--green)' : battery.percent <= 20 ? 'var(--red)' : 'var(--yellow)'}
              />
              <div className="rc-sub" style={{ textTransform: 'capitalize' }}>{battery.state}</div>
            </div>
          )}
        </div>
      </div>

      {/* Inventory counts */}
      <div>
        <div className="section-title">Inventory</div>
        <div className="stats-row">
          <div className="stat-chip">
            <strong>{runningServices}</strong>
            <span>Services</span>
          </div>
          <div className="stat-chip">
            <strong>{ports.length}</strong>
            <span>Open Ports</span>
          </div>
          <div className="stat-chip">
            <strong>{data?.InstalledApps?.count ?? 0}</strong>
            <span>Apps</span>
          </div>
          <div className="stat-chip">
            <strong>{usb.length}</strong>
            <span>USB</span>
          </div>
          {osUpd && (
            <div className={`stat-chip ${osUpd.pending_count > 0 ? 'warn' : ''}`}>
              <strong>{osUpd.pending_count}</strong>
              <span>Updates</span>
            </div>
          )}
        </div>
      </div>

      {/* Top Processes */}
      {procs.length > 0 && (
        <div>
          <div className="section-title">Top Processes</div>
          <div className="mini-proc-list">
            {procs.slice(0, 5).map((p, i) => (
              <div key={i} className="mini-proc-row">
                <span className="mini-proc-name">{p.name}</span>
                <div className="mini-bars">
                  <div className="mini-bar-track">
                    <div className="mini-bar-fill" style={{ width: `${Math.min(p.cpu, 100)}%`, background: 'var(--yellow)' }} />
                  </div>
                  <span className="mini-pct pct-cpu">{p.cpu.toFixed(1)}%</span>
                  <div className="mini-bar-track">
                    <div className="mini-bar-fill" style={{ width: `${Math.min(p.memory, 100)}%`, background: 'var(--cyan)' }} />
                  </div>
                  <span className="mini-pct pct-mem">{p.memory.toFixed(1)}%</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Focus */}
      {topApps.length > 0 && (
        <div>
          <div className="section-title">
            App Focus
            {activeWin?.current_app && (
              <span className="focus-live-tag">
                <span className="live-dot" />
                {activeWin.current_app}
              </span>
            )}
          </div>
          <div className="mini-focus-list">
            {topApps.map((app, i) => {
              const pct = totalFocus > 0 ? (app.total_focus_seconds / totalFocus) * 100 : 0;
              const isActive = app.app_name === activeWin?.current_app;
              return (
                <div key={i} className="mini-focus-row">
                  <span className={`mini-focus-name ${isActive ? 'is-active' : ''}`}>
                    {isActive && <span className="live-dot" />}
                    {app.app_name}
                  </span>
                  <div className="mini-bar-track" style={{ flex: 1 }}>
                    <div className="mini-bar-fill" style={{ width: `${pct}%`, background: isActive ? 'var(--green)' : 'var(--blue)' }} />
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
