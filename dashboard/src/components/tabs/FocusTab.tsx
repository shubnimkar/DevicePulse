'use client';

import { AppFocusSummary, ActiveWindowData } from '@/types';
import { formatDuration } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';

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
  for (const e of liveSummaries) merged.set(e.app_name, { ...e });
  for (const e of cachedSummaries) {
    const ex = merged.get(e.app_name);
    if (!ex || e.total_focus_seconds > ex.total_focus_seconds) merged.set(e.app_name, { ...e });
  }

  const summaries = Array.from(merged.values()).sort((a, b) => b.total_focus_seconds - a.total_focus_seconds);
  const totalSeconds = summaries.reduce((s, a) => s + a.total_focus_seconds, 0);
  const currentApp = data?.current_app ?? '';

  return (
    <div>
      {currentApp && (
        <div className="focus-current">
          <span className="live-dot" />
          <span className="focus-current-label">Currently in focus:</span>
          <span className="focus-current-app">{currentApp}</span>
        </div>
      )}

      {summaries.length === 0 ? (
        <div className="no-data">No focus sessions recorded yet.</div>
      ) : (
        <div className="focus-list">
          {summaries.map((app, i) => {
            const pct = totalSeconds > 0 ? (app.total_focus_seconds / totalSeconds) * 100 : 0;
            const isActive = app.app_name === currentApp;
            return (
              <div key={i} className={`focus-item ${isActive ? 'is-active' : ''}`}>
                <div className="focus-row">
                  <span className="focus-app-name" title={app.app_name}>
                    {isActive && <span className="live-dot" />}
                    {app.app_name}
                  </span>
                  <span className="focus-duration">{formatDuration(app.total_focus_seconds)}</span>
                  <span className="focus-sessions">{app.session_count} session{app.session_count !== 1 ? 's' : ''}</span>
                </div>
                <GaugeBar
                  value={pct}
                  color={isActive ? 'var(--green)' : 'var(--blue)'}
                  height={3}
                />
                <div className="focus-pct">{pct.toFixed(1)}% of tracked time</div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
