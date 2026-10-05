import assert from 'node:assert/strict';
import test from 'node:test';
import { answerFromComposer, answerPlaceholder, canAnswer, recommendedChoice } from '../src/lib/answer.ts';

const both = { custom: true };
const optionsOnly = { custom: false };

test('the answer is the typed text or the staged options, never both and nothing beside them', () => {
  assert.deepEqual(answerFromComposer(both, '', ['Retry once']), [['Retry once']]);
  assert.deepEqual(answerFromComposer(both, '', ['npm', 'yarn']), [['npm', 'yarn']]);
  assert.deepEqual(answerFromComposer(both, ' Once, then fail. ', []), [['Once, then fail.']]);
  assert.deepEqual(answerFromComposer(both, 'bun', ['npm']), [['bun']]);
  assert.equal(answerFromComposer(both, '   ', []), null);
});

test('a question without free text takes only an option', () => {
  assert.equal(answerFromComposer(optionsOnly, 'npm please', []), null);
  assert.deepEqual(answerFromComposer(optionsOnly, 'npm please', ['pnpm']), [['pnpm']]);
  assert.equal(answerFromComposer(optionsOnly, '', []), null);
});

test('the result never shares arrays with the input', () => {
  const staged = ['npm'];
  const out = answerFromComposer(both, '', staged);
  out[0].push('extra');
  assert.deepEqual(staged, ['npm']);
});

test('Answer is enabled by a staged option, or by typed text where free text is allowed', () => {
  assert.equal(canAnswer(both, [], ''), false);
  assert.equal(canAnswer(both, [], '  '), false);
  assert.equal(canAnswer(both, [], 'yes'), true);
  assert.equal(canAnswer(optionsOnly, [], 'yes'), false);
  assert.equal(canAnswer(optionsOnly, ['npm'], ''), true);
});

test('the placeholder invites a typed answer, which replaces a staged option', () => {
  assert.equal(answerPlaceholder(both, false), 'Type your answer…');
  assert.equal(answerPlaceholder(both, true), 'Or type your own answer…');
  assert.equal(answerPlaceholder(optionsOnly, false), 'Choose an option above…');
});

test('the recommended option is the first whose label ends with "(Recommended)", in any case', () => {
  assert.equal(recommendedChoice(['Arcade / reflex game (Recommended)', 'Puzzle']), 'Arcade / reflex game (Recommended)');
  assert.equal(recommendedChoice(['npm', 'pnpm (recommended)  ', 'yarn (RECOMMENDED)']), 'pnpm (recommended)  ');
  assert.equal(recommendedChoice(['Recommended defaults', '(Recommended) first', 'npm']), undefined);
  assert.equal(recommendedChoice([]), undefined);
  assert.equal(recommendedChoice(undefined), undefined);
});
