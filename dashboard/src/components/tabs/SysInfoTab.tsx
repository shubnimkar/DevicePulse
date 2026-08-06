'use client';

import { SystemInfo } from '@/types';

function StatBlock({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}</div>
      {sub && <div className="metric-sub">{sub}</div>}
    </div>
  );
}

interface Props {
  sys?: SystemInfo;
}

export default function SysInfoTab({ sys }: Props) {
  return (
    <div className="metric-grid">
      <StatBlock label="Hostname" value={sys?.hostname ?? '—'} />
      <StatBlock label="OS" value={sys?.os ?? '—'} sub={sys?.platform_version} />
      <StatBlock label="Architecture" value={sys?.architecture ?? '—'} />
      <StatBlock label="CPU Cores" value={String(sys?.num_cpus ?? '—')} />
      <StatBlock label="Platform" value={sys?.platform ?? '—'} />
      <StatBlock label="Kernel" value={sys?.kernel_version ?? '—'} />
    </div>
  );
}
