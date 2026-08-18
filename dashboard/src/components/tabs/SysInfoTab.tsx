'use client';

import { SystemInfo } from '@/types';

interface Props { sys?: SystemInfo; }

export default function SysInfoTab({ sys }: Props) {
  const fields = [
    { label: 'Hostname',     value: sys?.hostname,         sub: undefined },
    { label: 'OS',           value: sys?.os,               sub: sys?.platform_version },
    { label: 'Architecture', value: sys?.architecture,     sub: undefined },
    { label: 'CPU Cores',    value: String(sys?.num_cpus ?? '—'), sub: undefined },
    { label: 'Platform',     value: sys?.platform,         sub: undefined },
    { label: 'Kernel',       value: sys?.kernel_version,   sub: undefined },
  ];

  return (
    <div className="sysinfo-grid">
      {fields.map((f, i) => (
        <div key={i} className="sysinfo-card">
          <div className="sysinfo-label">{f.label}</div>
          <div className="sysinfo-value">{f.value ?? '—'}</div>
          {f.sub && <div className="sysinfo-sub">{f.sub}</div>}
        </div>
      ))}
    </div>
  );
}
