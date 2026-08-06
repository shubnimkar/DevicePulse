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
      <div className="collector-header">
        <span className="count-pill count-running">🔌 {ports.length} open ports</span>
        {data?.source && <span className="count-pill count-source">{data.source}</span>}
      </div>
      <table className="ports-table">
        <thead>
          <tr>
            <th>Protocol</th>
            <th>Address</th>
            <th>State</th>
            <th>Process</th>
            <th>PID</th>
          </tr>
        </thead>
        <tbody>
          {ports.map((p, i) => (
            <tr key={i}>
              <td>
                <span
                  className={`proto-badge proto-${p.protocol
                    .replace('4', '')
                    .replace('6', '')}`}
                >
                  {p.protocol.toUpperCase()}
                </span>
              </td>
              <td>
                <span className="mono-text">{p.local_addr}</span>
              </td>
              <td>
                <span className="state-text">{p.state ?? '—'}</span>
              </td>
              <td>
                <span className="process-badge">{p.process ?? '—'}</span>
              </td>
              <td>
                <span className="mono-text muted">{p.pid ?? '—'}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
