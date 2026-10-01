import assert from 'node:assert/strict';
import test from 'node:test';
import { byDay, dayLabel, dayText, durationText, mergeCalls, outcomeLabel, purposeLabel, tokenText } from '../src/lib/utility.ts';

const call = (id, day = '2026-10-01') => ({ id, day, at: `${day}T10:00:00+02:00`, purpose: 'title', prompt_chars: 10, reply_chars: 3, input_tokens: 1, output_tokens: 1, duration_ms: 5, outcome: 'ok' });

test('purposes and outcomes read as words', () => {
  assert.equal(purposeLabel('title'), 'Task title');
  assert.equal(purposeLabel('planner-suggest'), 'Planner suggestion');
  assert.equal(purposeLabel('commit-message'), 'Commit message');
  assert.equal(purposeLabel('some-new-job'), 'some new job');
  assert.equal(outcomeLabel({ outcome: 'skipped', reason: 'daily_limit' }), 'Skipped: daily limit');
  assert.equal(outcomeLabel({ outcome: 'skipped', reason: 'off' }), 'Skipped: Background AI off');
  assert.equal(outcomeLabel({ outcome: 'error', reason: 'boom' }), 'Failed');
  assert.equal(outcomeLabel({ outcome: 'ok' }), '');
});

test('estimated tokens are marked, durations and day totals are short', () => {
  assert.equal(tokenText({ input_tokens: 1200, output_tokens: 12, estimated: true }), '≈1.2K in, 12 out tokens');
  assert.equal(tokenText({ input_tokens: 90, output_tokens: 5 }), '90 in, 5 out tokens');
  assert.equal(durationText(850), '850 ms');
  assert.equal(durationText(1234), '1.2 s');
  assert.equal(durationText(75000), '75 s');
  assert.equal(dayText({ day: 'd', calls: 1, errors: 0, skipped: 2, prompt_chars: 0, reply_chars: 0, input_tokens: 90, output_tokens: 5, credits: 0.002 }), '1 call · 2 skipped · 90 in, 5 out tokens · <0.01 credits');
  assert.equal(dayText({ day: 'd', calls: 0, errors: 0, skipped: 3, prompt_chars: 0, reply_chars: 0, input_tokens: 0, output_tokens: 0 }), '0 calls · 3 skipped');
});

test('days are named against the server day', () => {
  assert.equal(dayLabel('2026-10-01', '2026-10-01'), 'Today');
  assert.equal(dayLabel('2026-09-30', '2026-10-01'), 'Yesterday');
  assert.equal(dayLabel('2026-02-28', '2026-03-01'), 'Yesterday');
  assert.match(dayLabel('2026-09-28', '2026-10-01'), /28 Sept?/);
});

test('a refresh puts new calls on top and keeps the older pages already shown', () => {
  const shown = [call(5), call(4), call(3)];
  assert.deepEqual(mergeCalls(shown, 3, [call(7), call(6), call(5)], 5).calls.map((c) => c.id), [7, 6, 5, 4, 3]);
  assert.equal(mergeCalls(shown, 3, [call(7), call(6), call(5)], 5).next, 3);
  // More new calls than a page: the newest page replaces what was shown, and pages on from its own cursor.
  assert.deepEqual(mergeCalls(shown, 3, [call(9), call(8)], 8), { calls: [call(9), call(8)], next: 8 });
  assert.deepEqual(mergeCalls([], undefined, [call(1)], undefined), { calls: [call(1)], next: undefined });
  assert.deepEqual(byDay([call(3), call(2), call(1, '2026-09-30')]).map((g) => [g.day, g.calls.length]), [['2026-10-01', 2], ['2026-09-30', 1]]);
});
