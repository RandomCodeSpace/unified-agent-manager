import assert from 'node:assert/strict';
import test from 'node:test';
import { exitCode, terminalUrl } from '../src/lib/terminal.ts';

test('the terminal socket follows the page scheme and host and carries the first size', () => {
  assert.equal(terminalUrl({ protocol: 'http:', host: '127.0.0.1:5312' }, 'p1', 80, 24), 'ws://127.0.0.1:5312/api/projects/p1/terminal?cols=80&rows=24');
  assert.equal(terminalUrl({ protocol: 'https:', host: 'example.test' }, 'a b/c', 120, 40), 'wss://example.test/api/projects/a%20b%2Fc/terminal?cols=120&rows=40');
});

test('only an exit frame with an integer code is an exit', () => {
  assert.equal(exitCode('{"type":"exit","code":0}'), 0);
  assert.equal(exitCode('{"type":"exit","code":130}'), 130);
  assert.equal(exitCode('{"type":"exit"}'), null);
  assert.equal(exitCode('{"type":"resize","cols":80,"rows":24}'), null);
  assert.equal(exitCode('not json'), null);
});
