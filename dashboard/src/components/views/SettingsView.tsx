'use client';

import { useEffect, useMemo, useState, type ReactNode } from 'react';
import type { EnterprisePolicy } from '@/types';
import ConfirmDialog from '@/components/ConfirmDialog';

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
  deviceCount: number;
  audit?: { by: string; at: string } | null;
  onDirtyChange?: (dirty: boolean) => void;
}

// Collectors that capture what a person is doing, not just machine health.
const SENSITIVE: ReadonlySet<string> = new Set(['collect_processes', 'collect_active_window', 'collect_installed_apps']);

const fmtWhen = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' });
};

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

// clickable: the whole row is a <label>, so the text toggles the switch inside it.
function Row({ title, hint, children, error, clickable }: { title: string; hint: string; children: ReactNode; error?: string; clickable?: boolean }) {
  const Tag = clickable ? 'label' : 'div';
  return (
    <Tag className={`set-row${clickable ? ' is-clickable' : ''}`}>
      <div className="set-row-text"><strong>{title}</strong><small>{hint}</small>{error && <em role="alert">{error}</em>}</div>
      <div className="set-row-control">{children}</div>
    </Tag>
  );
}

export default function SettingsView({ policy, canManage, collectors, onSave, saving, savedAt, saveFailed, releases, deviceCount, audit, onDirtyChange }: Props) {
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

  // Changes that reduce what the fleet collects or keeps get an explicit confirm.
  const risky = [
    ...collectors.filter(c => policy[c.key] && !draft[c.key]).map(c => `Stop collecting ${c.label.toLowerCase()}`),
    ...(draft.telemetry_retention_days < policy.telemetry_retention_days ? [`Shorten retention to ${draft.telemetry_retention_days} days`] : []),
  ];
  const [confirming, setConfirming] = useState(false);
  const dirty = changed.length > 0;
  useEffect(() => {
    onDirtyChange?.(dirty);
    return () => onDirtyChange?.(false);
  }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  const requestSave = () => (risky.length ? setConfirming(true) : void onSave(draft));

  const [active, setActive] = useState('set-collect');
  useEffect(() => {
    const ids = ['set-collect', 'set-retention', 'set-upload', 'set-privacy', 'set-releases'];
    const seen = new Map<string, boolean>();
    const io = new IntersectionObserver(entries => {
      entries.forEach(e => seen.set(e.target.id, e.isIntersecting));
      const first = ids.find(id => seen.get(id));
      if (first) setActive(first);
    }, { rootMargin: '-15% 0px -70% 0px' });
    ids.forEach(id => { const el = document.getElementById(id); if (el) io.observe(el); });
    return () => io.disconnect();
  }, []);

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
        {sections.map(s => <button key={s.id} type="button" aria-current={active === s.id ? 'true' : undefined} onClick={() => go(s.id)}>{s.label}</button>)}
      </nav>

      <div className="set-main">
        {audit?.at && <p className="set-meta">Last changed by <strong>{audit.by || 'unknown'}</strong> on {fmtWhen(audit.at)}</p>}
        {!canManage && <div className="set-notice" role="status">Read-only access. A manager or admin can change fleet policy.</div>}

        <section id="set-collect" className="set-card" aria-labelledby="set-collect-h">
          <header><div><h2 id="set-collect-h">Data collection</h2><p>Only enable the telemetry categories your environment needs.</p></div><span className="count-badge">{enabledCount} of {collectors.length} on</span></header>
          {([
            ['Device health', collectors.filter(c => !SENSITIVE.has(c.key)), false],
            ['User activity', collectors.filter(c => SENSITIVE.has(c.key)), true],
          ] as const).map(([title, items, sensitive]) => items.length > 0 && (
            <div key={title} className="set-group">
              <h3>{title}{sensitive && <span className="tag tag-resource">Privacy-sensitive</span>}</h3>
              <div className="set-grid">
                {items.map(c => (
                  <label key={c.key} className="set-tile">
                    <span><strong>{c.label}</strong><small>{c.meta}</small></span>
                    <Switch label={c.label} checked={Boolean(draft[c.key])} disabled={!canManage} onChange={v => set(c.key, v)} />
                  </label>
                ))}
              </div>
            </div>
          ))}
        </section>

        <section id="set-retention" className="set-card" aria-labelledby="set-retention-h">
          <header><div><h2 id="set-retention-h">Retention &amp; sync</h2><p>How long telemetry stays queryable and how often agents report.</p></div></header>
          <Row title="Telemetry retention" hint="Applies to newly stored server telemetry." error={errors.telemetry_retention_days}>{num('telemetry_retention_days', 'days')}</Row>
          <Row title="Sync interval" hint="Seconds between agent check-ins." error={errors.sync_interval_seconds}>{num('sync_interval_seconds', 'seconds')}</Row>
        </section>

        <section id="set-upload" className="set-card" aria-labelledby="set-upload-h">
          <header><div><h2 id="set-upload-h">Upload efficiency</h2><p>Send less by skipping data that has not changed.</p></div></header>
          <Row clickable title="Delta upload" hint="Upload changed collector payloads instead of full repeated snapshots.">
            <Switch label="Delta upload" checked={draft.delta_upload_enabled} disabled={!canManage} onChange={v => set('delta_upload_enabled', v)} />
          </Row>
          <Row clickable title="Cache unchanged collector data" hint="Keep local fingerprints so unchanged apps, services, ports and browser entries are skipped.">
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
                <button type="button" className="action-btn primary" disabled={saving || invalid} onClick={requestSave}>{saving ? 'Saving…' : 'Save changes'}</button>
              </div>
            )}
          </div>
        )}
      </div>

      <ConfirmDialog
        open={confirming}
        title="Apply to the whole fleet?"
        message={`${risky.join('\n')}\n\nThis applies to all ${deviceCount} device${deviceCount === 1 ? '' : 's'} on their next check-in.`}
        confirmLabel="Apply to all devices"
        danger
        onConfirm={() => { setConfirming(false); void onSave(draft); }}
        onCancel={() => setConfirming(false)}
      />
    </div>
  );
}
