import assert from 'node:assert/strict';
import test from 'node:test';
import { applyPick, commandPending, filterCommands, parseCommand, pruneFiles, removeToken, triggerAt } from '../src/lib/composer.ts';

const commands = [
  { name: 'review', description: 'Review the changes', kind: 'command', input_hint: '' },
  { name: 'init', description: 'Create instructions', kind: 'command', input_hint: '' },
  { name: 'release-notes', description: 'Draft release notes from recent commits', kind: 'skill', input_hint: '<range>' },
];

test('/ opens only as the first character and closes at the first space', () => {
  assert.deepEqual(triggerAt('/', 1), { kind: '/', start: 0, end: 1, query: '' });
  assert.deepEqual(triggerAt('/rev', 4), { kind: '/', start: 0, end: 4, query: 'rev' });
  assert.equal(triggerAt('/review ', 8), null);
  assert.equal(triggerAt('/review fo', 10), null);
  assert.equal(triggerAt('see /rev', 8), null);
  assert.equal(triggerAt(' /rev', 5), null);
});

test('@ opens at the start or after whitespace, never inside a word', () => {
  assert.deepEqual(triggerAt('@', 1), { kind: '@', start: 0, end: 1, query: '' });
  assert.deepEqual(triggerAt('look at @doc', 12), { kind: '@', start: 8, end: 12, query: 'doc' });
  assert.deepEqual(triggerAt('a\n@x', 4), { kind: '@', start: 2, end: 4, query: 'x' });
  assert.equal(triggerAt('mail a@b.c', 10), null);
  assert.equal(triggerAt('@docs/web.md done', 17), null);
});

test('the picker follows the caret, not the end of the text', () => {
  assert.deepEqual(triggerAt('@do rest', 3), { kind: '@', start: 0, end: 3, query: 'do' });
  assert.equal(triggerAt('@do rest', 8), null);
});

test('picking replaces the token and adds one space unless one follows', () => {
  assert.deepEqual(applyPick('/rev', triggerAt('/rev', 4), '/review'), { text: '/review ', caret: 8 });
  assert.deepEqual(applyPick('@do rest', triggerAt('@do rest', 3), '@docs/web.md'), { text: '@docs/web.md rest', caret: 13 });
  assert.deepEqual(applyPick('see @', triggerAt('see @', 5), '@Makefile'), { text: 'see @Makefile ', caret: 14 });
});

test('only a listed command runs as one; the rest of the text is its arguments', () => {
  assert.deepEqual(parseCommand('/review', commands), { name: 'review', args: '' });
  assert.deepEqual(parseCommand('/review  the last commit ', commands), { name: 'review', args: 'the last commit' });
  assert.deepEqual(parseCommand('/release-notes v1..v2\nbe brief', commands), { name: 'release-notes', args: 'v1..v2\nbe brief' });
  assert.equal(parseCommand('/reviews', commands), null);
  assert.equal(parseCommand('/unknown thing', commands), null);
  assert.equal(parseCommand('run /review', commands), null);
  assert.equal(parseCommand('/', commands), null);
});

test('a /name text waits for the command list, never for a loaded or failed one', () => {
  assert.equal(commandPending('/review', null, null), true);
  assert.equal(commandPending('/review the last commit', null, null), true);
  assert.equal(commandPending(' /nope ', null, null), true);
  assert.equal(commandPending('/', null, null), false);
  assert.equal(commandPending('run /review', null, null), false);
  assert.equal(commandPending('/review', commands, null), false);
  assert.equal(commandPending('/review', [], null), false);
  assert.equal(commandPending('/review', null, 'the provider conversation is not open'), false);
});

test('filtering ranks a name prefix first, then a name or description match', () => {
  assert.deepEqual(filterCommands(commands, '').map((c) => c.name), ['review', 'init', 'release-notes']);
  assert.deepEqual(filterCommands(commands, 're').map((c) => c.name), ['review', 'release-notes', 'init']);
  assert.deepEqual(filterCommands(commands, 'rel').map((c) => c.name), ['release-notes']);
  assert.deepEqual(filterCommands(commands, 'notes').map((c) => c.name), ['release-notes']);
  assert.deepEqual(filterCommands(commands, 'COMMITS').map((c) => c.name), ['release-notes']);
  assert.deepEqual(filterCommands(commands, 'zzz'), []);
});

test('a file chip lives as long as its @token; removing the chip removes the token', () => {
  const files = ['docs/web.md', 'internal/vterm/redraw.go'];
  assert.deepEqual(pruneFiles('read @docs/web.md and @internal/vterm/redraw.go', files), files);
  assert.deepEqual(pruneFiles('read @docs/web.md', files), ['docs/web.md']);
  assert.deepEqual(pruneFiles('read @docs/web.mdx', files), []);
  assert.deepEqual(pruneFiles('x@docs/web.md', files), []);
  assert.equal(removeToken('read @docs/web.md and more', 'docs/web.md'), 'read and more');
  assert.equal(removeToken('@docs/web.md', 'docs/web.md'), '');
  assert.equal(removeToken('a\n@docs/web.md', 'docs/web.md'), 'a\n');
  assert.equal(removeToken('@docs/web.md.bak', 'docs/web.md'), '@docs/web.md.bak');
});
