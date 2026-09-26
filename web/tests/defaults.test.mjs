import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks } from 'node:module';

// The browser resolves extensionless TypeScript imports; node's strip-types runner does not.
const hooks = registerHooks({
  resolve(specifier, context, nextResolve) {
    return nextResolve(['./lib/models', './lib/reads'].includes(specifier) ? `${specifier}.ts` : specifier, context);
  },
});
const { resolveTaskDefaults } = await import('../src/api.ts');
hooks.deregister();

const sizes = [{ id: 'default', tokens: 200 }, { id: 'long_context', tokens: 900 }];
const copilot = {
  name: 'copilot',
  display_name: 'GitHub Copilot',
  available: true,
  capabilities: { context_size: true },
  models: [
    { id: 'auto', name: 'Auto' },
    { id: 'haiku', name: 'Haiku', efforts: ['low', 'high'], context_sizes: sizes },
    { id: 'mini', name: 'Mini', efforts: ['low'] },
  ],
};
const meta = { version: 'test', recent_workdirs: [], providers: [copilot] };
const fallback = { provider: 'copilot', model: 'auto', effort: '', context_size: 'default', mode: 'safe' };

test('a project without defaults gets what the old form started with', () => {
  assert.deepEqual(resolveTaskDefaults(meta), fallback);
  assert.deepEqual(resolveTaskDefaults(meta, undefined), fallback);
});

test('defaults the catalog still offers are kept as they are', () => {
  const d = { provider: 'copilot', model: 'haiku', effort: 'high', context_size: 'long_context', mode: 'yolo' };
  assert.deepEqual(resolveTaskDefaults(meta, d), d);
});

test('new tasks fall back to a visible model when the project default is hidden', () => {
  const d = { provider: 'copilot', model: 'haiku', effort: 'high', context_size: 'long_context', mode: 'yolo' };
  assert.deepEqual(resolveTaskDefaults(meta, d, { copilot: ['haiku'] }), { ...fallback, mode: 'yolo' });
  assert.deepEqual(resolveTaskDefaults(meta, d, { copilot: ['haiku', 'auto'] }), { ...fallback, model: 'mini', mode: 'yolo' });
});

test('effort and context size survive only where the model and provider offer them', () => {
  const d = { provider: 'copilot', model: 'mini', effort: 'high', context_size: 'long_context', mode: 'safe' };
  assert.deepEqual(resolveTaskDefaults(meta, d), { ...d, effort: '', context_size: 'default' });
  const noContext = { ...meta, providers: [{ ...copilot, capabilities: {} }] };
  const withHaiku = { ...d, model: 'haiku', effort: 'low' };
  assert.deepEqual(resolveTaskDefaults(noContext, withHaiku), { ...withHaiku, context_size: 'default' });
});

test('a stale model falls back to auto with effort cleared and the default context size, keeping the mode', () => {
  const d = { provider: 'copilot', model: 'gone', effort: 'high', context_size: 'long_context', mode: 'yolo' };
  assert.deepEqual(resolveTaskDefaults(meta, d), { ...fallback, mode: 'yolo' });
  const noAuto = { ...meta, providers: [{ ...copilot, models: copilot.models.slice(1) }] };
  assert.deepEqual(resolveTaskDefaults(noAuto, d), { ...fallback, model: 'haiku', mode: 'yolo' });
});

test('an unlisted or unavailable default provider gives way to the first available one', () => {
  const other = { ...copilot, name: 'other', available: false };
  const both = { ...meta, providers: [other, copilot] };
  const d = { provider: 'other', model: 'haiku', effort: 'low', context_size: 'default', mode: 'safe' };
  assert.deepEqual(resolveTaskDefaults(both, d), { ...d, provider: 'copilot' });
  assert.deepEqual(resolveTaskDefaults(meta, { ...d, provider: 'missing' }), { ...d, provider: 'copilot' });
  assert.equal(resolveTaskDefaults(null, d), null);
});
