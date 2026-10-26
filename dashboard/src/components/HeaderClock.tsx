'use client';

import { useEffect, useState } from 'react';

function formatHeaderTime(date: Date): string {
  return date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/**
 * Isolated header clock. Ticks every second but only re-renders itself,
 * not the entire page tree.
 */
export default function HeaderClock() {
  const [now, setNow] = useState<Date>(() => new Date());

  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(id);
  }, []);

  return <span className="header-time">{formatHeaderTime(now)}</span>;
}