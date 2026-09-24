import assert from 'node:assert/strict';
import test from 'node:test';
import { modelChoices, visibleModels } from '../src/lib/models.ts';

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
