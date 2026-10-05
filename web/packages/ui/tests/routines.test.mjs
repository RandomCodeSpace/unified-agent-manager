import assert from 'node:assert/strict';
import test from 'node:test';
import { clockText, describeSchedule, outcomeLabel, outcomeTone, routineMode, routineModeInput, routineModeLabel, scheduleOf, untilText } from '../src/lib/routines.ts';

test('a schedule reads in plain words', () => {
  assert.equal(describeSchedule({ kind: 'weekdays', time: '09:00' }, 'en-GB'), 'Every weekday at 09:00');
  assert.equal(describeSchedule({ kind: 'daily', time: '18:30' }, 'en-GB'), 'Every day at 18:30');
  assert.equal(describeSchedule({ kind: 'weekly', time: '08:00', weekday: 1 }, 'en-GB'), 'Every Monday at 08:00');
  // The time reads as the form's time field shows it in that locale.
  assert.match(describeSchedule({ kind: 'daily', time: '18:30' }, 'en-US'), /^Every day at 06:30\sPM$/);
  assert.match(clockText('09:00', 'en-US'), /^09:00\sAM$/);
  assert.equal(clockText('bad', 'en-GB'), 'bad');
  assert.equal(describeSchedule({ kind: 'hours', hours: 1 }), 'Every hour');
  assert.equal(describeSchedule({ kind: 'hours', hours: 6 }), 'Every 6 hours');
});

test('the form sends only the fields its kind takes', () => {
  assert.deepEqual(scheduleOf('hours', '09:00', 3, 6), { kind: 'hours', hours: 6 });
  assert.deepEqual(scheduleOf('weekly', '09:00', 3, 6), { kind: 'weekly', time: '09:00', weekday: 3 });
  assert.deepEqual(scheduleOf('weekdays', '07:15', 3, 6), { kind: 'weekdays', time: '07:15' });
});

test('a routine’s mode reads as the form offers it, and the form sends both fields', () => {
  assert.equal(routineModeLabel({ mode: 'yolo', autopilot: true }), 'Yolo with autopilot');
  assert.equal(routineModeLabel({ mode: 'yolo', autopilot: false }), 'Yolo');
  assert.equal(routineModeLabel({ mode: 'safe', autopilot: false }), 'Safe');
  assert.equal(routineModeLabel({ mode: 'safe', autopilot: true }), 'Safe with autopilot');
  assert.equal(routineMode({ mode: 'safe', autopilot: true }), 'safe');
  assert.equal(routineMode({ mode: 'yolo', autopilot: true }), 'autopilot');
  assert.deepEqual(routineModeInput('autopilot'), { mode: 'yolo', autopilot: true });
  assert.deepEqual(routineModeInput('yolo'), { mode: 'yolo', autopilot: false });
  assert.deepEqual(routineModeInput('safe'), { mode: 'safe', autopilot: false });
});

test('a running run whose task waits for the owner needs you', () => {
  assert.equal(outcomeLabel({ outcome: 'running' }, 'working'), 'Running');
  assert.equal(outcomeLabel({ outcome: 'running' }, 'awaiting_permission'), 'Needs you');
  assert.equal(outcomeTone({ outcome: 'running' }, 'awaiting_answer'), 'attention');
  assert.equal(outcomeLabel({ outcome: 'time_limit' }), 'Stopped at the time limit');
  assert.equal(outcomeTone({ outcome: 'failed' }), 'error');
  assert.equal(outcomeTone({ outcome: 'skipped' }), 'muted');
});

test('the next run reads as a distance', () => {
  const now = Date.parse('2026-10-01T09:00:00Z');
  assert.equal(untilText('2026-10-01T09:00:20Z', now), 'now');
  assert.equal(untilText('2026-10-01T09:05:00Z', now), 'in 5 min');
  assert.equal(untilText('2026-10-01T12:00:00Z', now), 'in 3 h');
  assert.equal(untilText('2026-10-04T09:00:00Z', now), 'in 3 days');
});
