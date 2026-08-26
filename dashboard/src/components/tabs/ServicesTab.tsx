'use client';

import { useState } from 'react';
import { ServiceEntry } from '@/types';

interface Props {
  data?: { services: ServiceEntry[] | null; source?: string };
}

export default function ServicesTab({ data }: Props) {
  const [filter, setFilter] = useState<'all' | 'running' | 'stopped'>('all');
  const services = data?.services ?? [];
  if (!services.length) return <div className="no-data">No service data yet.</div>;

  const filtered = filter === 'all' ? services : services.filter(s => s.status === filter);
  const runCount  = services.filter(s => s.status === 'running').length;
  const stopCount = services.filter(s => s.status === 'stopped').length;

  return (
    <div>
      <div className="svc-header">
        <span className="pill pill-green">&#9654; {runCount} running</span>
        <span className="pill pill-neutral">&#9632; {stopCount} stopped</span>
        {data?.source && <span className="pill pill-blue mono">{data.source}</span>}
        <div className="svc-filter" role="group" aria-label="Filter services by status">
          {(['all', 'running', 'stopped'] as const).map(f => (
            <button key={f} type="button" className={`svc-filter-btn ${filter === f ? 'active' : ''}`} aria-pressed={filter === f} onClick={() => setFilter(f)}>
              {f}
            </button>
          ))}
        </div>
      </div>
      <div className="svc-grid">
        {filtered.map((s, i) => (
          <div key={`${s.name}:${s.pid ?? 'x'}:${i}`} className="svc-item">
            <div className={`svc-dot ${s.status}`} />
            <div className="svc-name" title={s.name}>{s.name}</div>
            {s.pid && <div className="svc-pid">{s.pid}</div>}
          </div>
        ))}
      </div>
    </div>
  );
}
