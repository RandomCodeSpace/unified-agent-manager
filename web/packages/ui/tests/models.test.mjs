import assert from 'node:assert/strict';
import test from 'node:test';
import { byModelName, cheapestLabel, effortLabel, modelChoices, visibleModels } from '../src/lib/models.ts';

const models = [{ id: 'a', name: 'A' }, { id: 'b', name: 'B' }, { id: 'new', name: 'New' }];
test('hidden models leave choices and new models stay visible', () => {
  assert.deepEqual(visibleModels(models, ['b', 'unavailable']), [models[0], models[2]]);
  assert.equal(models.length, 3);
});
test('current hidden and absent models retain a label without becoming available choices', () => {
  const hidden = modelChoices(models, ['b'], 'b');
  assert.equal(hidden[0].note, 'Hidden in Settings');
  assert.equal(hidden[0].model, models[1]);
  assert.deepEqual(hidden.filter((c) => !c.note).map((c) => c.model.id), ['a', 'new']);
  assert.equal(modelChoices(models, [], 'removed')[0].note, 'Not offered now');
});
test('the unset utility model names the cheapest model the service picked, or says none is priced', () => {
  const provider = { display_name: 'GitHub Copilot', models: [{ id: 'gpt-6-luna', name: 'GPT-6 Luna' }, { id: 'raw', name: '' }] };
  assert.equal(cheapestLabel({ ...provider, cheapest_model: 'gpt-6-luna' }), 'Cheapest (currently GPT-6 Luna)');
  assert.equal(cheapestLabel({ ...provider, cheapest_model: 'raw' }), 'Cheapest (currently raw)');
  assert.equal(cheapestLabel({ ...provider, cheapest_model: 'unlisted' }), 'Cheapest (currently unlisted)');
  assert.equal(cheapestLabel(provider), 'Cheapest (none priced)');
});
test('models sort by display name with numbers compared as numbers, so versions sit together', () => {
  const list = [{ id: 'c55', name: 'Claude Sonnet 5.5' }, { id: 'g', name: 'GPT-6 Luna' }, { id: 'c5', name: 'Claude Sonnet 5' }, { id: 'raw-id', name: '' }, { id: 'a', name: 'auto' }];
  assert.deepEqual([...list].sort(byModelName).map((m) => m.id), ['a', 'c5', 'c55', 'g', 'raw-id']);
});
test('efforts read as words in menus while the stored value stays the provider one', () => {
  assert.deepEqual(['none', 'low', 'medium', 'high', 'xhigh', 'max', 'turbo'].map(effortLabel), ['None', 'Low', 'Medium', 'High', 'Extra high', 'Max', 'Turbo']);
});

test('modelBrand reads the brand from the model ID, provider prefixes included', async () => {
  const { modelBrand } = await import('../src/lib/models.ts');
  const cases = {
    'claude-haiku-4.5': 'claude', 'gpt-6-luna': 'openai', 'gpt-5.3-codex': 'openai', 'o4-mini': 'openai', 'mai-code-1.1-flash': 'microsoft',
    'grok-4.7': 'grok', 'kimi-k3': 'kimi', 'ollama/deepseek-v4.1-flash': 'deepseek', 'openrouter/qwen/qwen3-coder': 'qwen',
    'gemini-3-pro': 'gemini', 'meta-llama/llama-4': 'meta', 'mistralai/devstral-2': 'mistral', 'z-ai/glm-5': null, auto: 'copilot', 'my-local-model': null, '': null,
  };
  for (const [id, brand] of Object.entries(cases)) assert.equal(modelBrand(id), brand, id);
});
