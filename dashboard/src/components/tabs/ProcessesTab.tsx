'use client';

import { ProcessData } from '@/types';

interface Props {
  procs: ProcessData[];
}

export default function ProcessesTab({ procs }: Props) {
  if (!procs.length) return <div className="no-data">No process data yet.</div>;

  return (
    <ul className="process-list">
      {procs.map((p, i) => (
        <li key={i} className="process-item">
          <div className="process-left">
            <span className="process-name" title={p.name}>{p.name}</span>
            <span className="process-pid">PID {p.pid}</span>
            <div className="cpu-bar-wrap">
              <div className="cpu-bar" style={{ width: `${Math.min(p.cpu, 100)}%` }} />
            </div>
          </div>
          <div className="process-stats">
            <span className="process-cpu">{p.cpu.toFixed(1)}% CPU</span>
            <span className="process-mem">{p.memory.toFixed(1)}% Mem</span>
          </div>
        </li>
      ))}
    </ul>
  );
}
