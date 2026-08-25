// ─── Utility helpers ──────────────────────────────────────────────────────────

export const API = process.env.NEXT_PUBLIC_API_URL ?? '';

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

export function faviconUrl(url: string): string {
  return `https://www.google.com/s2/favicons?domain=${getDomain(url)}&sz=32`;
}

export function browserEmoji(browser: string): string {
  const b = (browser || '').toLowerCase();
  if (b.includes('chrome')) return '🟡';
  if (b.includes('firefox')) return '🦊';
  if (b.includes('safari')) return '🧭';
  if (b.includes('edge')) return '🌊';
  return '🌐';
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
