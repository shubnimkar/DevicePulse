'use client';

import type { ReactNode } from 'react';
import { motion } from 'motion/react';
import TopBar, { type FleetConn } from './TopBar';
import SideRail from './SideRail';
import MobileTabBar from './MobileTabBar';
import type { ShellPage } from './nav';

interface Props {
  currentPage: ShellPage;
  onNavigate: (page: ShellPage) => void;
  canManageUsers: boolean;
  fleetConn: FleetConn;
  loading: boolean;
  staleSeconds: number | null;
  onlineCount: number;
  totalCount: number;
  user: { name: string; email: string; roleText: string };
  onSignOut: () => void;
  children: ReactNode; // the <main> element
}

export default function AppShell({ currentPage, onNavigate, canManageUsers, fleetConn, loading, staleSeconds, onlineCount, totalCount, user, onSignOut, children }: Props) {
  return (
    <motion.div className="app-shell shell" initial={{ opacity: 0 }} animate={{ opacity: 1 }}>
      <TopBar fleetConn={fleetConn} loading={loading} staleSeconds={staleSeconds} user={user} onSignOut={onSignOut} />
      <SideRail currentPage={currentPage} onNavigate={onNavigate} canManageUsers={canManageUsers} fleetConn={fleetConn} onlineCount={onlineCount} totalCount={totalCount} />
      {children}
      <MobileTabBar currentPage={currentPage} onNavigate={onNavigate} canManageUsers={canManageUsers} />
    </motion.div>
  );
}
