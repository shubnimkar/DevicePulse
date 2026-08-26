'use client';

import { AppFocusSummary, ActiveWindowData, DailyAppUsageData } from '@/types';
import { formatDuration, isVisibleAppUsageName } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';

interface Props {
  data?: ActiveWindowData;
  cachedSummaries: AppFocusSummary[];
  dailyUsage?: DailyAppUsageData;
  dailyUsageLoading?: boolean;
  usageDate?: string;
  onUsageDateChange?: (date: string) => void;
}

export default function FocusTab({ data, cachedSummaries, dailyUsage, dailyUsageLoading = false, usageDate, onUsageDateChange }: Props) {
  if (!data && cachedSummaries.length === 0 && !dailyUsage) {
    return <div className="no-data">No active window data yet.</div>;
  }

  const dailyUsers = dailyUsage?.users ?? [];
  const dailyApps = dailyUsers.flatMap(user => (user.top_apps ?? []).map(app => ({
    ...app,
    app_name: app.app_name,
    total_focus_seconds: app.total_focus_seconds ?? app.total_seconds ?? 0,
  }))).filter(app => isVisibleAppUsageName(app.app_name));
  const liveSummaries = (dailyApps.length > 0 ? dailyApps : data?.cumulative_summaries ?? data?.app_summaries ?? [])
    .filter(app => isVisibleAppUsageName(app.app_name));
  const merged = new Map<string, AppFocusSummary>();
  for (const e of liveSummaries) {
    const seconds = e.total_focus_seconds ?? e.total_seconds ?? 0;
    const ex = merged.get(e.app_name);
    if (ex) {
      ex.total_focus_seconds += seconds;
      ex.session_count += e.session_count ?? 0;
    } else {
      merged.set(e.app_name, { ...e, total_focus_seconds: seconds });
    }
  }
  if (dailyApps.length === 0) {
    for (const e of cachedSummaries) {
      const ex = merged.get(e.app_name);
      if (!ex || e.total_focus_seconds > ex.total_focus_seconds) merged.set(e.app_name, { ...e });
    }
  }

  const summaries = Array.from(merged.values()).sort((a, b) => b.total_focus_seconds - a.total_focus_seconds);
  const totalSeconds = summaries.reduce((s, a) => s + a.total_focus_seconds, 0);
  const currentApp = data?.current_app ?? '';

  return (
    <div>
      <div className="focus-toolbar">
        {usageDate && onUsageDateChange && (
          <label className="focus-date-field">
            <span>Day</span>
            <input type="date" value={usageDate} onChange={e => onUsageDateChange(e.target.value)} />
          </label>
        )}
        {dailyUsers.length > 0 && (
          <span className="focus-archive-note">Daily archive saved in S3</span>
        )}
        {dailyUsageLoading && <span className="focus-archive-note">Loading daily usage...</span>}
      </div>

      {currentApp && (
        <div className="focus-current">
          <span className="live-dot" />
          <span className="focus-current-label">Current app:</span>
          <span className="focus-current-app">{currentApp}</span>
        </div>
      )}

      {summaries.length === 0 ? (
        <div className="no-data">No app usage recorded for this day yet.</div>
      ) : (
        <div className="focus-list">
          {summaries.map(app => {
            const pct = totalSeconds > 0 ? (app.total_focus_seconds / totalSeconds) * 100 : 0;
            const isActive = app.app_name === currentApp;
            return (
              <div key={app.app_name} className={`focus-item ${isActive ? 'is-active' : ''}`}>
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
                <div className="focus-pct">{pct.toFixed(1)}% of app usage time</div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
