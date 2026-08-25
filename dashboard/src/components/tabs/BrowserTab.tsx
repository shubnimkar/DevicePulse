'use client';

import { useState } from 'react';
import { HistoryEntry } from '@/types';
import { getDomain, formatVisitTime, formatFullDateTime, faviconUrl, browserEmoji, browserClass } from '@/lib/utils';

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
  recent: 'Recent',
  last_day: 'Last Day',
  day_before: 'Day Before',
};

export default function BrowserTab({
  history,
  canFilterHistory = false,
  historyRange = 'recent',
  historyLoading = false,
  historyLoaded = true,
  onHistoryRangeChange,
}: Props) {
  const [expanded, setExpanded] = useState(false);

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
              setExpanded(false);
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

  const topRecent = uniqueHistory.slice(0, 10);
  const hasMore   = uniqueHistory.length > 10;

  const byBrowser = uniqueHistory.reduce((acc, h) => {
    const b = h.browser || 'Unknown';
    if (!acc[b]) acc[b] = [];
    acc[b].push(h);
    return acc;
  }, {} as Record<string, HistoryEntry[]>);
  const browsers = Object.keys(byBrowser).sort();

  const HistoryRow = ({ h }: { h: HistoryEntry }) => {
    const domain = getDomain(h.url);
    return (
      <li>
        <a href={h.url} target="_blank" rel="noopener noreferrer" className="history-item">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            className="history-favicon"
            src={faviconUrl(h.url)}
            alt=""
            width={16}
            height={16}
            onError={e => { (e.target as HTMLImageElement).style.display = 'none'; }}
          />
          <div className="history-content">
            <div className="history-title" title={h.title || h.url}>{h.title || domain}</div>
            <div className="history-meta">
              <span className="history-domain">{domain}</span>
              {h.last_visit_time > 0 && (
                <span className="history-time">{formatVisitTime(h.last_visit_time)}</span>
              )}
              <span className={`browser-badge ${browserClass(h.browser)}`}>
                {browserEmoji(h.browser)} {h.browser || 'Unknown'}
              </span>
            </div>
          </div>
        </a>
      </li>
    );
  };

  return (
    <div>
      {toolbar}

      {topRecent.length > 0 && (
      <div className="browser-section">
        <div className="browser-section-title">{rangeLabels[historyRange]} ({topRecent.length})</div>
        <ul className="history-list">
          {topRecent.map((h, i) => <HistoryRow key={i} h={h} />)}
        </ul>
      </div>
      )}

      {!uniqueHistory.length && (
        <div className="no-data">No browser history entries for {rangeLabels[historyRange].toLowerCase()}.</div>
      )}

      {hasMore && (
        <button className="view-all-btn" onClick={() => setExpanded(!expanded)}>
          {expanded ? '▲ Hide full history' : `▼ View all ${uniqueHistory.length} entries`}
        </button>
      )}

      {expanded && (
        <div>
          {browsers.map(browser => (
            <div key={browser} className="browser-group">
              <div className="browser-group-title">
                <span className={`browser-badge ${browserClass(browser)}`}>
                  {browserEmoji(browser)} {browser}
                </span>
                <span className="browser-group-count">({byBrowser[browser].length})</span>
              </div>
              <ul className="history-list">
                {byBrowser[browser].map((h, i) => {
                  const domain = getDomain(h.url);
                  return (
                    <li key={i}>
                      <a href={h.url} target="_blank" rel="noopener noreferrer" className="history-item">
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img
                          className="history-favicon"
                          src={faviconUrl(h.url)}
                          alt=""
                          width={16}
                          height={16}
                          onError={e => { (e.target as HTMLImageElement).style.display = 'none'; }}
                        />
                        <div className="history-content">
                          <div className="history-title" title={h.title || h.url}>{h.title || domain}</div>
                          <div className="history-meta">
                            <span className="history-domain">{domain}</span>
                            {h.last_visit_time > 0 && (
                              <span className="history-time-full">{formatFullDateTime(h.last_visit_time)}</span>
                            )}
                          </div>
                        </div>
                      </a>
                    </li>
                  );
                })}
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
  const title = (item.title || '').trim().toLowerCase();
  if (title) return title;
  try {
    const parsed = new URL(item.url);
    return `${parsed.hostname}${parsed.pathname}`.toLowerCase();
  } catch {
    return (item.url || '').trim().toLowerCase();
  }
}
