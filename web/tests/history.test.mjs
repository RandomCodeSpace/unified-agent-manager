import assert from 'node:assert/strict';
import test from 'node:test';
import { historyEntries, historyKey, onFirstLine, onLastLine } from '../src/lib/history.ts';

const items = [
  { kind: 'user', text: 'first\nline two' },
  { kind: 'assistant', text: 'reply' },
  { kind: 'user', text: 'second' },
  { kind: 'user', text: '   ' },
  { kind: 'user', text: 'second' },
  { kind: 'tool' },
  { kind: 'user', text: 'third' },
];
const entries = historyEntries(items, undefined);

// Runs the keys in order from a fresh state, returning the final text and where browsing ended.
function drive(keys, start = '', list = entries, caret = (t) => t.length) {
  let browsing = null;
  let text = start;
  const steps = [];
  for (const key of keys) {
    const step = historyKey(browsing, list, text, caret(text), key);
    steps.push(step ? step.text : null);
    if (!step) continue;
    ({ browsing, text } = step);
  }
  return { text, browsing, steps };
}

test('entries are user messages newest first, blanks dropped, consecutive repeats collapsed, queue newest of all', () => {
  assert.deepEqual(entries, ['third', 'second', 'first\nline two']);
  assert.deepEqual(historyEntries(items, [{ text: 'queued' }]), ['queued', 'third', 'second', 'first\nline two']);
  assert.deepEqual(historyEntries([{ kind: 'user', text: 'a' }, { kind: 'user', text: 'b' }, { kind: 'user', text: 'a' }], []), ['a', 'b', 'a']);
  assert.deepEqual(historyEntries(undefined, undefined), []);
});

test('Up walks back through history and stops at the oldest', () => {
  const { text, browsing, steps } = drive(['ArrowUp', 'ArrowUp', 'ArrowUp', 'ArrowUp']);
  // The fourth Up lands on line two of the oldest entry, so the browser keeps it.
  assert.deepEqual(steps, ['third', 'second', 'first\nline two', null]);
  assert.equal(text, 'first\nline two');
  // From its first line, Up at the oldest entry is consumed without change.
  assert.deepEqual(historyKey(browsing, entries, text, 0, 'ArrowUp'), { browsing, text });
});

test('Down walks forward and one step past the newest restores the draft', () => {
  const { text, browsing, steps } = drive(['ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown'], 'my draft');
  assert.deepEqual(steps, ['third', 'second', 'third', 'my draft']);
  assert.equal(text, 'my draft');
  assert.equal(browsing, null);
});

test('Escape while browsing restores the draft; otherwise it is not ours', () => {
  const { text, browsing } = drive(['ArrowUp', 'ArrowUp', 'Escape'], 'kept');
  assert.equal(text, 'kept');
  assert.equal(browsing, null);
  assert.equal(historyKey(null, entries, 'kept', 4, 'Escape'), null);
});

test('Up with nothing to recall and Down when not browsing leave the key to the browser', () => {
  assert.equal(historyKey(null, [], '', 0, 'ArrowUp'), null);
  assert.equal(historyKey(null, entries, 'draft', 5, 'ArrowDown'), null);
  assert.equal(historyKey(null, entries, '', 0, 'Enter'), null);
});

test('Up only recalls from the first line and Down only advances from the last line', () => {
  const twoLines = 'one\ntwo';
  assert.equal(historyKey(null, entries, twoLines, twoLines.length, 'ArrowUp'), null);
  assert.ok(historyKey(null, entries, twoLines, 2, 'ArrowUp'));
  const browsing = { index: 2, draft: '', shown: 'first\nline two' };
  assert.equal(historyKey(browsing, entries, 'first\nline two', 2, 'ArrowDown'), null);
  assert.equal(historyKey(browsing, entries, 'first\nline two', 14, 'ArrowDown').text, 'second');
  assert.equal(onFirstLine('a\nb', 1), true);
  assert.equal(onFirstLine('a\nb', 3), false);
  assert.equal(onLastLine('a\nb', 3), true);
  assert.equal(onLastLine('a\nb', 1), false);
});

test('editing a recalled entry makes it the new draft', () => {
  const first = historyKey(null, entries, 'old draft', 9, 'ArrowUp');
  const edited = `${first.text}!`;
  const next = historyKey(first.browsing, entries, edited, edited.length, 'ArrowUp');
  assert.equal(next.text, 'third');
  assert.equal(next.browsing.draft, edited);
  assert.equal(next.browsing.index, 0);
  assert.equal(historyKey(first.browsing, entries, edited, edited.length, 'Escape'), null);
});

test('the last prompt is the newest user message with text; steer messages count, blanks and tool rows do not', async () => {
  const { lastPrompt } = await import('../src/lib/history.ts');
  assert.equal(lastPrompt(items)?.text, 'third');
  assert.equal(lastPrompt([{ kind: 'user', text: 'a' }, { kind: 'user', text: 'b', delivery: 'steer' }, { kind: 'tool' }, { kind: 'user', text: ' ' }])?.text, 'b');
  assert.equal(lastPrompt([{ kind: 'assistant', text: 'x' }]), null);
  assert.equal(lastPrompt(undefined), null);
  assert.equal(lastPrompt([]), null);
});
