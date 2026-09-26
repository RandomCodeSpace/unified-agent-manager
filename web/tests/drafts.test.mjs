import assert from 'node:assert/strict';
import test from 'node:test';
import { DRAFT_PREFIX, changeSettings, createRequest, draftKey, newTaskKey, parseDraft, serializeDraft, staleDraftKeys } from '../src/lib/drafts.ts';

const full = { text: 'Fix @docs/web.md', files: ['docs/web.md'], attachments: [{ id: 'att-1', name: 'shot.png', size: 2048, kind: 'image' }] };

test('a draft round-trips through storage; an empty one serializes to nothing', () => {
  assert.equal(draftKey('t1'), `${DRAFT_PREFIX}t1`);
  assert.deepEqual(parseDraft(serializeDraft(full)), full);
  assert.equal(serializeDraft({ text: '  \n', files: [], attachments: [] }), null);
  assert.ok(serializeDraft({ text: '', files: ['a.go'], attachments: [] }));
  assert.ok(serializeDraft({ text: '', files: [], attachments: full.attachments }));
});

test('queued draft settings survive reload with text and attachments without changing task settings', () => {
  const settings = { model: 'vision', effort: 'high', context_size: 'default' };
  assert.deepEqual(parseDraft(serializeDraft({ ...full, settings })), { ...full, settings });
  assert.deepEqual(parseDraft(serializeDraft({ text: '', files: [], attachments: [], settings })), { text: '', files: [], attachments: [], settings });
  assert.deepEqual(parseDraft(JSON.stringify({ ...full, settings: { model: 3 } })), full);
});

test('malformed or empty stored values read as no draft; bad entries are dropped', () => {
  assert.equal(parseDraft(null), null);
  assert.equal(parseDraft(''), null);
  assert.equal(parseDraft('{not json'), null);
  assert.equal(parseDraft('"text"'), null);
  assert.equal(parseDraft(JSON.stringify({ text: '   ', files: [3], attachments: [{ id: 'x' }] })), null);
  assert.deepEqual(parseDraft(JSON.stringify({ text: 'hi', files: ['a', 7, ''], attachments: [{ id: 'a', name: 'n', kind: 'video' }, { id: 'b', name: 'b.txt', kind: 'text' }] })), {
    text: 'hi',
    files: ['a'],
    attachments: [{ id: 'b', name: 'b.txt', size: 0, kind: 'text' }],
  });
});

test('the sweep names draft keys of Tasks that are gone and leaves everything else', () => {
  const keys = ['uam.viewed', `${DRAFT_PREFIX}t1`, `${DRAFT_PREFIX}gone`, 'uam.draft', 'other'];
  assert.deepEqual(staleDraftKeys(keys, ['t1', 't2'], []), [`${DRAFT_PREFIX}gone`]);
  assert.deepEqual(staleDraftKeys(keys, [], []), [`${DRAFT_PREFIX}t1`, `${DRAFT_PREFIX}gone`]);
  assert.deepEqual(staleDraftKeys([], ['t1'], []), []);
});

test('a new Task keeps its draft per Project until the Project goes', () => {
  assert.equal(newTaskKey('p1'), `${DRAFT_PREFIX}new.p1`);
  assert.notEqual(newTaskKey('p1'), draftKey('p1'));
  const keys = [newTaskKey('p1'), newTaskKey('gone'), draftKey('t1')];
  assert.deepEqual(staleDraftKeys(keys, ['t1'], ['p1']), [newTaskKey('gone')]);
  // A Task id never shields a new-Task key, nor a Project id a Task's.
  assert.deepEqual(staleDraftKeys(keys, ['p1', 'gone'], ['t1']), [newTaskKey('p1'), newTaskKey('gone'), draftKey('t1')]);
});

test('a new Task changes settings by the service rule and creates with them, without a prompt', () => {
  const s = { provider: 'copilot', model: 'gpt-6', effort: 'high', context_size: 'long_context', mode: 'safe' };
  const both = { efforts: ['low', 'high'], context_sizes: [{ id: 'long_context', tokens: 1 }] };
  assert.deepEqual(changeSettings(s, { model: 'luna' }, both), { ...s, model: 'luna' });
  assert.deepEqual(changeSettings(s, { model: 'auto' }, {}), { ...s, model: 'auto', effort: '', context_size: 'default' });
  assert.deepEqual(changeSettings(s, { model: 'auto', effort: 'low' }, {}), { ...s, model: 'auto', effort: 'low', context_size: 'default' });
  assert.deepEqual(changeSettings(s, { mode: 'yolo' }), { ...s, mode: 'yolo' });
  assert.deepEqual(changeSettings(s, { effort: '' }), { ...s, effort: '' });

  assert.deepEqual(createRequest('p1', s, 'r1'), { project_id: 'p1', provider: 'copilot', model: 'gpt-6', effort: 'high', context_size: 'long_context', mode: 'safe', request_id: 'r1' });
  assert.equal(createRequest('p1', { ...s, model: '' }, 'r1').model, undefined);
  assert.ok(!('prompt' in createRequest('p1', s, 'r1')));
});
