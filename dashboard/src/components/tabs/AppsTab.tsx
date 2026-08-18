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
      <div className="apps-header">
        <span className="pill pill-neutral">{data?.count ?? allApps.length} apps</span>
        {data?.source && <span className="pill pill-blue mono">{data.source}</span>}
        <input
          className="app-search"
          placeholder="Search apps…"
          value={search}
          onChange={e => setSearch(e.target.value)}
          aria-label="Search installed applications"
        />
      </div>
      <div className="apps-grid">
        {filtered.map((app, i) => (
          <div key={i} className="app-item" title={app.path}>
            <div className="app-name">{app.name}</div>
            <div className="app-version">{app.version ?? '—'}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
