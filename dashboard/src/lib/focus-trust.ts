import type { ActiveWindowData } from '@/types';

export type FocusTrustLevel = 'ok' | 'estimated' | 'unavailable' | 'legacy';

export interface FocusTrust {
  level: FocusTrustLevel;
  /** Short heading; empty when everything is fine. */
  title: string;
  /** Plain-language explanation shown under the heading. */
  detail: string;
  /** How the focused window is read, for example "GNOME extension". */
  sourceLabel: string;
  away: boolean;
}

const SOURCE_LABELS: Record<string, string> = {
  'gnome-extension': 'GNOME extension',
  'gnome-eval': 'GNOME Shell',
  x11: 'X11',
  kde: 'KDE',
  sway: 'Sway',
  macos: 'macOS',
  windows: 'Windows',
  'user-session': 'user session',
  'proc-guess': 'CPU activity (estimate)',
};

/**
 * Says how far to trust a device's app-usage numbers, from what the agent
 * reports about how it finds the focused window. Agents that predate this
 * report nothing: on GNOME Wayland they guessed from CPU activity.
 */
export function focusTrust(data?: Pick<ActiveWindowData, 'focus_source' | 'focus_quality' | 'focus_note' | 'idle'>): FocusTrust {
  const source = data?.focus_source ?? '';
  const sourceLabel = SOURCE_LABELS[source] ?? '';
  const away = !!data?.idle;
  switch (data?.focus_quality) {
    case 'accurate':
      return { level: 'ok', title: '', detail: '', sourceLabel, away };
    case 'estimated':
      return {
        level: 'estimated',
        title: 'App times are estimated',
        detail: data.focus_note || 'The agent is guessing the active app from CPU activity, not reading the focused window, so these times can be wrong.',
        sourceLabel,
        away,
      };
    case 'unavailable':
      return {
        level: 'unavailable',
        title: 'App tracking is not working on this device',
        detail: data.focus_note || 'The agent cannot read which window has focus, so no new app usage is being recorded.',
        sourceLabel,
        away,
      };
    default:
      return {
        level: 'legacy',
        title: 'Older agent: times may be inaccurate',
        detail: 'This device reports with an agent that does not say how it tracks apps. On GNOME Wayland (Ubuntu 24.04) older agents guess the focused app, so these numbers can be wrong until the agent is updated.',
        sourceLabel,
        away,
      };
  }
}
