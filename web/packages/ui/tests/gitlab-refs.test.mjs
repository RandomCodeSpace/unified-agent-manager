import assert from 'node:assert/strict';
import test from 'node:test';
import { gitLabCandidates, gitLabCommitUrl, isGitLabCommitUrl, gitLabCompareUrl, gitLabMarkdown, gitLabProjectUrl, gitLabTreeUrl, isGitLabProjectUrl, needsBody, parseGitLabUrl, parseRefKey, refKey, turnGitLabRefs } from '../src/lib/gitlabRefs.ts';

const origin = { host: 'gitlab.com', path: 'aksops/demo' };
const P = 'https://gitlab.com/aksops/demo/-';
const at = (s) => `2026-10-12T10:00:${String(s).padStart(2, '0')}Z`;
let n = 0;
const bash = (command, output, extra = {}) => ({ id: `c${++n}`, kind: 'tool', time: at(n), tool: { name: 'bash', status: 'completed', input: JSON.stringify({ command }), output, ...extra } });
const reply = (text, extra = {}) => ({ id: `r${++n}`, kind: 'assistant', time: at(n), text, ...extra });
const keys = (items, own) => turnGitLabRefs(items, origin, own).map(refKey);

test('only links on the Thread project and its groups are items', () => {
  assert.deepEqual(parseGitLabUrl(`${P}/merge_requests/2`, origin), { kind: 'mr', id: 2 });
  assert.deepEqual(parseGitLabUrl(`${P}/issues/14#note_1`, origin), { kind: 'issue', id: 14 });
  assert.deepEqual(parseGitLabUrl(`${P}/work_items/22`, origin), { kind: 'issue', id: 22 });
  assert.deepEqual(parseGitLabUrl(`${P}/pipelines/2936820380/`, origin), { kind: 'pipeline', id: 2936820380 });
  // A malformed escape in the path is read as written, never thrown.
  assert.equal(parseGitLabUrl(`${P}/wikis/50%off`, origin), null);
  assert.deepEqual(parseGitLabUrl(`${P}/jobs/7?x=1`, origin), { kind: 'job', id: 7 });
  assert.deepEqual(parseGitLabUrl('https://GitLab.com/groups/aksops/-/epics/3', origin), { kind: 'epic', id: 3 });
  assert.equal(parseGitLabUrl('https://gitlab.com/groups/other/-/epics/3', origin), null);
  assert.equal(parseGitLabUrl('https://gitlab.com/aksops/other/-/merge_requests/2', origin), null);
  assert.equal(parseGitLabUrl('https://example.com/aksops/demo/-/merge_requests/2', origin), null);
  assert.equal(parseGitLabUrl(`${P}/merge_requests/new?merge_request[source_branch]=x`, origin), null);
  assert.equal(parseGitLabUrl(`${P}/merge_requests/2/diffs`, origin), null);
  const nested = { host: 'gitlab.com', path: 'a/b/c' };
  assert.deepEqual(parseGitLabUrl('https://gitlab.com/groups/a/-/epics/5', nested), { kind: 'epic', id: 5, group: 'a' });
  assert.deepEqual(parseRefKey('epic:5:a'), { kind: 'epic', id: 5, group: 'a' });
  assert.equal(parseRefKey('mr:2:a'), undefined);
});

test('a turn lists what its glab calls name and print, the push link, and its reply links, once each', () => {
  const items = [
    { id: 'u', kind: 'user', time: at(0), text: `check ${P}/issues/99` },
    bash('glab ci status --branch demo', `(failed) • #2936820380\t(#6)\tdemo\n${P}/pipelines/2936820380`),
    bash('rtk glab ci trace 11873350112 -p 2936820380 | tail -14', 'ERROR: Job failed'),
    bash('glab mr view 2 --comments', 'title:\tDraft: demo\nnumber:\t2\nurl:\t' + `${P}/merge_requests/2`),
    bash('glab issue view 14', 'Closes #14. Child of &3'),
    bash('glab work-items list --type task -F json -P 10', `[{"iid":"22","webUrl":"${P}/work_items/22"}]`),
    bash('glab -R someone/else mr view 9', '!9'),
    bash('glab mr list -P 10', `!2\taksops/demo!2\tDraft\n!1\tother/repo!7\tx`),
    { ...bash('glab mr view 5', '!5'), agent_id: 'sub' },
    reply(`Pipeline [#2936820380](${P}/pipelines/2936820380) failed. See ${P}/issues/14 and ${P}/merge_requests/3.`),
  ];
  assert.deepEqual(keys(items), ['pipeline:2936820380', 'job:11873350112', 'mr:2', 'issue:14', 'epic:3', 'issue:22', 'mr:1', 'mr:3']);
});

test('the Thread own commit pipeline joins the turn that committed or pushed it', () => {
  const sha = '8c41d2e7aa'.padEnd(40, '0');
  const own = { sha, pipelineId: 91 };
  const commit = bash('git commit -am "fix"', '[demo 8c41d2e] fix\n 1 file changed');
  const push = bash('git push origin demo', `remote: View merge request for demo:\nremote:   ${P}/merge_requests/2\n   3b0f6a1..8c41d2e7  demo -> demo`);
  assert.deepEqual(keys([commit, push], own), ['pipeline:91', 'mr:2']);
  assert.deepEqual(keys([bash('git push origin demo', '   3b0f6a1..1234567  demo -> demo')], own), []);
  assert.deepEqual(keys([{ ...commit, tool: { ...commit.tool, status: 'failed' } }], own), []);
});

test('at most 20 items per turn', () => {
  const out = Array.from({ length: 30 }, (_, i) => `!${i + 1}`).join('\n');
  assert.equal(keys([bash('glab mr list -P 20', out)]).length, 20);
});

test('in the compact view a call shows its command; only GitLab-relevant items are candidates, and held-back output needs a read', () => {
  const compact = { id: 'k1', kind: 'tool', time: at(1), tool: { name: 'bash', status: 'completed', display_arg: 'glab mr view 4', has_input: true, has_output: true } };
  assert.deepEqual(keys([compact]), ['mr:4']);
  assert.equal(needsBody(compact), true);
  const unrelated = bash('go test ./...', 'ok');
  const text = reply('Done, nothing on GitLab.');
  const linked = reply(`See ${P}/merge_requests/4`);
  const push = bash('git push', '');
  const other = bash('glab -R x/y mr view 1', '');
  assert.deepEqual(gitLabCandidates([compact, unrelated, text, linked, push, other], origin).map((i) => i.id), ['k1', linked.id, push.id]);
  assert.equal(needsBody(push), false);
});

test('GitLab uploads link to the project', () => {
  const body = '![shot](/uploads/abc/shot.png) and [log](/uploads/def/log.txt)\n[ref]: /uploads/ghi/a.png\nKeep `/uploads/x` and https://gitlab.com/uploads/y';
  assert.equal(gitLabMarkdown(body, origin), '![shot](https://gitlab.com/aksops/demo/uploads/abc/shot.png) and [log](https://gitlab.com/aksops/demo/uploads/def/log.txt)\n[ref]: https://gitlab.com/aksops/demo/uploads/ghi/a.png\nKeep `/uploads/x` and https://gitlab.com/uploads/y');
  assert.equal(gitLabMarkdown(body), body);
});

test('project places: branches keep their slashes with each segment escaped; only the project itself is its address', () => {
  assert.equal(gitLabProjectUrl(origin), 'https://gitlab.com/aksops/demo');
  assert.equal(gitLabTreeUrl(origin, 'feat/a#b c'), `${P}/tree/feat/a%23b%20c`);
  assert.equal(gitLabCompareUrl(origin, 'main', 'feat/x?y'), `${P}/compare/main...feat/x%3Fy`);
  assert.equal(gitLabCommitUrl(origin, 'a'.repeat(40)), `${P}/commit/${'a'.repeat(40)}`);
  assert.equal(isGitLabProjectUrl('https://gitlab.com/aksops/demo', origin), true);
  assert.equal(isGitLabProjectUrl('https://GitLab.com/Aksops/Demo/', origin), true);
  assert.equal(isGitLabProjectUrl('https://gitlab.com/aksops/demo/-/issues', origin), false);
  assert.equal(isGitLabProjectUrl('https://gitlab.com/aksops/demo?x=1', origin), false);
  assert.equal(isGitLabProjectUrl('http://gitlab.com/aksops/demo', origin), false);
  assert.equal(isGitLabProjectUrl('https://gitlab.com/aksops', origin), false);
});

test('a reply\'s prose names merge requests and pipelines for the bar, after its links; issues, jobs, code and other projects do not count', () => {
  const r = reply(`Opened aksops/demo!7 and MR !2 (pipeline #91). See ${P}/issues/14, issue #15 and job #16. Code: \`!3\` and\n\n\`\`\`\npipeline #4\n\`\`\`\nother/demo!9`);
  assert.deepEqual(keys([r]), ['issue:14', 'mr:7', 'mr:2', 'pipeline:91']);
  const prose = reply('Merged MR !5.');
  assert.deepEqual(gitLabCandidates([prose, reply('Took 5 minutes.')], origin).map((i) => i.id), [prose.id]);
  assert.equal(isGitLabCommitUrl(`${P}/commit/${'a'.repeat(40)}`, origin), true);
  assert.equal(isGitLabCommitUrl(`${P}/commit/main`, origin), false);
  assert.equal(isGitLabCommitUrl(`https://gitlab.com/other/demo/-/commit/${'a'.repeat(40)}`, origin), false);
});
