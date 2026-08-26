'use client';

import { HardwareStats, BatteryStat, DiskStat } from '@/types';
import { displayDisks, formatBytes, metricColor, primaryDisk } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';

function BatteryCard({ battery }: { battery?: BatteryStat }) {
  if (!battery?.available) return null;
  const pct = Math.min(Math.max(battery.percent ?? 0, 0), 100);
  const state = battery.state ?? 'unknown';
  const rateW = Number.isFinite(battery.charge_rate_w) ? battery.charge_rate_w : 0;
  const isCharging = state === 'charging' || state === 'full' || state === 'idle';
  const color = isCharging ? 'var(--green)' : pct <= 20 ? 'var(--red)' : pct <= 40 ? 'var(--yellow)' : 'var(--green)';
  const stateLabel: Record<string, string> = { charging: 'Charging', full: 'Full', idle: 'Plugged In', discharging: 'Discharging', empty: 'Empty', unknown: 'Unknown' };

  return (
    <div className="hw-card">
      <div className="hw-label">Battery</div>
      <div className="battery-pct-row">
        <span className="hw-value" style={{ color }}>{pct.toFixed(0)}%</span>
      </div>
      <div className="battery-bar">
        <div className="battery-fill" style={{ width: `${pct}%`, background: color }} />
        <div className="battery-nub" />
      </div>
      <div className="battery-state-label" style={{ color }}>{stateLabel[state] ?? state}</div>
      {rateW !== 0 && <div className="hw-sub">{rateW > 0 ? '+' : ''}{rateW.toFixed(1)} W</div>}
    </div>
  );
}

interface Props { hw?: HardwareStats; }

function diskUsagePercent(disk?: DiskStat): number | null {
  if (!disk) return null;
  if (
    typeof disk.used_bytes === 'number' &&
    typeof disk.total_bytes === 'number' &&
    Number.isFinite(disk.used_bytes) &&
    Number.isFinite(disk.total_bytes) &&
    disk.total_bytes > 0
  ) {
    return Math.min(Math.max((disk.used_bytes / disk.total_bytes) * 100, 0), 100);
  }
  if (disk.total_gb > 0 && disk.used_gb >= 0) {
    return Math.min(Math.max((disk.used_gb / disk.total_gb) * 100, 0), 100);
  }
  return null;
}

function diskSize(disk: DiskStat, kind: 'total' | 'used' | 'free'): string {
  const bytes = disk[`${kind}_bytes` as keyof DiskStat];
  if (typeof bytes === 'number' && Number.isFinite(bytes)) {
    return formatBytes(bytes);
  }

  const gb = disk[`${kind}_gb` as keyof DiskStat];
  if (typeof gb !== 'number' || !Number.isFinite(gb)) return '—';
  if (gb > 0 && gb < 1) return formatBytes(gb * 1024 * 1024 * 1024);
  if (gb === 0 && disk.total_gb > 0 && disk.total_gb < 1 && disk.used_percent > 0) {
    const totalBytes = disk.total_gb * 1024 * 1024 * 1024;
    const usedBytes = totalBytes * Math.min(Math.max(disk.used_percent, 0), 100) / 100;
    if (kind === 'used') return `~${formatBytes(usedBytes)}`;
    if (kind === 'free') return `~${formatBytes(Math.max(totalBytes - usedBytes, 0))}`;
  }
  return `${gb.toFixed(1)} GB`;
}

export default function HardwareTab({ hw }: Props) {
  if (!hw) return <div className="no-data">No hardware data yet.</div>;

  const cpu  = hw.cpu;
  const ram  = hw.ram;
  const disks = displayDisks(hw.disks);
  const disk = primaryDisk(hw.disks);
  const net  = hw.network?.[0];
  const diskPct = diskUsagePercent(disk);

  return (
    <div>
      <div className="hw-grid">
        {/* CPU */}
        <div className="hw-card">
          <div className="hw-label">CPU</div>
          <div className="hw-value" style={{ color: metricColor(cpu?.usage_percent ?? 0) }}>
            {cpu?.usage_percent?.toFixed(1) ?? '—'}%
          </div>
          <GaugeBar value={cpu?.usage_percent ?? 0} color={metricColor(cpu?.usage_percent ?? 0)} />
          <div className="hw-sub">{cpu?.core_count ?? '?'} logical cores</div>
        </div>

        {/* RAM */}
        <div className="hw-card">
          <div className="hw-label">Memory</div>
          <div className="hw-value" style={{ color: metricColor(ram?.used_percent ?? 0) }}>
            {ram?.used_percent?.toFixed(1) ?? '—'}%
          </div>
          <GaugeBar value={ram?.used_percent ?? 0} color={metricColor(ram?.used_percent ?? 0)} />
          <div className="hw-sub">{ram?.used_gb?.toFixed(1) ?? '?'} / {ram?.total_gb?.toFixed(1) ?? '?'} GB</div>
        </div>

        {/* Disk */}
        <div className="hw-card">
          <div className="hw-label">Disk {disk?.mount}</div>
          <div className="hw-value" style={{ color: metricColor(diskPct ?? 0) }}>
            {diskPct !== null ? diskPct.toFixed(1) : '—'}%
          </div>
          <GaugeBar value={diskPct ?? 0} color={metricColor(diskPct ?? 0)} />
          <div className="hw-sub">{disk ? `${diskSize(disk, 'used')} / ${diskSize(disk, 'total')}` : '—'}</div>
        </div>

        {/* Network */}
        <div className="hw-card">
          <div className="hw-label">Network {net?.interface}</div>
          <div className="net-io-pair">
            <div className="net-io-item">
              <span className="net-io-arrow" style={{ color: 'var(--cyan)' }}>↑</span>
              <span className="net-io-val">{net ? formatBytes(net.bytes_sent) : '—'}</span>
            </div>
            <div className="net-io-item">
              <span className="net-io-arrow" style={{ color: 'var(--blue)' }}>↓</span>
              <span className="net-io-val">{net ? formatBytes(net.bytes_recv) : '—'}</span>
            </div>
          </div>
          {hw.uptime_human && <div className="hw-sub">Up {hw.uptime_human}</div>}
        </div>

        <BatteryCard battery={hw.battery} />
      </div>

      {/* All disks */}
      {disks.length > 1 && (
        <div className="sub-section">
          <div className="sub-section-title">All Volumes</div>
          <div className="disk-table-wrap">
            <table className="ports-table">
              <thead>
                <tr>
                  <th scope="col">Mount</th>
                  <th scope="col">Total</th>
                  <th scope="col">Used</th>
                  <th scope="col">Free</th>
                  <th scope="col">Usage</th>
                </tr>
              </thead>
              <tbody>
                {disks.map((d, i) => {
                  const pct = diskUsagePercent(d);
                  return (
                    <tr key={i}>
                      <td><span className="mono">{d.mount}</span></td>
                      <td><span className="mono">{diskSize(d, 'total')}</span></td>
                      <td><span className="mono" style={{ color: metricColor(pct ?? 0) }}>{diskSize(d, 'used')}</span></td>
                      <td><span className="mono">{diskSize(d, 'free')}</span></td>
                      <td className="disk-usage-cell">
                        <div className="disk-usage-wrap">
                          <div className="disk-usage-gauge">
                            <GaugeBar value={pct ?? 0} color={metricColor(pct ?? 0)} height={3} />
                          </div>
                          <span className="mono disk-usage-pct" style={{ color: metricColor(pct ?? 0) }}>
                            {pct !== null ? `${pct.toFixed(0)}%` : '—'}
                          </span>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* All network interfaces */}
      {hw.network && hw.network.length > 1 && (
        <div className="sub-section">
          <div className="sub-section-title">Network Interfaces</div>
          <table className="ports-table">
            <thead>
              <tr>
                <th scope="col">Interface</th>
                <th scope="col">Sent</th>
                <th scope="col">Received</th>
              </tr>
            </thead>
            <tbody>
              {hw.network.map((n, i) => (
                <tr key={i}>
                  <td><span className="mono">{n.interface}</span></td>
                  <td><span className="mono" style={{ color: 'var(--cyan)' }}>↑ {formatBytes(n.bytes_sent)}</span></td>
                  <td><span className="mono" style={{ color: 'var(--blue)' }}>↓ {formatBytes(n.bytes_recv)}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
