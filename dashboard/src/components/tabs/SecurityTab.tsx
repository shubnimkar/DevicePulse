'use client';

import { useCallback, useEffect, useState } from 'react';
import { USBDevice, OSUpdateInfo, UserRole, RemoteActionType, DeviceCommand } from '@/types';
import { API, timeAgo } from '@/lib/utils';
import ConfirmDialog from '@/components/ConfirmDialog';

interface Props {
  usb?:   { usb_devices: USBDevice[] | null; count: number; source?: string };
  osUpd?: { os_updates: OSUpdateInfo };
  deviceId: string;
  userRole: UserRole;
  deviceOnline: boolean;
  quarantined: boolean;
}

type ActionDef = {
  type: RemoteActionType;
  label: string;
  hint: string;
  minRole: UserRole;
  danger?: boolean;
  confirm?: string;
};

const ROLE_RANK: Record<UserRole, number> = { viewer: 1, manager: 2, admin: 3 };

const ACTIONS: ActionDef[] = [
  { type: 'collect_now',       label: 'Collect Now',   hint: 'Run a full telemetry cycle immediately',     minRole: 'manager' },
  { type: 'restart_agent',     label: 'Restart Agent', hint: 'Restart the agent service on the endpoint',  minRole: 'manager', confirm: 'Restart the DevicePulse agent on this device?' },
  { type: 'lock_screen',       label: 'Lock Screen',   hint: 'Lock the interactive user session',          minRole: 'admin',   danger: true, confirm: 'Lock the screen of this device now?' },
  { type: 'quarantine_enable', label: 'Quarantine',    hint: 'Firewall-isolate the host (API + DNS only)', minRole: 'admin',   danger: true, confirm: 'QUARANTINE this device? All network traffic except the DevicePulse API and DNS will be blocked.' },
];

const ACTIONS_WIPE: ActionDef = {
  type: 'wipe_agent',
  label: 'Wipe Agent',
  hint: 'Remove the agent + local data and revoke this device',
  minRole: 'admin',
  danger: true,
  confirm:
    'WIPE the DevicePulse agent from this device?\n\n' +
    '• Local agent data (queue, credentials, history cursor) is deleted\n' +
    '• The agent service is disabled and its binary removed\n' +
    '• This device credential is revoked server-side\n\n' +
    'The device itself is NOT factory-reset. Continue?',
};

const STATUS_CLASS: Record<string, string> = {
  pending:     'cmd-st-pending',
  delivered:   'cmd-st-delivered',
  success:     'cmd-st-success',
  failed:      'cmd-st-failed',
  unsupported: 'cmd-st-failed',
};

export default function SecurityTab({ usb, osUpd, deviceId, userRole, deviceOnline, quarantined }: Props) {
  const upd        = osUpd?.os_updates;
  const usbDevices = usb?.usb_devices ?? [];

  const [commands, setCommands]     = useState<DeviceCommand[]>([]);
  const [cmdLoading, setCmdLoading] = useState(false);
  const [busyType, setBusyType]     = useState('');
  const [notice, setNotice]         = useState<{ kind: 'ok' | 'err'; msg: string } | null>(null);
  const [pendingAction, setPendingAction] = useState<ActionDef | null>(null);

  const loadCommands = useCallback(async () => {
    if (!deviceId) return;
    setCmdLoading(true);
    try {
      const res = await fetch(`${API}/devices/${encodeURIComponent(deviceId)}/commands`, {
        credentials: 'include',
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const json = await res.json();
      setCommands(Array.isArray(json.commands) ? json.commands : []);
    } catch {
      // history is non-critical; keep previous state
    } finally {
      setCmdLoading(false);
    }
  }, [deviceId]);

  useEffect(() => {
    const t = window.setTimeout(() => { void loadCommands(); }, 0);
    return () => window.clearTimeout(t);
  }, [loadCommands]);

  const issueCommand = useCallback(async (action: ActionDef) => {
    setBusyType(action.type);
    setNotice(null);
    try {
      const res = await fetch(`${API}/devices/${encodeURIComponent(deviceId)}/commands`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: action.type }),
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        const msg = data && typeof data === 'object' && 'message' in data
          ? String((data as { message: unknown }).message)
          : null;
        setNotice({ kind: 'err', msg: msg || `Failed to queue ${action.label} (HTTP ${res.status})` });
      } else {
        setNotice({ kind: 'ok', msg: `${action.label} queued — runs when the agent next checks in (~5s while online).` });
      }
    } catch (e) {
      setNotice({ kind: 'err', msg: `Network error: ${e instanceof Error ? e.message : String(e)}` });
    } finally {
      setBusyType('');
      loadCommands();
    }
  }, [deviceId, loadCommands]);

  // The quarantine button toggles between enable and release.
  const quarantineAction: ActionDef = quarantined
    ? { type: 'quarantine_release', label: 'Release Quarantine', hint: 'Restore full network connectivity', minRole: 'admin' }
    : ACTIONS[3];
  const visibleActions = [ACTIONS[0], ACTIONS[1], ACTIONS[2], quarantineAction, ACTIONS_WIPE];



  return (
    <div className="security-grid">
      {/* Remote Actions */}
      <div className="sec-card ra-card">
        <div className="sec-card-title">Remote Actions</div>
        {!deviceOnline && (
          <div className="ra-offline-note">Device offline — commands queue until it reconnects.</div>
        )}
        <div className="ra-grid">
          {visibleActions.map(a => {
            const allowed = ROLE_RANK[userRole] >= ROLE_RANK[a.minRole];
            const busy = busyType === a.type;
            return (
              <button
                key={a.type}
                className={`ra-btn${a.danger ? ' ra-btn-danger' : ''}${a.type === 'wipe_agent' ? ' ra-btn-wipe' : ''}${quarantined && a.type === 'quarantine_release' ? ' ra-btn-release' : ''}`}
                disabled={!allowed || !!busyType}
                title={allowed ? a.hint : `Requires role: ${a.minRole}`}
                aria-label={`${a.label}${allowed ? '' : ` (requires role: ${a.minRole})`}`}
                onClick={() => (a.confirm ? setPendingAction(a) : void issueCommand(a))}
              >
                <span className="ra-btn-label">{busy ? 'Sending…' : a.label}</span>
                {!allowed && <span className="ra-btn-role">🔒 {a.minRole}</span>}
              </button>
            );
          })}
        </div>
        {notice && (
          <div className={`ra-notice ${notice.kind === 'ok' ? 'ra-notice-ok' : 'ra-notice-err'}`}>{notice.msg}</div>
        )}

        {/* Command history */}
        <div className="ra-history">
          <div className="ra-history-title">
            Recent Commands
            <button className="ra-refresh" onClick={loadCommands} disabled={cmdLoading} aria-label="Refresh command history">
              {cmdLoading ? '…' : '⟳'}
            </button>
          </div>
          {commands.length === 0 && !cmdLoading && (
            <div className="no-data no-data-tight">No remote commands issued yet.</div>
          )}
          {commands.slice(0, 8).map(c => (
            <div key={c.id} className="cmd-row">
              <div className="cmd-main">
                <span className="cmd-type mono">{c.type}</span>
                <span className={`cmd-status ${STATUS_CLASS[c.status] ?? ''}`}>{c.status}</span>
              </div>
              <div className="cmd-meta">
                by {c.created_by || '—'} · {timeAgo(c.created_at)}
                {c.result && <span className="cmd-result" title={c.result}> — {c.result}</span>}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* OS Updates */}
      <div className="sec-card">
        <div className="sec-card-title">OS Updates</div>
        {upd ? (
          <>
            <div className="sec-row">
              <span className="sec-label">Source</span>
              <span className="sec-value mono">{upd.source}</span>
            </div>
            <div className="sec-row">
              <span className="sec-label">Last Updated</span>
              <span className="sec-value">
                {upd.last_update_time
                  ? new Date(upd.last_update_time).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
                  : upd.last_update_raw ?? '—'}
              </span>
            </div>
            <div className="sec-row">
              <span className="sec-label">Pending</span>
              <span className={`sec-value ${upd.pending_count > 0 ? 'warn' : 'ok'}`}>
                {upd.pending_count} update{upd.pending_count !== 1 ? 's' : ''}
              </span>
            </div>
            {upd.pending_updates && upd.pending_updates.length > 0 && (
              <div className="pending-list">
                {upd.pending_updates.slice(0, 5).map((u, i) => (
                  <div key={i} className="pending-item">↑ {u}</div>
                ))}
                {upd.pending_updates.length > 5 && (
                  <div className="pending-item muted">+{upd.pending_updates.length - 5} more</div>
                )}
              </div>
            )}
          </>
        ) : (
          <div className="no-data no-data-roomy">No update data yet.</div>
        )}
      </div>

      {/* USB Devices */}
      <div className="sec-card">
        <div className="sec-card-title">USB Devices ({usb?.count ?? 0})</div>
        {usbDevices.length ? (
          <ul className="usb-list">
            {usbDevices.map((d, i) => (
              <li key={i} className="usb-item">
                <div className="usb-name">{d.name || 'Unknown Device'}</div>
                <div className="usb-meta">
                  {d.manufacturer && <span>{d.manufacturer}</span>}
                  {d.vendor_id && <span className="mono">{d.vendor_id}:{d.product_id}</span>}
                  {d.speed && <span className="usb-speed">{d.speed}</span>}
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className="no-data no-data-roomy">No USB devices connected.</div>
        )}
      </div>

      <ConfirmDialog
        open={!!pendingAction}
        title={pendingAction?.label ?? ''}
        message={pendingAction?.confirm ?? ''}
        confirmLabel={pendingAction?.label ?? 'Confirm'}
        danger={pendingAction?.danger}
        onConfirm={() => {
          const action = pendingAction;
          setPendingAction(null);
          if (action) void issueCommand(action);
        }}
        onCancel={() => setPendingAction(null)}
      />
    </div>
  );
}
