import assert from 'node:assert/strict';
import test from 'node:test';
import { fusionRoute, phaseCaption } from '../src/lib/fusion.ts';

const name = (id) => ({ 'claude-opus-5.5': 'Claude Opus 5.5', 'gpt-6-luna': 'GPT-6 Luna' })[id] ?? id;

test('the turn head names Fusion\'s level and pattern, or its one model', () => {
  assert.equal(fusionRoute({ level: 'high', pattern: 'critique', models: ['claude-opus-5.5', 'gpt-6-luna'], phases: [] }, name), 'Fusion High · Critique · 2 models');
  assert.equal(fusionRoute({ level: 'max', pattern: 'single', models: ['claude-opus-5.5'], phases: [] }, name), 'Fusion Max · Claude Opus 5.5');
  // An unknown level and no model yet: Fusion alone.
  assert.equal(fusionRoute({ level: 'turbo', pattern: 'single', phases: [] }, name), 'Fusion');
});

test('a phase says what it does, a reviewer its verdict, and a failure its reason', () => {
  assert.equal(phaseCaption({ kind: 'revision', status: 'planned', conditional: true }), 'Runs only if the review asks for changes');
  assert.equal(phaseCaption({ kind: 'draft', status: 'running' }), 'Working in the conversation');
  assert.equal(phaseCaption({ kind: 'critic', status: 'running' }), 'Reviewing');
  assert.equal(phaseCaption({ kind: 'critic', status: 'succeeded', verdict: 'approve', note: 'No defect.' }), 'Approved: No defect.');
  assert.equal(phaseCaption({ kind: 'judge', status: 'succeeded', verdict: 'revise' }), 'Asked for changes');
  assert.equal(phaseCaption({ kind: 'draft', status: 'succeeded' }), 'Done');
  assert.equal(phaseCaption({ kind: 'critic', status: 'failed', note: 'timeout' }), 'timeout');
});
