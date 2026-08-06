'use client';

import { HardwareStats, BatteryStat } from '@/types';
import { formatBytes, metricColor } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';

function BatteryCard({ battery }: { battery?: BatteryStat }) {
  if (!battery?.available) return null;

  const pct = Math.min(Math.max(battery.percent ?? 0, 0), 100);
  const state = battery.state ?? 'unknown';
  const rateW = Number.isFinite(battery.charge_rate_w) ? battery.charge_rate_w : 0;

  const color =
    state === 'charging' || state === 'full' || state === 'idle'
      ? 'var(--success-color)'
      : pct <= 20
      ? 'var(--danger-color)'
      : pct <= 40
      ? 'var(--warning-color)'
      : 'var(--success-color)';

  const icon =
    state === 'charging' ? '⚡'
    : state === 'idle' ? '🔌'
    : state === 'full' ? '🔋'
    : pct <= 20 ? '🪫'
    : '🔋';

  const stateLabel: Record<string, string> = {
    charging: 'Charging',
    full: 'Full',
    idle: 'Plugged In',
    discharging: 'Discharging',
    empty: 'Empty',
    unknown: 'Unknown',
  };

  return (
    <div className="hw-card battery-card">
      <div className="hw-card-title">Battery</div>
      <div className="battery-top">
        <span className="hw-big-value" style={{ color }}>
          {pct.toFixed(0)}%
        </span>
        <span className="battery-icon">{icon}</span>
      </div>
      <div className="battery-shell">
        <div className="battery-fill" style={{ width: `${pct}%`, background: color }} />
        <div className="battery-nub" />
      </div>
      <div className="battery-state" style={{ color }}>
        {stateLabel[state] ?? state}
      </div>
      {rateW !== 0 && (
        <div className="hw-sub">
          {rateW > 0 ? '+' : ''}{rateW.toFixed(1)} W
        </div>
      )}
    </div>
  );
}

interface Props {
  hw?: HardwareStats;
}

export default function HardwareTab({ hw }: Props) {
  if (!hw) return <div className="no-data">No hardware data yet.</div>;

  const cpu = hw.cpu;
  const ram = hw.ram;
  const disk = hw.disks?.[0];
  const net = hw.network?.[0];

  return (
    <div>
      <div className="hw-grid">
        {/* CPU */}
        <div className="hw-card">
          <div className="hw-card-title">CPU</div>
          <div className="hw-big-value" style={{ color: metricColor(cpu?.usage_percent ?? 0) }}>
            {cpu?.usage_percent?.toFixed(1) ?? '—'}%
          </div>
          <GaugeBar value={cpu?.usage_percent ?? 0} color={metricColor(cpu?.usage_percent ?? 0)} />
          <div className="hw-sub">{cpu?.core_count ?? '?'} logical cores</div>
        </div>

        {/* RAM */}
        <div className="hw-card">
          <div className="hw-card-title">Memory</div>
          <div className="hw-big-value" style={{ color: metricColor(ram?.used_percent ?? 0) }}>
            {ram?.used_percent?.toFixed(1) ?? '—'}%
          </div>
          <GaugeBar value={ram?.used_percent ?? 0} color={metricColor(ram?.used_percent ?? 0)} />
          <div className="hw-sub">
            {ram?.used_gb?.toFixed(1) ?? '?'} / {ram?.total_gb?.toFixed(1) ?? '?'} GB
          </div>
        </div>

        {/* Primary Disk */}
        <div className="hw-card">
          <div className="hw-card-title">Disk {disk?.mount}</div>
          <div className="hw-big-value" style={{ color: metricColor(disk?.used_percent ?? 0) }}>
            {disk?.used_percent?.toFixed(1) ?? '—'}%
          </div>
          <GaugeBar value={disk?.used_percent ?? 0} color={metricColor(disk?.used_percent ?? 0)} />
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
          {hw.uptime_human && <div className="hw-sub">Up {hw.uptime_human}</div>}
        </div>

        {/* Battery */}
        <BatteryCard battery={hw.battery} />
      </div>

      {/* Additional disks */}
      {hw.disks && hw.disks.length > 1 && (
        <div style={{ marginTop: '1.5rem' }}>
          <div className="overview-section-title" style={{ marginBottom: '0.75rem' }}>All Volumes</div>
          <div className="disks-table-wrap">
            <table className="ports-table">
              <thead>
                <tr>
                  <th>Mount</th>
                  <th>Total</th>
                  <th>Used</th>
                  <th>Free</th>
                  <th>Usage</th>
                </tr>
              </thead>
              <tbody>
                {hw.disks.map((d, i) => (
                  <tr key={i}>
                    <td><span className="mono-text">{d.mount}</span></td>
                    <td><span className="mono-text">{d.total_gb.toFixed(1)} GB</span></td>
                    <td><span className="mono-text" style={{ color: metricColor(d.used_percent) }}>{d.used_gb.toFixed(1)} GB</span></td>
                    <td><span className="mono-text">{d.free_gb.toFixed(1)} GB</span></td>
                    <td style={{ minWidth: 120 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                        <div style={{ flex: 1 }}>
                          <GaugeBar value={d.used_percent} color={metricColor(d.used_percent)} height={4} />
                        </div>
                        <span className="mono-text" style={{ fontSize: '0.72rem', color: metricColor(d.used_percent), minWidth: 36 }}>
                          {d.used_percent.toFixed(0)}%
                        </span>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* All network interfaces */}
      {hw.network && hw.network.length > 1 && (
        <div style={{ marginTop: '1.5rem' }}>
          <div className="overview-section-title" style={{ marginBottom: '0.75rem' }}>Network Interfaces</div>
          <table className="ports-table">
            <thead>
              <tr>
                <th>Interface</th>
                <th>Sent</th>
                <th>Received</th>
              </tr>
            </thead>
            <tbody>
              {hw.network.map((n, i) => (
                <tr key={i}>
                  <td><span className="mono-text">{n.interface}</span></td>
                  <td><span className="mono-text" style={{ color: 'var(--accent-cyan)' }}>↑ {formatBytes(n.bytes_sent)}</span></td>
                  <td><span className="mono-text" style={{ color: 'var(--accent-purple)' }}>↓ {formatBytes(n.bytes_recv)}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
