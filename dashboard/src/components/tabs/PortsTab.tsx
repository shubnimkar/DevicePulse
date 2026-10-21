'use client';

import { PortEntry } from '@/types';
import DataTable, { type Column } from '@/components/ui/DataTable';

interface Props {
  data?: { open_ports: PortEntry[] | null; source?: string };
}

const columns: Column<PortEntry>[] = [
  { key: 'protocol', header: 'Protocol', render: p => (
    <span className={`proto-badge proto-${p.protocol.replace('4','').replace('6','')}`}>{p.protocol.toUpperCase()}</span>
  ) },
  { key: 'address', header: 'Address', render: p => <span className="mono">{p.local_addr}</span> },
  { key: 'state', header: 'State', render: p => <span className="port-state">{p.state ?? '—'}</span> },
  { key: 'process', header: 'Process', render: p => <span className="port-proc">{p.process ?? '—'}</span> },
  { key: 'pid', header: 'PID', render: p => <span className="mono" style={{ color: 'var(--text-3)' }}>{p.pid ?? '—'}</span> },
];

export default function PortsTab({ data }: Props) {
  const ports = data?.open_ports ?? [];
  if (!ports.length) return <div className="no-data">No open ports detected.</div>;

  return (
    <div>
      <div className="ports-header">
        <span className="pill pill-green">{ports.length} open ports</span>
        {data?.source && <span className="pill pill-blue mono">{data.source}</span>}
      </div>
      <DataTable
        columns={columns}
        rows={ports}
        getRowKey={(p, i) => `${p.protocol}:${p.local_addr}:${p.state ?? ''}:${p.process ?? ''}:${i}`}
        caption="Open ports"
        ariaLabel="Open ports, scrollable table"
        density="compact"
      />
    </div>
  );
}
