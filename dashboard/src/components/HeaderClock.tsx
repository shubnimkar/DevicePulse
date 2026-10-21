'use client';

import { useEffect, useState } from 'react';

const FULL: Intl.DateTimeFormatOptions = { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' };
const COMPACT: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' };

/**
 * Isolated clock. Ticks every second but only re-renders itself,
 * not the entire page tree. `compact` shows HH:MM with the full date as title.
 */
export default function HeaderClock({ compact = false, className = 'header-time' }: { compact?: boolean; className?: string }) {
  const [now, setNow] = useState<Date>(() => new Date());

  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(id);
  }, []);

  return (
    <time className={className} dateTime={now.toISOString()} title={compact ? now.toLocaleString(undefined, FULL) : undefined}>
      {now.toLocaleString(undefined, compact ? COMPACT : FULL)}
    </time>
  );
}
