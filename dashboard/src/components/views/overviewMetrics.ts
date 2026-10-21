import type { Device } from '@/types';
import { isOnline, primaryDisk } from '@/lib/utils';
import type { Status } from '@/components/ui/StatusPill';

export type IssueKind = 'critical' | 'resource' | 'offline' | 'patches' | 'agent' | 'quarantine';

export interface Issue { kind: IssueKind; device: Device; detail: string; weight: number }

export const ISSUE_LABEL: Record<IssueKind, string> = {
  critical: 'Critical', resource: 'Resource', offline: 'Offline', patches: 'Patches', agent: 'Agent', quarantine: 'Quarantined',
};

const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : undefined);

// Numeric dotted compare ("1.10.0" > "1.9.2"); non-numeric parts count as 0.
export function cmpVersion(a = '', b = ''): number {
  const pa = a.replace(/^v/i, '').split('.'), pb = b.replace(/^v/i, '').split('.');
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (parseInt(pa[i] ?? '0', 10) || 0) - (parseInt(pb[i] ?? '0', 10) || 0);
    if (d) return d;
  }
  return 0;
}

export const platformOf = (d: Device) => d.data?.SystemInfo?.platform || d.data?.SystemInfo?.os || d.agent_os || 'Unknown';
export const pendingUpdates = (d: Device) => d.data?.OSUpdates?.os_updates?.pending_count ?? 0;

export function fleetMetrics(devices: Device[], getState: (d: Device) => Status) {
  const total = devices.length;
  const live = devices.filter(d => isOnline(d.last_seen));
  const cpu = live.flatMap(d => d.data?.HardwareStats?.cpu?.usage_percent ?? []);
  const mem = live.flatMap(d => d.data?.HardwareStats?.ram?.used_percent ?? []);
  const peak = (key: 'cpu' | 'ram') => live
    .map(d => ({ d, v: key === 'cpu' ? d.data?.HardwareStats?.cpu?.usage_percent : d.data?.HardwareStats?.ram?.used_percent }))
    .filter((r): r is { d: Device; v: number } => r.v !== undefined)
    .sort((a, b) => b.v - a.v)[0];

  const withUpdateData = devices.filter(d => d.data?.OSUpdates);
  const patched = withUpdateData.filter(d => pendingUpdates(d) === 0).length;
  const pendingTotal = withUpdateData.reduce((n, d) => n + pendingUpdates(d), 0);

  const versions = devices.map(d => d.agent_version).filter((v): v is string => !!v);
  const latest = versions.reduce<string | undefined>((m, v) => (m === undefined || cmpVersion(v, m) > 0 ? v : m), undefined);
  const outdated = (d: Device) => d.agent_update_status === 'update_available' || (!!d.agent_version && !!latest && cmpVersion(d.agent_version, latest) < 0);
  const onLatest = devices.filter(d => d.agent_version && !outdated(d)).length;

  const platforms = Object.entries(devices.reduce<Record<string, number>>((m, d) => { const k = platformOf(d); m[k] = (m[k] ?? 0) + 1; return m; }, {}))
    .sort((a, b) => b[1] - a[1]);

  const disks = devices.flatMap(d => { const p = primaryDisk(d.data?.HardwareStats?.disks)?.used_percent; return p === undefined ? [] : [{ d, v: p }]; });
  const buckets = [
    { label: '< 50%', tone: 'ok', n: disks.filter(x => x.v < 50).length },
    { label: '50–70%', tone: 'ok', n: disks.filter(x => x.v >= 50 && x.v < 70).length },
    { label: '70–85%', tone: 'warn', n: disks.filter(x => x.v >= 70 && x.v < 85).length },
    { label: '≥ 85%', tone: 'crit', n: disks.filter(x => x.v >= 85).length },
  ];
  const fullest = [...disks].sort((a, b) => b.v - a.v)[0];

  const issues: Issue[] = devices.flatMap<Issue>(d => {
    const st = getState(d), out: Issue[] = [];
    const hw = d.data?.HardwareStats;
    const top = ([['CPU', hw?.cpu?.usage_percent], ['Memory', hw?.ram?.used_percent], ['Disk', primaryDisk(hw?.disks)?.used_percent]] as const)
      .reduce((a, b) => ((b[1] ?? 0) > (a[1] ?? 0) ? b : a));
    if (st === 'critical') out.push({ kind: 'critical', device: d, detail: `${top[0]} at ${Math.round(top[1] ?? 0)}%`, weight: 100 });
    if (st === 'warning') out.push({ kind: 'resource', device: d, detail: `${top[0]} at ${Math.round(top[1] ?? 0)}%`, weight: 60 });
    if (st === 'offline') out.push({ kind: 'offline', device: d, detail: 'No telemetry', weight: 40 });
    if (st !== 'offline' && pendingUpdates(d) > 0) out.push({ kind: 'patches', device: d, detail: `${pendingUpdates(d).toLocaleString()} pending`, weight: 30 });
    if (outdated(d)) out.push({ kind: 'agent', device: d, detail: `v${(d.agent_version ?? '?').replace(/^v/i, '')} → v${(latest ?? '?').replace(/^v/i, '')}`, weight: 20 });
    if (d.quarantined) out.push({ kind: 'quarantine', device: d, detail: 'Network isolated', weight: 80 });
    return out;
  }).sort((a, b) => b.weight - a.weight);

  return {
    total, onlineCount: live.length,
    cpu: avg(cpu), mem: avg(mem), peakCpu: peak('cpu'), peakMem: peak('ram'),
    patched, patchScope: withUpdateData.length, pendingTotal,
    latest, onLatest, versioned: versions.length,
    platforms, buckets, fullest, diskCount: disks.length,
    issues,
  };
}
