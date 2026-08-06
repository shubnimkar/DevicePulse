'use client';

import { AppFocusSummary, Device, DeviceTab } from '@/types';
import { isOnline, getOSIcon, timeAgo, metricColor } from '@/lib/utils';
import OverviewTab from '@/components/tabs/OverviewTab';
import HardwareTab from '@/components/tabs/HardwareTab';
import ProcessesTab from '@/components/tabs/ProcessesTab';
import BrowserTab from '@/components/tabs/BrowserTab';
import ServicesTab from '@/components/tabs/ServicesTab';
import PortsTab from '@/components/tabs/PortsTab';
import AppsTab from '@/components/tabs/AppsTab';
import SecurityTab from '@/components/tabs/SecurityTab';
import FocusTab from '@/components/tabs/FocusTab';
import SysInfoTab from '@/components/tabs/SysInfoTab';

interface Props {
  device: Device;
  tab: DeviceTab;
  onTabChange: (tab: DeviceTab) => void;
  onDelete: () => void;
  cachedFocus: AppFocusSummary[];
}

const TABS: { key: DeviceTab; label: string; icon: string }[] = [
  { key: 'overview',   label: 'Overview',  icon: '🏠' },
  { key: 'hardware',   label: 'Hardware',  icon: '📊' },
  { key: 'processes',  label: 'Processes', icon: '⚙️' },
  { key: 'browser',    label: 'Browser',   icon: '🌐' },
  { key: 'services',   label: 'Services',  icon: '🔧' },
  { key: 'ports',      label: 'Ports',     icon: '🔌' },
  { key: 'apps',       label: 'Apps',      icon: '📦' },
  { key: 'security',   label: 'Security',  icon: '🛡' },
  { key: 'focus',      label: 'Focus',     icon: '🎯' },
  { key: 'sysinfo',    label: 'System',    icon: '🖥' },
];

export default function DeviceCard({ device, tab, onTabChange, onDelete, cachedFocus }: Props) {
  const sys     = device.data?.SystemInfo;
  const procs   = device.data?.ProcessMonitor?.top_processes ?? [];
  const history = device.data?.BrowserHistory?.top_recent_urls ?? [];
  const hw      = device.data?.HardwareStats;
  const online  = isOnline(device.last_seen);

  const hostname = sys?.hostname || device.hostname || device.device_id;
  const osIcon   = getOSIcon(sys?.os);

  // Quick status badges for the card header
  const cpuPct = hw?.cpu?.usage_percent;
  const ramPct = hw?.ram?.used_percent;

  return (
    <div className="device-card glass-card">
      {/* Accent top bar */}
      <div
        className="device-card-accent"
        style={{ background: online ? 'var(--accent-gradient)' : 'rgba(107,114,128,0.4)' }}
      />

      {/* Card header */}
      <div className="device-header">
        <div className="device-header-left">
          <div className="device-name-row">
            <span className={`online-dot ${online ? 'online' : 'offline'}`} />
            <span className="device-hostname">{osIcon} {hostname}</span>
            <span className={`status-pill ${online ? 'pill-online' : 'pill-offline'}`}>
              {online ? 'Online' : 'Offline'}
            </span>
          </div>
          <div className="device-meta-row">
            <span className="device-meta-item">🕐 {timeAgo(device.last_seen)}</span>
            {sys?.platform_version && (
              <span className="device-os-badge">
                {sys.os} {sys.platform_version}
              </span>
            )}
            <span className="device-id-badge">{device.device_id.slice(0, 22)}…</span>
          </div>
        </div>

        <div className="device-header-right">
          {/* Inline resource pills */}
          {cpuPct !== undefined && (
            <span className="resource-pill" style={{ color: metricColor(cpuPct) }}>
              CPU {cpuPct.toFixed(0)}%
            </span>
          )}
          {ramPct !== undefined && (
            <span className="resource-pill" style={{ color: metricColor(ramPct) }}>
              MEM {ramPct.toFixed(0)}%
            </span>
          )}
          <button
            className="device-delete-btn"
            title="Remove device"
            onClick={onDelete}
            aria-label="Remove device"
          >
            🗑
          </button>
        </div>
      </div>

      {/* Tab bar */}
      <div className="tab-bar" role="tablist">
        {TABS.map(t => (
          <button
            key={t.key}
            role="tab"
            aria-selected={tab === t.key}
            className={`tab-btn ${tab === t.key ? 'tab-active' : ''}`}
            onClick={() => onTabChange(t.key)}
          >
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="device-section" role="tabpanel">
        {tab === 'overview'  && <OverviewTab data={device.data} cachedFocus={cachedFocus} />}
        {tab === 'hardware'  && <HardwareTab hw={hw} />}
        {tab === 'processes' && <ProcessesTab procs={procs} />}
        {tab === 'browser'   && <BrowserTab history={history} />}
        {tab === 'services'  && <ServicesTab data={device.data?.Services} />}
        {tab === 'ports'     && <PortsTab data={device.data?.NetworkPorts} />}
        {tab === 'apps'      && <AppsTab data={device.data?.InstalledApps} />}
        {tab === 'security'  && (
          <SecurityTab usb={device.data?.USBEvents} osUpd={device.data?.OSUpdates} />
        )}
        {tab === 'focus' && (
          <FocusTab data={device.data?.ActiveWindowTracker} cachedSummaries={cachedFocus} />
        )}
        {tab === 'sysinfo' && <SysInfoTab sys={sys} />}
      </div>
    </div>
  );
}
