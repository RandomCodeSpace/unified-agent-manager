import assert from 'node:assert/strict';
import test from 'node:test';
import { selectionAfterWritingEdit } from '../src/lib/writing-edit.ts';

test('corrections retain selection before them and shift a later caret by the length change', () => {
  assert.deepEqual(selectionAfterWritingEdit({ start: 1, end: 2, direction: 'backward' }, 4, 6, 3), { start: 1, end: 2, direction: 'backward' });
  assert.deepEqual(selectionAfterWritingEdit({ start: 15, end: 15, direction: 'none' }, 4, 6, 3), { start: 16, end: 16, direction: 'none' });
  assert.deepEqual(selectionAfterWritingEdit({ start: 5, end: 10, direction: 'forward' }, 4, 6, 0), { start: 4, end: 8, direction: 'forward' });
});

test('selection offsets remain UTF-16 offsets with emoji before the correction', () => {
  const text = '🙂 There is two checks.';
  const start = text.indexOf('is');
  assert.deepEqual(selectionAfterWritingEdit({ start: text.length, end: text.length, direction: 'none' }, start, start + 2, 3), { start: text.length + 1, end: text.length + 1, direction: 'none' });
});
