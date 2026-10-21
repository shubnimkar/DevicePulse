import HeaderClock from '@/components/HeaderClock';
import type { FleetConn } from './TopBar';
import { getNavItems, type ShellPage } from './nav';

interface Props {
  currentPage: ShellPage;
  onNavigate: (page: ShellPage) => void;
  canManageUsers: boolean;
  fleetConn: FleetConn;
  onlineCount: number;
  totalCount: number;
}

export default function SideRail({ currentPage, onNavigate, canManageUsers, fleetConn, onlineCount, totalCount }: Props) {
  const { primary, more } = getNavItems(canManageUsers);
  const statusText = `${fleetConn === 'live' ? 'Fleet connected' : 'Updating fleet'}: ${onlineCount} of ${totalCount} online now`;
  return (
    <aside className="shell-rail" aria-label="Primary navigation">
      <nav className="rail-nav" aria-label="Dashboard pages">
        {[...primary, ...more].map(item => (
          <button
            key={item.page}
            type="button"
            className={`rail-item ${currentPage === item.page ? 'active' : ''}`}
            aria-label={item.label}
            data-tip={item.label}
            aria-current={currentPage === item.page ? 'page' : undefined}
            onClick={() => onNavigate(item.page)}
          >
            {item.icon}
          </button>
        ))}
      </nav>
      <div className="rail-foot">
        <div className="rail-status" role="group" aria-label={statusText} title={statusText}>
          <span className={`status-indicator ${fleetConn === 'live' ? 'is-live' : ''}`} aria-hidden="true" />
          <span aria-hidden="true">{onlineCount}/{totalCount}</span>
        </div>
        <HeaderClock compact className="rail-clock" />
      </div>
    </aside>
  );
}
