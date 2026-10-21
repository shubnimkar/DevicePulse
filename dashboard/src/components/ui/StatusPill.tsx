import type { ReactNode } from 'react';

export type Status = 'online' | 'warning' | 'critical' | 'offline';

const LABELS: Record<Status, string> = { online: 'Online', warning: 'Warning', critical: 'Critical', offline: 'Offline' };

// Shape differs per status so state never relies on color alone.
export function StatusIcon({ status }: { status: Status }) {
  return (
    <svg className="sp-icon" viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      {status === 'online' && <path d="M2.5 6.5l2.3 2.3L9.5 3.5" />}
      {status === 'warning' && <path d="M6 1.8L11 10.2H1L6 1.8zM6 5v2.4" />}
      {status === 'critical' && <path d="M4 1.2h4L10.8 4v4L8 10.8H4L1.2 8V4L4 1.2zM6 3.8v2.4" />}
      {status === 'offline' && <path d="M2.5 6h7" />}
    </svg>
  );
}

interface Props {
  status: Status;
  label?: string;
  as?: 'span' | 'button';
  pressed?: boolean;
  onClick?: () => void;
  className?: string;
  children?: ReactNode; // e.g. a count, rendered after the label
}

export default function StatusPill({ status, label, as = 'span', pressed, onClick, className = '', children }: Props) {
  const cls = `sp sp-${status} ${as === 'button' ? 'sp-button' : ''} ${pressed ? 'is-pressed' : ''} ${className}`.replace(/\s+/g, ' ').trim();
  const body = (
    <>
      <StatusIcon status={status} />
      {label ?? LABELS[status]}
      {children}
    </>
  );
  return as === 'button'
    ? <button type="button" className={cls} aria-pressed={pressed} onClick={onClick}>{body}</button>
    : <span className={cls}>{body}</span>;
}
