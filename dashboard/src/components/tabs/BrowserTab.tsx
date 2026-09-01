'use client';

import { useState } from 'react';
import { HistoryEntry } from '@/types';
import { getDomain, formatVisitTime, formatFullDateTime, browserEmoji, browserClass } from '@/lib/utils';

interface Props {
  history: HistoryEntry[];
  canFilterHistory?: boolean;
  historyRange?: BrowserHistoryRange;
  historyLoading?: boolean;
  historyLoaded?: boolean;
  onHistoryRangeChange?: (range: BrowserHistoryRange) => void;
}

export type BrowserHistoryRange = 'recent' | 'last_day' | 'day_before';

const rangeLabels: Record<BrowserHistoryRange, string> = {
  recent: 'Today',
  last_day: 'Last Day',
  day_before: 'Day Before',
};

const EXPAND_STEP = 1000;

export default function BrowserTab({
  history,
  canFilterHistory = false,
  historyRange = 'recent',
  historyLoading = false,
  historyLoaded = true,
  onHistoryRangeChange,
}: Props) {
  const [expandedCount, setExpandedCount] = useState(0);

  const uniqueHistory = dedupeHistory(history);

  const toolbar = canFilterHistory ? (
    <div className="browser-history-toolbar">
      <div className="segmented-control compact" role="group" aria-label="Browser history date range">
        {(['recent', 'last_day', 'day_before'] as const).map(range => (
          <button
            key={range}
            type="button"
            className={historyRange === range ? 'active' : ''}
            onClick={() => {
              setExpandedCount(0);
              onHistoryRangeChange?.(range);
            }}
          >
            {rangeLabels[range]}
          </button>
        ))}
      </div>
      {historyLoading && historyLoaded && <span className="history-loading">Refreshing</span>}
    </div>
  ) : historyLoading && historyLoaded ? (
    <div className="browser-history-toolbar">
      <span className="history-loading">Refreshing</span>
    </div>
  ) : null;

  if (!historyLoaded) {
    return (
      <div>
        {toolbar}
        <div className="loading">Loading browser history...</div>
      </div>
    );
  }

  if (!uniqueHistory.length && !canFilterHistory) return <div className="no-data">No browser history entries.</div>;

  const topRecent = uniqueHistory.slice(0, 200);
  const hasMore   = uniqueHistory.length > 200;

  const byBrowser = uniqueHistory.reduce((acc, h) => {
    const b = h.browser || 'Unknown';
    if (!acc[b]) acc[b] = [];
    acc[b].push(h);
    return acc;
  }, {} as Record<string, HistoryEntry[]>);
  const browsers = Object.keys(byBrowser).sort();

  // Reveal the full history in chunks so we never mount thousands of rows at once.
  const shownGroups = browsers
    .map(browser => ({ browser, items: byBrowser[browser] }))
    .reduce<Array<{ browser: string; items: HistoryEntry[] }>>((groups, group) => {
      const used = groups.reduce((n, g) => n + g.items.length, 0);
      const items = group.items.slice(0, Math.max(expandedCount - used, 0));
      return items.length > 0 ? [...groups, { browser: group.browser, items }] : groups;
    }, []);

  return (
    <div>
      {toolbar}

      {topRecent.length > 0 && (
      <div className="browser-section">
        <div className="browser-section-title">{rangeLabels[historyRange]} ({topRecent.length})</div>
        <ul className="history-list">
          {topRecent.map(h => <HistoryRow variant="recent" key={`${h.browser || 'Unknown'}\u0000${historyIdentity(h)}`} h={h} />)}
        </ul>
      </div>
      )}

      {!uniqueHistory.length && (
        <div className="no-data">No browser history entries for {rangeLabels[historyRange].toLowerCase()}.</div>
      )}

      {hasMore && (
        <button
          className="view-all-btn"
          onClick={() => setExpandedCount(c => {
            if (c === 0) return Math.min(uniqueHistory.length, EXPAND_STEP);
            if (c >= uniqueHistory.length) return 0;
            return Math.min(uniqueHistory.length, c + EXPAND_STEP);
          })}
          aria-expanded={expandedCount > 0}
        >
          {expandedCount === 0
            ? `▼ View all ${uniqueHistory.length} entries`
            : expandedCount >= uniqueHistory.length
              ? '▲ Hide full history'
              : `▼ Show more (${uniqueHistory.length - expandedCount} remaining)`}
        </button>
      )}

      {expandedCount > 0 && (
        <div>
          {shownGroups.map(({ browser, items }) => (
            <div key={browser} className="browser-group">
              <div className="browser-group-title">
                <span className={`browser-badge ${browserClass(browser)}`}>
                  {browserEmoji(browser)} {browser}
                </span>
                <span className="browser-group-count">({byBrowser[browser].length})</span>
              </div>
              <ul className="history-list">
                {items.map(h => (
                  <HistoryRow key={`${h.browser || 'Unknown'}\u0000${historyIdentity(h)}`} h={h} />
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function dedupeHistory(history: HistoryEntry[]): HistoryEntry[] {
  const seen = new Set<string>();
  const result: HistoryEntry[] = [];
  for (const item of history) {
    const key = `${(item.browser || '').toLowerCase()}\u0000${historyIdentity(item)}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(item);
  }
  return result;
}

function historyIdentity(item: HistoryEntry): string {
  const visitTime = Number.isFinite(item.last_visit_time) ? item.last_visit_time : 0;
  try {
    const parsed = new URL(item.url);
    parsed.hash = '';
    return `${parsed.toString().toLowerCase()}\u0000${visitTime}`;
  } catch {
    return `${(item.url || '').trim().toLowerCase()}\u0000${visitTime}`;
  }
}

// Local letter avatar instead of third-party favicon lookups — keeps visited
// domains inside the deployment instead of leaking them to Google.
function Favicon({ url }: { url: string }) {
  const domain = getDomain(url).replace(/^www\./, '');
  let hash = 0;
  for (let i = 0; i < domain.length; i++) hash = (hash * 31 + domain.charCodeAt(i)) >>> 0;
  return (
    <span
      className="history-favicon favicon-letter"
      style={{ background: `hsl(${hash % 360} 45% 32%)` }}
      aria-hidden="true"
    >
      {(domain[0] || '?').toUpperCase()}
    </span>
  );
}

function HistoryRow({ h, variant }: { h: HistoryEntry; variant?: 'recent' | 'full' }) {
  const domain = getDomain(h.url);
  return (
    <li>
      <a href={h.url} target="_blank" rel="noopener noreferrer" className="history-item">
        <Favicon url={h.url} />
        <div className="history-content">
          <div className="history-title" title={h.title || h.url}>{h.title || domain}</div>
          <div className="history-meta">
            <span className="history-domain">{domain}</span>
            {h.last_visit_time > 0 && (
              variant === 'recent'
                ? <span className="history-time">{formatVisitTime(h.last_visit_time)}</span>
                : <span className="history-time-full">{formatFullDateTime(h.last_visit_time)}</span>
            )}
            <span className={`browser-badge ${browserClass(h.browser)}`}>
              {browserEmoji(h.browser)} {h.browser || 'Unknown'}
            </span>
          </div>
        </div>
      </a>
    </li>
  );
}
