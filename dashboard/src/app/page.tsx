'use client';

import { useEffect, useState, useCallback, useRef } from 'react';
import type { FormEvent } from 'react';
import { Device, AppFocusSummary, FocusCacheData, DeviceTab, EnterprisePolicy, DashboardUser, UserRole, HistoryEntry, BrowserHistoryArchiveData, DailyAppUsageData, DailyPresenceData, AgentRelease, AgentBuildJob, AgentRolloutResponse, WeeklyReport } from '@/types';
import { API, readHeaders, adminHeaders, isOnline, timeAgo, primaryDisk, deviceDisplayName, formatDuration } from '@/lib/utils';
import DeviceCard from '@/components/DeviceCard';
import HeaderClock from '@/components/HeaderClock';
import ConfirmDialog from '@/components/ConfirmDialog';
import type { BrowserHistoryRange } from '@/components/tabs/BrowserTab';

type PageView = 'dashboard' | 'inventory' | 'reports' | 'inspect' | 'settings' | 'access';
type StatusFilter = 'all' | 'online' | 'critical' | 'warning' | 'offline';
type AuthMode = 'login' | 'register';
type OnDemandReportType = 'executive' | 'app_usage' | 'device_health';
type BrowserHistoryCache = Record<string, Partial<Record<BrowserHistoryRange, HistoryEntry[]>>>;
type DailyAppUsageCache = Record<string, Record<string, DailyAppUsageData>>;
type DailyPresenceCache = Record<string, Record<string, DailyPresenceData>>;
type AgentPingState = 'idle' | 'checking' | 'online' | 'offline' | 'error';
type AgentPingStatus = Record<string, { state: AgentPingState; message?: string }>;
type AgentBuildForm = { version: string; api_url: string; platforms: AgentRelease['os'][]; archs: string[] };
type CollectorPolicyKey =
  | 'collect_system_info'
  | 'collect_hardware_stats'
  | 'collect_processes'
  | 'collect_active_window'
  | 'collect_services'
  | 'collect_network_ports'
  | 'collect_installed_apps'
  | 'collect_os_updates'
  | 'collect_usb_devices';

const DEFAULT_POLICY: EnterprisePolicy = {
  sync_interval_seconds: 10,
  telemetry_retention_days: 30,
  delta_upload_enabled: true,
  cache_unchanged_collector_data: true,
  browser_history_mode: 'full_url',
  browser_history_limit: 200,
  collect_system_info: true,
  collect_hardware_stats: true,
  collect_processes: true,
  collect_browser_history: true,
  collect_active_window: true,
  collect_services: true,
  collect_network_ports: true,
  collect_installed_apps: true,
  collect_os_updates: true,
  collect_usb_devices: true,
};

const DEFAULT_BUILD_FORM: AgentBuildForm = {
  version: '',
  api_url: '',
  platforms: ['linux'],
  archs: ['amd64'],
};

const RELEASE_TARGETS: Array<{ os: AgentRelease['os']; label: string }> = [
  { os: 'linux', label: 'Linux' },
  { os: 'windows', label: 'Windows' },
  { os: 'darwin', label: 'macOS' },
];

const PAGE_VIEWS: PageView[] = ['dashboard', 'inventory', 'reports', 'inspect', 'settings', 'access'];
const DEVICE_TABS: DeviceTab[] = ['overview', 'hardware', 'processes', 'browser', 'services', 'ports', 'apps', 'security', 'focus', 'sysinfo'];

const roleLabel: Record<UserRole, string> = {
  admin: 'Admin',
  manager: 'Manager',
  viewer: 'Viewer',
};

function localDateKey(daysAgo: number): string {
  const date = new Date();
  date.setDate(date.getDate() - daysAgo);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

// Absolute UTC instants (RFC3339, "Z") for the viewer's *local* calendar day,
// from 00:00:00.000 to 23:59:59.999. The browser's own timezone offset is baked
// in by new Date() so "today" always spans local midnight → midnight regardless
// of where the viewer (or dashboard) is located.
function localDayBounds(daysAgo: number): { from: string; to: string } {
  const start = new Date();
  start.setDate(start.getDate() - daysAgo);
  start.setHours(0, 0, 0, 0);
  const end = new Date(start);
  end.setHours(23, 59, 59, 999);
  return { from: start.toISOString(), to: end.toISOString() };
}

function browserHistoryQuery(range: BrowserHistoryRange): string {
  const params = new URLSearchParams({ limit: '500' });
  if (range === 'recent') {
    // "Today" only: the current day, 00:00:00 → 23:59:59.999 (viewer-local).
    const { from, to } = localDayBounds(0);
    params.set('from', from);
    params.set('to', to);
  } else {
    const offset = range === 'last_day' ? 1 : 2;
    const { from, to } = localDayBounds(offset);
    params.set('from', from);
    params.set('to', to);
  }
  return params.toString();
}

function parsePageView(value: string | null): PageView {
  return PAGE_VIEWS.includes(value as PageView) ? value as PageView : 'dashboard';
}

function parseDeviceTab(value: string | null): DeviceTab {
  return DEVICE_TABS.includes(value as DeviceTab) ? value as DeviceTab : 'overview';
}

function readNavigationState() {
  const params = new URLSearchParams(window.location.search);
  const view = parsePageView(params.get('view'));
  const deviceId = params.get('device') ?? '';
  const tab = parseDeviceTab(params.get('tab'));
  return {
    view: view === 'inspect' && !deviceId ? 'inventory' : view,
    deviceId,
    tab,
  };
}

function formatPingAge(seconds?: number): string {
  if (seconds === undefined || seconds < 0) return 'unknown age';
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

async function readApiError(res: Response): Promise<string> {
  const text = await res.text();
  const trimmed = text.trim();
  if (trimmed.startsWith('<!DOCTYPE') || trimmed.startsWith('<html')) {
    return `API route returned HTML instead of JSON/text (${res.status}). Check that /api is proxied to the DevicePulse API.`;
  }
  return trimmed || `Request failed with status ${res.status}.`;
}

// ── SVG icons ────────────────────────────────────────────────────────────────
const IconGrid = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="currentColor">
    <rect x="1" y="1" width="6" height="6" rx="1"/><rect x="9" y="1" width="6" height="6" rx="1"/>
    <rect x="1" y="9" width="6" height="6" rx="1"/><rect x="9" y="9" width="6" height="6" rx="1"/>
  </svg>
);
const IconList = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="currentColor">
    <rect x="1" y="2" width="14" height="2.5" rx="1"/><rect x="1" y="6.75" width="14" height="2.5" rx="1"/>
    <rect x="1" y="11.5" width="14" height="2.5" rx="1"/>
  </svg>
);
const IconSearch = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <circle cx="6.5" cy="6.5" r="5"/><path d="M10.5 10.5L14 14"/>
  </svg>
);
const IconLogo = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
    <circle cx="8" cy="8" r="6" stroke="white" strokeWidth="1.5"/>
    <circle cx="8" cy="8" r="2.5" fill="white"/>
    <path d="M8 2v2M8 12v2M2 8h2M12 8h2" stroke="white" strokeWidth="1.5" strokeLinecap="round"/>
  </svg>
);
const IconMail = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4">
    <rect x="1.75" y="3.25" width="12.5" height="9.5" rx="1.5"/>
    <path d="M2.5 4.5L8 8.75l5.5-4.25"/>
  </svg>
);
const IconLock = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4">
    <rect x="3" y="7" width="10" height="7" rx="1.5"/>
    <path d="M5.5 7V5a2.5 2.5 0 015 0v2"/>
  </svg>
);
const IconEye = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round">
    <path d="M1.5 8s2.4-4.25 6.5-4.25S14.5 8 14.5 8 12.1 12.25 8 12.25 1.5 8 1.5 8z"/>
    <circle cx="8" cy="8" r="2"/>
  </svg>
);
const IconEyeOff = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
    <path d="M3 3l10 10"/>
    <path d="M10.6 10.9a6.4 6.4 0 01-2.6.6c-4.1 0-6.5-4.25-6.5-4.25a10.6 10.6 0 013.2-3.3M6.6 3.4a6.9 6.9 0 011.4-.15c4.1 0 6.5 4 6.5 4a11 11 0 01-2.1 2.6"/>
    <path d="M6.2 6.4a2 2 0 003.1 2.5"/>
  </svg>
);
const IconPulse = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <path d="M1.5 8.5h3l1.75-4.5 3.5 8 1.75-3.5h3"/>
  </svg>
);
const IconShield = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
    <path d="M8 1.75l5 1.9v4.1c0 3.1-2.1 5.4-5 6.5-2.9-1.1-5-3.4-5-6.5v-4.1z"/>
    <path d="M5.9 8l1.5 1.5 2.7-2.9"/>
  </svg>
);
const IconDevices = () => (
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4">
    <rect x="1.5" y="2.5" width="9.5" height="7" rx="1"/>
    <path d="M4 12.5h4.5"/>
    <rect x="11.5" y="6.5" width="3.5" height="6" rx="0.75"/>
  </svg>
);
const IconSettings = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <circle cx="8" cy="8" r="2.25"/>
    <path d="M13 8a5.3 5.3 0 00-.07-.86l1.42-1.08-1.35-2.33-1.66.68a5.1 5.1 0 00-1.48-.86L9.62 1.8H6.38l-.24 1.75a5.1 5.1 0 00-1.48.86L3 3.73 1.65 6.06l1.42 1.08A5.3 5.3 0 003 8c0 .3.02.58.07.86L1.65 9.94 3 12.27l1.66-.68c.44.37.94.66 1.48.86l.24 1.75h3.24l.24-1.75c.54-.2 1.04-.49 1.48-.86l1.66.68 1.35-2.33-1.42-1.08c.05-.28.07-.57.07-.86z"/>
  </svg>
);
const IconReport = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
    <path d="M3 2.5h10v11H3z"/>
    <path d="M5.5 6.5h5M5.5 9h5M5.5 11.5h2.5"/>
    <path d="M11 2.5v3h2"/>
  </svg>
);

export default function Home() {
  const [authUser, setAuthUser]       = useState<DashboardUser | null>(null);
  const [authLoading, setAuthLoading] = useState(true);
  const [authMode, setAuthMode]       = useState<AuthMode>('login');
  const [authError, setAuthError]     = useState('');
  const [authForm, setAuthForm]       = useState({ name: '', email: '', password: '' });
  const [showAuthPassword, setShowAuthPassword] = useState(false);
  const [authSubmitting, setAuthSubmitting] = useState(false);
  const [bootstrapRequired, setBootstrapRequired] = useState(false);
  const [users, setUsers]             = useState<DashboardUser[]>([]);
  const [userForm, setUserForm]       = useState<{ name: string; email: string; password: string; role: UserRole }>({
    name: '',
    email: '',
    password: '',
    role: 'viewer',
  });
  const [userCreateStatus, setUserCreateStatus] = useState('');
  const [passwordResetFor, setPasswordResetFor] = useState('');
  const [passwordResetValue, setPasswordResetValue] = useState('');
  const [passwordResetStatus, setPasswordResetStatus] = useState('');
  const [roleUpdateStatus, setRoleUpdateStatus] = useState('');
  const [devices, setDevices]         = useState<Device[]>([]);
  const [loading, setLoading]         = useState(true);
  const [isUpdating, setIsUpdating]   = useState(false);
  const [policy, setPolicy]           = useState<EnterprisePolicy>(DEFAULT_POLICY);
  const [policySavedAt, setPolicySavedAt] = useState<string>('');
  const [agentReleases, setAgentReleases] = useState<AgentRelease[]>([]);
  const [allAgentReleases, setAllAgentReleases] = useState<AgentRelease[]>([]);
  const [agentBuildJobs, setAgentBuildJobs] = useState<AgentBuildJob[]>([]);
  const [buildForm, setBuildForm] = useState<AgentBuildForm>(DEFAULT_BUILD_FORM);
  const [releaseStatus, setReleaseStatus] = useState('');
  const [rolloutSaving, setRolloutSaving] = useState(false);
  const [showAllBuildHistory, setShowAllBuildHistory] = useState(false);
  const [activeTab, setActiveTab]     = useState<Record<string, DeviceTab>>({});
  const [focusCache, setFocusCache]   = useState<Record<string, AppFocusSummary[]>>({});
  const [browserHistoryCache, setBrowserHistoryCache] = useState<BrowserHistoryCache>({});
  const [browserHistoryRange, setBrowserHistoryRange] = useState<BrowserHistoryRange>('recent');
  const [browserHistoryLoading, setBrowserHistoryLoading] = useState<Record<string, boolean>>({});
  const [appUsageDate, setAppUsageDate] = useState(() => localDateKey(0));
  const [dailyAppUsageCache, setDailyAppUsageCache] = useState<DailyAppUsageCache>({});
  const [dailyAppUsageLoading, setDailyAppUsageLoading] = useState<Record<string, boolean>>({});
  const [dailyPresenceCache, setDailyPresenceCache] = useState<DailyPresenceCache>({});
  const [weeklyReport, setWeeklyReport] = useState<WeeklyReport | null>(null);
  const [weeklyReportLoading, setWeeklyReportLoading] = useState(false);
  const [weeklyReportError, setWeeklyReportError] = useState('');
  const [weeklyReportFrom, setWeeklyReportFrom] = useState(() => localDateKey(6));
  const [weeklyReportTo, setWeeklyReportTo] = useState(() => localDateKey(0));
  const [onDemandReportType, setOnDemandReportType] = useState<OnDemandReportType>('executive');
  const [onDemandReportText, setOnDemandReportText] = useState('');
  const [onDemandReportName, setOnDemandReportName] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [currentPage, setCurrentPage] = useState<PageView>('dashboard');
  const [selectedDeviceId, setSelectedDeviceId] = useState<string>('');
  const [fleetConn, setFleetConn] = useState<'connecting' | 'live' | 'stale'>('connecting');
  const [confirmTarget, setConfirmTarget] = useState<{ deviceId: string; name: string } | null>(null);
  const [renameTarget, setRenameTarget] = useState<Device | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameError, setRenameError] = useState('');
  const [renameSaving, setRenameSaving] = useState(false);
  const [agentPingStatus, setAgentPingStatus] = useState<AgentPingStatus>({});
  const [deviceActionError, setDeviceActionError] = useState('');

  const canManagePolicy = authUser?.role === 'admin' || authUser?.role === 'manager';
  const canManageUsers = authUser?.role === 'admin';
  const canDeleteDevices = authUser?.role === 'admin';
  const canFilterBrowserHistory = authUser?.role === 'admin';
  const canManageReleases = authUser?.role === 'admin';
  const canPingAgents = authUser?.role === 'admin';

  const apiFetch = useCallback((path: string, init?: RequestInit) => {
    return fetch(`${API}${path}`, {
      ...init,
      credentials: 'include',
      headers: init?.headers,
    });
  }, []);

  const applyNavigationState = useCallback(() => {
    const { view, deviceId, tab } = readNavigationState();
    setCurrentPage(view);
    setSelectedDeviceId(view === 'inspect' ? deviceId : '');
    if (view === 'inspect' && deviceId) {
      setActiveTab(prev => ({ ...prev, [deviceId]: tab }));
    }
  }, []);

  const writeNavigationState = useCallback((
    view: PageView,
    options: { deviceId?: string; tab?: DeviceTab; replace?: boolean } = {}
  ) => {
    const params = new URLSearchParams();
    if (view !== 'dashboard') params.set('view', view);
    if (view === 'inspect' && options.deviceId) {
      params.set('device', options.deviceId);
      if (options.tab && options.tab !== 'overview') params.set('tab', options.tab);
    }
    const nextUrl = `${window.location.pathname}${params.toString() ? `?${params}` : ''}${window.location.hash}`;
    const method = options.replace ? 'replaceState' : 'pushState';
    window.history[method](null, '', nextUrl);
  }, []);

  const goToPage = useCallback((
    view: PageView,
    options: { deviceId?: string; tab?: DeviceTab; replace?: boolean } = {}
  ) => {
    const deviceId = options.deviceId ?? '';
    const tab = options.tab ?? 'overview';
    setCurrentPage(view);
    setSelectedDeviceId(view === 'inspect' ? deviceId : '');
    if (view === 'inspect' && deviceId) {
      setActiveTab(prev => ({ ...prev, [deviceId]: tab }));
    }
    writeNavigationState(view, { ...options, deviceId, tab });
  }, [writeNavigationState]);

  const loadCurrentUser = useCallback(async () => {
    setAuthLoading(true);
    try {
      const bootRes = await apiFetch('/auth/bootstrap');
      if (bootRes.ok) {
        const boot = await bootRes.json();
        setBootstrapRequired(Boolean(boot.bootstrap_required));
        if (boot.bootstrap_required) setAuthMode('register');
      }

      const res = await apiFetch('/auth/me');
      if (res.ok) {
        const data = await res.json();
        setAuthUser(data.user);
      } else {
        setAuthUser(null);
      }
    } catch {
      setAuthUser(null);
      setAuthError('Could not reach the API. Check that the DevicePulse API is running.');
    } finally {
      setAuthLoading(false);
    }
  }, [apiFetch]);

  // ── Data fetching ─────────────────────────────────────────────────────────

  useEffect(() => {
    const t = window.setTimeout(() => { void loadCurrentUser(); }, 0);
    return () => window.clearTimeout(t);
  }, [loadCurrentUser]);

  useEffect(() => {
    const id = window.setTimeout(applyNavigationState, 0);
    window.addEventListener('popstate', applyNavigationState);
    return () => {
      window.clearTimeout(id);
      window.removeEventListener('popstate', applyNavigationState);
    };
  }, [applyNavigationState]);

  // Latest-value refs keep the 5s polling interval stable across tab/range/date changes.
  const activeTabRef = useRef(activeTab);
  const browserHistoryRangeRef = useRef(browserHistoryRange);
  const appUsageDateRef = useRef(appUsageDate);
  useEffect(() => { activeTabRef.current = activeTab; }, [activeTab]);
  useEffect(() => { browserHistoryRangeRef.current = browserHistoryRange; }, [browserHistoryRange]);
  useEffect(() => { appUsageDateRef.current = appUsageDate; }, [appUsageDate]);

  const fetchBrowserHistory = useCallback(async (
    deviceId: string,
    range: BrowserHistoryRange,
    options: { showLoader?: boolean } = {}
  ) => {
    const showLoader = options.showLoader ?? true;
    try {
      if (showLoader) setBrowserHistoryLoading(state => ({ ...state, [deviceId]: true }));
      const res = await apiFetch(`/devices/${encodeURIComponent(deviceId)}/browser-history?${browserHistoryQuery(range)}`, { headers: readHeaders() });
      const data: BrowserHistoryArchiveData = res.ok ? await res.json() : { device_id: deviceId, from: '', to: '', count: 0, entries: [] };
      setBrowserHistoryCache(cache => ({
        ...cache,
        [deviceId]: {
          ...cache[deviceId],
          [range]: data.entries ?? [],
        },
      }));
    } catch {
      setBrowserHistoryCache(cache => ({
        ...cache,
        [deviceId]: {
          ...cache[deviceId],
          [range]: [],
        },
      }));
    } finally {
      if (showLoader) setBrowserHistoryLoading(state => ({ ...state, [deviceId]: false }));
    }
  }, [apiFetch]);

  const fetchDailyAppUsage = useCallback(async (
    deviceId: string,
    date: string,
    options: { showLoader?: boolean } = {}
  ) => {
    const showLoader = options.showLoader ?? true;
    try {
      if (showLoader) setDailyAppUsageLoading(state => ({ ...state, [deviceId]: true }));
      const res = await apiFetch(`/devices/${encodeURIComponent(deviceId)}/app-usage?date=${encodeURIComponent(date)}`, { headers: readHeaders() });
      const data: DailyAppUsageData = res.ok ? await res.json() : { device_id: deviceId, date, users: [] };
      setDailyAppUsageCache(cache => ({
        ...cache,
        [deviceId]: {
          ...(cache[deviceId] ?? {}),
          [date]: data,
        },
      }));
    } catch {
      setDailyAppUsageCache(cache => ({
        ...cache,
        [deviceId]: {
          ...(cache[deviceId] ?? {}),
          [date]: { device_id: deviceId, date, users: [] },
        },
      }));
    } finally {
      if (showLoader) setDailyAppUsageLoading(state => ({ ...state, [deviceId]: false }));
    }
  }, [apiFetch]);

  const fetchDailyPresence = useCallback(async (deviceId: string, date: string) => {
    try {
      const res = await apiFetch(`/devices/${encodeURIComponent(deviceId)}/presence?date=${encodeURIComponent(date)}`, { headers: readHeaders() });
      const data: DailyPresenceData = res.ok ? await res.json() : { device_id: deviceId, date, online_seconds: 0, heartbeat_count: 0, connection_count: 0 };
      setDailyPresenceCache(cache => ({ ...cache, [deviceId]: { ...(cache[deviceId] ?? {}), [date]: data } }));
    } catch {}
  }, [apiFetch]);

  const fetchWeeklyReport = useCallback(async () => {
    setWeeklyReportLoading(true);
    setWeeklyReportError('');
    try {
      const params = new URLSearchParams({ from: weeklyReportFrom, to: weeklyReportTo });
      const res = await apiFetch(`/reports/weekly?${params.toString()}`, { headers: readHeaders() });
      if (!res.ok) {
        setWeeklyReportError(await readApiError(res));
        return null;
      }
      const data: WeeklyReport = await res.json();
      setWeeklyReport(data);
      return data;
    } catch {
      setWeeklyReportError('Could not load weekly report. Check the API connection and try again.');
      return null;
    } finally {
      setWeeklyReportLoading(false);
    }
  }, [apiFetch, weeklyReportFrom, weeklyReportTo]);

  const fetchAll = useCallback(async () => {
    try {
      const devRes = await apiFetch('/devices', { headers: readHeaders() });
      if (!devRes.ok) throw new Error(`HTTP ${devRes.status}`);
      const devList: Device[] = (await devRes.json()) ?? [];
      setDevices(devList);
      // Focus summaries are fetched lazily for the inspected device only — no fleet-wide fan-out.
      // Browser history / daily usage refresh only for devices whose relevant tab is open.
      await Promise.allSettled([
        ...devList
          .filter(d => activeTabRef.current[d.device_id] === 'browser')
          .map(d => fetchBrowserHistory(d.device_id, browserHistoryRangeRef.current, { showLoader: false })),
        ...devList
          .filter(d => activeTabRef.current[d.device_id] === 'focus')
          .map(d => fetchDailyAppUsage(d.device_id, appUsageDateRef.current, { showLoader: false })),
      ]);
      setFleetConn('live');
    } catch (e) {
      console.error('Fetch error:', e);
      setFleetConn('stale');
    } finally {
      setLoading(false);
    }
  }, [apiFetch, fetchBrowserHistory, fetchDailyAppUsage]);

  const fetchPolicy = useCallback(async () => {
    try {
      const res = await apiFetch('/policy', { headers: readHeaders() });
      if (res.ok) {
        const d = await res.json();
        const nextPolicy = { ...DEFAULT_POLICY, ...d };
        setPolicy(nextPolicy);
      }
    } catch {}
  }, [apiFetch]);

  const fetchUsers = useCallback(async () => {
    if (!canManageUsers) return;
    try {
      const res = await apiFetch('/users');
      if (res.ok) setUsers(await res.json());
    } catch {}
  }, [apiFetch, canManageUsers]);

  const fetchAgentReleases = useCallback(async () => {
    if (!canManageReleases) return;
    try {
      const res = await apiFetch('/update/releases', { headers: readHeaders() });
      if (res.ok) {
        const data = await res.json();
        setAgentReleases(data.releases ?? []);
        setAllAgentReleases(data.all_releases ?? []);
      }
    } catch {}
  }, [apiFetch, canManageReleases]);

  const fetchAgentBuildJobs = useCallback(async () => {
    if (!canManageReleases) return;
    try {
      const res = await apiFetch('/update/builds', { headers: readHeaders() });
      if (res.ok) {
        const data = await res.json();
        setAgentBuildJobs(data.jobs ?? []);
      }
    } catch {}
  }, [apiFetch, canManageReleases]);

  useEffect(() => {
    if (!authUser) return;
    const t = window.setTimeout(() => { void fetchAll(); void fetchPolicy(); }, 0);
    const id = setInterval(fetchAll, 5000);
    return () => { window.clearTimeout(t); clearInterval(id); };
  }, [authUser, fetchAll, fetchPolicy]);

  useEffect(() => {
    if (currentPage !== 'access') return;
    const t = window.setTimeout(() => { void fetchUsers(); }, 0);
    return () => window.clearTimeout(t);
  }, [currentPage, fetchUsers]);

  useEffect(() => {
    if (currentPage !== 'settings') return;
    const t = window.setTimeout(() => { void fetchAgentReleases(); void fetchAgentBuildJobs(); }, 0);
    return () => window.clearTimeout(t);
  }, [currentPage, fetchAgentBuildJobs, fetchAgentReleases]);

  useEffect(() => {
    if (!authUser || currentPage !== 'reports') return;
    const t = window.setTimeout(() => { void fetchWeeklyReport(); }, 0);
    return () => window.clearTimeout(t);
  }, [authUser, currentPage, fetchWeeklyReport]);

  useEffect(() => {
    if (currentPage !== 'settings' || !canManageReleases) return;
    const hasActiveJob = agentBuildJobs.some(job => !['published', 'failed'].includes(job.status));
    if (!hasActiveJob) return;
    const id = setInterval(() => {
      void fetchAgentReleases();
      void fetchAgentBuildJobs();
    }, 3000);
    return () => clearInterval(id);
  }, [agentBuildJobs, canManageReleases, currentPage, fetchAgentBuildJobs, fetchAgentReleases]);

  // Focus summaries are only rendered on the inspected device's Overview/Focus tabs.
  // Fetch once per selected device instead of fan-out polling the whole fleet.
  useEffect(() => {
    if (!authUser || currentPage !== 'inspect' || !selectedDeviceId) return;
    let cancelled = false;
    const t = window.setTimeout(() => {
      (async () => {
        try {
          const res = await apiFetch(`/focus/${encodeURIComponent(selectedDeviceId)}`, { headers: readHeaders() });
          if (!res.ok || cancelled) return;
          const data = (await res.json()) as FocusCacheData;
          if (!cancelled) {
            setFocusCache(cache => ({ ...cache, [selectedDeviceId]: data.app_summaries ?? [] }));
          }
        } catch { /* focus cache is non-critical */ }
      })();
    }, 0);
    return () => { cancelled = true; window.clearTimeout(t); };
  }, [apiFetch, authUser, currentPage, selectedDeviceId]);

  // ── Actions ───────────────────────────────────────────────────────────────

  const submitAuth = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setAuthError('');
    setAuthSubmitting(true);
    const endpoint = authMode === 'register' ? '/auth/register' : '/auth/login';
    try {
      const res = await apiFetch(endpoint, {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify(authForm),
      });
      if (!res.ok) {
        setAuthError(await readApiError(res));
        return;
      }
      const data = await res.json();
      setAuthUser(data.user);
      setAuthForm({ name: '', email: '', password: '' });
      setShowAuthPassword(false);
      setLoading(true);
    } catch {
      setAuthError('Could not sign in. Check the API connection and try again.');
    } finally {
      setAuthSubmitting(false);
    }
  };

  const logout = async () => {
    try {
      await apiFetch('/auth/logout', { method: 'POST' });
    } finally {
      setAuthUser(null);
      setDevices([]);
      setFocusCache({});
      setBrowserHistoryCache({});
      setDailyAppUsageCache({});
      setDailyPresenceCache({});
      setAgentPingStatus({});
      setDeviceActionError('');
      goToPage('dashboard', { replace: true });
      setAuthMode('login');
      void loadCurrentUser();
    }
  };

  const createUser = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!canManageUsers) {
      setUserCreateStatus('Admin access required.');
      return;
    }
    setUserCreateStatus('');
    try {
      const res = await apiFetch('/users', {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify(userForm),
      });
      if (!res.ok) {
        setUserCreateStatus(await readApiError(res));
        return;
      }
      setUserForm({ name: '', email: '', password: '', role: 'viewer' });
      setUserCreateStatus('User created');
      void fetchUsers();
    } catch {
      setUserCreateStatus('Could not create user. Check the API connection and try again.');
    }
  };

  const resetUserPassword = async (e: FormEvent<HTMLFormElement>, user: DashboardUser) => {
    e.preventDefault();
    if (!canManageUsers) {
      setPasswordResetStatus('Admin access required.');
      return;
    }
    setPasswordResetStatus('');
    try {
      const res = await apiFetch(`/users/${user.id}/password`, {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify({ password: passwordResetValue }),
      });
      if (!res.ok) {
        setPasswordResetStatus(await readApiError(res));
        return;
      }
      setPasswordResetValue('');
      setPasswordResetFor('');
      setPasswordResetStatus(`Password reset for ${user.email}`);
      void fetchUsers();
    } catch {
      setPasswordResetStatus('Could not reset password. Check the API connection and try again.');
    }
  };

  const updateUserRole = async (user: DashboardUser, role: UserRole) => {
    if (!canManageUsers) {
      setRoleUpdateStatus('Admin access required.');
      return;
    }
    setRoleUpdateStatus('');
    try {
      const res = await apiFetch(`/users/${user.id}/role`, {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify({ role }),
      });
      if (!res.ok) {
        setRoleUpdateStatus(await readApiError(res));
        return;
      }
      setUsers(list => list.map(item => item.id === user.id ? { ...item, role } : item));
      setRoleUpdateStatus(`Role updated for ${user.email}`);
      if (authUser?.id === user.id) void loadCurrentUser();
    } catch {
      setRoleUpdateStatus('Could not update role. Check the API connection and try again.');
    }
  };

  const pingAgent = async (deviceId: string) => {
    if (!canPingAgents) return;
    setAgentPingStatus(prev => ({
      ...prev,
      [deviceId]: { state: 'checking', message: 'Pinging...' },
    }));
    try {
      const res = await apiFetch(`/devices/${encodeURIComponent(deviceId)}/ping`, {
        method: 'POST',
        headers: adminHeaders(),
      });
      if (!res.ok) {
        const message = await readApiError(res);
        setAgentPingStatus(prev => ({
          ...prev,
          [deviceId]: { state: 'error', message },
        }));
        return;
      }
      const data: { online?: boolean; age_seconds?: number; last_seen?: string } = await res.json();
      const age = formatPingAge(data.age_seconds);
      setAgentPingStatus(prev => ({
        ...prev,
        [deviceId]: {
          state: data.online ? 'online' : 'offline',
          message: data.online ? `Online, seen ${age}` : `No response, seen ${age}`,
        },
      }));
      if (data.last_seen) {
        setDevices(list => list.map(item => (
          item.device_id === deviceId ? { ...item, last_seen: data.last_seen } : item
        )));
      }
    } catch {
      setAgentPingStatus(prev => ({
        ...prev,
        [deviceId]: { state: 'error', message: 'Ping failed' },
      }));
    }
  };

  const savePolicy = async (nextPolicy: EnterprisePolicy) => {
    if (!canManagePolicy) return;
    setIsUpdating(true);
    setPolicy(nextPolicy);
    try {
      const res = await apiFetch('/policy', {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify(nextPolicy),
      });
      if (res.ok) setPolicySavedAt(new Date().toLocaleTimeString());
    } catch {} finally { setIsUpdating(false); }
  };

  const clampSyncInterval = (value: number) => Math.min(3600, Math.max(10, value || 10));

  const updatePolicyField = <K extends keyof EnterprisePolicy>(key: K, value: EnterprisePolicy[K]) => {
    const nextValue = key === 'sync_interval_seconds'
      ? clampSyncInterval(Number(value)) as EnterprisePolicy[K]
      : value;
    const nextPolicy = { ...policy, [key]: nextValue };
    if (key === 'browser_history_mode') {
      nextPolicy.collect_browser_history = nextValue !== 'disabled';
    }
    void savePolicy(nextPolicy);
  };

  const startAgentBuild = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!canManageReleases) return;
    setReleaseStatus('');
    const payload = {
      ...buildForm,
      version: buildForm.version.trim(),
      api_url: buildForm.api_url.trim(),
    };
    try {
      const res = await apiFetch('/update/builds', {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        setReleaseStatus(await readApiError(res));
        return;
      }
      setReleaseStatus(`Build queued for ${payload.version}`);
      setBuildForm(form => ({ ...form, version: '' }));
      await fetchAgentBuildJobs();
    } catch {
      setReleaseStatus('Could not start agent build.');
    }
  };

  const activateAgentRelease = async (rel: AgentRelease) => {
    if (!canManageReleases) return;
    setReleaseStatus('');
    try {
      const res = await apiFetch('/update/release/activate', {
        method: 'POST',
        headers: adminHeaders(),
        body: JSON.stringify({ os: rel.os, arch: rel.arch, version: rel.version }),
      });
      if (!res.ok) {
        setReleaseStatus(await readApiError(res));
        return;
      }
      setReleaseStatus(`Activated ${rel.version} for ${rel.os}/${rel.arch}`);
      await fetchAgentReleases();
    } catch {
      setReleaseStatus('Could not activate agent release.');
    }
  };

  const rolloutLatestAgents = async () => {
    if (!canManageReleases || rolloutSaving) return;
    setReleaseStatus('');
    setRolloutSaving(true);
    try {
      const res = await apiFetch('/update/release/rollout-latest', {
        method: 'POST',
        headers: adminHeaders(),
      });
      if (!res.ok) {
        setReleaseStatus(await readApiError(res));
        return;
      }
      const data: AgentRolloutResponse = await res.json();
      const targets = data.activated.map(rel => `${rel.os}/${rel.arch} ${rel.version}`).join(', ');
      setReleaseStatus(`Immediate rollout started for ${targets}. ${data.devices_marked} device${data.devices_marked === 1 ? '' : 's'} will check on their next heartbeat.`);
      await fetchAgentReleases();
      await fetchAll();
    } catch {
      setReleaseStatus('Could not start agent rollout.');
    } finally {
      setRolloutSaving(false);
    }
  };

  const openDeleteConfirm = (deviceId: string, name: string) => {
    if (!canDeleteDevices) return;
    setDeviceActionError('');
    setConfirmTarget({ deviceId, name });
  };

  const performDeleteDevice = async (deviceId: string) => {
    if (!canDeleteDevices) return;
    try {
      const res = await apiFetch(`/devices/${encodeURIComponent(deviceId)}`, { method: 'DELETE', headers: adminHeaders() });
      if (!res.ok) throw new Error(await readApiError(res));
      setDevices(d => d.filter(x => x.device_id !== deviceId));
      setActiveTab(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      setFocusCache(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      setBrowserHistoryCache(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      setBrowserHistoryLoading(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      setDailyAppUsageCache(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      setDailyAppUsageLoading(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      setAgentPingStatus(prev => {
        const next = { ...prev };
        delete next[deviceId];
        return next;
      });
      if (selectedDeviceId === deviceId) {
        goToPage('inventory', { replace: true });
      }
    } catch (e) {
      console.error('Device deletion failed:', e);
      setDeviceActionError(e instanceof Error ? e.message : 'Device deletion failed.');
    }
  };

  const openRenameDevice = (device: Device) => {
    if (!canDeleteDevices) return;
    setRenameTarget(device);
    setRenameValue(device.display_name || '');
    setRenameError('');
  };

  const closeRenameDevice = useCallback(() => {
    if (renameSaving) return;
    setRenameTarget(null);
    setRenameValue('');
    setRenameError('');
  }, [renameSaving]);

  // Rename dialog: Escape closes, Tab is trapped inside, background scroll is locked.
  useEffect(() => {
    if (!renameTarget) return;
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    document.body.style.overflow = 'hidden';

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeRenameDevice();
        return;
      }
      if (e.key !== 'Tab') return;
      const panel = document.querySelector<HTMLElement>('.rename-modal');
      if (!panel) return;
      const items = Array.from(panel.querySelectorAll<HTMLElement>('button, input, select, textarea, a[href]'))
        .filter(el => !el.hasAttribute('disabled'));
      if (!items.length) return;
      const first = items[0];
      const last = items[items.length - 1];
      const activeEl = document.activeElement;
      const inside = panel.contains(activeEl);
      if (e.shiftKey && (activeEl === first || !inside)) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && (activeEl === last || !inside)) { e.preventDefault(); first.focus(); }
    };

    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = '';
      previouslyFocused?.focus();
    };
  }, [renameTarget, closeRenameDevice]);

  const saveRenameDevice = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!renameTarget || !canDeleteDevices) return;
    const name = renameValue.trim();
    if (name.length > 80) {
      setRenameError('Name must be 80 characters or fewer.');
      return;
    }
    setRenameSaving(true);
    setRenameError('');
    try {
      const res = await apiFetch(`/devices/${encodeURIComponent(renameTarget.device_id)}/name`, {
        method: 'PUT',
        headers: adminHeaders(),
        body: JSON.stringify({ name }),
      });
      if (!res.ok) throw new Error(await readApiError(res));
      setDevices(list => list.map(item => (
        item.device_id === renameTarget.device_id
          ? { ...item, display_name: name || undefined }
          : item
      )));
      setRenameTarget(null);
      setRenameValue('');
    } catch {
      setRenameError('Could not rename device. Check the API connection and try again.');
    } finally {
      setRenameSaving(false);
    }
  };

  const getTab = (id: string): DeviceTab => activeTab[id] ?? 'overview';
  const setTab = (id: string, tab: DeviceTab) => {
    setActiveTab(prev => ({ ...prev, [id]: tab }));
    if (selectedDeviceId === id && currentPage === 'inspect') {
      writeNavigationState('inspect', { deviceId: id, tab, replace: true });
    }
    if (tab === 'browser') void fetchBrowserHistory(id, browserHistoryRange);
  };

  const updateBrowserHistoryRange = (range: BrowserHistoryRange) => {
    setBrowserHistoryRange(range);
    if (selectedDeviceId && getTab(selectedDeviceId) === 'browser') {
      void fetchBrowserHistory(selectedDeviceId, range);
    }
  };

  const updateAppUsageDate = (date: string) => {
    setAppUsageDate(date);
  };

  useEffect(() => {
    if (!authUser || currentPage !== 'inspect' || !selectedDeviceId) return;
    const tab = activeTab[selectedDeviceId] ?? 'overview';
    if (tab !== 'overview' && tab !== 'focus') return;
    const t = window.setTimeout(() => {
      void fetchDailyAppUsage(selectedDeviceId, appUsageDate, { showLoader: false });
      void fetchDailyPresence(selectedDeviceId, appUsageDate);
    }, 0);
    return () => window.clearTimeout(t);
  }, [activeTab, appUsageDate, authUser, currentPage, fetchDailyAppUsage, fetchDailyPresence, selectedDeviceId]);

  const buildOnDemandReport = (report: WeeklyReport, type: OnDemandReportType): string => {
    const lines: string[] = [];
    const title = type === 'app_usage'
      ? 'Weekly App Usage Report'
      : type === 'device_health'
        ? 'Weekly Device Health Report'
        : 'Weekly Executive Report';
    lines.push(title);
    lines.push(`Window: ${report.from} to ${report.to}`);
    lines.push(`Generated: ${new Date().toLocaleString()}`);
    lines.push('');
    lines.push('Coverage');
    lines.push(`Devices: ${report.coverage.device_count}`);
    lines.push(`Telemetry devices: ${report.coverage.telemetry_devices}`);
    lines.push(`Telemetry events: ${report.coverage.telemetry_events}`);
    lines.push(`App usage days: ${report.coverage.app_usage_days}/${report.requested_days}`);
    lines.push(`Browser entries: ${report.coverage.browser_entries}`);
    lines.push('');

    if (type === 'executive') {
      lines.push('Fleet Summary');
      lines.push(`Online: ${report.fleet.online}`);
      lines.push(`Offline: ${report.fleet.offline}`);
      lines.push(`Warning: ${report.fleet.warning}`);
      lines.push(`Critical: ${report.fleet.critical}`);
      lines.push(`Total app usage: ${formatDuration(report.app_usage.total_seconds)}`);
      lines.push(`Sessions: ${report.app_usage.sessions}`);
      lines.push('');
      lines.push('Highest Usage Devices');
      report.devices.forEach((device, index) => {
        lines.push(`${index + 1}. ${device.name}: ${formatDuration(device.app_usage_seconds)}, ${device.browser_entries} browser entries, ${device.state}`);
      });
    }

    if (type === 'app_usage') {
      lines.push('Apps Used');
      report.app_usage.top_apps.forEach((app, index) => {
        lines.push(`${index + 1}. ${app.app_name}: ${formatDuration(app.total_seconds)}, ${app.session_count} sessions, ${app.device_count} devices`);
      });
      lines.push('');
      lines.push('Device Usage');
      report.devices.forEach(device => {
        lines.push(`${device.name}: ${formatDuration(device.app_usage_seconds)}, ${device.app_usage_sessions} sessions, ${device.app_usage_days}/${report.requested_days} days`);
      });
    }

    if (type === 'device_health') {
      lines.push('Fleet Health');
      lines.push(`Average CPU: ${report.fleet.avg_cpu.toFixed(1)}%`);
      lines.push(`Average memory: ${report.fleet.avg_ram.toFixed(1)}%`);
      lines.push('');
      lines.push('Devices');
      report.devices.forEach(device => {
        lines.push(`${device.name}: ${device.state}, CPU ${(device.cpu_percent ?? 0).toFixed(1)}%, RAM ${(device.ram_percent ?? 0).toFixed(1)}%, disk ${(device.disk_percent ?? 0).toFixed(1)}%, pending updates ${device.pending_updates}`);
      });
    }

    return lines.join('\n');
  };

  const generateOnDemandReport = async () => {
    const needsFreshReport = !weeklyReport || weeklyReport.from !== weeklyReportFrom || weeklyReport.to !== weeklyReportTo;
    const report = needsFreshReport ? await fetchWeeklyReport() : weeklyReport;
    if (!report) return;
    const text = buildOnDemandReport(report, onDemandReportType);
    const name = `devicepulse-${onDemandReportType}-${report.from}-to-${report.to}.txt`;
    setOnDemandReportText(text);
    setOnDemandReportName(name);
  };

  const downloadOnDemandReport = () => {
    if (!onDemandReportText || !onDemandReportName) return;
    const blob = new Blob([onDemandReportText], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = onDemandReportName;
    link.click();
    URL.revokeObjectURL(url);
  };

  // ── Computed stats ────────────────────────────────────────────────────────

  const onlineCount  = devices.filter(d => isOnline(d.last_seen)).length;
  const offlineCount = devices.length - onlineCount;
  const fleetUptimePercent = devices.length > 0 ? (onlineCount / devices.length) * 100 : 0;
  const selectedDevice = devices.find(d => d.device_id === selectedDeviceId);
  const selectedDeviceName = selectedDevice ? deviceDisplayName(selectedDevice) : selectedDeviceId;
  const selectedBrowserHistory = selectedDevice
    ? browserHistoryCache[selectedDevice.device_id]?.[browserHistoryRange]
    : undefined;
  const selectedBrowserHistoryLoaded = selectedDevice
    ? browserHistoryCache[selectedDevice.device_id]?.[browserHistoryRange] !== undefined
    : false;
  const selectedDailyAppUsage = selectedDevice
    ? dailyAppUsageCache[selectedDevice.device_id]?.[appUsageDate]
    : undefined;
  const selectedDailyPresence = selectedDevice
    ? dailyPresenceCache[selectedDevice.device_id]?.[appUsageDate]
    : undefined;

  const deviceRisk = (device: Device) => {
    const hw = device.data?.HardwareStats;
    return Math.max(hw?.cpu?.usage_percent ?? 0, hw?.ram?.used_percent ?? 0, primaryDisk(hw?.disks)?.used_percent ?? 0);
  };
  const getDeviceState = (device: Device): 'online' | 'critical' | 'warning' | 'offline' => {
    if (!isOnline(device.last_seen)) return 'offline';
    const risk = deviceRisk(device);
    if (risk >= 90) return 'critical';
    if (risk >= 70) return 'warning';
    return 'online';
  };

  const criticalCount = devices.filter(d => getDeviceState(d) === 'critical').length;
  const warningCount  = devices.filter(d => getDeviceState(d) === 'warning').length;
  const attentionDevice = [...devices]
    .sort((a, b) => deviceRisk(b) - deviceRisk(a))
    .find(d => deviceRisk(d) > 0);
  const hotDevices = [...devices]
    .sort((a, b) => {
      const stateWeight = (device: Device) => {
        const state = getDeviceState(device);
        if (state === 'critical') return 400;
        if (state === 'offline') return 300;
        if (state === 'warning') return 200;
        return 0;
      };
      return stateWeight(b) + deviceRisk(b) - (stateWeight(a) + deviceRisk(a));
    })
    .slice(0, 5);
  const radarDevices = hotDevices.length ? hotDevices : devices.slice(0, 5);
  const avgCpu = devices.length > 0
    ? devices.reduce((sum, d) => sum + (d.data?.HardwareStats?.cpu?.usage_percent ?? 0), 0) / devices.length
    : 0;
  const avgRam = devices.length > 0
    ? devices.reduce((sum, d) => sum + (d.data?.HardwareStats?.ram?.used_percent ?? 0), 0) / devices.length
    : 0;
  const avgDisk = devices.length > 0
    ? devices.reduce((sum, d) => sum + (primaryDisk(d.data?.HardwareStats?.disks)?.used_percent ?? 0), 0) / devices.length
    : 0;
  const commandStatus = criticalCount > 0
    ? 'Critical'
    : offlineCount > 0 || warningCount > 0
      ? 'Watch'
      : 'Stable';
  const signalBars = [
    { label: 'CPU', value: avgCpu, className: 'signal-cpu' },
    { label: 'RAM', value: avgRam, className: 'signal-ram' },
    { label: 'Disk', value: avgDisk, className: 'signal-disk' },
    { label: 'Link', value: fleetUptimePercent, className: 'signal-link' },
  ];

  const normalizedQuery = searchQuery.trim().toLowerCase();
  const filteredDevices = devices.filter(device => {
    const state = getDeviceState(device);
    if (statusFilter === 'online' && !isOnline(device.last_seen)) return false;
    if (statusFilter !== 'all' && statusFilter !== 'online' && state !== statusFilter) return false;
    if (!normalizedQuery) return true;
    const sys = device.data?.SystemInfo;
    return [device.device_id, device.display_name, device.hostname, sys?.hostname, sys?.os, sys?.platform, sys?.platform_version]
      .filter(Boolean).join(' ').toLowerCase().includes(normalizedQuery);
  });

  const filterOptions: { key: StatusFilter; label: string; count: number }[] = [
    { key: 'all',      label: 'All Devices', count: devices.length },
    { key: 'online',   label: 'Online',      count: onlineCount },
    { key: 'critical', label: 'Critical',    count: criticalCount },
    { key: 'warning',  label: 'Warning',     count: warningCount },
    { key: 'offline',  label: 'Offline',     count: offlineCount },
  ];

  const viewDeviceDetails = (deviceId: string) => {
    goToPage('inspect', { deviceId, tab: 'overview' });
  };

  const collectorControls: { key: CollectorPolicyKey; label: string; meta: string }[] = [
    { key: 'collect_system_info', label: 'System info', meta: 'Hostname, OS, architecture, kernel' },
    { key: 'collect_hardware_stats', label: 'Hardware stats', meta: 'CPU, RAM, disk, network and battery' },
    { key: 'collect_processes', label: 'Processes', meta: 'Active process names with CPU and memory' },
    { key: 'collect_active_window', label: 'App usage', meta: 'Foreground app and time used' },
    { key: 'collect_services', label: 'Services', meta: 'Running and stopped system services' },
    { key: 'collect_network_ports', label: 'Network ports', meta: 'Open TCP/UDP ports and owning process' },
    { key: 'collect_installed_apps', label: 'Installed apps', meta: 'Application inventory and versions' },
    { key: 'collect_os_updates', label: 'OS updates', meta: 'Update status and pending count' },
    { key: 'collect_usb_devices', label: 'USB devices', meta: 'Connected USB device inventory' },
  ];

  const releasesByOS = RELEASE_TARGETS.map(target => ({
    ...target,
    releases: agentReleases.filter(rel => rel.os === target.os),
  }));

  const releaseOptionsFor = (os: AgentRelease['os'], arch: string) => {
    const seen = new Set<string>();
    return allAgentReleases
      .filter(rel => rel.os === os && rel.arch === arch)
      .filter(rel => {
        const key = rel.version;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  };
  const visibleBuildJobs = showAllBuildHistory ? agentBuildJobs : agentBuildJobs.slice(0, 2);
  const hiddenBuildJobCount = Math.max(0, agentBuildJobs.length - visibleBuildJobs.length);
  const weeklyCoverageLabel = weeklyReport
    ? weeklyReport.coverage.complete_app_usage_week
      ? `${weeklyReport.coverage.app_usage_days}/${weeklyReport.requested_days} app-usage days`
      : `Partial app-usage data: ${weeklyReport.coverage.app_usage_days}/${weeklyReport.requested_days} days`
    : '';
  const weeklyDevices = weeklyReport?.devices ?? [];

  const toggleBuildPlatform = (os: AgentRelease['os'], checked: boolean) => {
    setBuildForm(form => ({
      ...form,
      platforms: checked ? Array.from(new Set([...form.platforms, os])) : form.platforms.filter(item => item !== os),
    }));
  };

  const toggleBuildArch = (arch: string, checked: boolean) => {
    setBuildForm(form => ({
      ...form,
      archs: checked ? Array.from(new Set([...form.archs, arch])) : form.archs.filter(item => item !== arch),
    }));
  };

  // ── Render ────────────────────────────────────────────────────────────────

  if (authLoading) {
    return (
      <div className="auth-shell auth-shell-solo">
        <div className="auth-form-col">
          <div className="auth-panel">
            <div className="auth-logo-lg pulse"><IconLogo /></div>
            <h1>DevicePulse</h1>
            <p>Checking dashboard session…</p>
          </div>
        </div>
      </div>
    );
  }

  if (!authUser) {
    const isBootstrap = authMode === 'register' && bootstrapRequired;
    return (
      <div className="auth-shell">
        {/* ── Brand panel ── */}
        <aside className="auth-brand" aria-hidden="true">
          <div className="auth-brand-inner">
            <div className="auth-brand-mark"><IconLogo /></div>
            <div className="auth-brand-name">DevicePulse</div>
            <h2 className="auth-brand-headline">Your fleet,<br />at a glance.</h2>
            <p className="auth-brand-sub">
              Real-time endpoint telemetry for Linux, Windows and macOS — hardware,
              processes, browser activity and security posture in a single console.
            </p>
            <ul className="auth-brand-points">
              <li><IconPulse /> Live telemetry, refreshed every few seconds</li>
              <li><IconShield /> Role-based access with remote quarantine</li>
              <li><IconDevices /> Cross-platform agents, one dashboard</li>
            </ul>
            <div className="auth-demo">
              <div className="auth-demo-title"><span className="auth-demo-dot" />Live fleet snapshot</div>
              <div className="auth-demo-row">
                <span className="auth-demo-label">CPU</span>
                <span className="auth-demo-track"><span className="auth-demo-fill" style={{ width: '34%' }} /></span>
                <span className="auth-demo-val">34%</span>
              </div>
              <div className="auth-demo-row">
                <span className="auth-demo-label">MEM</span>
                <span className="auth-demo-track"><span className="auth-demo-fill warn" style={{ width: '58%' }} /></span>
                <span className="auth-demo-val">58%</span>
              </div>
              <div className="auth-demo-row">
                <span className="auth-demo-label">Online</span>
                <span className="auth-demo-track"><span className="auth-demo-fill ok" style={{ width: '86%' }} /></span>
                <span className="auth-demo-val">128</span>
              </div>
            </div>
          </div>
          <div className="auth-brand-foot">Sessions are secured with HTTP-only cookies over TLS.</div>
        </aside>

        {/* ── Form panel ── */}
        <main className="auth-form-col">
          <form className="auth-form" onSubmit={submitAuth}>
            <div className="auth-form-brand">
              <div className="auth-logo-lg"><IconLogo /></div>
              <span>DevicePulse</span>
            </div>
            <h1>{isBootstrap ? 'Create first admin' : 'Sign in'}</h1>
            <p className="auth-form-sub">
              {isBootstrap
                ? 'Bootstrap the first administrator account for this DevicePulse dashboard.'
                : 'Use your dashboard account to view endpoint telemetry.'}
            </p>

            {authMode === 'register' && (
              <label className="auth-field">
                <span>Name</span>
                <input
                  value={authForm.name}
                  onChange={e => setAuthForm(f => ({ ...f, name: e.target.value }))}
                  autoComplete="name"
                  required
                />
              </label>
            )}

            <label className="auth-field">
              <span>Email</span>
              <span className="auth-input-wrap">
                <span className="auth-input-icon"><IconMail /></span>
                <input
                  type="email"
                  value={authForm.email}
                  onChange={e => setAuthForm(f => ({ ...f, email: e.target.value }))}
                  autoComplete="email"
                  required
                />
              </span>
            </label>

            <label className="auth-field">
              <span>Password</span>
              <span className="auth-input-wrap has-toggle">
                <span className="auth-input-icon"><IconLock /></span>
                <input
                  type={showAuthPassword ? 'text' : 'password'}
                  value={authForm.password}
                  onChange={e => setAuthForm(f => ({ ...f, password: e.target.value }))}
                  autoComplete={authMode === 'register' ? 'new-password' : 'current-password'}
                  minLength={8}
                  required
                />
                <button
                  type="button"
                  className="auth-pass-toggle"
                  onClick={() => setShowAuthPassword(v => !v)}
                  aria-label={showAuthPassword ? 'Hide password' : 'Show password'}
                  aria-pressed={showAuthPassword}
                >
                  {showAuthPassword ? <IconEyeOff /> : <IconEye />}
                </button>
              </span>
            </label>

            {authError && <div className="auth-error" role="alert">{authError}</div>}

            <button type="submit" className="auth-submit" disabled={authSubmitting}>
              {authSubmitting && <span className="auth-btn-spinner" aria-hidden="true" />}
              {authSubmitting
                ? (authMode === 'register' ? 'Creating account…' : 'Signing in…')
                : (authMode === 'register' ? 'Create admin account' : 'Sign in')}
            </button>

            {!bootstrapRequired && (
              <p className="auth-hint">New accounts are created by an admin.</p>
            )}
          </form>
          <div className="auth-form-foot">DevicePulse · Endpoint telemetry console</div>
        </main>
      </div>
    );
  }

  return (
    <div className="app-shell">

      {/* ── Top header bar ── */}
      <header className="app-header">
        <div className="header-brand">
          <div className="header-logo"><IconLogo /></div>
          <span className="brand-name">DevicePulse</span>
        </div>

        <div className="header-right">
          <div className="header-status">
            <HeaderClock />
            {!loading && fleetConn === 'live' && (
              <span className="live-badge">
                <span className="dot" />
                Live
              </span>
            )}
            {fleetConn === 'stale' && (
              <span className="live-badge is-stale" role="status">
                <span className="dot" />
                Reconnecting…
              </span>
            )}
          </div>
          <details className="user-menu">
            <summary className="user-chip" aria-label="User menu">
              <span className="user-avatar">{(authUser.name || authUser.email || 'U').charAt(0).toUpperCase()}</span>
              <span className="user-copy">
                <span className="user-name">{authUser.name || authUser.email}</span>
                <span className="user-role">{roleLabel[authUser.role]}</span>
              </span>
              <span className="user-caret" aria-hidden="true" />
            </summary>
            <div className="user-menu-panel">
              <button type="button" onClick={logout}>Sign out</button>
            </div>
          </details>
        </div>
      </header>

      {/* ── Sidebar ── */}
      <aside className="app-sidebar">
        <div className="sidebar-section">
          <div className="sidebar-label">Navigation</div>
          <nav className="sidebar-nav" aria-label="Dashboard pages">
            <button
              type="button"
              className={`nav-item ${currentPage === 'dashboard' ? 'active' : ''}`}
              onClick={() => goToPage('dashboard')}
              aria-current={currentPage === 'dashboard' ? 'page' : undefined}
            >
              <IconGrid />
              Dashboard
            </button>
            <button
              type="button"
              className={`nav-item ${currentPage === 'inventory' ? 'active' : ''}`}
              onClick={() => goToPage('inventory')}
              aria-current={currentPage === 'inventory' ? 'page' : undefined}
            >
              <IconList />
              Device Inventory
            </button>
            <button
              type="button"
              className={`nav-item ${currentPage === 'reports' ? 'active' : ''}`}
              onClick={() => goToPage('reports')}
              aria-current={currentPage === 'reports' ? 'page' : undefined}
            >
              <IconReport />
              Reports
            </button>
            <button
              type="button"
              className={`nav-item ${currentPage === 'settings' ? 'active' : ''}`}
              onClick={() => goToPage('settings')}
              aria-current={currentPage === 'settings' ? 'page' : undefined}
            >
              <IconSettings />
              Settings
            </button>
            {canManageUsers && (
              <button
                type="button"
                className={`nav-item ${currentPage === 'access' ? 'active' : ''}`}
                onClick={() => goToPage('access')}
                aria-current={currentPage === 'access' ? 'page' : undefined}
              >
                <IconSettings />
                Access
              </button>
            )}
          </nav>
        </div>

        <div className="sidebar-section">
          <div className="sidebar-label">Fleet — {filteredDevices.length}/{devices.length}</div>
          <div className="fleet-widget">
            <input
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              placeholder="Search devices…"
              className="fleet-search"
              aria-label="Search devices"
            />
            <div className="fleet-filter-row" role="group" aria-label="Status filter">
              {(['all', 'online', 'offline'] as const).map(f => (
                <button
                  key={f}
                  type="button"
                  className={`fleet-filter-btn ${statusFilter === f ? 'active' : ''}`}
                  onClick={() => setStatusFilter(f)}
                >
                  {f}
                </button>
              ))}
            </div>
            {attentionDevice && (
              <div className="attention-strip">
                <div className="kicker">Needs Attention</div>
                <strong>{deviceDisplayName(attentionDevice)}</strong>
                <span>Peak load {deviceRisk(attentionDevice).toFixed(0)}%</span>
              </div>
            )}
          </div>
        </div>

      </aside>

      {/* ── Main content ── */}
      <main className="app-main">
        <div className="main-inner">

          {/* Page header */}
          <div className="page-header">
            <div>
              <h1>
                {currentPage === 'settings'
                  ? 'Settings'
                  : currentPage === 'access'
                    ? 'Access Control'
                  : currentPage === 'inspect'
                    ? selectedDeviceName || 'Inspect Device'
                  : currentPage === 'reports'
                    ? 'Weekly Report'
                  : currentPage === 'inventory'
                    ? 'Device Inventory'
                    : 'Telemetry Dashboard'}
              </h1>
              <p>
                {currentPage === 'settings'
                  ? 'Enterprise data collection, retention and upload controls'
                  : currentPage === 'access'
                  ? 'Create dashboard users and assign roles'
                  : currentPage === 'inspect'
                  ? selectedDevice?.device_id || 'Device telemetry details'
                  : currentPage === 'reports'
                  ? 'Seven-day fleet activity and coverage'
                  : currentPage === 'inventory'
                  ? `${devices.length.toLocaleString()} device${devices.length !== 1 ? 's' : ''} registered`
                  : 'Fleet status summary'}
              </p>
            </div>
            {currentPage === 'inspect' ? (
              <div className="toolbar">
                <button type="button" className="btn" onClick={() => goToPage('inventory')}>
                  ‹ Back to Inventory
                </button>
              </div>
            ) : currentPage === 'inventory' && (
              <div className="toolbar">
                <div className="search-box">
                  <IconSearch />
                  <input
                    value={searchQuery}
                    onChange={e => setSearchQuery(e.target.value)}
                    placeholder="Search by ID, name or OS…"
                    aria-label="Search devices"
                  />
                </div>
              </div>
            )}
            {currentPage === 'reports' && (
              <div className="toolbar report-toolbar">
                <label>
                  <span>From</span>
                  <input
                    type="date"
                    value={weeklyReportFrom}
                    onChange={e => {
                      setWeeklyReportFrom(e.target.value);
                      setOnDemandReportText('');
                      setOnDemandReportName('');
                    }}
                  />
                </label>
                <label>
                  <span>To</span>
                  <input
                    type="date"
                    value={weeklyReportTo}
                    onChange={e => {
                      setWeeklyReportTo(e.target.value);
                      setOnDemandReportText('');
                      setOnDemandReportName('');
                    }}
                  />
                </label>
                <button type="button" className="btn" onClick={() => void fetchWeeklyReport()} disabled={weeklyReportLoading}>
                  {weeklyReportLoading ? 'Loading...' : 'Refresh'}
                </button>
              </div>
            )}
          </div>

          {deviceActionError && <div className="auth-error" role="alert">{deviceActionError}</div>}

          {currentPage === 'dashboard' && (
            <>
              <section className="dashboard-tiles" aria-label="Fleet summary">
                <button
                  type="button"
                  className="dashboard-tile"
                  onClick={() => {
                    setStatusFilter('all');
                    goToPage('inventory');
                  }}
                >
                  <span>Total Devices</span>
                  <strong>{devices.length.toLocaleString()}</strong>
                </button>
                <button
                  type="button"
                  className="dashboard-tile tile-online"
                  onClick={() => {
                    setStatusFilter('online');
                    goToPage('inventory');
                  }}
                >
                  <span>Online</span>
                  <strong>{onlineCount.toLocaleString()}</strong>
                </button>
                <button
                  type="button"
                  className="dashboard-tile tile-offline"
                  onClick={() => {
                    setStatusFilter('offline');
                    goToPage('inventory');
                  }}
                >
                  <span>Offline</span>
                  <strong>{offlineCount.toLocaleString()}</strong>
                </button>
                <button
                  type="button"
                  className="dashboard-tile tile-uptime"
                  onClick={() => {
                    setStatusFilter('all');
                    goToPage('inventory');
                  }}
                >
                  <span>Total Uptime</span>
                  <strong>{fleetUptimePercent.toFixed(1)}%</strong>
                </button>
              </section>

              <section className="command-panel" aria-label="Fleet command radar">
                <div className="command-radar">
                  <div className="radar-header">
                    <div>
                      <span>Fleet Command</span>
                      <strong>{commandStatus}</strong>
                    </div>
                    <button
                      type="button"
                      className={`command-state state-${commandStatus.toLowerCase()}`}
                      onClick={() => {
                        setStatusFilter(commandStatus === 'Stable' ? 'online' : criticalCount > 0 ? 'critical' : 'offline');
                        goToPage('inventory');
                      }}
                    >
                      {criticalCount + warningCount + offlineCount} Signals
                    </button>
                  </div>

                  <div className="radar-stage">
                    <div className="radar-sweep" />
                    <div className="radar-ring ring-one" />
                    <div className="radar-ring ring-two" />
                    <div className="radar-ring ring-three" />
                    {radarDevices.map((device, index) => {
                      const state = getDeviceState(device);
                      const positions = [
                        { left: '68%', top: '28%' },
                        { left: '34%', top: '34%' },
                        { left: '54%', top: '62%' },
                        { left: '77%', top: '68%' },
                        { left: '24%', top: '70%' },
                      ];
                      return (
                        <button
                          key={device.device_id}
                          type="button"
                          className={`radar-blip blip-${state}`}
                          style={positions[index % positions.length]}
                          onClick={() => goToPage('inspect', { deviceId: device.device_id, tab: 'overview' })}
                          aria-label={`Inspect ${deviceDisplayName(device)}`}
                        />
                      );
                    })}
                    <div className="radar-core">
                      <strong>{fleetUptimePercent.toFixed(0)}%</strong>
                      <span>link</span>
                    </div>
                  </div>
                </div>

                <div className="command-side">
                  <div className="signal-stack">
                    {signalBars.map(signal => (
                      <div key={signal.label} className="signal-row">
                        <span>{signal.label}</span>
                        <div className="signal-track">
                          <i className={signal.className} style={{ width: `${Math.min(100, Math.max(0, signal.value))}%` }} />
                        </div>
                        <strong>{signal.value.toFixed(0)}%</strong>
                      </div>
                    ))}
                  </div>

                  <div className="hot-list">
                    <div className="hot-list-title">
                      <span>Heat Map</span>
                      <strong>{hotDevices.length ? 'Top Signals' : 'No Devices'}</strong>
                    </div>
                    {hotDevices.length ? hotDevices.map(device => {
                      const state = getDeviceState(device);
                      return (
                        <button
                          key={device.device_id}
                          type="button"
                          className={`hot-device hot-${state}`}
                          onClick={() => goToPage('inspect', { deviceId: device.device_id, tab: 'overview' })}
                        >
                          <span>{deviceDisplayName(device)}</span>
                          <strong>{state === 'offline' ? 'Offline' : `${deviceRisk(device).toFixed(0)}%`}</strong>
                        </button>
                      );
                    }) : (
                      <div className="hot-device hot-empty">
                        <span>Waiting for telemetry</span>
                        <strong>Idle</strong>
                      </div>
                    )}
                  </div>
                </div>
              </section>
            </>
          )}

          {/* Status filter tabs */}
          {currentPage === 'inventory' && (
            <div className="filter-tabs" role="group" aria-label="Filter by status">
              {filterOptions.map(opt => (
                <button
                  key={opt.key}
                  type="button"
                  className={`filter-tab tab-${opt.key} ${statusFilter === opt.key ? 'active' : ''}`}
                  onClick={() => setStatusFilter(opt.key)}
                >
                  {opt.label}
                  <span className="tab-count">{opt.count.toLocaleString()}</span>
                </button>
              ))}
            </div>
          )}

          {/* Content */}
          {currentPage === 'access' && !canManageUsers ? (
            <div className="empty-state">
              <span className="empty-icon">🔒</span>
              <div className="empty-title">Admin access required</div>
              <div className="empty-sub">User management is available to dashboard admins only.</div>
            </div>
          ) : currentPage === 'access' ? (
            <div className="settings-layout">
              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Create Dashboard User</h2>
                    <p>New users can sign in after an admin creates their account.</p>
                  </div>
                  {userCreateStatus && <span className="save-state" role="status">{userCreateStatus}</span>}
                </div>
                <form className="user-form" onSubmit={createUser}>
                  <label className="auth-field">
                    <span>Name</span>
                    <input
                      value={userForm.name}
                      onChange={e => setUserForm(f => ({ ...f, name: e.target.value }))}
                      autoComplete="name"
                    />
                  </label>
                  <label className="auth-field">
                    <span>Email</span>
                    <input
                      type="email"
                      value={userForm.email}
                      onChange={e => setUserForm(f => ({ ...f, email: e.target.value }))}
                      autoComplete="email"
                      required
                    />
                  </label>
                  <label className="auth-field">
                    <span>Role</span>
                    <select
                      value={userForm.role}
                      onChange={e => setUserForm(f => ({ ...f, role: e.target.value as UserRole }))}
                    >
                      <option value="viewer">Viewer</option>
                      <option value="manager">Manager</option>
                      <option value="admin">Admin</option>
                    </select>
                  </label>
                  <label className="auth-field">
                    <span>Temporary Password</span>
                    <input
                      type="password"
                      value={userForm.password}
                      onChange={e => setUserForm(f => ({ ...f, password: e.target.value }))}
                      autoComplete="new-password"
                      minLength={8}
                      required
                    />
                  </label>
                  <button type="submit" className="auth-submit user-submit">Create user</button>
                </form>
              </section>

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Dashboard Users</h2>
                    <p>Accounts with active access to this dashboard.</p>
                  </div>
                  {passwordResetStatus && <span className="save-state" role="status">{passwordResetStatus}</span>}
                  {roleUpdateStatus && <span className="save-state" role="status">{roleUpdateStatus}</span>}
                </div>
                <div className="table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>User</th>
                        <th>Role</th>
                        <th>Status</th>
                        <th>Created</th>
                        <th style={{ textAlign: 'right' }}>Password</th>
                      </tr>
                    </thead>
                    <tbody>
                      {users.map(user => (
                        <tr key={user.id}>
                          <td>
                            <div className="device-cell-text">
                              <strong>{user.name || user.email}</strong>
                              <span>{user.email}</span>
                            </div>
                          </td>
                          <td>
                            <select
                              className="role-select"
                              value={user.role}
                              onChange={e => updateUserRole(user, e.target.value as UserRole)}
                              aria-label={`Change role for ${user.email}`}
                            >
                              <option value="viewer">Viewer</option>
                              <option value="manager">Manager</option>
                              <option value="admin">Admin</option>
                            </select>
                          </td>
                          <td>{user.status ?? 'active'}</td>
                          <td>{user.created_at ? new Date(user.created_at).toLocaleDateString() : '—'}</td>
                          <td>
                            {passwordResetFor === user.id ? (
                              <form className="reset-password-form" onSubmit={e => resetUserPassword(e, user)}>
                                <input
                                  type="password"
                                  value={passwordResetValue}
                                  onChange={e => setPasswordResetValue(e.target.value)}
                                  placeholder="New password"
                                  minLength={8}
                                  autoComplete="new-password"
                                  required
                                />
                                <button type="submit" className="action-btn">Save</button>
                                <button
                                  type="button"
                                  className="action-btn"
                                  onClick={() => {
                                    setPasswordResetFor('');
                                    setPasswordResetValue('');
                                  }}
                                >
                                  Cancel
                                </button>
                              </form>
                            ) : (
                              <div className="row-actions">
                                <button
                                  type="button"
                                  className="action-btn"
                                  onClick={() => {
                                    setPasswordResetStatus('');
                                    setPasswordResetFor(user.id);
                                    setPasswordResetValue('');
                                  }}
                                >
                                  Reset
                                </button>
                              </div>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            </div>
          ) : currentPage === 'settings' ? (
            <div className="settings-layout">
              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Retention</h2>
                    <p>Controls how long new server telemetry remains queryable.</p>
                  </div>
                  {isUpdating
                    ? <span className="save-state muted" role="status">Saving…</span>
                    : policySavedAt && <span className="save-state" role="status">Saved {policySavedAt}</span>}
                  {!canManagePolicy && <span className="save-state muted">Read only</span>}
                </div>
                <div className="settings-grid two-col">
                  <label className="setting-field">
                    <span>Telemetry Retention</span>
                    <div className="number-control">
                      <input
                        type="number"
                        min="1"
                        max="3650"
                        value={policy.telemetry_retention_days}
                        disabled={!canManagePolicy}
                        onChange={e => setPolicy(p => ({ ...p, telemetry_retention_days: Number(e.target.value) }))}
                        onBlur={e => updatePolicyField('telemetry_retention_days', Number(e.target.value))}
                      />
                      <span>days</span>
                    </div>
                  </label>
                  <label className="setting-field">
                    <span>Sync Interval</span>
                    <div className="number-control">
                      <input
                        type="number"
                        min="10"
                        max="3600"
                        value={policy.sync_interval_seconds}
                        disabled={!canManagePolicy}
                        onChange={e => {
                          const next = Number(e.target.value);
                          setPolicy(p => ({ ...p, sync_interval_seconds: next }));
                        }}
                        onBlur={e => updatePolicyField('sync_interval_seconds', Number(e.target.value))}
                      />
                      <span>seconds</span>
                    </div>
                  </label>
                </div>
              </section>

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Data Caching</h2>
                    <p>Reduces repeated uploads by sending only data that changed or has not been sent before.</p>
                  </div>
                </div>
                <div className="toggle-list">
                  <label className="toggle-row">
                    <span>
                      <strong>Delta upload</strong>
                      <small>Upload changed collector payloads instead of full repeated snapshots.</small>
                    </span>
                    <input
                      type="checkbox"
                      checked={policy.delta_upload_enabled}
                      disabled={!canManagePolicy}
                      onChange={e => updatePolicyField('delta_upload_enabled', e.target.checked)}
                    />
                  </label>
                  <label className="toggle-row">
                    <span>
                      <strong>Cache unchanged collector data</strong>
                      <small>Keep local fingerprints so unchanged apps, services, ports and browser entries are skipped.</small>
                    </span>
                    <input
                      type="checkbox"
                      checked={policy.cache_unchanged_collector_data}
                      disabled={!canManagePolicy}
                      onChange={e => updatePolicyField('cache_unchanged_collector_data', e.target.checked)}
                    />
                  </label>
                </div>
              </section>

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Browser History</h2>
                    <p>Choose how much URL detail is retained for browser history reporting.</p>
                  </div>
                </div>
                <div className="segmented-control" role="group" aria-label="Browser history mode">
                  {([
                    ['domain_only', 'Domain only'],
                    ['full_url', 'Full URL'],
                  ] as const).map(([value, label]) => (
                    <button
                      key={value}
                      type="button"
                      className={policy.browser_history_mode === value ? 'active' : ''}
                      disabled={!canManagePolicy}
                      onClick={() => updatePolicyField('browser_history_mode', value)}
                    >
                      {label}
                    </button>
                  ))}
                </div>
                <label className="setting-field compact-field">
                  <span>Browser Entry Limit</span>
                  <div className="number-control">
                    <input
                      type="number"
                      min="0"
                      max="1000"
                      value={policy.browser_history_limit}
                      disabled={!canManagePolicy}
                      onChange={e => setPolicy(p => ({ ...p, browser_history_limit: Number(e.target.value) }))}
                      onBlur={e => updatePolicyField('browser_history_limit', Number(e.target.value))}
                    />
                    <span>entries</span>
                  </div>
                </label>
              </section>

              {canManageReleases && (
                <section className="settings-panel">
                  <div className="settings-panel-header">
                    <div>
                      <h2>Agent Rollouts</h2>
                      <p>Build, upload, publish, and roll back agent binaries for supported targets.</p>
                    </div>
                    <div className="release-header-actions">
                      <button
                        type="button"
                        className="action-btn primary"
                        onClick={() => void rolloutLatestAgents()}
                        disabled={rolloutSaving || allAgentReleases.length === 0}
                      >
                        {rolloutSaving ? 'Starting...' : 'Update Agents'}
                      </button>
                      <button type="button" className="action-btn" onClick={() => { void fetchAgentReleases(); void fetchAgentBuildJobs(); }}>
                        Refresh
                      </button>
                    </div>
                  </div>

                  <div className="release-grid">
                    {releasesByOS.map(target => (
                      <div key={target.os} className="release-card">
                        <div className="release-card-head">
                          <strong>{target.label}</strong>
                          <span>{target.releases.length ? `${target.releases.length} build${target.releases.length !== 1 ? 's' : ''}` : 'No release'}</span>
                        </div>
                        {target.releases.length ? (
                          <div className="release-builds">
                            {target.releases.map(rel => (
                              <div key={`${rel.os}-${rel.arch}`} className="release-build">
                                <span className="pill pill-blue mono">{rel.arch}</span>
                                <strong>{rel.version}</strong>
                                <small>{rel.published_at ? new Date(rel.published_at).toLocaleString() : 'Published'}</small>
                                <select
                                  value={rel.version}
                                  onChange={e => {
                                    const selected = releaseOptionsFor(rel.os, rel.arch).find(item => item.version === e.target.value);
                                    if (selected) void activateAgentRelease(selected);
                                  }}
                                  aria-label={`Activate ${target.label} ${rel.arch} release`}
                                >
                                  {releaseOptionsFor(rel.os, rel.arch).map(option => (
                                    <option key={`${option.os}-${option.arch}-${option.version}`} value={option.version}>
                                      {option.version}
                                    </option>
                                  ))}
                                </select>
                              </div>
                            ))}
                          </div>
                        ) : (
                          <div className="release-empty">No binary published yet.</div>
                        )}
                      </div>
                    ))}
                  </div>

                  <form className="release-form build-form" onSubmit={startAgentBuild}>
                    <label className="setting-field">
                      <span>Version</span>
                      <input
                        value={buildForm.version}
                        onChange={e => setBuildForm(form => ({ ...form, version: e.target.value }))}
                        placeholder="1.0.1"
                        required
                      />
                    </label>
                    <label className="setting-field">
                      <span>API URL</span>
                      <input
                        value={buildForm.api_url}
                        onChange={e => setBuildForm(form => ({ ...form, api_url: e.target.value }))}
                        placeholder={API || 'Auto from hosted API'}
                        type="url"
                      />
                    </label>
                    <label className="setting-field release-choice-field">
                      <span>Platforms</span>
                      <div className="choice-row">
                        {RELEASE_TARGETS.map(target => (
                          <label key={target.os}>
                            <input
                              type="checkbox"
                              checked={buildForm.platforms.includes(target.os)}
                              onChange={e => toggleBuildPlatform(target.os, e.target.checked)}
                            />
                            {target.label}
                          </label>
                        ))}
                      </div>
                    </label>
                    <label className="setting-field release-choice-field">
                      <span>Architectures</span>
                      <div className="choice-row">
                        {['amd64', 'arm64'].map(arch => (
                          <label key={arch}>
                            <input
                              type="checkbox"
                              checked={buildForm.archs.includes(arch)}
                              onChange={e => toggleBuildArch(arch, e.target.checked)}
                            />
                            {arch}
                          </label>
                        ))}
                      </div>
                    </label>
                    <button type="submit" className="action-btn release-submit">Build New Version</button>
                  </form>
                  {releaseStatus && <div className="form-status" role="status">{releaseStatus}</div>}

                  {agentBuildJobs.length > 0 && (
                    <div className="build-history">
                      <div className="build-history-head">
                        <div className="sub-section-title">Build History</div>
                        {agentBuildJobs.length > 2 && (
                          <button
                            type="button"
                            className="action-btn compact"
                            onClick={() => setShowAllBuildHistory(value => !value)}
                          >
                            {showAllBuildHistory ? 'Hide Older' : `Show ${hiddenBuildJobCount} Older`}
                          </button>
                        )}
                      </div>
                      {visibleBuildJobs.map(job => (
                        <div key={job.id} className={`build-job build-${job.status}`}>
                          <div className="build-job-head">
                            <strong>{job.version}</strong>
                            <span className="pill pill-neutral mono">{job.status}</span>
                            <small>{new Date(job.created_at).toLocaleString()}</small>
                          </div>
                          <div className="build-job-meta">
                            <span>{job.platforms.join(', ')}</span>
                            <span>{job.archs.join(', ')}</span>
                            <span>{job.api_url}</span>
                          </div>
                          {job.error && <div className="build-error">{job.error}</div>}
                          {job.artifacts?.length > 0 && (
                            <div className="artifact-list">
                              {job.artifacts.map(artifact => (
                                <a
                                  key={artifact.s3_key || artifact.file_name}
                                  href={artifact.download_url}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  className="artifact-link"
                                >
                                  <span className="pill pill-blue mono">{artifact.kind || 'binary'}</span>
                                  <span>{artifact.file_name}</span>
                                </a>
                              ))}
                            </div>
                          )}
                          {job.logs?.length > 0 && <pre className="build-log">{job.logs.slice(-2).join('\n\n')}</pre>}
                        </div>
                      ))}
                    </div>
                  )}
                </section>
              )}

              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Collectors</h2>
                    <p>Enable only the telemetry categories required by your environment.</p>
                  </div>
                </div>
                <div className="collector-grid">
                  {collectorControls.map(item => (
                    <label key={item.key} className="collector-toggle">
                      <span>
                        <strong>{item.label}</strong>
                        <small>{item.meta}</small>
                      </span>
                      <input
                        type="checkbox"
                        checked={Boolean(policy[item.key])}
                        disabled={!canManagePolicy}
                        onChange={e => updatePolicyField(item.key, e.target.checked)}
                      />
                    </label>
                  ))}
                </div>
              </section>
            </div>
          ) : currentPage === 'reports' ? (
            <div className="reports-layout">
              {weeklyReportError && <div className="auth-error" role="alert">{weeklyReportError}</div>}
              {weeklyReportLoading && !weeklyReport ? (
                <div className="loading" role="status">Building weekly report...</div>
              ) : weeklyReport ? (
                <>
                  <section className="report-strip">
                    <div>
                      <span className="kicker">Window</span>
                      <strong>{weeklyReport.from} to {weeklyReport.to}</strong>
                    </div>
                    <div>
                      <span className="kicker">Coverage</span>
                      <strong>{weeklyCoverageLabel}</strong>
                    </div>
                    <div>
                      <span className="kicker">Telemetry</span>
                      <strong>{weeklyReport.coverage.telemetry_events.toLocaleString()} events</strong>
                    </div>
                    <div>
                      <span className="kicker">Browser</span>
                      <strong>{weeklyReport.coverage.browser_entries.toLocaleString()} entries</strong>
                    </div>
                  </section>

                  <section className="dashboard-tiles report-tiles" aria-label="Weekly report summary">
                    <div className="dashboard-tile">
                      <span>Devices</span>
                      <strong>{weeklyReport.coverage.device_count.toLocaleString()}</strong>
                    </div>
                    <div className="dashboard-tile tile-online">
                      <span>Online</span>
                      <strong>{weeklyReport.fleet.online.toLocaleString()}</strong>
                    </div>
                    <div className="dashboard-tile tile-offline">
                      <span>Offline</span>
                      <strong>{weeklyReport.fleet.offline.toLocaleString()}</strong>
                    </div>
                    <div className="dashboard-tile tile-uptime">
                      <span>App Usage</span>
                      <strong>{formatDuration(weeklyReport.app_usage.total_seconds)}</strong>
                    </div>
                  </section>

                  <section className="settings-panel on-demand-panel">
                    <div className="settings-panel-header">
                      <div>
                        <h2>On-Demand Reports</h2>
                        <p>Generate a focused report from the selected date window.</p>
                      </div>
                      {onDemandReportName && <span className="save-state" role="status">Generated</span>}
                    </div>
                    <div className="on-demand-controls">
                      <div className="segmented-control compact" role="group" aria-label="On-demand report type">
                        {([
                          ['executive', 'Executive'],
                          ['app_usage', 'App Usage'],
                          ['device_health', 'Device Health'],
                        ] as const).map(([value, label]) => (
                          <button
                            key={value}
                            type="button"
                            className={onDemandReportType === value ? 'active' : ''}
                            onClick={() => {
                              setOnDemandReportType(value);
                              setOnDemandReportText('');
                              setOnDemandReportName('');
                            }}
                          >
                            {label}
                          </button>
                        ))}
                      </div>
                      <div className="on-demand-actions">
                        <button type="button" className="action-btn primary" onClick={() => void generateOnDemandReport()} disabled={weeklyReportLoading}>
                          Generate Report
                        </button>
                        <button type="button" className="action-btn" onClick={downloadOnDemandReport} disabled={!onDemandReportText}>
                          Download
                        </button>
                      </div>
                    </div>
                    {onDemandReportText && (
                      <pre className="on-demand-preview">{onDemandReportText}</pre>
                    )}
                  </section>

                  {onDemandReportType === 'executive' && (
                    <>
                      <div className="reports-grid">
                        <section className="settings-panel">
                          <div className="settings-panel-header">
                            <div>
                              <h2>Executive Summary</h2>
                              <p>High-level fleet activity, coverage and device posture.</p>
                            </div>
                          </div>
                          <div className="report-list">
                            {weeklyDevices.map(device => (
                              <div key={device.device_id} className="report-list-row">
                                <div>
                                  <strong>{device.name}</strong>
                                  <span>{device.state} · {device.browser_entries.toLocaleString()} browser entries</span>
                                </div>
                                <span className="mono">{formatDuration(device.app_usage_seconds)}</span>
                              </div>
                            ))}
                          </div>
                        </section>

                        <section className="settings-panel">
                          <div className="settings-panel-header">
                            <div>
                              <h2>Fleet Snapshot</h2>
                              <p>Current endpoint posture paired with weekly activity volume.</p>
                            </div>
                          </div>
                          <div className="report-metrics">
                            <div><span>Average CPU</span><strong>{weeklyReport.fleet.avg_cpu.toFixed(1)}%</strong></div>
                            <div><span>Average Memory</span><strong>{weeklyReport.fleet.avg_ram.toFixed(1)}%</strong></div>
                            <div><span>Warning</span><strong>{weeklyReport.fleet.warning}</strong></div>
                            <div><span>Critical</span><strong>{weeklyReport.fleet.critical}</strong></div>
                          </div>
                        </section>
                      </div>

                      <section className="settings-panel">
                        <div className="settings-panel-header">
                          <div>
                            <h2>Highest Usage Devices</h2>
                            <p>Devices sorted by total app usage in this report window.</p>
                          </div>
                        </div>
                        <div className="table-wrap">
                          <table className="data-table report-table">
                            <thead>
                              <tr>
                                <th>Device</th>
                                <th>Status</th>
                                <th>Usage Days</th>
                                <th>App Usage</th>
                                <th>Browser Entries</th>
                              </tr>
                            </thead>
                            <tbody>
                              {weeklyDevices.map(device => (
                                <tr key={device.device_id} className={`row-${device.state}`}>
                                  <td>
                                    <div className="device-cell-text">
                                      <strong>{device.name}</strong>
                                      <span>{device.hostname || device.device_id}</span>
                                    </div>
                                  </td>
                                  <td>
                                    <span className={`status-badge badge-${device.state}`}>
                                      <span className="dot" />
                                      {device.state === 'online' ? 'Online' : device.state}
                                    </span>
                                  </td>
                                  <td>{device.app_usage_days}/{weeklyReport.requested_days}</td>
                                  <td>{formatDuration(device.app_usage_seconds)}</td>
                                  <td>{device.browser_entries.toLocaleString()}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      </section>
                    </>
                  )}

                  {onDemandReportType === 'app_usage' && (
                    <>
                      <section className="settings-panel">
                        <div className="settings-panel-header">
                          <div>
                            <h2>Apps Used</h2>
                            <p>Aggregated from indexed daily app-usage summaries in the selected window.</p>
                          </div>
                        </div>
                        <div className="report-list">
                          {weeklyReport.app_usage.top_apps.length ? weeklyReport.app_usage.top_apps.map(app => (
                            <div key={app.app_name} className="report-list-row">
                              <div>
                                <strong>{app.app_name}</strong>
                                <span>{app.device_count} device{app.device_count === 1 ? '' : 's'} · {app.session_count.toLocaleString()} sessions</span>
                              </div>
                              <span className="mono">{formatDuration(app.total_seconds)}</span>
                            </div>
                          )) : (
                            <div className="no-data">No app usage available for this window.</div>
                          )}
                        </div>
                      </section>

                      <section className="settings-panel">
                        <div className="settings-panel-header">
                          <div>
                            <h2>Device Usage</h2>
                            <p>Per-device app usage coverage and session volume.</p>
                          </div>
                        </div>
                        <div className="table-wrap">
                          <table className="data-table report-table">
                            <thead>
                              <tr>
                                <th>Device</th>
                                <th>Usage Days</th>
                                <th>App Usage</th>
                                <th>Sessions</th>
                                <th>Browser Entries</th>
                              </tr>
                            </thead>
                            <tbody>
                              {weeklyDevices.map(device => (
                                <tr key={device.device_id} className={`row-${device.state}`}>
                                  <td>
                                    <div className="device-cell-text">
                                      <strong>{device.name}</strong>
                                      <span>{device.hostname || device.device_id}</span>
                                    </div>
                                  </td>
                                  <td>{device.app_usage_days}/{weeklyReport.requested_days}</td>
                                  <td>{formatDuration(device.app_usage_seconds)}</td>
                                  <td>{device.app_usage_sessions.toLocaleString()}</td>
                                  <td>{device.browser_entries.toLocaleString()}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      </section>
                    </>
                  )}

                  {onDemandReportType === 'device_health' && (
                    <>
                      <section className="settings-panel">
                        <div className="settings-panel-header">
                          <div>
                            <h2>Device Health</h2>
                            <p>Current hardware posture and update exposure for reporting devices.</p>
                          </div>
                        </div>
                        <div className="report-metrics health-metrics">
                          <div><span>Average CPU</span><strong>{weeklyReport.fleet.avg_cpu.toFixed(1)}%</strong></div>
                          <div><span>Average Memory</span><strong>{weeklyReport.fleet.avg_ram.toFixed(1)}%</strong></div>
                          <div><span>Warning Devices</span><strong>{weeklyReport.fleet.warning}</strong></div>
                          <div><span>Critical Devices</span><strong>{weeklyReport.fleet.critical}</strong></div>
                        </div>
                      </section>

                      <section className="settings-panel">
                        <div className="settings-panel-header">
                          <div>
                            <h2>Health Breakdown</h2>
                            <p>Sorted by usage, with current CPU, memory, disk and pending updates.</p>
                          </div>
                        </div>
                        <div className="table-wrap">
                          <table className="data-table report-table">
                            <thead>
                              <tr>
                                <th>Device</th>
                                <th>Status</th>
                                <th>CPU</th>
                                <th>Memory</th>
                                <th>Disk</th>
                                <th>Updates</th>
                                <th>Telemetry</th>
                              </tr>
                            </thead>
                            <tbody>
                              {weeklyDevices.map(device => (
                                <tr key={device.device_id} className={`row-${device.state}`}>
                                  <td>
                                    <div className="device-cell-text">
                                      <strong>{device.name}</strong>
                                      <span>{device.hostname || device.device_id}</span>
                                    </div>
                                  </td>
                                  <td>
                                    <span className={`status-badge badge-${device.state}`}>
                                      <span className="dot" />
                                      {device.state === 'online' ? 'Online' : device.state}
                                    </span>
                                  </td>
                                  <td>{(device.cpu_percent ?? 0).toFixed(1)}%</td>
                                  <td>{(device.ram_percent ?? 0).toFixed(1)}%</td>
                                  <td>{(device.disk_percent ?? 0).toFixed(1)}%</td>
                                  <td>{device.pending_updates.toLocaleString()}</td>
                                  <td>{device.telemetry_events.toLocaleString()}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      </section>
                    </>
                  )}
                </>
              ) : (
                <div className="empty-state">
                  <span className="empty-icon">📄</span>
                  <div className="empty-title">No report loaded</div>
                  <div className="empty-sub">Choose a date window and refresh.</div>
                </div>
              )}
            </div>
          ) : loading ? (
            <div className="loading" role="status" aria-label="Loading">
              Connecting to telemetry stream…
            </div>
          ) : currentPage === 'dashboard' ? null : currentPage === 'inspect' ? (
            selectedDevice ? (
              <div className="inspect-device">
                <DeviceCard
                  device={selectedDevice}
                  tab={getTab(selectedDevice.device_id)}
                  onTabChange={tab => setTab(selectedDevice.device_id, tab)}
                  onDelete={canDeleteDevices ? () => openDeleteConfirm(selectedDevice.device_id, deviceDisplayName(selectedDevice)) : undefined}
                  onPing={canPingAgents ? () => pingAgent(selectedDevice.device_id) : undefined}
                  pingStatus={agentPingStatus[selectedDevice.device_id]}
                  cachedFocus={focusCache[selectedDevice.device_id] ?? []}
                  dailyAppUsage={selectedDailyAppUsage}
                  dailyPresence={selectedDailyPresence}
                  dailyAppUsageLoading={Boolean(dailyAppUsageLoading[selectedDevice.device_id])}
                  appUsageDate={appUsageDate}
                  onAppUsageDateChange={updateAppUsageDate}
                  browserHistory={selectedBrowserHistory}
                  browserHistoryRange={browserHistoryRange}
                  browserHistoryLoading={Boolean(browserHistoryLoading[selectedDevice.device_id])}
                  browserHistoryLoaded={selectedBrowserHistoryLoaded}
                  canFilterBrowserHistory={canFilterBrowserHistory}
                  onBrowserHistoryRangeChange={updateBrowserHistoryRange}
                  userRole={authUser?.role ?? 'viewer'}
                />
              </div>
            ) : (
              <div className="empty-state">
                <span className="empty-icon">🔍</span>
                <div className="empty-title">Device not found</div>
                <div className="empty-sub">Return to inventory and choose a device.</div>
              </div>
            )
          ) : devices.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon">📡</span>
              <div className="empty-title">No devices connected</div>
              <div className="empty-sub">Start the DevicePulse agent on a machine to begin.</div>
            </div>
          ) : filteredDevices.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon">🔍</span>
              <div className="empty-title">No matching devices</div>
              <div className="empty-sub">Adjust your search or status filter.</div>
            </div>
          ) : currentPage === 'inventory' ? (
            /* ── Inventory table ── */
            <div>
              <div className="table-wrap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Device</th>
                      <th>Status</th>
                      <th>Agent</th>
                      <th>Platform</th>
                      <th>Connection</th>
                      <th>Last Seen</th>
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredDevices.map(device => {
                      const sys    = device.data?.SystemInfo;
                      const state  = getDeviceState(device);
                      const online = isOnline(device.last_seen);
                      const name   = deviceDisplayName(device);
                      const risk   = deviceRisk(device);
                      const hw     = device.data?.HardwareStats;

                      const stateLabel = state === 'critical' ? 'Critical'
                        : state === 'warning' ? 'Warning'
                        : online ? 'Online' : 'Offline';

                      const platform = sys?.platform
                        ? `${sys.platform}${sys.architecture ? `, ${sys.architecture}` : ''}`
                        : '—';

                      return (
                        <tr key={device.device_id} className={`row-${state}`}>
                          <td>
                            <div className="device-cell">
                              <div className="device-cell-text">
                                <strong>{name}</strong>
                                <span>{device.device_id}</span>
                              </div>
                            </div>
                          </td>
                          <td>
                            <span className={`status-badge badge-${state}`}>
                              <span className="dot" />
                              {stateLabel}
                            </span>
                          </td>
                          <td>
                            <div className="cell-stack">
                              <strong>
                                {device.agent_version
                                  ? `Agent ${device.agent_version}`
                                  : online ? 'DevicePulse Agent' : 'Unknown agent'}
                              </strong>
                              <span>
                                {device.agent_update_status === 'update_requested'
                                  ? `Update requested${device.agent_target_version ? ` -> ${device.agent_target_version}` : ''}`
                                  : device.agent_update_status === 'update_available'
                                    ? `Update available${device.agent_target_version ? ` -> ${device.agent_target_version}` : ''}`
                                    : device.agent_update_status === 'checking'
                                      ? 'Checking for update'
                                      : [device.agent_os, device.agent_arch].filter(Boolean).join(', ') ||
                                        device.agent_update_status ||
                                        (online ? 'Waiting for version' : 'No check-in')}
                              </span>
                            </div>
                          </td>
                          <td>
                            <div className="cell-stack">
                              <strong>{platform}</strong>
                              <span>{sys?.platform_version ?? '—'}</span>
                            </div>
                          </td>
                          <td>
                            <div className="cell-stack">
                              <strong style={{ color: online ? 'var(--text-1)' : 'var(--red)' }}>
                                {online ? `${risk.toFixed(0)}% peak load` : 'Connection lost'}
                              </strong>
                              <span>{hw?.uptime_human || sys?.os || '—'}</span>
                            </div>
                          </td>
                          <td>
                            <span className="mono" style={{ fontSize: '0.8125rem', color: 'var(--text-2)' }}>
                              {timeAgo(device.last_seen)}
                            </span>
                          </td>
                          <td>
                            <div className="row-actions">
                              <button
                                type="button"
                                className="action-btn"
                                onClick={() => viewDeviceDetails(device.device_id)}
                              >
                                Inspect
                              </button>
                              {canDeleteDevices && (
                                <button
                                  type="button"
                                  className="action-btn"
                                  onClick={() => openRenameDevice(device)}
                                >
                                  Rename
                                </button>
                              )}
                              {canDeleteDevices && (
                                <button
                                  type="button"
                                  className="action-btn danger"
                                  onClick={() => openDeleteConfirm(device.device_id, name)}
                                  aria-label={`Remove ${name}`}
                                >
                                  ×
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                <div className="table-footer">
                  <span>
                    Showing {filteredDevices.length ? '1' : '0'}–{filteredDevices.length} of {devices.length.toLocaleString()} devices
                  </span>
                </div>
              </div>
            </div>
          ) : null}

        </div>
      </main>

      {renameTarget && (
        <div className="modal-backdrop" role="presentation" onMouseDown={closeRenameDevice}>
          <form
            className="modal-panel rename-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="rename-device-title"
            onSubmit={saveRenameDevice}
            onMouseDown={e => e.stopPropagation()}
          >
            <div className="modal-header">
              <div>
                <h2 id="rename-device-title">Rename Device</h2>
                <p>{renameTarget.device_id}</p>
              </div>
              <button type="button" className="modal-close" onClick={closeRenameDevice} aria-label="Close">
                ×
              </button>
            </div>
            <label className="auth-field">
              <span>Display Name</span>
              <input
                value={renameValue}
                onChange={e => setRenameValue(e.target.value)}
                placeholder={renameTarget.data?.SystemInfo?.hostname || renameTarget.hostname || 'Use hostname'}
                maxLength={80}
                autoFocus
              />
            </label>
            {renameError && <div className="auth-error" role="alert">{renameError}</div>}
            <div className="modal-actions">
              <button type="button" className="action-btn" onClick={closeRenameDevice} disabled={renameSaving}>
                Cancel
              </button>
              <button type="submit" className="action-btn primary" disabled={renameSaving}>
                {renameSaving ? 'Saving...' : 'Save Name'}
              </button>
            </div>
          </form>
        </div>
      )}

      <ConfirmDialog
        open={!!confirmTarget}
        title="Remove device"
        message={`Remove ${confirmTarget?.name || confirmTarget?.deviceId || 'this device'} (${confirmTarget?.deviceId ?? ''})?\n\nIts stored telemetry is deleted and future agent uploads are revoked.`}
        confirmLabel="Remove device"
        cancelLabel="Cancel"
        danger
        onConfirm={() => {
          const id = confirmTarget?.deviceId;
          setConfirmTarget(null);
          if (id) void performDeleteDevice(id);
        }}
        onCancel={() => setConfirmTarget(null)}
      />

    </div>
  );
}
