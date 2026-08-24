'use client';

import { useEffect, useMemo, useState } from 'react';
import { ProcessData } from '@/types';

interface Props { procs: ProcessData[]; }

const PAGE_SIZE = 10;

export default function ProcessesTab({ procs }: Props) {
  const [page, setPage] = useState(0);

  const pageCount = Math.max(Math.ceil(procs.length / PAGE_SIZE), 1);
  const safePage = Math.min(page, pageCount - 1);
  const start = safePage * PAGE_SIZE;
  const end = Math.min(start + PAGE_SIZE, procs.length);
  const pageItems = useMemo(() => procs.slice(start, end), [procs, start, end]);

  useEffect(() => {
    if (page !== safePage) setPage(safePage);
  }, [page, safePage]);

  if (!procs.length) return <div className="no-data">No process data yet.</div>;

  return (
    <div>
      <ul className="proc-list">
        {pageItems.map((p) => (
          <li key={`${p.pid}-${p.name}`} className="proc-item">
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

      {procs.length > PAGE_SIZE && (
        <div className="table-footer proc-footer">
          <span>
            Showing {start + 1}-{end} of {procs.length}
          </span>
          <div className="table-footer-pages">
            <button
              className="page-btn"
              type="button"
              disabled={safePage === 0}
              onClick={() => setPage(p => Math.max(p - 1, 0))}
              aria-label="Previous process page"
            >
              ‹
            </button>
            <span className="mono">{safePage + 1} / {pageCount}</span>
            <button
              className="page-btn"
              type="button"
              disabled={safePage >= pageCount - 1}
              onClick={() => setPage(p => Math.min(p + 1, pageCount - 1))}
              aria-label="Next process page"
            >
              ›
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
