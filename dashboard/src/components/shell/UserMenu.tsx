'use client';

import { useEffect, useRef } from 'react';

interface Props {
  name: string;
  email: string;
  roleText: string;
  onSignOut: () => void;
}

export default function UserMenu({ name, email, roleText, onSignOut }: Props) {
  const ref = useRef<HTMLDetailsElement>(null);
  const display = name || email;

  // Close on Escape (return focus to the trigger) or outside click.
  useEffect(() => {
    const menu = ref.current;
    if (!menu) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && menu.open) { menu.open = false; menu.querySelector('summary')?.focus(); }
    };
    const onDown = (e: MouseEvent) => {
      if (menu.open && e.target instanceof Node && !menu.contains(e.target)) menu.open = false;
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => { document.removeEventListener('keydown', onKey); document.removeEventListener('mousedown', onDown); };
  }, []);

  return (
    <details className="user-menu" ref={ref}>
      <summary className="user-chip" aria-label={`User menu, ${display}, ${roleText}`}>
        <span className="user-avatar">{(display || 'U').charAt(0).toUpperCase()}</span>
        <span className="user-copy">
          <span className="user-name">{display}</span>
          <span className="user-role">{roleText}</span>
        </span>
        <span className="user-caret" aria-hidden="true" />
      </summary>
      <div className="user-menu-panel">
        <button type="button" onClick={() => { if (ref.current) ref.current.open = false; onSignOut(); }}>Sign out</button>
      </div>
    </details>
  );
}
