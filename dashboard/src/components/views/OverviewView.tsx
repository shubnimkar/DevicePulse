'use client';

import { useMemo } from 'react';
import type { Device } from '@/types';
import { StatusIcon, type Status } from '@/components/ui/StatusPill';
import EmptyState from '@/components/ui/EmptyState';
import { KpiRow, ActionQueue, CapacityCard, CompositionCard, type StatusFilter } from './OverviewPanels';
import { fleetMetrics } from './overviewMetrics';

interface Props {
  devices: Device[];
  counts: Record<StatusFilter, number>;
  getState: (d: Device) => Status;
  onOpenDevices: (filter: StatusFilter) => void;
  onInspect: (deviceId: string) => void;
}

export default function OverviewView({ devices, counts, getState, onOpenDevices, onInspect }: Props) {
  const m = useMemo(() => fleetMetrics(devices, getState), [devices, getState]);

  return (
    <>
      <KpiRow m={m} counts={counts} onOpenDevices={onOpenDevices} onInspect={onInspect} />

      {devices.length === 0 ? (
        <EmptyState icon={<StatusIcon status="offline" />} title="No devices connected" description="Start the DevicePulse agent on a machine to begin." />
      ) : (
        <div className="overview-grid">
          <ActionQueue m={m} total={devices.length} onInspect={onInspect} onOpenDevices={onOpenDevices} />
          <div className="overview-side">
            <CapacityCard m={m} onInspect={onInspect} />
            <CompositionCard m={m} />
          </div>
        </div>
      )}
    </>
  );
}
