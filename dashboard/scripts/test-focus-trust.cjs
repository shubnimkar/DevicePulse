// Exercises src/lib/focus-trust.ts. Run from the dashboard directory:
//   node scripts/test-focus-trust.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');

const source = fs.readFileSync(path.join(__dirname, '../src/lib/focus-trust.ts'), 'utf8');
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } });
const mod = { exports: {} };
new Function('module', 'exports', outputText)(mod, mod.exports);
const { focusTrust } = mod.exports;

// Real focus source: no warning, source named.
let t = focusTrust({ focus_source: 'gnome-extension', focus_quality: 'accurate', idle: false });
assert.equal(t.level, 'ok');
assert.equal(t.sourceLabel, 'GNOME extension');
assert.equal(t.title, '');

// User away is flagged, not hidden.
assert.equal(focusTrust({ focus_source: 'x11', focus_quality: 'accurate', idle: true }).away, true);

// Guessing from CPU is called out as an estimate.
t = focusTrust({ focus_source: 'proc-guess', focus_quality: 'estimated' });
assert.equal(t.level, 'estimated');
assert.match(t.detail, /CPU/);

// Nothing works: the agent's own explanation is shown.
t = focusTrust({ focus_source: 'none', focus_quality: 'unavailable', focus_note: 'log out and back in' });
assert.equal(t.level, 'unavailable');
assert.equal(t.detail, 'log out and back in');
assert.match(focusTrust({ focus_quality: 'unavailable' }).detail, /cannot read which window/);

// An agent that predates the field: warn that numbers may be wrong.
t = focusTrust({});
assert.equal(t.level, 'legacy');
assert.match(t.detail, /Wayland/);
assert.equal(focusTrust(undefined).level, 'legacy');

console.log('focus-trust: ok');
