// ─── Utility helpers ──────────────────────────────────────────────────────────

import type { ActiveWindowData, AppFocusSummary, DailyAppUsageData, Device, DiskStat } from '@/types';

const rawAPI = process.env.NEXT_PUBLIC_API_URL || '/api';
export const API = rawAPI.endsWith('/') ? rawAPI.slice(0, -1) : rawAPI;

export function readHeaders(): HeadersInit {
  return {};
}

export function adminHeaders(): HeadersInit {
  return { 'Content-Type': 'application/json' };
}

export function getDomain(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return url;
  }
}

export function formatVisitTime(nanos: number): string {
  if (!nanos) return '';
  const ms = nanos / 1_000_000;
  const diff = Date.now() - ms;
  if (diff < 60_000) return 'just now';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

export function formatFullDateTime(nanos: number): string {
  if (!nanos) return '';
  const ms = nanos / 1_000_000;
  const date = new Date(ms);
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

export function isOnline(lastSeen?: string): boolean {
  if (!lastSeen) return false;
  return Date.now() - new Date(lastSeen).getTime() < 60_000;
}

export function formatBytes(bytes: number): string {
  if (bytes >= 1e9) return (bytes / 1e9).toFixed(1) + ' GB';
  if (bytes >= 1e6) return (bytes / 1e6).toFixed(1) + ' MB';
  if (bytes >= 1e3) return (bytes / 1e3).toFixed(1) + ' KB';
  return bytes + ' B';
}

export function formatDuration(secs: number): string {
  if (secs < 60) return `${secs.toFixed(0)}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s`;
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

export function browserEmoji(browser: string): string {
  const b = (browser || '').toLowerCase();
  if (b.includes('chrome')) return '🟡';
  if (b.includes('firefox')) return '🦊';
  if (b.includes('safari')) return '🧭';
  if (b.includes('edge')) return '🌊';
  return '🌐';
}

// ─── Shared app-usage filtering (used by Overview + Focus tabs) ────────────────

const ACTIVE_WINDOW_FRESH_MS = 5 * 60 * 1000;

export const IGNORED_APP_USAGE_NAMES = new Set([
  'apt-check',
  'apt.systemd.daily',
  'packagekitd',
  'snapd',
  'fwupd',
  'devicepulse-age',
  'devicepulse-agent',
]);

export function isVisibleAppUsageName(name: string): boolean {
  const key = (name || '').trim().toLowerCase().replace(/\.service$/, '');
  if (!key) return false;
  if (IGNORED_APP_USAGE_NAMES.has(key)) return false;
  return !key.startsWith('devicepulse-');
}

export function isFreshActiveWindowData(activeWindow?: ActiveWindowData): boolean {
  if (!activeWindow || activeWindow.stale || activeWindow.tracker_fresh === false) return false;
  const candidates = [
    activeWindow.collected_at,
    activeWindow.last_sample_at,
    ...(activeWindow.sessions ?? []).map(session => session.end_time),
  ];
  const newest = candidates.reduce((max, raw) => {
    if (!raw) return max;
    const ms = new Date(raw).getTime();
    return Number.isFinite(ms) && ms > max ? ms : max;
  }, 0);
  if (!newest) return false;
  const age = Date.now() - newest;
  return age >= -ACTIVE_WINDOW_FRESH_MS && age <= ACTIVE_WINDOW_FRESH_MS;
}

export function buildAppUsageSummaries(
  activeWindow?: ActiveWindowData,
  cachedSummaries: AppFocusSummary[] = [],
  dailyUsage?: DailyAppUsageData,
  maxTotalSeconds?: number,
): { summaries: AppFocusSummary[]; source: 'daily' | 'live' | 'empty' } {
  const dailyApps = dailyUsage?.users.flatMap(user => (user.top_apps ?? []).map(app => ({
    ...app,
    app_name: app.app_name,
    total_focus_seconds: app.total_focus_seconds ?? app.total_seconds ?? 0,
    session_count: app.session_count ?? 0,
  }))).filter(app => isVisibleAppUsageName(app.app_name)) ?? [];
  const liveWindowFresh = isFreshActiveWindowData(activeWindow);
  const dailyUsageLoaded = dailyUsage !== undefined;

  const sourceApps = dailyApps.length > 0
    ? dailyApps
    : dailyUsageLoaded
      ? []
    : liveWindowFresh
      ? (activeWindow?.app_summaries ?? [])
        .filter(app => isVisibleAppUsageName(app.app_name))
      : [];

  const merged = new Map<string, AppFocusSummary>();
  for (const app of sourceApps) {
    const seconds = app.total_focus_seconds ?? app.total_seconds ?? 0;
    const existing = merged.get(app.app_name);
    if (existing) {
      existing.total_focus_seconds += seconds;
      existing.session_count += app.session_count ?? 0;
    } else {
      merged.set(app.app_name, {
        ...app,
        total_focus_seconds: seconds,
        session_count: app.session_count ?? 0,
      });
    }
  }

  if (dailyApps.length === 0 && !dailyUsageLoaded && liveWindowFresh) {
    for (const app of cachedSummaries.filter(app => isVisibleAppUsageName(app.app_name))) {
      const existing = merged.get(app.app_name);
      if (!existing || app.total_focus_seconds > existing.total_focus_seconds) {
        merged.set(app.app_name, { ...app });
      }
    }
  }

  let summaries = Array.from(merged.values()).sort((a, b) => b.total_focus_seconds - a.total_focus_seconds);
  const totalSeconds = summaries.reduce((sum, app) => sum + (app.total_focus_seconds ?? 0), 0);
  if (maxTotalSeconds !== undefined && maxTotalSeconds <= 0) {
    summaries = [];
  } else if (maxTotalSeconds !== undefined && totalSeconds > maxTotalSeconds) {
    const scale = maxTotalSeconds / totalSeconds;
    summaries = summaries
      .map(app => ({
        ...app,
        total_focus_seconds: (app.total_focus_seconds ?? 0) * scale,
        session_count: app.session_count > 0 ? Math.max(1, Math.ceil(app.session_count * scale)) : 0,
      }))
      .filter(app => app.total_focus_seconds > 0);
  }

  return {
    summaries,
    source: dailyApps.length > 0 ? 'daily' : merged.size > 0 ? 'live' : 'empty',
  };
}

export function deviceDisplayName(device?: Device | null): string {
  if (!device) return '';
  return device.display_name || device.data?.SystemInfo?.hostname || device.hostname || device.device_id;
}

export function browserClass(browser: string): string {
  const b = (browser || '').toLowerCase();
  if (b.includes('chrome')) return 'browser-chrome';
  if (b.includes('firefox')) return 'browser-firefox';
  if (b.includes('safari')) return 'browser-safari';
  if (b.includes('edge')) return 'browser-edge';
  return 'browser-unknown';
}

export function metricColor(pct: number): string {
  if (pct > 85) return 'var(--red)';
  if (pct > 65) return 'var(--yellow)';
  return 'var(--green)';
}

export function isSystemDiskMount(mount?: string): boolean {
  if (!mount) return true;
  return ['/proc', '/sys', '/dev', '/run', '/snap'].some(prefix => mount === prefix || mount.startsWith(`${prefix}/`));
}

export function displayDisks(disks?: DiskStat[]): DiskStat[] {
  return (disks ?? []).filter(disk => !isSystemDiskMount(disk.mount));
}

export function primaryDisk(disks?: DiskStat[]): DiskStat | undefined {
  const visible = displayDisks(disks);
  return visible.find(disk => disk.mount === '/') ?? visible[0];
}

export function getOSIcon(os?: string): string {
  const o = (os || '').toLowerCase();
  if (o.includes('darwin') || o.includes('mac')) return '🍎';
  if (o.includes('linux')) return '🐧';
  if (o.includes('windows')) return '🪟';
  return '💻';
}

export function timeAgo(dateStr?: string): string {
  if (!dateStr) return 'Never';
  const diff = Date.now() - new Date(dateStr).getTime();
  if (diff < 10_000) return 'just now';
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`;
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return new Date(dateStr).toLocaleDateString();
}
