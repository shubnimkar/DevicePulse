import type { ReactNode } from 'react';

export type ShellPage = 'dashboard' | 'inventory' | 'reports' | 'inspect' | 'settings' | 'access';

export const IconGrid = () => (
  <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" focusable="false">
    <rect x="1" y="1" width="6" height="6" rx="1"/><rect x="9" y="1" width="6" height="6" rx="1"/>
    <rect x="1" y="9" width="6" height="6" rx="1"/><rect x="9" y="9" width="6" height="6" rx="1"/>
  </svg>
);
export const IconList = () => (
  <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" focusable="false">
    <rect x="1" y="2" width="14" height="2.5" rx="1"/><rect x="1" y="6.75" width="14" height="2.5" rx="1"/>
    <rect x="1" y="11.5" width="14" height="2.5" rx="1"/>
  </svg>
);
export const IconSettings = () => (
  <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true" focusable="false">
    <circle cx="8" cy="8" r="2.25"/>
    <path d="M13 8a5.3 5.3 0 00-.07-.86l1.42-1.08-1.35-2.33-1.66.68a5.1 5.1 0 00-1.48-.86L9.62 1.8H6.38l-.24 1.75a5.1 5.1 0 00-1.48.86L3 3.73 1.65 6.06l1.42 1.08A5.3 5.3 0 003 8c0 .3.02.58.07.86L1.65 9.94 3 12.27l1.66-.68c.44.37.94.66 1.48.86l.24 1.75h3.24l.24-1.75c.54-.2 1.04-.49 1.48-.86l1.66.68 1.35-2.33-1.42-1.08c.05-.28.07-.57.07-.86z"/>
  </svg>
);
export const IconReport = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    <path d="M3 2.5h10v11H3z"/>
    <path d="M5.5 6.5h5M5.5 9h5M5.5 11.5h2.5"/>
    <path d="M11 2.5v3h2"/>
  </svg>
);
export const IconShield = () => (
  <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" aria-hidden="true" focusable="false">
    <path d="M8 1.5l5.5 2v4.2c0 3.2-2.3 5.6-5.5 6.8-3.2-1.2-5.5-3.6-5.5-6.8V3.5z"/>
  </svg>
);
export const IconMore = () => (
  <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" focusable="false">
    <circle cx="3" cy="8" r="1.4"/><circle cx="8" cy="8" r="1.4"/><circle cx="13" cy="8" r="1.4"/>
  </svg>
);

export interface NavItem { page: ShellPage; label: string; icon: ReactNode }

// Labels, order and gating match the previous sidebar: Access is admin-only.
export function getNavItems(canManageUsers: boolean): { primary: NavItem[]; more: NavItem[] } {
  return {
    primary: [
      { page: 'dashboard', label: 'Overview', icon: <IconGrid /> },
      { page: 'inventory', label: 'Devices', icon: <IconList /> },
      { page: 'reports', label: 'Reports', icon: <IconReport /> },
    ],
    more: [
      { page: 'settings', label: 'Settings', icon: <IconSettings /> },
      ...(canManageUsers ? [{ page: 'access' as const, label: 'Access', icon: <IconShield /> }] : []),
    ],
  };
}
