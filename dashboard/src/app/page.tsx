'use client';

import { useEffect, useState, useCallback } from 'react';
import type { FormEvent } from 'react';
import { Device, AppFocusSummary, FocusCacheData, DeviceTab, EnterprisePolicy, DashboardUser, UserRole, HistoryEntry, BrowserHistoryArchiveData, DailyAppUsageData, AgentRelease, AgentBuildJob, AgentRolloutResponse } from '@/types';
import { API, readHeaders, adminHeaders, isOnline, timeAgo, primaryDisk } from '@/lib/utils';
import DeviceCard from '@/components/DeviceCard';
import type { BrowserHistoryRange } from '@/components/tabs/BrowserTab';

type PageView = 'dashboard' | 'inventory' | 'inspect' | 'settings' | 'access';
type StatusFilter = 'all' | 'online' | 'critical' | 'warning' | 'offline';
type AuthMode = 'login' | 'register';
type BrowserHistoryCache = Record<string, Partial<Record<BrowserHistoryRange, HistoryEntry[]>>>;
type DailyAppUsageCache = Record<string, Record<string, DailyAppUsageData>>;
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

const PAGE_VIEWS: PageView[] = ['dashboard', 'inventory', 'inspect', 'settings', 'access'];
const DEVICE_TABS: DeviceTab[] = ['overview', 'hardware', 'processes', 'browser', 'services', 'ports', 'apps', 'security', 'focus', 'sysinfo'];

const roleLabel: Record<UserRole, string> = {
  admin: 'Admin',
  manager: 'Manager',
  viewer: 'Viewer',
};

function deviceDisplayName(device?: Device | null): string {
  if (!device) return '';
  return device.display_name || device.data?.SystemInfo?.hostname || device.hostname || device.device_id;
}

function formatHeaderTime(date: Date): string {
  return date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function localDateKey(daysAgo: number): string {
  const date = new Date();
  date.setDate(date.getDate() - daysAgo);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function browserHistoryQuery(range: BrowserHistoryRange): string {
  const params = new URLSearchParams({ limit: '500' });
  if (range === 'recent') {
    // Span today + yesterday so admins always see entries regardless of
    // whether today's S3 archive has been written yet (early in the day).
    params.set('from', localDateKey(1)); // yesterday
    params.set('to', localDateKey(0));   // today
  } else {
    const offset = range === 'last_day' ? 1 : 2;
    const date = localDateKey(offset);
    params.set('from', date);
    params.set('to', date);
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
const IconSettings = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <circle cx="8" cy="8" r="2.25"/>
    <path d="M13 8a5.3 5.3 0 00-.07-.86l1.42-1.08-1.35-2.33-1.66.68a5.1 5.1 0 00-1.48-.86L9.62 1.8H6.38l-.24 1.75a5.1 5.1 0 00-1.48.86L3 3.73 1.65 6.06l1.42 1.08A5.3 5.3 0 003 8c0 .3.02.58.07.86L1.65 9.94 3 12.27l1.66-.68c.44.37.94.66 1.48.86l.24 1.75h3.24l.24-1.75c.54-.2 1.04-.49 1.48-.86l1.66.68 1.35-2.33-1.42-1.08c.05-.28.07-.57.07-.86z"/>
  </svg>
);

export default function Home() {
  const [authUser, setAuthUser]       = useState<DashboardUser | null>(null);
  const [authLoading, setAuthLoading] = useState(true);
  const [authMode, setAuthMode]       = useState<AuthMode>('login');
  const [authError, setAuthError]     = useState('');
  const [authForm, setAuthForm]       = useState({ name: '', email: '', password: '' });
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
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [currentPage, setCurrentPage] = useState<PageView>('dashboard');
  const [selectedDeviceId, setSelectedDeviceId] = useState<string>('');
  const [currentTime, setCurrentTime] = useState(() => new Date());
  const [renameTarget, setRenameTarget] = useState<Device | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameError, setRenameError] = useState('');
  const [renameSaving, setRenameSaving] = useState(false);
  const [agentPingStatus, setAgentPingStatus] = useState<AgentPingStatus>({});

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
    void loadCurrentUser();
  }, [loadCurrentUser]);

  useEffect(() => {
    const id = window.setInterval(() => setCurrentTime(new Date()), 1000);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    const id = window.setTimeout(applyNavigationState, 0);
    window.addEventListener('popstate', applyNavigationState);
    return () => {
      window.clearTimeout(id);
      window.removeEventListener('popstate', applyNavigationState);
    };
  }, [applyNavigationState]);

  const fetchBrowserHistory = useCallback(async (
    deviceId: string,
    range = browserHistoryRange,
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
  }, [apiFetch, browserHistoryRange]);

  const fetchDailyAppUsage = useCallback(async (
    deviceId: string,
    date = appUsageDate,
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
  }, [apiFetch, appUsageDate]);

  const fetchAll = useCallback(async () => {
    try {
      const devRes = await apiFetch('/devices', { headers: readHeaders() });
      if (devRes.ok) {
        const data: Device[] = await devRes.json();
        const devList = data ?? [];
        setDevices(devList);
        const focusResults = await Promise.allSettled(
          devList.map(d =>
            apiFetch(`/focus/${d.device_id}`, { headers: readHeaders() }).then(r =>
              r.ok ? (r.json() as Promise<FocusCacheData>) : null
            )
          )
        );
        const cache: Record<string, AppFocusSummary[]> = {};
        focusResults.forEach((result, i) => {
          if (result.status === 'fulfilled' && result.value)
            cache[devList[i].device_id] = result.value.app_summaries ?? [];
        });
        setFocusCache(cache);
        await Promise.allSettled(
          devList
            .filter(d => activeTab[d.device_id] === 'browser')
            .map(d => fetchBrowserHistory(d.device_id, browserHistoryRange, { showLoader: false }))
        );
        await Promise.allSettled(
          devList
            .filter(d => activeTab[d.device_id] === 'focus')
            .map(d => fetchDailyAppUsage(d.device_id, appUsageDate, { showLoader: false }))
        );
      }
    } catch (e) {
      console.error('Fetch error:', e);
    } finally {
      setLoading(false);
    }
  }, [activeTab, apiFetch, appUsageDate, browserHistoryRange, fetchBrowserHistory, fetchDailyAppUsage]);

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
    if (currentPage === 'access') void fetchUsers();
  }, [currentPage, fetchUsers]);

  useEffect(() => {
    if (currentPage !== 'settings') return;
    void fetchAgentReleases();
    void fetchAgentBuildJobs();
  }, [currentPage, fetchAgentBuildJobs, fetchAgentReleases]);

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

  // ── Actions ───────────────────────────────────────────────────────────────

  const submitAuth = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setAuthError('');
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
      setLoading(true);
    } catch {
      setAuthError('Could not sign in. Check the API connection and try again.');
    }
  };

  const logout = async () => {
    try {
      await apiFetch('/auth/logout', { method: 'POST' });
    } finally {
      setAuthUser(null);
      setDevices([]);
      setFocusCache({});
      goToPage('dashboard', { replace: true });
      setAuthMode('login');
      void loadCurrentUser();
    }
  };

  const createUser = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
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

  const deleteDevice = async (deviceId: string) => {
    if (!canDeleteDevices) return;
    if (!confirm(`Remove device ${deviceId}, delete its stored data, and revoke future agent uploads?`)) return;
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
    } catch {}
  };

  const openRenameDevice = (device: Device) => {
    if (!canDeleteDevices) return;
    setRenameTarget(device);
    setRenameValue(device.display_name || '');
    setRenameError('');
  };

  const closeRenameDevice = () => {
    if (renameSaving) return;
    setRenameTarget(null);
    setRenameValue('');
    setRenameError('');
  };

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
    if (tab === 'focus') void fetchDailyAppUsage(id, appUsageDate);
  };

  const updateBrowserHistoryRange = (range: BrowserHistoryRange) => {
    setBrowserHistoryRange(range);
    if (selectedDeviceId && getTab(selectedDeviceId) === 'browser') {
      void fetchBrowserHistory(selectedDeviceId, range);
    }
  };

  const updateAppUsageDate = (date: string) => {
    setAppUsageDate(date);
    if (selectedDeviceId && getTab(selectedDeviceId) === 'focus') {
      void fetchDailyAppUsage(selectedDeviceId, date);
    }
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
      <div className="auth-shell">
        <div className="auth-panel">
          <div className="header-logo"><IconLogo /></div>
          <h1>DevicePulse</h1>
          <p>Checking dashboard session…</p>
        </div>
      </div>
    );
  }

  if (!authUser) {
    const isBootstrap = authMode === 'register' && bootstrapRequired;
    return (
      <div className="auth-shell">
        <form className="auth-panel" onSubmit={submitAuth}>
          <div className="header-logo"><IconLogo /></div>
          <h1>{isBootstrap ? 'Create first admin' : 'Sign in'}</h1>
          <p>
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
              />
            </label>
          )}
          <label className="auth-field">
            <span>Email</span>
            <input
              type="email"
              value={authForm.email}
              onChange={e => setAuthForm(f => ({ ...f, email: e.target.value }))}
              autoComplete="email"
              required
            />
          </label>
          <label className="auth-field">
            <span>Password</span>
            <input
              type="password"
              value={authForm.password}
              onChange={e => setAuthForm(f => ({ ...f, password: e.target.value }))}
              autoComplete={authMode === 'register' ? 'new-password' : 'current-password'}
              minLength={8}
              required
            />
          </label>
          {authError && <div className="auth-error">{authError}</div>}
          <button type="submit" className="auth-submit">
            {authMode === 'register' ? 'Create admin account' : 'Sign in'}
          </button>
          {!bootstrapRequired && (
            <button type="button" className="auth-link" onClick={() => setAuthMode('login')}>
              Registration is admin-managed
            </button>
          )}
        </form>
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
            <span className="header-time">{formatHeaderTime(currentTime)}</span>
            {!loading && (
              <span className="live-badge">
                <span className="dot" />
                Live
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
          </div>

          {currentPage === 'dashboard' && (
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
          {currentPage === 'access' ? (
            <div className="settings-layout">
              <section className="settings-panel">
                <div className="settings-panel-header">
                  <div>
                    <h2>Create Dashboard User</h2>
                    <p>New users can sign in after an admin creates their account.</p>
                  </div>
                  {userCreateStatus && <span className="save-state">{userCreateStatus}</span>}
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
                  {passwordResetStatus && <span className="save-state">{passwordResetStatus}</span>}
                  {roleUpdateStatus && <span className="save-state">{roleUpdateStatus}</span>}
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
                  {policySavedAt && <span className="save-state">Saved {policySavedAt}</span>}
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
                  {releaseStatus && <div className="form-status">{releaseStatus}</div>}

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
                  onDelete={canDeleteDevices ? () => deleteDevice(selectedDevice.device_id) : undefined}
                  onPing={canPingAgents ? () => pingAgent(selectedDevice.device_id) : undefined}
                  pingStatus={agentPingStatus[selectedDevice.device_id]}
                  cachedFocus={focusCache[selectedDevice.device_id] ?? []}
                  dailyAppUsage={selectedDailyAppUsage}
                  dailyAppUsageLoading={Boolean(dailyAppUsageLoading[selectedDevice.device_id])}
                  appUsageDate={appUsageDate}
                  onAppUsageDateChange={updateAppUsageDate}
                  browserHistory={selectedBrowserHistory}
                  browserHistoryRange={browserHistoryRange}
                  browserHistoryLoading={Boolean(browserHistoryLoading[selectedDevice.device_id])}
                  browserHistoryLoaded={selectedBrowserHistoryLoaded}
                  canFilterBrowserHistory={canFilterBrowserHistory}
                  onBrowserHistoryRangeChange={updateBrowserHistoryRange}
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
                                  onClick={() => deleteDevice(device.device_id)}
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
                  <div className="table-footer-pages">
                    <button type="button" className="page-btn" aria-label="Previous page">‹</button>
                    <span>Page 1 / 1</span>
                    <button type="button" className="page-btn" aria-label="Next page">›</button>
                  </div>
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
            {renameError && <div className="auth-error">{renameError}</div>}
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

    </div>
  );
}
