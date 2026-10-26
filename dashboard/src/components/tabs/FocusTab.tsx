'use client';

import { motion } from 'motion/react';
import { AppFocusSummary, ActiveWindowData, DailyAppUsageData, DailyPresenceData } from '@/types';
import { buildAppUsageSummaries, formatDuration } from '@/lib/utils';
import GaugeBar from '@/components/GaugeBar';
import { focusTrust } from '@/lib/focus-trust';

interface Props {
  data?: ActiveWindowData;
  cachedSummaries: AppFocusSummary[];
  dailyUsage?: DailyAppUsageData;
  dailyPresence?: DailyPresenceData;
  dailyUsageLoading?: boolean;
  usageDate?: string;
  onUsageDateChange?: (date: string) => void;
}

export default function FocusTab({ data, cachedSummaries, dailyUsage, dailyPresence, dailyUsageLoading = false, usageDate, onUsageDateChange }: Props) {
  if (!data && cachedSummaries.length === 0 && !dailyUsage) {
    return <div className="no-data">App-usage telemetry was not included in the latest device check-in.</div>;
  }

  const dailyUsers = dailyUsage?.users ?? [];
  const { summaries } = buildAppUsageSummaries(data, cachedSummaries, dailyUsage, dailyPresence?.online_seconds);
  const totalSeconds = summaries.reduce((s, a) => s + a.total_focus_seconds, 0);
  const currentApp = data?.current_app ?? '';
  const trust = focusTrust(data);

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
        {dailyPresence?.first_seen && <span className="focus-archive-note">Connected {new Date(dailyPresence.first_seen).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} · online {formatDuration(dailyPresence.online_seconds)}</span>}
      </div>

      {trust.level !== 'ok' ? (
        <div className={`focus-quality is-${trust.level}`} role="status">
          <strong>{trust.title}</strong>
          <span>{trust.detail}</span>
        </div>
      ) : (
        (trust.sourceLabel || trust.away) && (
          <div className="focus-quality-ok">
            {trust.sourceLabel && <>Tracking via {trust.sourceLabel}</>}
            {trust.away && <> · user away (locked or idle), time not counted</>}
          </div>
        )
      )}

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
          {summaries.map((app, index) => {
            const pct = totalSeconds > 0 ? (app.total_focus_seconds / totalSeconds) * 100 : 0;
            const isActive = app.app_name === currentApp;
            return (
              <motion.div
                key={app.app_name}
                className={`focus-item ${isActive ? 'is-active' : ''}`}
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: index * 0.025 }}
                layout
              >
                <div className="focus-row">
                  <span className="focus-app-name" title={app.app_name}>
                    {isActive && <span className="live-dot" />}
                    {app.app_name}
                  </span>
                  <span className="focus-duration">{formatDuration(app.total_focus_seconds)}</span>
                  <span className="focus-sessions">{app.session_count} session{app.session_count !== 1 ? 's' : ''}</span>
                </div>
                <GaugeBar
                  label={`${app.app_name} share of app usage time`}
                  value={pct}
                  color={isActive ? 'var(--status-ok-fg)' : 'var(--accent)'}
                  height={3}
                />
                <div className="focus-pct">{pct.toFixed(1)}% of app usage time</div>
              </motion.div>
            );
          })}
        </div>
      )}
    </div>
  );
}
