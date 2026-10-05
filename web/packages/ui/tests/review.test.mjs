import assert from 'node:assert/strict';
import test from 'node:test';
import { byRisk, commentsMessage, parseReview, riskOf, turnFile, serializeReview, staleReviewKeys, statusLetter, viewState } from '../src/lib/review.ts';

test('risky paths get a short reason; ordinary ones none', () => {
  const label = p => riskOf(p)?.label ?? null;
  assert.equal(label('.github/workflows/ci.yml'), 'CI workflow');
  assert.equal(label('.gitlab-ci.yml'), 'CI workflow');
  assert.equal(label('web/package-lock.json'), 'Lockfile');
  assert.equal(label('go.sum'), 'Lockfile');
  assert.equal(label('Dockerfile'), 'Docker');
  assert.equal(label('deploy/Dockerfile.web'), 'Docker');
  assert.equal(label('db/migrations/0003_users.sql'), 'Migration');
  assert.equal(label('deploy/run.sh'), 'Deploy');
  assert.equal(label('infra/main.tf'), 'Deploy');
  assert.equal(label('internal/web/auth.go'), 'Auth/security');
  assert.equal(label('src/lib/crypto.ts'), 'Auth/security');
  assert.equal(label('src/author.ts'), null);
  assert.equal(label('AUTHORS.md'), null);
  assert.equal(label('internal/authorization/policy.go'), 'Auth/security');
  assert.equal(label('middleware/authorize.go'), 'Auth/security');
  assert.equal(label('pkg/authority/ca.go'), 'Auth/security');
  assert.equal(label('.env'), 'Auth/security');
  assert.equal(label('web/.env.production'), 'Auth/security');
  assert.equal(label('.npmrc'), 'Auth/security');
  assert.equal(label('.github/actions/setup/action.yml'), 'CI workflow');
  assert.equal(label('src/environment.ts'), null);
  assert.equal(label('README.md'), null);
  assert.deepEqual(byRisk([{ path: 'a.go' }, { path: 'go.sum' }, { path: 'b.go' }]).map(f => f.path), ['go.sum', 'a.go', 'b.go']);
});

test('review state round-trips, drops malformed parts, and empties to no key', () => {
  const r = parseReview(JSON.stringify({ viewed: { 'a.go': 'd1', bad: 3 }, comments: [{ id: 'c', path: 'a.go', line: 2, side: 'new', code: 'x', body: 'fix' }, { id: 'e', path: 'a.go', line: 2, side: 'new', body: '  ' }] }));
  assert.deepEqual(r.viewed, { 'a.go': 'd1' });
  assert.equal(r.comments.length, 1);
  assert.equal(serializeReview({ viewed: {}, comments: [] }), null);
  assert.deepEqual(parseReview('{nope'), { viewed: {}, comments: [] });
  assert.deepEqual(staleReviewKeys(['uam.review.a', 'uam.review.b', 'uam.draft.b'], ['a']), ['uam.review.b']);
});

test('viewed follows the digest, or status and counts without one', () => {
  const r = { viewed: { 'a.go': 'd1', 'b.go': 'modified:1:0' }, comments: [] };
  assert.equal(viewState(r, { path: 'a.go', status: 'modified', additions: 1, deletions: 0, digest: 'd1' }), 'viewed');
  assert.equal(viewState(r, { path: 'a.go', status: 'modified', additions: 1, deletions: 0, digest: 'd2' }), 'changed');
  assert.equal(viewState(r, { path: 'b.go', status: 'modified', additions: 1, deletions: 0 }), 'viewed');
  assert.equal(viewState(r, { path: 'c.go', status: 'modified', additions: 1, deletions: 0 }), 'unviewed');
  assert.equal(statusLetter('untracked'), 'U');
  assert.equal(statusLetter('M'), 'M');
});

test('one comment reads in the singular', () => {
  assert.equal(commentsMessage([{ id: 'c', path: 'a.go', line: 4, side: 'new', code: '', body: 'Add a test.' }]), 'I reviewed your changes and have 1 comment. Please address it.\n\n1. a.go, line 4:\n   Add a test.');
});


test('a turn\'s edits find their row in Changes, absolute or relative to a folder below the repository root', () => {
  const listed = ['.github/workflows/ci.yml', 'web/src/a.ts', 'docs/terminal.md'];
  assert.equal(turnFile(listed, ['/home/u/p/docs/terminal.md']), 'docs/terminal.md');
  assert.equal(turnFile(listed, ['src/a.ts']), 'web/src/a.ts');
  assert.equal(turnFile(listed, ['docs/terminal.md', 'web/src/a.ts']), 'web/src/a.ts');
  assert.equal(turnFile(listed, ['/elsewhere/other.go']), null);
  assert.equal(turnFile(listed, ['/a.ts']), null);
});
