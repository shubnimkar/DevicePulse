// Run through: cd api && go test -run TestResponseEncryptionWebCrypto -v
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const source = fs.readFileSync(path.join(__dirname, '../src/lib/encrypted-fetch.ts'), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const client = {};
new Function('exports', compiled)(client);
const base = process.argv[2];
const realFetch = globalThis.fetch;

(async () => {
  let captured;
  globalThis.fetch = async (...args) => {
    const response = await realFetch(...args);
    captured = response.clone();
    return response;
  };
  const result = await client.encryptedFetch(`${base}/data`);
  assert.equal(result.status, 200);
  assert.deepEqual(await result.json(), { message: 'private telemetry ✓' });
  assert.equal(captured.headers.get('content-type'), 'application/octet-stream');
  const saved = captured.clone();
  assert.equal((await captured.text()).includes('private telemetry'), false);

  globalThis.fetch = realFetch;
  const error = await client.encryptedFetch(`${base}/error`);
  assert.equal(error.status, 403);
  assert.equal(error.ok, false);
  assert.deepEqual(await error.json(), { message: 'private telemetry ✓' });
  assert.equal((await client.encryptedFetch(`${base}/empty`)).status, 204);
  const parallel = await Promise.all(Array.from({ length: 12 }, () => client.encryptedFetch(`${base}/data`).then(r => r.json())));
  assert.equal(parallel.length, 12);
  assert.ok(parallel.every(r => r.message === 'private telemetry ✓'));

  for (const mode of ['ciphertext', 'status', 'type', 'replay', 'plaintext', 'truncated']) {
    globalThis.fetch = async (...args) => {
      if (mode === 'plaintext') return new Response('{"secret":true}');
      if (mode === 'replay') return saved.clone();
      const original = await realFetch(...args);
      let bytes = new Uint8Array(await original.arrayBuffer());
      const headers = new Headers(original.headers);
      if (mode === 'ciphertext') bytes[bytes.length - 1] ^= 1;
      if (mode === 'type') headers.set('X-DevicePulse-Content-Type', 'text/plain');
      if (mode === 'truncated') bytes = bytes.slice(0, 100);
      return new Response(bytes, { status: mode === 'status' ? 201 : original.status, headers });
    };
    await assert.rejects(() => client.encryptedFetch(`${base}/data`), /encrypted response|Encrypted response/, mode);
  }
  globalThis.fetch = realFetch;
  console.log('Web Crypto: decryption, errors, concurrency, tampering, replay, and plaintext rejection passed.');
})().catch(error => { console.error(error); process.exitCode = 1; });
