'use client';

import { ProcessData } from '@/types';

interface Props { procs: ProcessData[]; }

export default function ProcessesTab({ procs }: Props) {
  if (!procs.length) return <div className="no-data">No process data yet.</div>;

  return (
    <ul className="proc-list">
      {procs.map((p, i) => (
        <li key={i} className="proc-item">
          <div className="proc-left">
            <span className="proc-name" title={p.name}>{p.name}</span>
            <span className="proc-pid">PID {p.pid}</span>
            <div className="proc-bar-track">
              <div className="proc-bar-fill" style={{ width: `${Math.min(p.cpu, 100)}%` }} />
            </div>
          </div>
          <div className="proc-stats">
            <span className="proc-cpu">{p.cpu.toFixed(1)}% CPU</span>
            <span className="proc-mem">{p.memory.toFixed(1)}% Mem</span>
          </div>
        </li>
      ))}
    </ul>
  );
}
