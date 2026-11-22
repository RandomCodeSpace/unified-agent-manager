import assert from 'node:assert/strict';
import test from 'node:test';
import { citedNumbers, citedSpans, citesReferences, gitLabKind, mismatched, referenceName, referenceSummary, remarkReferences } from '../src/lib/references.ts';

const ref = (number, extra = {}) => ({ label: `issue ${number}`, noun: 'issue', number: String(number), status: 'resolved', title: `T${number}`, state: 'open', claimed: '', ...extra });

test('only a reply that cites a numbered item asks the service', () => {
  assert.equal(citesReferences('Issues 4, 8 and 17 are open.'), true);
  assert.equal(citesReferences('See #4.'), true);
  assert.equal(citesReferences('MR !7 merged'), true);
  assert.equal(citesReferences('findings 1-4 fixed'), true);
  assert.equal(citesReferences('Go 3 is faster than step-by-step'), false);
  assert.equal(citesReferences('the color #fff'), false);
  assert.equal(citesReferences('build#139'), false);
});

test('the summary counts recorded states, unresolved and ambiguous as unknown', () => {
  const refs = [ref(4), ref(8), ref(5, { state: 'closed' }), ref(6, { state: 'Closed' }), ref(31, { status: 'unresolved', state: '', title: '' }), ref(9, { status: 'ambiguous', state: '' })];
  assert.equal(referenceSummary(refs), '6 issues · 2 open · 2 closed · 2 unknown');
  assert.equal(referenceSummary([ref(4)]), '1 issue · 1 open');
  assert.equal(referenceSummary([ref(4), ref(3, { noun: 'finding', label: 'finding 3', state: '' })]), '2 references · 1 open');
  assert.equal(referenceSummary([ref(7, { noun: 'mr', label: '!7', state: 'merged' }), ref(8, { noun: 'mr', label: 'MR 8', state: 'merged' })]), '2 MRs · 2 merged');
});

test('a mismatch is the reply’s state word against a recorded one', () => {
  assert.equal(mismatched(ref(5, { claimed: 'closed' })), true);
  assert.equal(mismatched(ref(5, { claimed: 'Open' })), false);
  assert.equal(mismatched(ref(5, { claimed: '' })), false);
  assert.equal(mismatched(ref(31, { status: 'unresolved', state: '', claimed: 'open' })), false);
});

test('rows name an issue by its number and anything else by the reply’s label', () => {
  assert.equal(referenceName(ref(4)), '#4');
  assert.equal(referenceName(ref(7, { noun: 'mr', label: 'MR 7' })), '!7');
  assert.equal(referenceName(ref(7, { noun: '', label: '!7' })), '!7');
  assert.equal(referenceName(ref(3, { noun: 'finding', label: 'finding 3' })), 'finding 3');
  assert.equal(gitLabKind(ref(4)), 'issue');
  assert.equal(gitLabKind(ref(7, { noun: '', label: '!7' })), 'mr');
  assert.equal(gitLabKind(ref(3, { noun: 'finding', label: 'finding 3' })), undefined);
});

const cited = citedNumbers([ref(4), ref(8), ref(9), ref(17), ref(5), ref(6), ref(31)]);
const words = (text) => citedSpans(text, cited).map((s) => text.slice(s.start, s.end));

test('the prose marks prefixed numbers, numbers after an item noun and the lists they head', () => {
  assert.deepEqual(words('Issues 4, 8, 9 and 17 are still open; 5 and 6 are closed. Issue 31 came up last week.'), ['4', '8', '9', '17', '5', '6', '31']);
  assert.deepEqual(words('See #4 and !8.'), ['#4', '!8']);
});

test('a lone bare number, a version or a time stays text', () => {
  assert.deepEqual(words('It took 5 minutes on 4.8 at 9:17.'), []);
  assert.deepEqual(words('Retry in 5 seconds, then 31% later.'), []);
  assert.deepEqual(words('Issue 12 is new.'), []);
});

test('the Markdown pass wraps cited numbers in prose and leaves code and links alone', () => {
  const tree = { type: 'root', children: [
    { type: 'paragraph', children: [{ type: 'text', value: 'Issues 4 and 8 are open; ' }, { type: 'inlineCode', value: 'issue 4' }, { type: 'link', url: 'https://x', children: [{ type: 'text', value: 'issue 8' }] }] },
  ] };
  remarkReferences(cited)()(tree);
  const para = tree.children[0];
  const cites = para.children.filter((n) => n.type === 'referenceCite');
  assert.deepEqual(cites.map((n) => [n.children[0].value, n.data.hProperties.dataReference]), [['4', '0'], ['8', '1']]);
  assert.equal(para.children.find((n) => n.type === 'inlineCode').value, 'issue 4');
  assert.deepEqual(para.children.find((n) => n.type === 'link').children, [{ type: 'text', value: 'issue 8' }]);
});
