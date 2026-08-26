'use client';

import { AppFocusSummary, DailyAppUsageData, Device, DeviceTab, HistoryEntry, UserRole } from '@/types';
import { isOnline, timeAgo, metricColor, primaryDisk, deviceDisplayName } from '@/lib/utils';
import OverviewTab from '@/components/tabs/OverviewTab';
import HardwareTab from '@/components/tabs/HardwareTab';
import ProcessesTab from '@/components/tabs/ProcessesTab';
import BrowserTab from '@/components/tabs/BrowserTab';
import type { BrowserHistoryRange } from '@/components/tabs/BrowserTab';
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
  onDelete?: () => void;
  onPing?: () => void;
  pingStatus?: { state: 'idle' | 'checking' | 'online' | 'offline' | 'error'; message?: string };
  cachedFocus: AppFocusSummary[];
  dailyAppUsage?: DailyAppUsageData;
  dailyAppUsageLoading?: boolean;
  appUsageDate?: string;
  onAppUsageDateChange?: (date: string) => void;
  browserHistory?: HistoryEntry[];
  browserHistoryRange?: BrowserHistoryRange;
  browserHistoryLoading?: boolean;
  browserHistoryLoaded?: boolean;
  canFilterBrowserHistory?: boolean;
  onBrowserHistoryRangeChange?: (range: BrowserHistoryRange) => void;
  userRole?: UserRole;
}

const TABS: { key: DeviceTab; label: string; icon: React.ReactNode }[] = [
  { key: 'overview',   label: 'Overview',   icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><rect x="1" y="1" width="6" height="6" rx="1"/><rect x="9" y="1" width="6" height="6" rx="1"/><rect x="1" y="9" width="6" height="6" rx="1"/><rect x="9" y="9" width="6" height="6" rx="1"/></svg> },
  { key: 'hardware',   label: 'Hardware',   icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><path d="M3 2h10a1 1 0 011 1v8a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1zm2 11h6v1H5v-1z"/></svg> },
  { key: 'processes',  label: 'Processes',  icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><circle cx="4" cy="4" r="2"/><circle cx="12" cy="4" r="2"/><circle cx="4" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><path d="M4 6v4M12 6v4M6 4h4M6 12h4"/></svg> },
  { key: 'browser',    label: 'Browser',    icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.5" fill="none"/><path d="M1 8h14M8 1c-2 2-3 4-3 7s1 5 3 7M8 1c2 2 3 4 3 7s-1 5-3 7"/></svg> },
  { key: 'services',   label: 'Services',   icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><path d="M8 1l1.5 3h3l-2.5 2 1 3L8 7.5 5 9l1-3L3.5 4h3z"/><path d="M3 11h10M3 13h10"/></svg> },
  { key: 'ports',      label: 'Ports',      icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><rect x="2" y="5" width="12" height="6" rx="1" stroke="currentColor" strokeWidth="1.2" fill="none"/><circle cx="5" cy="8" r="1"/><circle cx="8" cy="8" r="1"/><circle cx="11" cy="8" r="1"/></svg> },
  { key: 'apps',       label: 'Apps',       icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><rect x="1" y="1" width="4" height="4" rx="0.5"/><rect x="6" y="1" width="4" height="4" rx="0.5"/><rect x="11" y="1" width="4" height="4" rx="0.5"/><rect x="1" y="6" width="4" height="4" rx="0.5"/><rect x="6" y="6" width="4" height="4" rx="0.5"/><rect x="11" y="6" width="4" height="4" rx="0.5"/><rect x="1" y="11" width="4" height="4" rx="0.5"/><rect x="6" y="11" width="4" height="4" rx="0.5"/><rect x="11" y="11" width="4" height="4" rx="0.5"/></svg> },
  { key: 'security',   label: 'Security',   icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><path d="M8 1L2 3v5c0 3.5 2.5 6 6 7 3.5-1 6-3.5 6-7V3L8 1z"/></svg> },
  { key: 'focus',      label: 'App Usage',  icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><circle cx="8" cy="8" r="2"/><circle cx="8" cy="8" r="5" stroke="currentColor" strokeWidth="1.2" fill="none"/><circle cx="8" cy="8" r="7.5" stroke="currentColor" strokeWidth="1" fill="none" opacity="0.4"/></svg> },
  { key: 'sysinfo',    label: 'System',     icon: <svg width="13" height="13" viewBox="0 0 16 16" fill="currentColor"><rect x="1" y="2" width="14" height="9" rx="1" stroke="currentColor" strokeWidth="1.2" fill="none"/><path d="M5 14h6M8 11v3"/></svg> },
];

export default function DeviceCard({
  device,
  tab,
  onTabChange,
  onDelete,
  onPing,
  pingStatus,
  cachedFocus,
  dailyAppUsage,
  dailyAppUsageLoading = false,
  appUsageDate,
  onAppUsageDateChange,
  browserHistory,
  browserHistoryRange = 'recent',
  browserHistoryLoading = false,
  browserHistoryLoaded = true,
  canFilterBrowserHistory = false,
  onBrowserHistoryRangeChange,
  userRole,
}: Props) {
  const sys    = device.data?.SystemInfo;
  const procs  = device.data?.ProcessMonitor?.top_processes ?? [];
  const history = (browserHistory && browserHistory.length > 0)
    ? browserHistory
    : (browserHistoryLoaded ? device.data?.BrowserHistory?.top_recent_urls : undefined) ?? [];
  const hw     = device.data?.HardwareStats;
  const online = isOnline(device.last_seen);

  const hostname = deviceDisplayName(device);
  const cpuPct = hw?.cpu?.usage_percent;
  const ramPct = hw?.ram?.used_percent;
  const diskPct = primaryDisk(hw?.disks)?.used_percent;

  // Derive card state for the left border color
  const risk = Math.max(cpuPct ?? 0, ramPct ?? 0, diskPct ?? 0);
  const state = !online ? 'offline' : risk >= 90 ? 'critical' : risk >= 70 ? 'warning' : 'online';

  // Roving-tabindex arrow-key navigation for the tabs pattern.
  const handleTabKeyDown = (e: React.KeyboardEvent) => {
    const idx = TABS.findIndex(t => t.key === tab);
    let next = -1;
    if (e.key === 'ArrowRight') next = (idx + 1) % TABS.length;
    else if (e.key === 'ArrowLeft') next = (idx - 1 + TABS.length) % TABS.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = TABS.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onTabChange(TABS[next].key);
    document.getElementById(`device-tab-${TABS[next].key}`)?.focus();
  };

  return (
    <div className={`device-card state-${state}`}>
      {/* Header */}
      <div className="device-header">
        <div className="device-header-left">
          <div className="device-name-row">
            <span className={`online-dot ${online ? 'on' : 'off'}`} />
            <span className="device-hostname">{hostname}</span>
            <span className={`status-badge badge-${online ? (state === 'critical' ? 'critical' : state === 'warning' ? 'warning' : 'online') : 'offline'}`}>
              <span className="dot" />
              {online ? (state === 'critical' ? 'Critical' : state === 'warning' ? 'Warning' : 'Online') : 'Offline'}
            </span>
          </div>
          <div className="device-meta-row">
            <span className="device-meta-item">{timeAgo(device.last_seen)}</span>
            {online && (
              <span className="device-badge">
                {device.agent_version ? `Agent ${device.agent_version}` : 'Agent version pending'}
              </span>
            )}
            {sys?.platform_version && (
              <span className="device-badge">{sys.os} {sys.platform_version}</span>
            )}
            <span className="device-badge">{device.device_id.slice(0, 20)}…</span>
          </div>
        </div>

        <div className="device-header-right">
          {onPing && (
            <>
              {pingStatus?.message && (
                <span className={`ping-state ping-${pingStatus.state}`}>
                  {pingStatus.message}
                </span>
              )}
              <button
                type="button"
                className="action-btn"
                onClick={onPing}
                disabled={pingStatus?.state === 'checking'}
              >
                {pingStatus?.state === 'checking' ? 'Pinging...' : 'Ping Agent'}
              </button>
            </>
          )}
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
          {onDelete && (
            <button className="delete-btn" title="Remove device" onClick={onDelete} aria-label="Remove device">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
                <path d="M2 4h12M5 4V2h6v2M6 7v5M10 7v5M3 4l1 10h8l1-10"/>
              </svg>
            </button>
          )}
        </div>
      </div>

      {/* Tab bar */}
      <div className="tab-bar" role="tablist" aria-label="Device details">
        {TABS.map(t => (
          <button
            key={t.key}
            type="button"
            id={`device-tab-${t.key}`}
            role="tab"
            aria-selected={tab === t.key}
            aria-controls="device-tab-panel"
            tabIndex={tab === t.key ? 0 : -1}
            className={`tab-btn ${tab === t.key ? 'active' : ''}`}
            onClick={() => onTabChange(t.key)}
            onKeyDown={handleTabKeyDown}
          >
            {t.icon}
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab panel */}
      <div
        className="tab-panel"
        id="device-tab-panel"
        role="tabpanel"
        aria-labelledby={`device-tab-${tab}`}
        tabIndex={0}
      >
        {tab === 'overview'  && <OverviewTab data={device.data} cachedFocus={cachedFocus} />}
        {tab === 'hardware'  && <HardwareTab hw={hw} />}
        {tab === 'processes' && <ProcessesTab procs={procs} />}
        {tab === 'browser'   && (
          <BrowserTab
            history={history}
            canFilterHistory={canFilterBrowserHistory}
            historyRange={browserHistoryRange}
            historyLoading={browserHistoryLoading}
            historyLoaded={browserHistoryLoaded}
            onHistoryRangeChange={onBrowserHistoryRangeChange}
          />
        )}
        {tab === 'services'  && <ServicesTab data={device.data?.Services} />}
        {tab === 'ports'     && <PortsTab data={device.data?.NetworkPorts} />}
        {tab === 'apps'      && <AppsTab data={device.data?.InstalledApps} />}
        {tab === 'security'  && (
          <SecurityTab
            usb={device.data?.USBEvents}
            osUpd={device.data?.OSUpdates}
            deviceId={device.device_id}
            userRole={userRole ?? 'viewer'}
            deviceOnline={isOnline(device.last_seen)}
            quarantined={!!device.quarantined}
          />
        )}
        {tab === 'focus'     && (
          <FocusTab
            data={device.data?.ActiveWindowTracker}
            cachedSummaries={cachedFocus}
            dailyUsage={dailyAppUsage}
            dailyUsageLoading={dailyAppUsageLoading}
            usageDate={appUsageDate}
            onUsageDateChange={onAppUsageDateChange}
          />
        )}
        {tab === 'sysinfo'   && <SysInfoTab sys={sys} />}
      </div>
    </div>
  );
}
