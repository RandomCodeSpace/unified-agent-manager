import assert from 'node:assert/strict';
import test from 'node:test';
import { foregroundRead } from '../src/lib/reads.ts';

test('foreground reads share two slots and cancelled queued work never starts', async () => {
  let active = 0, maximum = 0;
  const releases = [];
  const read = () => new Promise(resolve => { maximum = Math.max(maximum, ++active); releases.push(() => { active--; resolve('done'); }); });
  const first = foregroundRead(read), second = foregroundRead(read);
  const controller = new AbortController();
  let cancelledStarted = false;
  const cancelled = foregroundRead(async () => { cancelledStarted = true; }, controller.signal);
  const rejected = assert.rejects(cancelled, { name: 'AbortError' });
  const fourth = foregroundRead(read);
  assert.equal(releases.length, 2);
  controller.abort();
  releases.shift()();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(releases.length, 2);
  assert.equal(cancelledStarted, false);
  releases.splice(0).forEach(release => release());
  await Promise.all([first, second, fourth, rejected]);
  assert.equal(maximum, 2);
});

test('queued history/body reads precede resolver reads without preempting active work', async () => {
  const releases = [], starts = [];
  const read = name => () => new Promise(resolve => { starts.push(name); releases.push(resolve); });
  const first = foregroundRead(read('first')), second = foregroundRead(read('second'));
  const resolver = foregroundRead(read('resolver'), undefined, 'low');
  const history = foregroundRead(read('history'));
  assert.deepEqual(starts, ['first', 'second']);
  releases.shift()(); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(starts, ['first', 'second', 'history']);
  releases.shift()(); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(starts, ['first', 'second', 'history', 'resolver']);
  releases.splice(0).forEach(release => release());
  await Promise.all([first, second, resolver, history]);
});
