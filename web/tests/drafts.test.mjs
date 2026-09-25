import assert from 'node:assert/strict';
import test from 'node:test';
import { DRAFT_PREFIX, draftKey, parseDraft, serializeDraft, staleDraftKeys } from '../src/lib/drafts.ts';

const full = { text: 'Fix @docs/web.md', files: ['docs/web.md'], attachments: [{ id: 'att-1', name: 'shot.png', size: 2048, kind: 'image' }] };

test('a draft round-trips through storage; an empty one serializes to nothing', () => {
  assert.equal(draftKey('t1'), `${DRAFT_PREFIX}t1`);
  assert.deepEqual(parseDraft(serializeDraft(full)), full);
  assert.equal(serializeDraft({ text: '  \n', files: [], attachments: [] }), null);
  assert.ok(serializeDraft({ text: '', files: ['a.go'], attachments: [] }));
  assert.ok(serializeDraft({ text: '', files: [], attachments: full.attachments }));
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
  assert.deepEqual(staleDraftKeys(keys, ['t1', 't2']), [`${DRAFT_PREFIX}gone`]);
  assert.deepEqual(staleDraftKeys(keys, []), [`${DRAFT_PREFIX}t1`, `${DRAFT_PREFIX}gone`]);
  assert.deepEqual(staleDraftKeys([], ['t1']), []);
});
