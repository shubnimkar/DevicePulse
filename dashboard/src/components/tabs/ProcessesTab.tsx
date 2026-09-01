'use client';

import { useMemo, useState } from 'react';
import { motion } from 'motion/react';
import { ProcessData } from '@/types';

interface Props { procs: ProcessData[]; }

const PAGE_SIZE = 10;

export default function ProcessesTab({ procs }: Props) {
  const [selectedPage, setSelectedPage] = useState(0);

  // Derive the effective page during render instead of clamping via an effect.
  const pageCount = Math.max(Math.ceil(procs.length / PAGE_SIZE), 1);
  const page = Math.min(selectedPage, pageCount - 1);
  const start = page * PAGE_SIZE;
  const end = Math.min(start + PAGE_SIZE, procs.length);
  const pageItems = useMemo(() => procs.slice(start, end), [procs, start, end]);

  const goToPage = (next: number) => setSelectedPage(Math.max(0, Math.min(next, pageCount - 1)));

  if (!procs.length) return <div className="no-data">No process data yet.</div>;

  return (
    <div>
      <ul className="proc-list">
        {pageItems.map((p, i) => (
          <motion.li
            key={`${p.pid}-${p.name}-${i}`}
            className="proc-item"
            initial={{ opacity: 0, x: -8 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ delay: i * 0.02 }}
            layout
          >
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
          </motion.li>
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
              disabled={page === 0}
              onClick={() => goToPage(page - 1)}
              aria-label="Previous process page"
            >
              ‹
            </button>
            <span className="mono">{page + 1} / {pageCount}</span>
            <button
              className="page-btn"
              type="button"
              disabled={page >= pageCount - 1}
              onClick={() => goToPage(page + 1)}
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
