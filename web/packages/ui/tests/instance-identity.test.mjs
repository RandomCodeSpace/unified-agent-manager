import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeEntity, encodeEntity, hasIdentity, identityHash, resolveIdentity } from '../src/lib/instanceIdentity.ts';

const b = { id: 'connection-b', instance_id: 'instance-b', generation: 3, enabled: true };
const c = { ...b, id: 'connection-c', instance_id: 'instance-c' };
const hash = identityHash('#task=same-task', b, 'home-a');

test('legacy entities and hash routes stay unchanged', () => {
  assert.equal(encodeEntity(null, 'same-task'), 'same-task');
  assert.deepEqual(decodeEntity('same-task'), { connectionId: null, entityId: 'same-task' });
  for (const path of ['', '#', '#settings', '#task=same-task', '#planner=p&view=board', '#routines=r']) {
    assert.equal(identityHash(path, null, 'home-a'), path);
    assert.deepEqual(resolveIdentity(path, 'home-a', [b, c]), { connection: null, path });
  }
});

test('colliding remote entity IDs are source qualified with unambiguous escaping', () => {
  assert.notEqual(encodeEntity(b.id, 'same-task'), encodeEntity(c.id, 'same-task'));
  assert.notEqual(encodeEntity(b.id, 'same-task'), encodeEntity(null, 'same-task'));
  assert.deepEqual(decodeEntity(encodeEntity('host:with/slash', 'task:with/% snow')), { connectionId: 'host:with/slash', entityId: 'task:with/% snow' });
  for (const value of ['', 'uam:', 'uam:host:', 'uam::task', 'uam:host:task:extra', 'uam:host:%', 'uam:host:%00task', 'uam:host:raw/slash']) assert.equal(decodeEntity(value), null, value);
});

test('qualified links resolve only the exact saved owner and preserve route bytes', () => {
  assert.equal(hash, '#task=same-task&home=home-a&instance=instance-b&connection=connection-b&generation=3');
  assert.deepEqual(resolveIdentity(hash, 'home-a', [b, c]), { connection: b, path: '#task=same-task' });
  for (const path of ['#settings', '#planner=p%2Fq&view=board', '#routines=r']) {
    assert.deepEqual(resolveIdentity(identityHash(path, b, 'home-a'), 'home-a', [b]), { connection: b, path });
  }
  assert.equal(identityHash(hash, c, 'home-a'), '#task=same-task&home=home-a&instance=instance-c&connection=connection-c&generation=3');
  assert.equal(identityHash(hash, null, 'home-a'), '#task=same-task');
});

test('foreign home, removed, disabled and replaced owners fail closed', () => {
  for (const [home, connections] of [
    ['other-home', [b]], ['home-a', []], ['home-a', [{ ...b, enabled: false }]],
    ['home-a', [{ ...b, instance_id: 'replacement' }]],
  ]) assert.ok('error' in resolveIdentity(hash, home, connections));
});

test('a stale generation still resolves to the connection at its current generation', () => {
  const current = { ...b, generation: 4 };
  assert.deepEqual(resolveIdentity(hash, 'home-a', [current]), { connection: current, path: '#task=same-task' });
});

test('partial, duplicate, malformed and unsafe generation ownership cannot become local routes', () => {
  const invalid = [
    '#task=same-task&connection=connection-b', hash + '&connection=connection-c', hash + '&%68ome=home-a',
    hash.replace('instance-b', ''), hash.replace('instance-b', '%'), hash.replace('generation=3', 'generation=-1'),
    hash.replace('generation=3', 'generation=03'), hash.replace('generation=3', 'generation=3.1'),
    hash.replace('generation=3', 'generation=9007199254740992'), 'https://other.example/' + hash,
  ];
  for (const candidate of invalid) assert.ok('error' in resolveIdentity(candidate, 'home-a', [b, c]), candidate);
});


test('cold-route detection shares decoded ownership parsing with route validation', () => {
  const escaped = hash.replace('home=', '%68ome=').replace('instance=', '%69nstance=').replace('connection=', '%63onnection=').replace('generation=', '%67eneration=');
  assert.equal(hasIdentity(escaped), true);
  assert.deepEqual(resolveIdentity(escaped, 'home-a', [b]), { connection: b, path: '#task=same-task' });
  assert.equal(hasIdentity('#task=same-task&%63onnection=connection-b'), true);
  assert.equal(hasIdentity('#task=same-task&%='), true);
  for (const path of ['', '#settings', '#task=same-task', '#routines=all']) assert.equal(hasIdentity(path), false);
});
