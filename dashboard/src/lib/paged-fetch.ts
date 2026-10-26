/**
 * Loads a whole list from an endpoint that supports ?limit= paging.
 *
 * Paged responses are `{ items, total, next }`; `next` is the cursor for the
 * following page (`after` for devices, `offset` for users) and is empty on the
 * last one. An API that predates paging ignores `limit` and answers with a bare
 * array, which is returned as is, so the dashboard keeps working against it.
 */
export async function fetchAllPages<T>(
  get: (path: string) => Promise<Response>,
  path: string,
  cursorParam: 'after' | 'offset',
  pageSize: number,
): Promise<T[]> {
  const items: T[] = [];
  const seen = new Set<string>();
  let cursor = '';
  for (let page = 0; page < 1000; page++) {
    const query = `limit=${pageSize}${cursor ? `&${cursorParam}=${encodeURIComponent(cursor)}` : ''}`;
    const res = await get(`${path}?${query}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const body = await res.json();
    if (Array.isArray(body)) return body as T[];
    items.push(...((body?.items ?? []) as T[]));
    const next = typeof body?.next === 'string' ? body.next : '';
    if (!next || seen.has(next)) return items; // done, or a cursor that would loop forever
    seen.add(next);
    cursor = next;
  }
  return items;
}
