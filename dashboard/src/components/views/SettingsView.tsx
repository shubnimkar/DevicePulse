'use client';

import { useMemo, useState, type ReactNode } from 'react';
import type { EnterprisePolicy } from '@/types';

type CollectorKey = Extract<keyof EnterprisePolicy, `collect_${string}`>;

interface Props {
  policy: EnterprisePolicy;
  canManage: boolean;
  collectors: { key: CollectorKey; label: string; meta: string }[];
  onSave: (next: EnterprisePolicy) => Promise<boolean>;
  saving: boolean;
  savedAt: string;
  saveFailed: boolean;
  releases?: ReactNode; // admin-only agent rollout section, rendered by the page
}

const FIELD_LABEL: Partial<Record<keyof EnterprisePolicy, string>> = {
  telemetry_retention_days: 'Retention', sync_interval_seconds: 'Sync interval', delta_upload_enabled: 'Delta upload',
  cache_unchanged_collector_data: 'Collector cache', browser_history_mode: 'History detail', browser_history_limit: 'Entry limit',
};

const LIMITS = {
  telemetry_retention_days: [1, 3650],
  sync_interval_seconds: [10, 3600],
  browser_history_limit: [0, 1000],
} as const;

function Switch({ label, checked, disabled, onChange }: { label: string; checked: boolean; disabled: boolean; onChange: (v: boolean) => void }) {
  return <input type="checkbox" role="switch" className="switch" aria-label={label} checked={checked} disabled={disabled} onChange={e => onChange(e.target.checked)} />;
}

function Row({ title, hint, children, error }: { title: string; hint: string; children: ReactNode; error?: string }) {
  return (
    <div className="set-row">
      <div className="set-row-text"><strong>{title}</strong><small>{hint}</small>{error && <em role="alert">{error}</em>}</div>
      <div className="set-row-control">{children}</div>
    </div>
  );
}

export default function SettingsView({ policy, canManage, collectors, onSave, saving, savedAt, saveFailed, releases }: Props) {
  const [draft, setDraft] = useState(policy);
  const [base, setBase] = useState(policy);
  if (base !== policy) { setBase(policy); setDraft(policy); } // re-sync when the saved policy changes

  const set = <K extends keyof EnterprisePolicy>(key: K, value: EnterprisePolicy[K]) =>
    setDraft(d => {
      const next = { ...d, [key]: value };
      if (key === 'browser_history_mode') next.collect_browser_history = value !== 'disabled';
      return next;
    });

  const changed = useMemo(() => (Object.keys(policy) as (keyof EnterprisePolicy)[]).filter(k => draft[k] !== policy[k]), [draft, policy]);
  const errors = (Object.keys(LIMITS) as (keyof typeof LIMITS)[]).reduce<Partial<Record<keyof typeof LIMITS, string>>>((acc, k) => {
    const [lo, hi] = LIMITS[k], v = draft[k];
    if (!Number.isFinite(v) || v < lo || v > hi) acc[k] = `Enter a value from ${lo} to ${hi}.`;
    return acc;
  }, {});
  const invalid = Object.keys(errors).length > 0;
  const changedLabels = [...new Set(changed.map(k => FIELD_LABEL[k] ?? (k.startsWith('collect_') ? 'Collectors' : k)))];

  const num = (key: keyof typeof LIMITS, unit: string) => (
    <div className="number-control">
      <input type="number" min={LIMITS[key][0]} max={LIMITS[key][1]} value={Number.isFinite(draft[key]) ? draft[key] : ''} disabled={!canManage}
        aria-invalid={!!errors[key]} onChange={e => set(key, e.target.value === '' ? NaN : Number(e.target.value))} />
      <span>{unit}</span>
    </div>
  );

  const sections = [
    { id: 'set-collect', label: 'Data collection' },
    { id: 'set-retention', label: 'Retention & sync' },
    { id: 'set-upload', label: 'Upload efficiency' },
    { id: 'set-privacy', label: 'Browser privacy' },
    ...(releases ? [{ id: 'set-releases', label: 'Agent releases' }] : []),
  ];
  const go = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' });
  const enabledCount = collectors.filter(c => draft[c.key]).length;

  return (
    <div className="set-shell">
      <nav className="set-nav" aria-label="Settings sections">
        {sections.map(s => <button key={s.id} type="button" onClick={() => go(s.id)}>{s.label}</button>)}
      </nav>

      <div className="set-main">
        {!canManage && <div className="set-notice" role="status">Read-only access. A manager or admin can change fleet policy.</div>}

        <section id="set-collect" className="set-card" aria-labelledby="set-collect-h">
          <header><div><h2 id="set-collect-h">Data collection</h2><p>Only enable the telemetry categories your environment needs.</p></div><span className="count-badge">{enabledCount} of {collectors.length} on</span></header>
          <div className="set-grid">
            {collectors.map(c => (
              <label key={c.key} className="set-tile">
                <span><strong>{c.label}</strong><small>{c.meta}</small></span>
                <Switch label={c.label} checked={Boolean(draft[c.key])} disabled={!canManage} onChange={v => set(c.key, v)} />
              </label>
            ))}
          </div>
        </section>

        <section id="set-retention" className="set-card" aria-labelledby="set-retention-h">
          <header><div><h2 id="set-retention-h">Retention &amp; sync</h2><p>How long telemetry stays queryable and how often agents report.</p></div></header>
          <Row title="Telemetry retention" hint="Applies to newly stored server telemetry." error={errors.telemetry_retention_days}>{num('telemetry_retention_days', 'days')}</Row>
          <Row title="Sync interval" hint="Seconds between agent check-ins." error={errors.sync_interval_seconds}>{num('sync_interval_seconds', 'seconds')}</Row>
        </section>

        <section id="set-upload" className="set-card" aria-labelledby="set-upload-h">
          <header><div><h2 id="set-upload-h">Upload efficiency</h2><p>Send less by skipping data that has not changed.</p></div></header>
          <Row title="Delta upload" hint="Upload changed collector payloads instead of full repeated snapshots.">
            <Switch label="Delta upload" checked={draft.delta_upload_enabled} disabled={!canManage} onChange={v => set('delta_upload_enabled', v)} />
          </Row>
          <Row title="Cache unchanged collector data" hint="Keep local fingerprints so unchanged apps, services, ports and browser entries are skipped.">
            <Switch label="Cache unchanged collector data" checked={draft.cache_unchanged_collector_data} disabled={!canManage} onChange={v => set('cache_unchanged_collector_data', v)} />
          </Row>
        </section>

        <section id="set-privacy" className="set-card" aria-labelledby="set-privacy-h">
          <header><div><h2 id="set-privacy-h">Browser privacy</h2><p>How much URL detail is retained for browser history reporting.</p></div></header>
          <Row title="History detail" hint="Domain only stores hostnames; full URL also stores paths and queries.">
            <div className="segmented-control" role="group" aria-label="Browser history mode">
              {([['domain_only', 'Domain only'], ['full_url', 'Full URL']] as const).map(([value, label]) => (
                <button key={value} type="button" className={draft.browser_history_mode === value ? 'active' : ''} aria-pressed={draft.browser_history_mode === value}
                  disabled={!canManage} onClick={() => set('browser_history_mode', value)}>{label}</button>
              ))}
            </div>
          </Row>
          <Row title="Entry limit" hint="Maximum browser entries kept per collection cycle." error={errors.browser_history_limit}>{num('browser_history_limit', 'entries')}</Row>
        </section>

        {releases && <section id="set-releases" className="set-releases" aria-label="Agent releases">{releases}</section>}

        {canManage && (changed.length > 0 || saving || savedAt || saveFailed) && (
          <div className={`save-bar${changed.length ? ' is-dirty' : ''}`} role="region" aria-label="Policy changes" aria-live="polite">
            <div className="save-bar-text">
              {changed.length > 0 ? <><strong>{changed.length} unsaved change{changed.length === 1 ? '' : 's'}</strong><small>{changedLabels.join(', ')} · applies to the whole fleet</small></>
                : saveFailed ? <><strong className="txt-crit">Not saved</strong><small>The policy could not be saved. Try again.</small></>
                : <><strong>Policy saved</strong><small>Last saved {savedAt}</small></>}
            </div>
            {changed.length > 0 && (
              <div className="save-bar-actions">
                <button type="button" className="action-btn" disabled={saving} onClick={() => setDraft(policy)}>Discard</button>
                <button type="button" className="action-btn primary" disabled={saving || invalid} onClick={() => void onSave(draft)}>{saving ? 'Saving…' : 'Save changes'}</button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
