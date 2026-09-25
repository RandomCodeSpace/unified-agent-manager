import assert from 'node:assert/strict';
import test from 'node:test';
import { cheapestLabel, modelChoices, visibleModels } from '../src/lib/models.ts';

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
  assert.equal(cheapestLabel(provider), "Cheapest (none priced, so GitHub Copilot's own title)");
});
