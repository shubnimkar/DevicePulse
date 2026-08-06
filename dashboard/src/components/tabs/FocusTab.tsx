'use client';

import { AppFocusSummary, ActiveWindowData } from '@/types';
import { formatDuration } from '@/lib/utils';

interface Props {
  data?: ActiveWindowData;
  cachedSummaries: AppFocusSummary[];
}

export default function FocusTab({ data, cachedSummaries }: Props) {
  if (!data && cachedSummaries.length === 0) {
    return <div className="no-data">No active window data yet.</div>;
  }

  const liveSummaries = data?.cumulative_summaries ?? data?.app_summaries ?? [];
  const merged = new Map<string, AppFocusSummary>();
  for (const entry of liveSummaries) {
    merged.set(entry.app_name, { ...entry });
  }
  for (const entry of cachedSummaries) {
    const existing = merged.get(entry.app_name);
    if (!existing) {
      merged.set(entry.app_name, { ...entry });
    } else if (entry.total_focus_seconds > existing.total_focus_seconds) {
      merged.set(entry.app_name, { ...entry });
    }
  }

  const summaries = Array.from(merged.values()).sort(
    (a, b) => b.total_focus_seconds - a.total_focus_seconds
  );

  const totalSeconds = summaries.reduce((s, a) => s + a.total_focus_seconds, 0);
  const currentApp = data?.current_app ?? '';

  return (
    <div>
      {currentApp && (
        <div className="aw-current">
          <span className="aw-current-dot" />
          <span className="aw-current-label">Currently in focus:</span>
          <span className="aw-current-app">{currentApp}</span>
        </div>
      )}

      {summaries.length === 0 ? (
        <div className="no-data">No focus sessions recorded yet.</div>
      ) : (
        <div className="aw-list">
          {summaries.map((app, i) => {
            const pct =
              totalSeconds > 0
                ? (app.total_focus_seconds / totalSeconds) * 100
                : 0;
            const isActive = app.app_name === currentApp;
            return (
              <div key={i} className={`aw-item ${isActive ? 'aw-item-active' : ''}`}>
                <div className="aw-row">
                  <span className="aw-app-name" title={app.app_name}>
                    {isActive && <span className="aw-live-dot" />}
                    {app.app_name}
                  </span>
                  <span className="aw-duration">
                    {formatDuration(app.total_focus_seconds)}
                  </span>
                  <span className="aw-sessions">
                    {app.session_count} session{app.session_count !== 1 ? 's' : ''}
                  </span>
                </div>
                <div className="gauge-wrap">
                  <div
                    className="gauge-bar gauge-animated"
                    style={{
                      width: `${pct}%`,
                      background: isActive
                        ? 'var(--success-color)'
                        : 'var(--accent-color)',
                    }}
                  />
                </div>
                <div className="aw-pct">{pct.toFixed(1)}% of tracked time</div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
