import assert from 'node:assert/strict';
import test from 'node:test';
import { ALONGSIDE_TEXT, answerFromComposer, answerPlaceholder, canAnswer } from '../src/lib/answer.ts';

const both = { custom: true };
const optionsOnly = { custom: false };
const input = (over = {}) => ({ text: '', staged: [], files: [], attachments: [], ...over });

test('staged options are the answer; typed text beside them rides alongside as a steer', () => {
  assert.deepEqual(answerFromComposer(both, input({ staged: ['Retry once'] })), { answers: [['Retry once']], alongside: null });
  assert.deepEqual(answerFromComposer(both, input({ staged: ['npm', 'yarn'], text: '  and keep the lockfile ' })), {
    answers: [['npm', 'yarn']],
    alongside: { text: 'and keep the lockfile', files: [], attachments: [] },
  });
});

test('without a staged option the typed text answers, only where the question allows free text', () => {
  assert.deepEqual(answerFromComposer(both, input({ text: ' Once, then fail. ' })), { answers: [['Once, then fail.']], alongside: null });
  assert.equal(answerFromComposer(optionsOnly, input({ text: 'npm please' })), null);
  assert.equal(answerFromComposer(both, input({ text: '   ' })), null);
  assert.equal(answerFromComposer(optionsOnly, input()), null);
});

test('files never answer: they go alongside, with the typed note or the default text', () => {
  assert.deepEqual(answerFromComposer(both, input({ staged: ['npm'], attachments: ['att-1'], files: ['docs/web.md'] })), {
    answers: [['npm']],
    alongside: { text: ALONGSIDE_TEXT, files: ['docs/web.md'], attachments: ['att-1'] },
  });
  assert.deepEqual(answerFromComposer(both, input({ text: 'See the screenshot', attachments: ['att-1'] })), {
    answers: [['See the screenshot']],
    alongside: { text: ALONGSIDE_TEXT, files: [], attachments: ['att-1'] },
  });
  assert.deepEqual(answerFromComposer(optionsOnly, input({ staged: ['pnpm'], text: 'the CI uses it', attachments: ['att-2'] })), {
    answers: [['pnpm']],
    alongside: { text: 'the CI uses it', files: [], attachments: ['att-2'] },
  });
});

test('the result never shares arrays with the input', () => {
  const staged = ['npm'];
  const files = ['a.go'];
  const out = answerFromComposer(both, input({ staged, files, text: 'x' }));
  out.answers[0].push('extra');
  out.alongside.files.push('b.go');
  assert.deepEqual(staged, ['npm']);
  assert.deepEqual(files, ['a.go']);
});

test('Answer is enabled by a staged option, or by typed text where free text is allowed', () => {
  assert.equal(canAnswer(both, [], ''), false);
  assert.equal(canAnswer(both, [], '  '), false);
  assert.equal(canAnswer(both, [], 'yes'), true);
  assert.equal(canAnswer(optionsOnly, [], 'yes'), false);
  assert.equal(canAnswer(optionsOnly, ['npm'], ''), true);
});

test('the placeholder says whether typed text answers or annotates', () => {
  assert.equal(answerPlaceholder(both, false), 'Type your answer…');
  assert.equal(answerPlaceholder(both, true), 'Add a note (sent with your answer)…');
  assert.equal(answerPlaceholder(optionsOnly, false), 'Add a note (sent with your answer)…');
});
