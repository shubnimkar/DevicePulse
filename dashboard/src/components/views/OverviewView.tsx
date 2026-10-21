'use client';

import type { Device } from '@/types';
import { timeAgo, deviceDisplayName, primaryDisk } from '@/lib/utils';
import StatusPill, { StatusIcon, type Status } from '@/components/ui/StatusPill';
import DataTable, { type Column } from '@/components/ui/DataTable';
import EmptyState from '@/components/ui/EmptyState';

type StatusFilter = 'all' | Status;

interface Props {
  devices: Device[];
  counts: Record<StatusFilter, number>;
  getState: (d: Device) => Status;
  onOpenDevices: (filter: StatusFilter) => void;
  onInspect: (deviceId: string) => void;
}

const SEV: Record<Status, number> = { critical: 0, warning: 1, online: 2, offline: 3 };
const KPIS: [StatusFilter, string][] = [
  ['all', 'Total'], ['online', 'Online'], ['warning', 'Warning'], ['critical', 'Critical'], ['offline', 'Offline'],
];

const pct = (v?: number) => (v === undefined ? '—' : `${v.toFixed(0)}%`);

// Cause shown for non-healthy devices; mirrors deviceRisk() in page.tsx (max of CPU, RAM, primary disk).
function issueText(d: Device, state: Exclude<Status, 'online'>): string {
  if (state === 'offline') {
    const m = d.last_seen ? Math.max(0, Math.floor((Date.now() - Date.parse(d.last_seen)) / 60_000)) : NaN;
    if (Number.isNaN(m)) return 'Offline';
    if (m < 60) return `Offline ${m}m`;
    if (m < 1440) return `Offline ${Math.floor(m / 60)}h ${m % 60}m`;
    return `Offline ${Math.floor(m / 1440)}d`;
  }
  const hw = d.data?.HardwareStats;
  const top = ([['CPU', hw?.cpu?.usage_percent], ['Memory', hw?.ram?.used_percent], ['Disk', primaryDisk(hw?.disks)?.used_percent]] as const)
    .reduce((a, b) => ((b[1] ?? 0) > (a[1] ?? 0) ? b : a));
  return `${top[0]} ${pct(top[1] ?? 0)}`;
}

export default function OverviewView({
  devices, counts, getState, onOpenDevices, onInspect,
}: Props) {
  const noDevices = devices.length === 0;

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
    { key: 'issue', header: 'Issue', hideBelow: 'lg', render: d => {
      const st = getState(d);
      return st === 'online' ? <span style={{ color: 'var(--text-3)' }}>—</span> : issueText(d, st);
    } },
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

      {noDevices ? (
        <EmptyState icon={<StatusIcon status="offline" />} title="No devices connected" description="Start the DevicePulse agent on a machine to begin." />
      ) : <section className="overview-fleet" aria-labelledby="overview-fleet-heading">
        <div className="section-heading">
          <div><h2 id="overview-fleet-heading">Fleet</h2><span>{devices.length.toLocaleString()} device{devices.length === 1 ? '' : 's'}</span></div>
        </div>
        <DataTable
          columns={columns}
          rows={devices}
          getRowKey={d => d.device_id}
          caption="Fleet devices with status, CPU, memory, last seen time and issue"
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
