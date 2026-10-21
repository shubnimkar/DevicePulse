import LogoMark from '@/components/LogoMark';
import StatusPill from '@/components/ui/StatusPill';
import UserMenu from './UserMenu';

export type FleetConn = 'connecting' | 'live' | 'stale';

interface Props {
  fleetConn: FleetConn;
  loading: boolean;
  /** Seconds since the last successful fetch while stale, otherwise null. */
  staleSeconds: number | null;
  user: { name: string; email: string; roleText: string };
  onSignOut: () => void;
}

export default function TopBar({ fleetConn, loading, staleSeconds, user, onSignOut }: Props) {
  return (
    <header className="shell-top">
      <div className="header-brand">
        <div className="header-logo"><LogoMark /></div>
        <span className="brand-lockup">
          <span className="brand-name">DevicePulse</span>
          <span className="brand-mode">Fleet operations</span>
        </span>
      </div>
      <div className="shell-top-right">
        <span role="status" className="header-status">
          {!loading && fleetConn === 'live' && <StatusPill status="online" label="Live" />}
          {fleetConn === 'stale' && <StatusPill status="warning" label="Reconnecting…" />}
        </span>
        {staleSeconds !== null && <span className="last-updated">Last updated {staleSeconds}s ago</span>}
        <UserMenu name={user.name} email={user.email} roleText={user.roleText} onSignOut={onSignOut} />
      </div>
    </header>
  );
}
