// Exercises src/lib/paged-fetch.ts against fake paged and legacy responses.
// Run from the dashboard directory: node scripts/test-paged-fetch.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');

const source = fs.readFileSync(path.join(__dirname, '../src/lib/paged-fetch.ts'), 'utf8');
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } });
const mod = { exports: {} };
new Function('module', 'exports', outputText)(mod, mod.exports);
const { fetchAllPages } = mod.exports;

const json = (body, status = 200) => ({ ok: status < 400, status, json: async () => body });

(async () => {
  // Walks every page, passing the cursor back each time.
  const calls = [];
  const all = ['a', 'b', 'c', 'd', 'e'];
  const server = async (p) => {
    calls.push(p);
    const url = new URL(p, 'http://x');
    const start = Number(url.searchParams.get('offset') ?? 0);
    const size = Number(url.searchParams.get('limit'));
    const items = all.slice(start, start + size);
    return json({ items, total: all.length, next: start + size < all.length ? String(start + size) : '' });
  };
  assert.deepEqual(await fetchAllPages(server, '/users', 'offset', 2), all);
  assert.equal(calls.length, 3);
  assert.equal(calls[0], '/users?limit=2');
  assert.equal(calls[1], '/users?limit=2&offset=2');

  // An API without paging answers with a bare array: returned unchanged.
  assert.deepEqual(await fetchAllPages(async () => json(['x', 'y']), '/devices', 'after', 200), ['x', 'y']);

  // Errors surface as before.
  await assert.rejects(fetchAllPages(async () => json({}, 503), '/devices', 'after', 200), /HTTP 503/);

  // A server that keeps returning the same cursor cannot loop forever.
  let n = 0;
  const stuck = await fetchAllPages(async () => json({ items: [n++], next: 'same' }), '/devices', 'after', 1);
  assert.deepEqual(stuck, [0, 1]);

  // Empty fleet.
  assert.deepEqual(await fetchAllPages(async () => json({ items: [], total: 0, next: '' }), '/devices', 'after', 200), []);
  console.log('paged-fetch: ok');
})().catch((e) => { console.error(e); process.exit(1); });
