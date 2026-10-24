import assert from 'node:assert/strict';
import test from 'node:test';
import { exitCode } from '../src/lib/terminal.ts';

test('only an exit frame with an integer code is an exit', () => {
  assert.equal(exitCode('{"type":"exit","code":0}'), 0);
  assert.equal(exitCode('{"type":"exit","code":130}'), 130);
  assert.equal(exitCode('{"type":"exit"}'), null);
  assert.equal(exitCode('{"type":"resize","cols":80,"rows":24}'), null);
  assert.equal(exitCode('not json'), null);
});
