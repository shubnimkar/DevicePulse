'use client';

import { useMemo } from 'react';
import type { Device } from '@/types';
import { isOnline, timeAgo, deviceDisplayName } from '@/lib/utils';
import StatusPill, { type Status } from '@/components/ui/StatusPill';
import DataTable, { type Column, type SortState } from '@/components/ui/DataTable';
import EmptyState from '@/components/ui/EmptyState';

export type StatusFilter = 'all' | Status;
export type DeviceSort = SortState & { key: 'status' | 'lastSeen' };
export interface InventoryState { status: StatusFilter; q: string; sort: DeviceSort | null }

const FILTERS: [StatusFilter, string][] = [
  ['all', 'All Devices'], ['online', 'Online'], ['critical', 'Critical'], ['warning', 'Warning'], ['offline', 'Offline'],
];
const SEV: Record<Status, number> = { critical: 0, warning: 1, online: 2, offline: 3 };

// URL contract: ?status=online|warning|critical|offline  ?q=text  ?sort=status|lastSeen&dir=asc|desc
export function parseInventoryParams(params: URLSearchParams): InventoryState {
  const s = params.get('status');
  const k = params.get('sort');
  return {
    status: s === 'online' || s === 'warning' || s === 'critical' || s === 'offline' ? s : 'all',
    q: params.get('q') ?? '',
    sort: k === 'status' || k === 'lastSeen' ? { key: k, dir: params.get('dir') === 'desc' ? 'desc' : 'asc' } : null,
  };
}

export function setInventoryParams(params: URLSearchParams, { status, q, sort }: InventoryState) {
  if (status !== 'all') params.set('status', status);
  if (q.trim()) params.set('q', q);
  if (sort) { params.set('sort', sort.key); params.set('dir', sort.dir); }
}

const SearchIcon = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true"><circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5L14 14" /></svg>
);
const SignalIcon = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round">
    <circle cx="8" cy="8" r="1.4"/>
    <path d="M5.2 5.2a4 4 0 000 5.6M10.8 5.2a4 4 0 010 5.6M3 3a7 7 0 000 10M13 3a7 7 0 010 10"/>
  </svg>
);
const pct = (v?: number) => (v === undefined ? '—' : `${v.toFixed(0)}%`);

interface Props {
  devices: Device[];
  counts: Record<StatusFilter, number>;
  state: InventoryState;
  getState: (d: Device) => Status;
  onStatusChange: (status: StatusFilter) => void;
  onQueryChange: (q: string) => void;
  onSortChange: (sort: DeviceSort) => void;
  onClearFilters: () => void;
  onInspect: (deviceId: string) => void;
  onRename?: (d: Device) => void; // omitted when the user may not manage devices
  onRemove?: (deviceId: string, name: string) => void;
}

export default function DevicesView({
  devices, counts, state, getState, onStatusChange, onQueryChange, onSortChange, onClearFilters, onInspect, onRename, onRemove,
}: Props) {
  const { status, q, sort } = state;
  const query = q.trim().toLowerCase();
  const filtered = useMemo(() => devices.filter(d => {
    if (status === 'online' ? !isOnline(d.last_seen) : status !== 'all' && getState(d) !== status) return false;
    if (!query) return true;
    const sys = d.data?.SystemInfo;
    return [d.device_id, d.display_name, d.hostname, sys?.hostname, sys?.os, sys?.platform, sys?.platform_version]
      .filter(Boolean).join(' ').toLowerCase().includes(query);
  }), [devices, status, query, getState]);

  if (devices.length === 0) {
    return <EmptyState icon={<SignalIcon />} title="No devices connected" description="Start the DevicePulse agent on a machine to begin." />;
  }

  const total = devices.length;
  const countText = filtered.length === total
    ? `${total.toLocaleString()} device${total !== 1 ? 's' : ''}`
    : `${filtered.length.toLocaleString()} of ${total.toLocaleString()} device${total !== 1 ? 's' : ''}`;
  // Same ordering as DataTable's sort so the mobile cards follow the table.
  const sortedCards = (() => {
    if (!sort) return filtered;
    const val = (d: Device) => (sort.key === 'status' ? SEV[getState(d)] : Date.parse(d.last_seen ?? '') || 0);
    const sign = sort.dir === 'asc' ? 1 : -1;
    return [...filtered].sort((a, b) => sign * (val(a) - val(b)));
  })();

  const columns: Column<Device>[] = [
    { key: 'device', header: 'Device', render: d => {
      const sys = d.data?.SystemInfo;
      return (
        <div className="device-cell">
          <div className="device-cell-text">
            <button type="button" onClick={() => onInspect(d.device_id)}>{deviceDisplayName(d)}</button>
            <span>{sys?.hostname || d.hostname || d.device_id}{sys?.platform && <span className="dc-plat"> · {sys.platform}</span>}</span>
          </div>
        </div>
      );
    } },
    { key: 'status', header: 'Status', sortable: true, sortValue: d => SEV[getState(d)], render: d => <StatusPill status={getState(d)} /> },
    { key: 'resources', header: 'Resources', render: d => {
      const hw = d.data?.HardwareStats;
      const cpu = hw?.cpu?.usage_percent;
      const ram = hw?.ram?.used_percent;
      return (
        <div className="resource-pair">
          <div><span>CPU</span><i><b style={{ width: `${Math.min(100, cpu ?? 0)}%` }} /></i><strong>{pct(cpu)}</strong></div>
          <div><span>MEM</span><i><b style={{ width: `${Math.min(100, ram ?? 0)}%` }} /></i><strong>{pct(ram)}</strong></div>
        </div>
      );
    } },
    { key: 'platform', header: 'Platform', hideBelow: 'lg', render: d => {
      const sys = d.data?.SystemInfo;
      return (
        <div className="cell-stack">
          <strong>{sys?.platform ? `${sys.platform}${sys.architecture ? `, ${sys.architecture}` : ''}` : '—'}</strong>
          <span>{d.agent_version ? `Agent ${d.agent_version}` : 'Agent version unavailable'}</span>
        </div>
      );
    } },
    { key: 'lastSeen', header: 'Last Seen', sortable: true, sortValue: d => Date.parse(d.last_seen ?? '') || 0, render: d => (
      <div className="cell-stack"><strong>{timeAgo(d.last_seen)}</strong><span>{isOnline(d.last_seen) ? 'Receiving telemetry' : 'Last known state'}</span></div>
    ) },
    { key: 'actions', header: 'Actions', align: 'right', sticky: 'right', render: d => {
      const name = deviceDisplayName(d);
      return (
        <div className="row-actions">
          <button type="button" className="action-btn" onClick={() => onInspect(d.device_id)}>Inspect</button>
          {onRename && <button type="button" className="action-btn" onClick={() => onRename(d)}>Rename</button>}
          {onRemove && <button type="button" className="action-btn danger" onClick={() => onRemove(d.device_id, name)} aria-label={`Remove ${name}`}>×</button>}
        </div>
      );
    } },
  ];

  return (
    <div className="devices-view">
      <div className="devices-controls">
        <div className="filter-tabs" role="group" aria-label="Filter by status">
          {FILTERS.map(([key, label]) => key === 'all' ? (
            <button key={key} type="button" className={`filter-tab tab-all ${status === key ? 'active' : ''}`} aria-pressed={status === key} onClick={() => onStatusChange(key)}>
              {label}<span className="tab-count">{counts[key].toLocaleString()}</span>
            </button>
          ) : (
            <StatusPill key={key} as="button" status={key} label={label} className="filter-tab" pressed={status === key} onClick={() => onStatusChange(key)}>
              <span className="tab-count">{counts[key].toLocaleString()}</span>
            </StatusPill>
          ))}
        </div>
        <div className="search-box">
          <SearchIcon />
          <input value={q} onChange={e => onQueryChange(e.target.value)} placeholder="Search by ID, name or OS…" aria-label="Search devices" />
        </div>
      </div>
      <p className="results-count" role="status">{countText}</p>

      {filtered.length === 0 ? (
        <EmptyState
          icon={<SearchIcon />}
          title="No matching devices"
          description="Adjust your search or status filter."
          action={<button type="button" className="btn" onClick={onClearFilters}>Clear filters</button>}
        />
      ) : (
        <>
          <div className="inventory-mobile-list">
            {sortedCards.map(d => {
              const sys = d.data?.SystemInfo;
              const st = getState(d);
              const name = deviceDisplayName(d);
              const hw = d.data?.HardwareStats;
              const bars: [string, number | undefined][] = [['CPU', hw?.cpu?.usage_percent], ['MEM', hw?.ram?.used_percent]];
              return (
                <article key={d.device_id} className={`inventory-mobile-card state-${st}`}>
                  <button type="button" className="mobile-device-main" onClick={() => onInspect(d.device_id)}>
                    <span className="mobile-device-title"><strong>{name}</strong><StatusPill status={st} /></span>
                    <span className="mobile-device-meta">{sys?.hostname || d.hostname || d.device_id} · {sys?.platform || d.agent_os || 'Platform unavailable'} · {timeAgo(d.last_seen)}</span>
                    <span className="mini-bars">
                      {bars.map(([label, v]) => (
                        <span key={label} className="mini-bar"><span>{label}</span><i><b style={{ width: `${Math.min(100, v ?? 0)}%` }} /></i><strong>{pct(v)}</strong></span>
                      ))}
                    </span>
                  </button>
                  {(onRename || onRemove) && (
                    <div className="mobile-device-actions">
                      {onRename && <button type="button" className="action-btn" onClick={() => onRename(d)} aria-label={`Rename ${name}`}>Rename</button>}
                      {onRemove && <button type="button" className="action-btn danger" onClick={() => onRemove(d.device_id, name)} aria-label={`Remove ${name}`}>Remove</button>}
                    </div>
                  )}
                </article>
              );
            })}
          </div>
          <DataTable
            columns={columns}
            rows={filtered}
            getRowKey={d => d.device_id}
            caption="Device inventory"
            ariaLabel="Device inventory, scrollable table"
            density="compact"
            rowClassName={d => `row-${getState(d)}`}
            sort={sort}
            onSortChange={s => onSortChange(s as DeviceSort)}
          />
        </>
      )}
    </div>
  );
}
