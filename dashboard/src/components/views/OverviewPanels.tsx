'use client';

import { useState, type ReactNode } from 'react';
import { deviceDisplayName } from '@/lib/utils';
import { StatusIcon, type Status } from '@/components/ui/StatusPill';
import { ISSUE_LABEL, type IssueKind, type fleetMetrics } from './overviewMetrics';

export type StatusFilter = 'all' | Status;
export type Metrics = ReturnType<typeof fleetMetrics>;

export const pct = (v?: number) => (v === undefined ? '—' : `${v.toFixed(0)}%`);

const SEGMENTS: [Status, string][] = [['online', 'Online'], ['warning', 'Warning'], ['critical', 'Critical'], ['offline', 'Offline']];
const tone = (v: number) => (v >= 85 ? 'crit' : v >= 70 ? 'warn' : 'ok');
const ratio = (n: number, d: number) => (d ? Math.round((n / d) * 100) : 0);

function Kpi({ label, value, unit, children }: { label: string; value: ReactNode; unit?: string; children: ReactNode }) {
  return (
    <section className="kpi" aria-label={label}>
      <span className="kpi-label">{label}</span>
      <strong className="kpi-value">{value}{unit && <small>{unit}</small>}</strong>
      <div className="kpi-body">{children}</div>
    </section>
  );
}

function Meter({ label, value, caption }: { label: string; value?: number; caption?: string }) {
  return (
    <div className="meter">
      <span>{label}</span>
      <i role="progressbar" aria-label={label} aria-valuenow={Math.round(value ?? 0)} aria-valuemin={0} aria-valuemax={100}>
        <b className={`t-${tone(value ?? 0)}`} style={{ width: `${Math.min(100, Math.max(value ? 2 : 0, value ?? 0))}%` }} />
      </i>
      <em>{pct(value)}</em>
      {caption && <small>{caption}</small>}
    </div>
  );
}

interface KpiProps { m: Metrics; counts: Record<StatusFilter, number>; onOpenDevices: (f: StatusFilter) => void; onInspect: (id: string) => void }

export function KpiRow({ m, counts, onOpenDevices, onInspect }: KpiProps) {
  const total = counts.all;
  return (
    <div className="kpi-row">
      <Kpi label="Availability" value={ratio(counts.online, total)} unit="%">
        <div className="seg-bar" role="group" aria-label="Devices by status">
          {total === 0 ? <span className="seg seg-empty" /> : SEGMENTS.filter(([k]) => counts[k] > 0).map(([k, label]) => (
            <button key={k} type="button" className={`seg seg-${k}`} style={{ flexGrow: counts[k] }} title={`${label}: ${counts[k]}`}
              aria-label={`Show ${counts[k]} ${label.toLowerCase()} device${counts[k] === 1 ? '' : 's'}`} onClick={() => onOpenDevices(k)} />
          ))}
        </div>
        <p className="kpi-sub"><strong>{counts.online}</strong> of {total} online
          {counts.warning + counts.critical > 0 && <> · <span className="txt-warn">{counts.warning + counts.critical} degraded</span></>}
          {counts.offline > 0 && <> · {counts.offline} offline</>}
        </p>
      </Kpi>

      <Kpi label="Resource load" value={m.cpu === undefined ? '—' : Math.round(m.cpu)} unit={m.cpu === undefined ? undefined : '% CPU'}>
        <Meter label="Memory" value={m.mem} />
        <p className="kpi-sub">
          {m.peakCpu ? <>Peak CPU <button type="button" className="inline-link" onClick={() => onInspect(m.peakCpu!.d.device_id)}>{deviceDisplayName(m.peakCpu.d)}</button> {pct(m.peakCpu.v)}</> : 'No live devices reporting'}
        </p>
      </Kpi>

      <Kpi label="Patch compliance" value={m.patchScope ? ratio(m.patched, m.patchScope) : '—'} unit={m.patchScope ? '%' : undefined}>
        <div className="seg-bar" aria-hidden="true">
          {m.patched > 0 && <span className="seg seg-online" style={{ flexGrow: m.patched }} />}
          {m.patchScope > m.patched && <span className="seg seg-warning" style={{ flexGrow: m.patchScope - m.patched }} />}
        </div>
        <p className="kpi-sub"><strong>{m.patched}</strong> of {m.patchScope} fully patched · {m.pendingTotal.toLocaleString()} packages pending</p>
      </Kpi>

      <Kpi label="Agent currency" value={m.versioned ? ratio(m.onLatest, m.versioned) : '—'} unit={m.versioned ? '%' : undefined}>
        <div className="seg-bar" aria-hidden="true">
          {m.onLatest > 0 && <span className="seg seg-online" style={{ flexGrow: m.onLatest }} />}
          {m.versioned > m.onLatest && <span className="seg seg-warning" style={{ flexGrow: m.versioned - m.onLatest }} />}
        </div>
        <p className="kpi-sub"><strong>{m.onLatest}</strong> of {m.versioned} on {m.latest ? `v${m.latest.replace(/^v/i, '')}` : 'latest'}</p>
      </Kpi>
    </div>
  );
}

export function ActionQueue({ m, total, onInspect, onOpenDevices, limit = 6 }: { m: Metrics; total: number; onInspect: (id: string) => void; onOpenDevices: (f: StatusFilter) => void; limit?: number }) {
  const [kind, setKind] = useState<IssueKind | 'all'>('all');
  const kinds = (Object.keys(ISSUE_LABEL) as IssueKind[]).map(k => ({ k, n: m.issues.filter(i => i.kind === k).length })).filter(x => x.n > 0);
  const rows = kind === 'all' ? m.issues : m.issues.filter(i => i.kind === kind);
  return (
    <section className="card queue" aria-labelledby="queue-h">
      <header className="card-head">
        <div><h2 id="queue-h">Action queue</h2><p>Open issues across the fleet, most urgent first</p></div>
        <span className="count-badge">{m.issues.length}</span>
      </header>
      {m.issues.length > 0 && (
        <div className="chips" role="group" aria-label="Filter issues by type">
          <button type="button" className="chip" aria-pressed={kind === 'all'} onClick={() => setKind('all')}>All <b>{m.issues.length}</b></button>
          {kinds.map(({ k, n }) => (
            <button key={k} type="button" className="chip" aria-pressed={kind === k} onClick={() => setKind(k)}>{ISSUE_LABEL[k]} <b>{n}</b></button>
          ))}
        </div>
      )}
      {rows.length === 0 ? (
        <p className="card-empty"><StatusIcon status="online" /> No open issues. The fleet is healthy.</p>
      ) : (
        <ul className="queue-list">
          {rows.slice(0, limit).map((i, idx) => (
            <li key={`${i.device.device_id}:${i.kind}:${idx}`}>
              <button type="button" className="queue-row" onClick={() => onInspect(i.device.device_id)}>
                <span className={`tag tag-${i.kind}`}>{ISSUE_LABEL[i.kind]}</span>
                <span className="q-name">{deviceDisplayName(i.device)}</span>
                <span className="q-detail">{i.detail}</span>
                <span className="q-chev" aria-hidden="true">›</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <footer className="card-foot queue-foot">
        <span>{rows.length > limit ? `Showing ${limit} of ${rows.length}` : `${rows.length} open`}</span>
        <button type="button" className="inline-link" onClick={() => onOpenDevices('all')}>View all {total} devices →</button>
      </footer>
    </section>
  );
}

export function CapacityCard({ m, onInspect }: { m: Metrics; onInspect: (id: string) => void }) {
  const max = Math.max(1, ...m.buckets.map(b => b.n));
  return (
    <section className="card" aria-labelledby="cap-h">
      <header className="card-head"><div><h2 id="cap-h">Disk capacity</h2><p>Devices by system-disk usage</p></div></header>
      {m.diskCount === 0 ? <p className="card-empty">No disk data reported yet.</p> : (
        <>
          <div className="histo" role="list">
            {m.buckets.map(b => (
              <div key={b.label} className="histo-col" role="listitem" aria-label={`${b.n} devices at ${b.label}`}>
                <em>{b.n}</em>
                <i><b className={`t-${b.tone}`} style={{ height: `${Math.max(b.n ? 6 : 2, (b.n / max) * 100)}%` }} /></i>
                <span>{b.label}</span>
              </div>
            ))}
          </div>
          {m.fullest && <p className="card-foot">Fullest: <button type="button" className="inline-link" onClick={() => onInspect(m.fullest!.d.device_id)}>{deviceDisplayName(m.fullest.d)}</button> at {pct(m.fullest.v)}</p>}
        </>
      )}
    </section>
  );
}

const PALETTE = ['var(--accent)', '#5fb0ff', '#c58bff', '#f2a65a', '#7c8699'];

export function CompositionCard({ m }: { m: Metrics }) {
  return (
    <section className="card" aria-labelledby="comp-h">
      <header className="card-head"><div><h2 id="comp-h">Platform mix</h2><p>Operating systems under management</p></div></header>
      <div className="seg-bar seg-bar-lg" aria-hidden="true">
        {m.platforms.map(([name, n], i) => <span key={name} className="seg" style={{ flexGrow: n, background: PALETTE[Math.min(i, PALETTE.length - 1)] }} />)}
      </div>
      <ul className="legend">
        {m.platforms.map(([name, n], i) => (
          <li key={name}><i style={{ background: PALETTE[Math.min(i, PALETTE.length - 1)] }} /><span>{name}</span><b>{n}</b><em>{ratio(n, m.total)}%</em></li>
        ))}
      </ul>
    </section>
  );
}
