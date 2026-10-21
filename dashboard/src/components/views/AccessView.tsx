'use client';

import { useMemo, useState, type FormEvent } from 'react';
import type { AuditEvent, DashboardUser, UserRole } from '@/types';
import ConfirmDialog from '@/components/ConfirmDialog';

type Filter = 'all' | UserRole | 'disabled';

interface Props {
  users: DashboardUser[];
  currentUserId?: string;
  onRequestRoleChange: (user: DashboardUser, role: UserRole) => void;
  onCreate: (form: { name: string; email: string; password: string; role: UserRole }) => Promise<boolean>;
  onResetPassword: (user: DashboardUser, password: string) => Promise<boolean>;
  onSetStatus: (user: DashboardUser, status: 'active' | 'disabled') => Promise<boolean>;
  onRevokeSessions: (user: DashboardUser) => Promise<boolean>;
  notices: string[]; // latest create / reset / role-change results
  audit: AuditEvent[];
}

const ROLES: UserRole[] = ['admin', 'manager', 'viewer'];
const ROLE_LABEL: Record<UserRole, string> = { admin: 'Admin', manager: 'Manager', viewer: 'Viewer' };
const ROLE_HINT: Record<UserRole, string> = {
  admin: 'Full control: users, devices, releases and fleet policy.',
  manager: 'Viewer access plus fleet policy and routine device commands.',
  viewer: 'Read-only: devices, reports and current policy.',
};

// Derived from the API's route guards (requireRole) and the device Security tab.
const CAPABILITIES: { label: string; min: UserRole }[] = [
  { label: 'View devices, reports and policy', min: 'viewer' },
  { label: 'Change fleet policy', min: 'manager' },
  { label: 'Collect now and restart agents', min: 'manager' },
  { label: 'Lock, quarantine and wipe devices', min: 'admin' },
  { label: 'Rename, ping and remove devices', min: 'admin' },
  { label: 'Manage agent releases', min: 'admin' },
  { label: 'Manage dashboard users', min: 'admin' },
];
const RANK: Record<UserRole, number> = { viewer: 1, manager: 2, admin: 3 };

/** Plain-language effect of moving a user between roles, for the confirm dialog. */
export function roleChangeSummary(email: string, from: UserRole, to: UserRole): string {
  const gained = CAPABILITIES.filter(c => RANK[to] >= RANK[c.min] && RANK[from] < RANK[c.min]).map(c => c.label);
  const lost = CAPABILITIES.filter(c => RANK[from] >= RANK[c.min] && RANK[to] < RANK[c.min]).map(c => c.label);
  return [
    `Change ${email} from ${ROLE_LABEL[from]} to ${ROLE_LABEL[to]}?`,
    gained.length ? `\nGains:\n• ${gained.join('\n• ')}` : '',
    lost.length ? `\nLoses:\n• ${lost.join('\n• ')}` : '',
    '\nThe change takes effect on their next request.',
  ].join('\n');
}

const EVENT_TEXT: Record<string, (e: AuditEvent) => string> = {
  'user.created': e => `added ${e.target} as ${e.detail}`,
  'user.role_changed': e => `changed ${e.target}: ${e.detail}`,
  'user.password_reset': e => `reset the password for ${e.target}`,
  'user.deactivate': e => `deactivated ${e.target}`,
  'user.reactivate': e => `reactivated ${e.target}`,
  'user.sessions_revoked': e => `signed ${e.target} out everywhere`,
  'policy.updated': () => 'updated the fleet policy',
};

function generatePassword(): string {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789!@#$%^*-_';
  const buf = new Uint32Array(16);
  crypto.getRandomValues(buf);
  return Array.from(buf, n => chars[n % chars.length]).join('');
}

const initials = (u: DashboardUser) => (u.name || u.email).split(/[\s@.]+/).filter(Boolean).slice(0, 2).map(p => p[0]?.toUpperCase()).join('');
const fmtDate = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' }) : '—');
const fmtTime = (iso: string) => new Date(iso).toLocaleString(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

function PasswordField({ value, onChange, label, id }: { value: string; onChange: (v: string) => void; label: string; id: string }) {
  const [show, setShow] = useState(false);
  return (
    <div className="pw-field">
      <input id={id} type={show ? 'text' : 'password'} value={value} onChange={e => onChange(e.target.value)} minLength={8} required
        autoComplete="new-password" aria-label={label} placeholder="At least 8 characters" />
      <button type="button" className="action-btn" onClick={() => setShow(s => !s)} aria-pressed={show}>{show ? 'Hide' : 'Show'}</button>
      <button type="button" className="action-btn" onClick={() => { onChange(generatePassword()); setShow(true); }}>Generate</button>
    </div>
  );
}

type Pending = { kind: 'deactivate' | 'reactivate' | 'revoke'; user: DashboardUser } | null;

export default function AccessView({ users, currentUserId, onRequestRoleChange, onCreate, onResetPassword, onSetStatus, onRevokeSessions, notices, audit }: Props) {
  const [filter, setFilter] = useState<Filter>('all');
  const [query, setQuery] = useState('');
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState<{ name: string; email: string; password: string; role: UserRole }>({ name: '', email: '', password: '', role: 'viewer' });
  const [handoff, setHandoff] = useState<{ email: string; password: string; role: UserRole } | null>(null);
  const [copied, setCopied] = useState(false);
  const [openFor, setOpenFor] = useState('');
  const [resetting, setResetting] = useState(false);
  const [resetValue, setResetValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Pending>(null);

  const isActive = (u: DashboardUser) => (u.status ?? 'active') === 'active';
  const counts = useMemo(() => ROLES.reduce<Record<UserRole, number>>((m, r) => ({ ...m, [r]: users.filter(u => u.role === r && isActive(u)).length }), { admin: 0, manager: 0, viewer: 0 }), [users]);
  const disabledCount = users.filter(u => !isActive(u)).length;
  const q = query.trim().toLowerCase();
  const shown = users.filter(u => (filter === 'all' || (filter === 'disabled' ? !isActive(u) : u.role === filter && isActive(u))) && (!q || `${u.name} ${u.email}`.toLowerCase().includes(q)));
  const notice = notices.find(Boolean);
  const lastAdmin = (u: DashboardUser) => u.role === 'admin' && isActive(u) && counts.admin <= 1;

  const submitCreate = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const ok = await onCreate(form);
    setBusy(false);
    if (ok) {
      setHandoff({ email: form.email, password: form.password, role: form.role });
      setCopied(false);
      setForm({ name: '', email: '', password: '', role: 'viewer' });
      setAdding(false);
    }
  };
  const submitReset = async (e: FormEvent, user: DashboardUser) => {
    e.preventDefault();
    setBusy(true);
    const ok = await onResetPassword(user, resetValue);
    setBusy(false);
    if (ok) { setResetting(false); setResetValue(''); setHandoff({ email: user.email, password: resetValue, role: user.role }); setCopied(false); }
  };
  const copyHandoff = async () => {
    if (!handoff) return;
    const text = `DevicePulse sign-in\nURL: ${window.location.origin}\nEmail: ${handoff.email}\nTemporary password: ${handoff.password}`;
    try { await navigator.clipboard.writeText(text); setCopied(true); } catch { setCopied(false); }
  };
  const runPending = async () => {
    const p = pending;
    setPending(null);
    if (!p) return;
    if (p.kind === 'revoke') await onRevokeSessions(p.user);
    else await onSetStatus(p.user, p.kind === 'deactivate' ? 'disabled' : 'active');
  };
  const pendingText: Record<NonNullable<Pending>['kind'], { title: string; msg: (u: DashboardUser) => string; label: string; danger: boolean }> = {
    deactivate: { title: 'Deactivate account?', msg: u => `${u.email} will be signed out immediately and cannot sign in until you reactivate the account. Their history is kept.`, label: 'Deactivate', danger: true },
    reactivate: { title: 'Reactivate account?', msg: u => `${u.email} will be able to sign in again with their current password and the ${ROLE_LABEL[u.role]} role.`, label: 'Reactivate', danger: false },
    revoke: { title: 'Sign out everywhere?', msg: u => `All of ${u.email}'s active sessions end now. They can sign in again with their password.`, label: 'Sign out', danger: true },
  };

  return (
    <div className="acc">
      <header className="acc-top">
        <ul className="acc-stats" aria-label="Active users by role">
          <li><strong>{users.length - disabledCount}</strong><span>Active</span></li>
          {ROLES.map(r => <li key={r}><strong>{counts[r]}</strong><span>{ROLE_LABEL[r]}{counts[r] === 1 ? '' : 's'}</span></li>)}
          {disabledCount > 0 && <li className="is-muted"><strong>{disabledCount}</strong><span>Deactivated</span></li>}
        </ul>
        <button type="button" className="action-btn primary" aria-expanded={adding} onClick={() => setAdding(a => !a)}>{adding ? 'Close' : 'Add user'}</button>
      </header>

      {notice && <p className="acc-notice" role="status">{notice}</p>}

      {handoff && (
        <section className="card acc-handoff" aria-labelledby="acc-handoff-h">
          <div>
            <h2 id="acc-handoff-h">Share sign-in details</h2>
            <p>The temporary password is shown only now. Send it over a secure channel and ask {handoff.email} to change it after signing in.</p>
            <dl>
              <div><dt>Email</dt><dd>{handoff.email}</dd></div>
              <div><dt>Temporary password</dt><dd className="mono">{handoff.password}</dd></div>
              <div><dt>Role</dt><dd>{ROLE_LABEL[handoff.role]}</dd></div>
            </dl>
          </div>
          <div className="acc-handoff-actions">
            <button type="button" className="action-btn primary" onClick={() => void copyHandoff()}>{copied ? 'Copied' : 'Copy details'}</button>
            <button type="button" className="action-btn" onClick={() => setHandoff(null)}>Done</button>
          </div>
        </section>
      )}

      {adding && (
        <section className="card acc-add" aria-labelledby="acc-add-h">
          <header className="card-head"><div><h2 id="acc-add-h">Add dashboard user</h2><p>They can sign in as soon as the account is created.</p></div></header>
          <form onSubmit={submitCreate} className="acc-form">
            <label className="build-field"><span>Name</span><input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} autoComplete="name" /></label>
            <label className="build-field"><span>Email</span><input type="email" value={form.email} onChange={e => setForm(f => ({ ...f, email: e.target.value }))} autoComplete="email" required /></label>
            <label className="build-field"><span>Role</span>
              <select value={form.role} onChange={e => setForm(f => ({ ...f, role: e.target.value as UserRole }))}>
                {ROLES.map(r => <option key={r} value={r}>{ROLE_LABEL[r]}</option>)}
              </select>
              <small>{ROLE_HINT[form.role]}</small>
            </label>
            <div className="build-field"><span>Temporary password</span>
              <PasswordField id="acc-new-pw" label="Temporary password" value={form.password} onChange={v => setForm(f => ({ ...f, password: v }))} />
            </div>
            <div className="acc-form-actions">
              <button type="button" className="action-btn" onClick={() => setAdding(false)}>Cancel</button>
              <button type="submit" className="action-btn primary" disabled={busy}>{busy ? 'Creating…' : 'Create user'}</button>
            </div>
          </form>
        </section>
      )}

      <section className="card" aria-labelledby="acc-users-h">
        <header className="card-head"><div><h2 id="acc-users-h">Users</h2><p>Accounts with access to this dashboard</p></div></header>
        <div className="acc-toolbar">
          <input type="search" className="acc-search" value={query} onChange={e => setQuery(e.target.value)} placeholder="Search name or email" aria-label="Search users" />
          <div className="chips" role="group" aria-label="Filter by role">
            <button type="button" className="chip" aria-pressed={filter === 'all'} onClick={() => setFilter('all')}>All <b>{users.length}</b></button>
            {ROLES.map(r => <button key={r} type="button" className="chip" aria-pressed={filter === r} onClick={() => setFilter(r)}>{ROLE_LABEL[r]} <b>{counts[r]}</b></button>)}
            {disabledCount > 0 && <button type="button" className="chip" aria-pressed={filter === 'disabled'} onClick={() => setFilter('disabled')}>Deactivated <b>{disabledCount}</b></button>}
          </div>
        </div>

        <div className="acc-head" aria-hidden="true"><span>User</span><span>Role</span><span>Status</span><span>Created</span><span /></div>
        {shown.length === 0 ? <p className="card-empty">No users match this filter.</p> : (
          <ul className="acc-list">
            {shown.map(u => {
              const self = u.id === currentUserId;
              const active = isActive(u);
              const guard = self ? 'You cannot change your own role or status' : lastAdmin(u) ? 'At least one active admin must remain' : undefined;
              return (
                <li key={u.id} className={`acc-row${active ? '' : ' is-off'}`}>
                  <div className="acc-user">
                    <span className="acc-avatar" aria-hidden="true">{initials(u)}</span>
                    <div><strong>{u.name || u.email}{self && <span className="tag tag-agent">You</span>}</strong><small>{u.email}</small></div>
                  </div>
                  <div className="acc-cell" data-label="Role">
                    <select className="role-select" value={u.role} disabled={!!guard || !active} title={guard}
                      onChange={e => onRequestRoleChange(u, e.target.value as UserRole)} aria-label={`Role for ${u.email}`}>
                      {ROLES.map(r => <option key={r} value={r}>{ROLE_LABEL[r]}</option>)}
                    </select>
                  </div>
                  <div className="acc-cell" data-label="Status"><span className={`acc-status${active ? ' is-active' : ''}`}>{active ? 'Active' : 'Deactivated'}</span></div>
                  <div className="acc-cell acc-date" data-label="Created">{fmtDate(u.created_at)}</div>
                  <div className="acc-actions">
                    <button type="button" className="action-btn" aria-expanded={openFor === u.id}
                      onClick={() => { setOpenFor(openFor === u.id ? '' : u.id); setResetting(false); setResetValue(''); }}>Manage</button>
                  </div>
                  {openFor === u.id && (
                    <div className="acc-manage">
                      {resetting ? (
                        <form className="acc-reset" onSubmit={e => submitReset(e, u)}>
                          <PasswordField id={`reset-${u.id}`} label={`New password for ${u.email}`} value={resetValue} onChange={setResetValue} />
                          <button type="submit" className="action-btn primary" disabled={busy}>{busy ? 'Saving…' : 'Set password'}</button>
                          <button type="button" className="action-btn" onClick={() => { setResetting(false); setResetValue(''); }}>Cancel</button>
                        </form>
                      ) : (
                        <>
                          <button type="button" className="action-btn" onClick={() => setResetting(true)}>Reset password</button>
                          <button type="button" className="action-btn" disabled={!active} onClick={() => setPending({ kind: 'revoke', user: u })}>Sign out everywhere</button>
                          {active
                            ? <button type="button" className="action-btn danger" disabled={!!guard} title={guard} onClick={() => setPending({ kind: 'deactivate', user: u })}>Deactivate</button>
                            : <button type="button" className="action-btn" onClick={() => setPending({ kind: 'reactivate', user: u })}>Reactivate</button>}
                          {guard && active && <small className="acc-guard">{guard}.</small>}
                        </>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <div className="acc-lower">
        <section className="card" aria-labelledby="acc-matrix-h">
          <header className="card-head"><div><h2 id="acc-matrix-h">Role permissions</h2><p>What each role can do in the dashboard</p></div></header>
          <div className="table-wrap" role="region" tabIndex={0} aria-label="Role permissions, scrollable table">
            <table className="matrix">
              <thead><tr><th scope="col">Capability</th>{ROLES.slice().reverse().map(r => <th key={r} scope="col">{ROLE_LABEL[r]}</th>)}</tr></thead>
              <tbody>
                {CAPABILITIES.map(c => (
                  <tr key={c.label}>
                    <th scope="row">{c.label}</th>
                    {ROLES.slice().reverse().map(r => {
                      const yes = RANK[r] >= RANK[c.min];
                      return <td key={r} className={yes ? 'is-yes' : 'is-no'}><span aria-hidden="true">{yes ? '✓' : '—'}</span><span className="sr-only">{yes ? 'Allowed' : 'Not allowed'}</span></td>;
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className="card" aria-labelledby="acc-activity-h">
          <header className="card-head"><div><h2 id="acc-activity-h">Recent activity</h2><p>Access and policy changes</p></div></header>
          {audit.length === 0 ? <p className="card-empty">No changes recorded yet.</p> : (
            <ol className="acc-activity">
              {audit.slice(0, 8).map((e, i) => (
                <li key={`${e.at}-${i}`}>
                  <span><strong>{e.actor}</strong> {(EVENT_TEXT[e.action] ?? (() => e.action))(e)}</span>
                  <time dateTime={e.at}>{fmtTime(e.at)}</time>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>

      <ConfirmDialog
        open={pending !== null}
        title={pending ? pendingText[pending.kind].title : ''}
        message={pending ? pendingText[pending.kind].msg(pending.user) : ''}
        confirmLabel={pending ? pendingText[pending.kind].label : 'Confirm'}
        danger={pending ? pendingText[pending.kind].danger : false}
        onConfirm={() => void runPending()}
        onCancel={() => setPending(null)}
      />
    </div>
  );
}
