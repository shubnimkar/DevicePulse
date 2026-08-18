'use client';

import { useState } from 'react';
import { HistoryEntry } from '@/types';
import { getDomain, formatVisitTime, formatFullDateTime, faviconUrl, browserEmoji, browserClass } from '@/lib/utils';

interface Props { history: HistoryEntry[]; }

export default function BrowserTab({ history }: Props) {
  const [expanded, setExpanded] = useState(false);

  if (!history.length) return <div className="no-data">No browser history.</div>;

  const topRecent = history.slice(0, 10);
  const hasMore   = history.length > 10;

  const byBrowser = history.reduce((acc, h) => {
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
      <div className="browser-section">
        <div className="browser-section-title">Recent ({topRecent.length})</div>
        <ul className="history-list">
          {topRecent.map((h, i) => <HistoryRow key={i} h={h} />)}
        </ul>
      </div>

      {hasMore && (
        <button className="view-all-btn" onClick={() => setExpanded(!expanded)}>
          {expanded ? '▲ Hide full history' : `▼ View all ${history.length} entries`}
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
