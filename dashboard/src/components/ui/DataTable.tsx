'use client';

import { useMemo, useState } from 'react';
import type { ReactNode } from 'react';

export interface Column<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  sortable?: boolean;
  sortValue?: (row: T) => string | number; // defaults to nothing: sortable columns should provide it
  align?: 'left' | 'right' | 'center';
  className?: string;
  sticky?: 'right'; // only the last column is sticky (global .data-table rule)
  hideBelow?: 'lg'; // hides th+td at <=1024px
}

export type SortState = { key: string; dir: 'asc' | 'desc' };

interface Props<T> {
  columns: Column<T>[];
  rows: T[];
  getRowKey: (row: T, index: number) => string;
  caption: string; // sr-only
  ariaLabel: string; // labels the scroll region
  initialSort?: SortState;
  sort?: SortState | null; // controlled mode: pass together with onSortChange
  onSortChange?: (sort: SortState) => void;
  onRowClick?: (row: T) => void;
  rowClassName?: (row: T) => string;
  emptyState?: ReactNode;
  pageSize?: number;
  density?: 'compact' | 'comfortable';
  footer?: ReactNode;
}

export default function DataTable<T>({
  columns, rows, getRowKey, caption, ariaLabel, initialSort, sort: controlledSort, onSortChange, onRowClick, rowClassName,
  emptyState, pageSize, density = 'comfortable', footer,
}: Props<T>) {
  const [innerSort, setInnerSort] = useState(initialSort ?? null);
  const sort = onSortChange ? controlledSort ?? null : innerSort;
  const [limit, setLimit] = useState(pageSize ?? Infinity);

  const sorted = useMemo(() => {
    const col = columns.find(c => c.key === sort?.key);
    if (!sort || !col?.sortValue) return rows;
    const val = col.sortValue;
    const sign = sort.dir === 'asc' ? 1 : -1;
    return [...rows].sort((a, b) => {
      const x = val(a), y = val(b);
      return sign * (typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y)));
    });
  }, [rows, columns, sort]);

  if (!rows.length && emptyState) return <>{emptyState}</>;

  const visible = sorted.slice(0, limit);
  const lastSticky = columns[columns.length - 1]?.sticky === 'right';
  const toggle = (key: string) => {
    const next: SortState = sort?.key === key ? { key, dir: sort.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' };
    setInnerSort(next);
    onSortChange?.(next);
  };
  const glyph = (active: boolean, dir?: 'asc' | 'desc') => (
    <span className={`sort-ind${active ? ' is-active' : ''}`} aria-hidden="true">{active ? (dir === 'asc' ? '▲' : '▼') : '↕'}</span>
  );
  const hide = (c: Column<T>) => (c.hideBelow ? `dt-hide-${c.hideBelow}` : undefined);
  const style = (c: Column<T>) => (c.align ? { textAlign: c.align } : undefined);

  return (
    <>
      <div className="table-wrap" role="region" tabIndex={0} aria-label={ariaLabel}>
        <table className={`data-table dt-${density} ${lastSticky ? '' : 'dt-no-sticky'}`.trim()}>
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr>
              {columns.map(c => {
                const active = sort?.key === c.key;
                return (
                  <th
                    key={c.key}
                    scope="col"
                    className={hide(c)}
                    style={style(c)}
                    aria-sort={c.sortable ? (active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none') : undefined}
                  >
                    {c.sortable ? (
                      <button type="button" className="th-sort" onClick={() => toggle(c.key)}>
                        {c.align === 'right' && glyph(active, sort?.dir)}{c.header}{c.align !== 'right' && glyph(active, sort?.dir)}
                      </button>
                    ) : c.header}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {visible.map((row, i) => (
              <tr
                key={getRowKey(row, i)}
                className={`${rowClassName?.(row) ?? ''} ${onRowClick ? 'dt-clickable' : ''}`.trim() || undefined}
                tabIndex={onRowClick ? 0 : undefined}
                onClick={onRowClick && (() => onRowClick(row))}
                onKeyDown={onRowClick && (e => {
                  if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); onRowClick(row); }
                })}
              >
                {columns.map(c => <td key={c.key} className={[c.className, hide(c)].filter(Boolean).join(' ') || undefined} style={style(c)}>{c.render(row)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
        {footer}
      </div>
      {sorted.length > visible.length && (
        <button type="button" className="action-btn show-more-btn" onClick={() => setLimit(n => n + (pageSize ?? 0))}>
          Show more ({sorted.length - visible.length} remaining)
        </button>
      )}
    </>
  );
}
