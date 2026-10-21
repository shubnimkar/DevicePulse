'use client';

import { useEffect, useRef, useState } from 'react';
import { getNavItems, IconMore, type ShellPage } from './nav';

interface Props {
  currentPage: ShellPage;
  onNavigate: (page: ShellPage) => void;
  canManageUsers: boolean;
}

export default function MobileTabBar({ currentPage, onNavigate, canManageUsers }: Props) {
  const { primary, more } = getNavItems(canManageUsers);
  const [open, setOpen] = useState(false);
  const moreRef = useRef<HTMLButtonElement>(null);
  const sheetRef = useRef<HTMLDivElement>(null);
  const moreActive = more.some(i => i.page === currentPage);

  // Sheet: focus first item, close on Escape / outside press, return focus to "More".
  useEffect(() => {
    if (!open) return;
    const trigger = moreRef.current;
    sheetRef.current?.querySelector<HTMLElement>('button')?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!sheetRef.current?.contains(t) && !trigger?.contains(t)) setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('pointerdown', onDown);
    return () => { document.removeEventListener('keydown', onKey); document.removeEventListener('pointerdown', onDown); trigger?.focus(); };
  }, [open]);

  const go = (page: ShellPage) => { setOpen(false); onNavigate(page); };

  return (
    <>
      {open && (
        <div className="tabbar-sheet" ref={sheetRef} id="tabbar-more" role="group" aria-label="More pages">
          {more.map(item => (
            <button
              key={item.page}
              type="button"
              className={`tab-item sheet-item ${currentPage === item.page ? 'active' : ''}`}
              aria-current={currentPage === item.page ? 'page' : undefined}
              onClick={() => go(item.page)}
            >
              {item.icon}<span>{item.label}</span>
            </button>
          ))}
        </div>
      )}
      <nav className="shell-tabbar" aria-label="Primary">
        {primary.map(item => (
          <button
            key={item.page}
            type="button"
            className={`tab-item ${currentPage === item.page ? 'active' : ''}`}
            aria-current={currentPage === item.page ? 'page' : undefined}
            onClick={() => go(item.page)}
          >
            {item.icon}<span>{item.label}</span>
          </button>
        ))}
        <button
          ref={moreRef}
          type="button"
          className={`tab-item ${moreActive ? 'active' : ''}`}
          aria-expanded={open}
          aria-controls="tabbar-more"
          aria-current={moreActive ? 'page' : undefined}
          onClick={() => setOpen(o => !o)}
        >
          <IconMore /><span>More</span>
        </button>
      </nav>
    </>
  );
}
