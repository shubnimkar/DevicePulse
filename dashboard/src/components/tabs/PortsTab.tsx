'use client';

import { PortEntry } from '@/types';

interface Props {
  data?: { open_ports: PortEntry[] | null; source?: string };
}

export default function PortsTab({ data }: Props) {
  const ports = data?.open_ports ?? [];
  if (!ports.length) return <div className="no-data">No open ports detected.</div>;

  return (
    <div>
      <div className="ports-header">
        <span className="pill pill-green">{ports.length} open ports</span>
        {data?.source && <span className="pill pill-blue mono">{data.source}</span>}
      </div>
      <table className="ports-table">
        <thead>
          <tr>
            <th scope="col">Protocol</th>
            <th scope="col">Address</th>
            <th scope="col">State</th>
            <th scope="col">Process</th>
            <th scope="col">PID</th>
          </tr>
        </thead>
        <tbody>
          {ports.map((p, i) => (
            <tr key={`${p.protocol}:${p.local_addr}:${p.state ?? ''}:${p.process ?? ''}:${i}`}>
              <td>
                <span className={`proto-badge proto-${p.protocol.replace('4','').replace('6','')}`}>
                  {p.protocol.toUpperCase()}
                </span>
              </td>
              <td><span className="mono">{p.local_addr}</span></td>
              <td><span className="port-state">{p.state ?? '—'}</span></td>
              <td><span className="port-proc">{p.process ?? '—'}</span></td>
              <td><span className="mono" style={{ color: 'var(--text-3)' }}>{p.pid ?? '—'}</span></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
