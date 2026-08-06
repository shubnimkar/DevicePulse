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

  const filtered =
    filter === 'all' ? services : services.filter(s => s.status === filter);
  const runCount = services.filter(s => s.status === 'running').length;
  const stopCount = services.filter(s => s.status === 'stopped').length;

  return (
    <div>
      <div className="collector-header">
        <div className="collector-counts">
          <span className="count-pill count-running">▶ {runCount} running</span>
          <span className="count-pill count-stopped">■ {stopCount} stopped</span>
          {data?.source && <span className="count-pill count-source">{data.source}</span>}
        </div>
        <div className="filter-tabs">
          {(['all', 'running', 'stopped'] as const).map(f => (
            <button
              key={f}
              className={`filter-btn ${filter === f ? 'filter-active' : ''}`}
              onClick={() => setFilter(f)}
            >
              {f}
            </button>
          ))}
        </div>
      </div>
      <div className="service-grid">
        {filtered.map((s, i) => (
          <div key={i} className={`service-item ${s.status}`}>
            <div className={`service-dot ${s.status}`} />
            <div className="service-name" title={s.name}>{s.name}</div>
            {s.pid && <div className="service-pid">PID {s.pid}</div>}
          </div>
        ))}
      </div>
    </div>
  );
}
