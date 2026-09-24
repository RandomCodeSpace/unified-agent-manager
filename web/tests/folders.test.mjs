import assert from 'node:assert/strict';
import test from 'node:test';
import { breadcrumbs, cleanPath, listingError, matchFrom, parentOf } from '../src/lib/folders.ts';

test('a typed path is cleaned to the form the server accepts', () => {
  assert.equal(cleanPath(' /home/dev/ '), '/home/dev');
  assert.equal(cleanPath('//home//dev///'), '/home/dev');
  assert.equal(cleanPath('/home/dev/./projects/../x'), '/home/dev/x');
  assert.equal(cleanPath('/../..'), '/');
  assert.equal(cleanPath('/'), '/');
  assert.equal(cleanPath(''), '');
  assert.equal(cleanPath('  relative/path '), 'relative/path');
});

test('breadcrumbs start at the root and accumulate one segment at a time', () => {
  assert.deepEqual(breadcrumbs('/'), [{ name: '/', path: '/' }]);
  assert.deepEqual(breadcrumbs('/home/dev'), [
    { name: '/', path: '/' },
    { name: 'home', path: '/home' },
    { name: 'dev', path: '/home/dev' },
  ]);
  assert.deepEqual(breadcrumbs(''), [{ name: '/', path: '/' }]);
});

test('the parent of a path stops at the root', () => {
  assert.equal(parentOf('/home/dev'), '/home');
  assert.equal(parentOf('/home'), '/');
  assert.equal(parentOf('/'), undefined);
  assert.equal(parentOf(''), undefined);
});

test('listing errors are plain words for the known statuses', () => {
  assert.equal(listingError(403, 'permission denied'), "You don't have access to this folder");
  assert.equal(listingError(404, 'path does not exist'), 'This folder does not exist');
  assert.equal(listingError(400, 'path is not a directory'), 'This is not a folder');
  assert.equal(listingError(0, 'Could not reach the server'), 'Could not reach the server');
});

test('type-ahead finds the next match after the current row, wrapping and ignoring case', () => {
  const names = ['Alpha', 'beta', 'Bravo', 'gamma'];
  assert.equal(matchFrom(names, -1, 'b'), 1);
  assert.equal(matchFrom(names, 1, 'b'), 2);
  assert.equal(matchFrom(names, 2, 'b'), 1);
  assert.equal(matchFrom(names, 1, 'br'), 2);
  assert.equal(matchFrom(names, 0, 'br'), 2);
  assert.equal(matchFrom(names, 3, 'a'), 0);
  assert.equal(matchFrom(names, 0, 'z'), -1);
  assert.equal(matchFrom([], 0, 'a'), -1);
  assert.equal(matchFrom(names, 0, ''), -1);
});
