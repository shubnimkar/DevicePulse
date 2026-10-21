'use client';

import type { Device } from '@/types';
import { timeAgo, deviceDisplayName } from '@/lib/utils';
import StatusPill, { StatusIcon, type Status } from '@/components/ui/StatusPill';
import DataTable, { type Column } from '@/components/ui/DataTable';
import EmptyState from '@/components/ui/EmptyState';

type StatusFilter = 'all' | Status;

interface Props {
  devices: Device[];
  counts: Record<StatusFilter, number>;
  attentionDevices: Device[]; // already sorted by severity
  getState: (d: Device) => Status;
  getRisk: (d: Device) => number;
  avgCpu: number;
  avgRam: number;
  avgDisk: number;
  fleetUptimePercent: number;
  onOpenDevices: (filter: StatusFilter) => void;
  onInspect: (deviceId: string) => void;
}

const ATTENTION_CAP = 8;
const SEV: Record<Status, number> = { critical: 0, warning: 1, online: 2, offline: 3 };
const KPIS: [StatusFilter, string][] = [
  ['all', 'Total'], ['online', 'Online'], ['warning', 'Warning'], ['critical', 'Critical'], ['offline', 'Offline'],
];

const pct = (v?: number) => (v === undefined ? '—' : `${v.toFixed(0)}%`);

export default function OverviewView({
  devices, counts, attentionDevices, getState, getRisk, avgCpu, avgRam, avgDisk,
  fleetUptimePercent, onOpenDevices, onInspect,
}: Props) {
  const noDevices = devices.length === 0;
  const hiddenAttention = attentionDevices.length - ATTENTION_CAP;

  const columns: Column<Device>[] = [
    { key: 'device', header: 'Device', render: d => (
      <div className="cell-stack">
        <strong>{deviceDisplayName(d)}</strong>
        <span>{d.data?.SystemInfo?.hostname || d.hostname || d.device_id}</span>
      </div>
    ) },
    { key: 'status', header: 'Status', sortable: true, sortValue: d => SEV[getState(d)], render: d => <StatusPill status={getState(d)} /> },
    { key: 'cpu', header: 'CPU', align: 'right', className: 'num', render: d => pct(d.data?.HardwareStats?.cpu?.usage_percent) },
    { key: 'mem', header: 'Memory', align: 'right', className: 'num', render: d => pct(d.data?.HardwareStats?.ram?.used_percent) },
    { key: 'lastSeen', header: 'Last Seen', sortable: true, sortValue: d => Date.parse(d.last_seen ?? '') || 0, render: d => timeAgo(d.last_seen) },
  ];

  return (
    <>
      <section className="kpi-tiles" aria-label="Fleet summary">
        {KPIS.map(([key, label]) => {
          const n = counts[key];
          return (
            <button
              key={key}
              type="button"
              className={`kpi-tile kpi-${key}`}
              aria-label={key === 'all' ? `Show all ${n} devices` : `Show ${n} ${label.toLowerCase()} device${n === 1 ? '' : 's'}`}
              onClick={() => onOpenDevices(key)}
            >
              <span className="kpi-label">
                {key !== 'all' && <StatusIcon status={key} />}
                {label}
              </span>
              <strong>{n.toLocaleString()}</strong>
            </button>
          );
        })}
      </section>

      <section className="overview-workspace">
        <div className="attention-panel">
          <div className="section-heading">
            <div><h2>Needs attention</h2></div>
            <button type="button" className="text-button" onClick={() => onOpenDevices('all')}>View all devices</button>
          </div>
          {attentionDevices.length ? (
            <>
              <div className="attention-list">
                {attentionDevices.slice(0, ATTENTION_CAP).map(device => {
                  const state = getState(device);
                  const reason = state === 'offline' ? 'Device is no longer checking in' : `${getRisk(device).toFixed(0)}% peak resource usage`;
                  return (
                    <button key={device.device_id} type="button" className="attention-row" onClick={() => onInspect(device.device_id)}>
                      <span className={`attention-icon state-${state}`} aria-hidden="true">{state === 'critical' ? '!' : state === 'warning' ? '↑' : '–'}</span>
                      <span className="attention-copy"><strong>{deviceDisplayName(device)}</strong><small>{reason}</small></span>
                      <StatusPill status={state} className="attention-status" />
                      <span className="attention-seen">{timeAgo(device.last_seen)}</span>
                      <span className="row-chevron" aria-hidden="true">›</span>
                    </button>
                  );
                })}
              </div>
              {hiddenAttention > 0 && (
                <button type="button" className="text-button attention-more" onClick={() => onOpenDevices('all')}>
                  {hiddenAttention} more need attention — view all devices
                </button>
              )}
            </>
          ) : noDevices ? (
            <EmptyState icon={<StatusIcon status="offline" />} title="No devices connected" description="Start the DevicePulse agent on a machine to begin." />
          ) : (
            <EmptyState icon={<StatusIcon status="online" />} title="All devices healthy" description="Every connected device is within the current resource thresholds." />
          )}
        </div>

        <aside className="fleet-snapshot" aria-label="Fleet resource snapshot">
          <div className="section-heading"><div><h2>Fleet resources</h2></div></div>
          <div className="snapshot-metrics">
            {([['CPU', avgCpu], ['Memory', avgRam], ['Disk', avgDisk], ['Connected', fleetUptimePercent]] as const).map(([label, value]) => (
              <div className="snapshot-metric" key={label}>
                <div><span>{label}</span><strong>{value.toFixed(0)}%</strong></div>
                <div className="snapshot-track"><i style={{ width: `${Math.min(100, value)}%` }} /></div>
              </div>
            ))}
          </div>
          <p>Current fleet averages from the latest device check-ins.</p>
        </aside>
      </section>

      {!noDevices && <section className="overview-fleet" aria-labelledby="overview-fleet-heading">
        <div className="section-heading">
          <div><h2 id="overview-fleet-heading">Fleet</h2></div>
        </div>
        <DataTable
          columns={columns}
          rows={devices}
          getRowKey={d => d.device_id}
          caption="Fleet devices with status, CPU, memory and last seen time"
          ariaLabel="Fleet devices table"
          initialSort={{ key: 'status', dir: 'asc' }}
          density="compact"
          pageSize={10}
          onRowClick={d => onInspect(d.device_id)}
        />
      </section>}
    </>
  );
}
