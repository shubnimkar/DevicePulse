'use client';

import { HistoryEntry } from '@/types';
import { getDomain, formatVisitTime, faviconUrl, browserEmoji, browserClass } from '@/lib/utils';

interface Props {
  history: HistoryEntry[];
}

export default function BrowserTab({ history }: Props) {
  if (!history.length) return <div className="no-data">No browser history.</div>;

  return (
    <ul className="history-list">
      {history.map((h, i) => {
        const domain = getDomain(h.url);
        return (
          <li key={i}>
            <a href={h.url} target="_blank" rel="noopener noreferrer" className="history-item">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                className="history-favicon"
                src={faviconUrl(h.url)}
                alt=""
                width={20}
                height={20}
                onError={e => {
                  (e.target as HTMLImageElement).style.display = 'none';
                }}
              />
              <div className="history-content">
                <div className="history-title" title={h.title || h.url}>
                  {h.title || domain}
                </div>
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
      })}
    </ul>
  );
}
