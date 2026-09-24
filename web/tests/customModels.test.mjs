import assert from 'node:assert/strict';
import test from 'node:test';
import { customProviders, matchingIds, storedModels, withProvider } from '../src/lib/customModels.ts';

const conn = { base_url: 'https://ollama.com/v1', api_key_env: 'UAM_BYOM_OLLAMA' };
const list = [
  { name: 'ollama', ...conn, model_id: 'gpt-oss:20b', display_name: 'GPT-OSS', key_present: true },
  { name: 'router', base_url: 'https://openrouter.ai/api/v1', api_key_env: 'UAM_BYOM_ROUTER', model_id: 'x/y', key_present: false },
  { name: 'ollama', ...conn, model_id: 'qwen3.5:397b', key_present: true },
];

test('providers group their models in first-seen order', () => {
  const groups = customProviders(list);
  assert.deepEqual(groups.map((g) => [g.name, g.models.map((m) => m.model_id), g.key_present]), [['ollama', ['gpt-oss:20b', 'qwen3.5:397b'], true], ['router', ['x/y'], false]]);
});

test('saving a provider replaces its models in place and keeps display names', () => {
  const next = withProvider(list, 'ollama', { name: 'ollama', ...conn }, ['gpt-oss:20b', 'kimi-k2:1t', 'kimi-k2:1t']);
  assert.deepEqual(next.map((m) => `${m.name}/${m.model_id}`), ['ollama/gpt-oss:20b', 'ollama/kimi-k2:1t', 'router/x/y']);
  assert.equal(next[0].display_name, 'GPT-OSS');
  assert.equal(next[1].display_name, 'kimi-k2:1t');
  assert.ok(next.every((m) => !('key_present' in m)));
});

test('a new provider is appended; no models removes a provider', () => {
  assert.deepEqual(withProvider(list, undefined, { name: 'local', base_url: 'http://127.0.0.1:1/v1', api_key_env: 'UAM_BYOM_L' }, ['m']).map((m) => m.name), ['ollama', 'router', 'ollama', 'local']);
  assert.deepEqual(withProvider(list, 'ollama', { name: 'ollama', ...conn }, []).map((m) => m.name), ['router']);
  assert.deepEqual(storedModels(list).length, 3);
});

test('the checklist search matches IDs case-insensitively', () => {
  assert.deepEqual(matchingIds(['gpt-oss:20b', 'Qwen3.5:397b', 'kimi'], ' QWEN '), ['Qwen3.5:397b']);
  assert.deepEqual(matchingIds(['a', 'b'], ''), ['a', 'b']);
});
