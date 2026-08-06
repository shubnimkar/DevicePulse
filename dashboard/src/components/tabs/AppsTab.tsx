'use client';

import { useState } from 'react';
import { AppEntry } from '@/types';

interface Props {
  data?: { installed_apps: AppEntry[] | null; count: number; source?: string };
}

export default function AppsTab({ data }: Props) {
  const [search, setSearch] = useState('');
  const allApps = data?.installed_apps ?? [];
  if (!allApps.length) return <div className="no-data">No installed apps data yet.</div>;

  const filtered = search
    ? allApps.filter(a => a.name.toLowerCase().includes(search.toLowerCase()))
    : allApps;

  return (
    <div>
      <div className="collector-header">
        <span className="count-pill count-running">📦 {data?.count ?? allApps.length} apps</span>
        {data?.source && <span className="count-pill count-source">{data.source}</span>}
        <input
          className="app-search"
          placeholder="Search apps…"
          value={search}
          onChange={e => setSearch(e.target.value)}
          aria-label="Search installed applications"
        />
      </div>
      <div className="apps-grid">
        {filtered.slice(0, 60).map((app, i) => (
          <div key={i} className="app-item" title={app.path}>
            <div className="app-name">{app.name}</div>
            <div className="app-version">{app.version ?? '—'}</div>
          </div>
        ))}
        {filtered.length > 60 && (
          <div className="no-data" style={{ gridColumn: '1/-1', padding: '0.75rem' }}>
            +{filtered.length - 60} more
          </div>
        )}
      </div>
    </div>
  );
}
