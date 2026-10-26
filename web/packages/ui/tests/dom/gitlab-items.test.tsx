import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type GitLabPipelineState, type GitLabReview as Review, type Item, type TurnTiming } from '../../src/api';
import { GitLabItemView } from '../../src/components/GitLabItems';
import { GitLabItemsContext, stateBlocks, type GitLabItems } from '../../src/components/GitLabStrip';
import { Transcript } from '../../src/components/Transcript';
import { SessionContext } from '../../src/components/common';
import { DetailVisibility } from '../../src/components/Details';
import type { GitLabRef } from '../../src/lib/gitlabRefs';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

// A browser reports a strip in view as soon as it is observed; happy-dom's observer never reports.
function onScreen() {
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private callback: IntersectionObserverCallback) {}
    observe(target: Element) { this.callback([{ target, isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
    disconnect() {}
  });
}

const at = '2026-10-12T10:00:00Z';
const P = 'https://gitlab.example.com/group/repo/-';
const sha = 'a'.repeat(40);
const state: GitLabPipelineState = { supported: true, auto_investigate: false, can_investigate: true, commit: { sha, branch: 'demo', task_id: 'thread', task_name: 'Thread', created_at: at }, pipeline: { id: 91, status: 'failed', sha, ref: 'demo', web_url: `${P}/pipelines/91` }, merge_requests: [{ iid: 2, title: 'Draft: demo', state: 'opened', draft: true, web_url: `${P}/merge_requests/2` }], origin: { host: 'gitlab.example.com', path: 'group/repo' } };
const bash = (id: string, command: string, output: string): Item => ({ id, kind: 'tool', time: at, tool: { name: 'bash', status: 'completed', input: JSON.stringify({ command }), output } });
const review: Review = {
  merge_request: { iid: 2, title: 'Draft: demo', state: 'opened', draft: true, web_url: `${P}/merge_requests/2`, source_branch: 'demo', target_branch: 'main', author: { name: 'Amit', username: 'amit' }, head_pipeline: { id: 91, status: 'failed', sha, ref: 'demo', web_url: '' } },
  discussions: [{ id: 'd'.repeat(40), individual_note: false, notes: [{ id: 1, body: 'Return 503.', author: { name: 'Priya', username: 'priya' }, created_at: at, updated_at: at, resolvable: true, resolved: false, system: false }] }],
  truncated: false, checked_at: at,
};

function context(open = vi.fn(), current?: GitLabRef): GitLabItems {
  return { sessionId: 'thread', origin: state.origin!, own: { sha, pipelineId: 91 }, state, current, open };
}

function mockItems() {
  return vi.spyOn(api, 'gitlabItems').mockImplementation(async (_id, refs) => ({ items: refs.map((ref) => ({
    'mr:2': { ref, kind: 'mr', title: 'Draft: demo', state: 'opened', draft: true, pipeline_id: 91, pipeline_state: 'running', web_url: `${P}/merge_requests/2` },
    'issue:14': { ref, kind: 'issue', title: 'Health endpoint', state: 'opened', web_url: `${P}/issues/14` },
    'pipeline:91': { ref, kind: 'pipeline', title: 'demo', state: 'failed', pipeline_id: 91, web_url: `${P}/pipelines/91` },
    'job:7': { ref, kind: 'job', title: 'lint', state: 'failed', pipeline_id: 91, web_url: `${P}/jobs/7` },
  } as Record<string, NonNullable<Awaited<ReturnType<typeof api.gitlabItems>>['items'][number]>>)[ref] ?? { ref, kind: '', error: 'unavailable' }) }));
}

test('a reply that ended a turn which touched GitLab carries its items in its outcome bar with their states as blocks; a press opens one', async () => {
  onScreen();
  const read = mockItems();
  const open = vi.fn();
  const timing: TurnTiming = { id: 't', user_item_id: 'owner', started_at: at, ended_at: '2026-10-12T10:00:05Z', state: 'completed' };
  const items: Item[] = [
    { id: 'owner', kind: 'user', text: 'check the pipeline', time: at },
    bash('push', 'git push origin demo', `remote: View merge request for demo:\nremote:   ${P}/merge_requests/2\n   1111111..aaaaaaa  demo -> demo`),
    bash('trace', 'glab ci trace 7 -p 91', 'ERROR: Job failed'),
    { id: 'reply', kind: 'assistant', text: `Pipeline failed in lint; see [the issue](${P}/issues/14). Other: https://gitlab.example.com/else/repo/-/issues/1`, time: at },
  ];
  const user = userEvent.setup();
  render(<ApiContext.Provider value={api}><SessionContext.Provider value="thread"><DetailVisibility open><GitLabItemsContext.Provider value={context(open)}>
    <Transcript sessionId="thread" provider="copilot" workdir="/work" items={items} interactions={[]} subagents={[]} live={false} working={false} turnTimings={[timing]} />
  </GitLabItemsContext.Provider></DetailVisibility></SessionContext.Provider></ApiContext.Provider>);
  const strip = await screen.findByRole('group', { name: 'GitLab items in this turn' });
  // What a screen reader hears: the item, then its state blocks' words.
  const heard = (el: Element) => { const copy = el.cloneNode(true) as Element; copy.querySelectorAll('[aria-hidden="true"]').forEach((n) => n.remove()); return copy.textContent; };
  await waitFor(() => expect(within(strip).getAllByRole('link').map(heard)).toEqual(['!2draftpipeline running', 'Pipeline #91failed', 'lintfailed', '#14open']));
  // Chips in the turn card's outcome row, with no "GitLab" label; the open MR's pipeline is a block of its own.
  expect(strip.closest('[data-outcome]')).toBeTruthy();
  expect(strip.textContent).not.toContain('GitLab');
  expect(within(strip).getByTitle('pipeline running').className).toContain('bg-accent-wash');
  expect(read).toHaveBeenCalledExactlyOnceWith('thread', ['mr:2', 'pipeline:91', 'job:7', 'issue:14']);
  // The reply's own link stays a plain link.
  expect(screen.getByRole('link', { name: 'the issue' }).getAttribute('href')).toBe(`${P}/issues/14`);
  await user.click(within(strip).getByRole('link', { name: /lint/ }));
  expect(open).toHaveBeenCalledExactlyOnceWith([{ kind: 'mr', id: 2 }, { kind: 'pipeline', id: 91 }, { kind: 'job', id: 7 }, { kind: 'issue', id: 14 }], { kind: 'job', id: 7 });
});

test('a reply names merge requests and pipelines as links with GitLab\'s mark; a bare address reads "MR !2"; code and other projects stay as written', async () => {
  const open = vi.fn();
  const items: Item[] = [
    { id: 'owner', kind: 'user', text: 'status?', time: at },
    { id: 'reply', kind: 'assistant', text: `Opened MR !2; the run is pipeline #91. Bare: ${P}/merge_requests/2 and ${P}/pipelines/91. Code \`!3\` stays. Other project: https://gitlab.example.com/else/repo/-/merge_requests/5`, time: at },
  ];
  const user = userEvent.setup();
  render(<ApiContext.Provider value={api}><SessionContext.Provider value="thread"><DetailVisibility open><GitLabItemsContext.Provider value={context(open)}>
    <Transcript sessionId="thread" provider="copilot" workdir="/work" items={items} interactions={[]} subagents={[]} live={false} working={false} turnTimings={[]} />
  </GitLabItemsContext.Provider></DetailVisibility></SessionContext.Provider></ApiContext.Provider>);
  const reply = screen.getByText(/^Opened/).closest('p')!;
  const links = [...reply.querySelectorAll('a')].map((a) => [a.textContent, a.getAttribute('href'), a.hasAttribute('data-gitlab-link'), !!a.querySelector('svg')]);
  expect(links).toEqual([
    ['MR !2', `${P}/merge_requests/2`, true, true],
    ['pipeline #91', `${P}/pipelines/91`, true, true],
    ['MR !2', `${P}/merge_requests/2`, true, true],
    ['Pipeline #91', `${P}/pipelines/91`, true, true],
    ['https://gitlab.example.com/else/repo/-/merge_requests/5', 'https://gitlab.example.com/else/repo/-/merge_requests/5', false, false],
  ]);
  expect(within(reply).getByText('!3').tagName).toBe('CODE');
  // A plain press opens the item in the GitLab panel, not a new tab.
  await user.click(within(reply).getAllByRole('link', { name: 'MR !2' })[0]);
  expect(open).toHaveBeenCalledExactlyOnceWith([{ kind: 'mr', id: 2 }], { kind: 'mr', id: 2 });
});

test('a name right before a link to the same item shows the item once; other links keep the name', () => {
  const items: Item[] = [
    { id: 'owner', kind: 'user', text: 'status?', time: at },
    { id: 'reply', kind: 'assistant', text: `Done. MR !2: ${P}/merge_requests/2\n\nRan pipeline #91 \u2014 [pipeline #91](${P}/pipelines/91)\n\nSee MR !2: [the fix](${P}/merge_requests/2) and MR !3: ${P}/merge_requests/2\n\nLast: MR !4`, time: at },
  ];
  render(<ApiContext.Provider value={api}><SessionContext.Provider value="thread"><DetailVisibility open><GitLabItemsContext.Provider value={context(vi.fn())}>
    <Transcript sessionId="thread" provider="copilot" workdir="/work" items={items} interactions={[]} subagents={[]} live={false} working={false} turnTimings={[]} />
  </GitLabItemsContext.Provider></DetailVisibility></SessionContext.Provider></ApiContext.Provider>);
  const para = (start: RegExp) => screen.getByText(start).closest('p')!;
  const links = (p: HTMLElement) => [...p.querySelectorAll('a')].map((a) => a.textContent);
  expect(para(/^Done/).textContent).toBe('Done. MR !2');
  expect(links(para(/^Done/))).toEqual(['MR !2']);
  expect(links(para(/^Ran/))).toEqual(['pipeline #91']);
  expect(links(para(/^See/))).toEqual(['MR !2', 'the fix', 'MR !3', 'MR !2']);
  expect(links(para(/^Last/))).toEqual(['MR !4']);
});

test('the panel steps between the turn items; another MR reads without writes, the Thread own MR keeps them', async () => {
  mockItems();
  const reviews = vi.spyOn(api, 'gitlabReview').mockResolvedValue(review);
  const onOpen = vi.fn();
  const refs: GitLabRef[] = [{ kind: 'mr', id: 2 }, { kind: 'mr', id: 5 }];
  const view = (current: GitLabRef) => <GitLabItemsContext.Provider value={context(vi.fn(), current)}><GitLabItemView refs={refs} current={current} onOpen={onOpen} /></GitLabItemsContext.Provider>;
  const user = userEvent.setup();
  const { rerender } = render(view({ kind: 'mr', id: 2 }));
  expect((await screen.findAllByText('Return 503.')).length).toBeGreaterThan(0);
  expect(reviews).toHaveBeenLastCalledWith('thread', 2, sha, expect.any(AbortSignal));
  expect(screen.getByRole('button', { name: 'Post reply' })).toBeTruthy();
  await user.click(screen.getByRole('button', { name: /#91/ }));
  expect(onOpen).toHaveBeenLastCalledWith({ kind: 'pipeline', id: 91 });
  await user.click(within(screen.getByRole('group', { name: "This turn's GitLab items" })).getByRole('button', { name: /!5/ }));
  expect(onOpen).toHaveBeenLastCalledWith({ kind: 'mr', id: 5 });
  rerender(view({ kind: 'mr', id: 5 }));
  await waitFor(() => expect(reviews).toHaveBeenLastCalledWith('thread', 5, undefined, expect.any(AbortSignal)));
  expect((await screen.findAllByText('Return 503.')).length).toBeGreaterThan(0);
  expect(screen.queryByRole('textbox')).toBeNull();
  for (const name of ['Resolve discussion', 'Post reply', 'Fix', 'Draft reply', 'Add comment']) expect(screen.queryByRole('button', { name })).toBeNull();
});

test('a job opens its pipeline with the job selected and its log tail; a work item lists its children', async () => {
  mockItems();
  vi.spyOn(api, 'gitlabPipelineDetail').mockResolvedValue({ pipeline: { id: 91, status: 'failed', sha, ref: 'demo', web_url: '' }, jobs: [
    { id: 7, name: 'lint', stage: 'lint', status: 'failed', allow_failure: false, web_url: `${P}/jobs/7` },
    { id: 8, name: 'unit', stage: 'test', status: 'success', allow_failure: false, web_url: '' },
  ] });
  const trace = vi.spyOn(api, 'gitlabJobTrace').mockResolvedValue({ text: 'ok line\nERROR: Job failed: exit code 1\n', truncated: false });
  const onOpen = vi.fn();
  const { rerender } = render(<GitLabItemsContext.Provider value={context()}><GitLabItemView refs={[{ kind: 'job', id: 7 }]} current={{ kind: 'job', id: 7 }} onOpen={onOpen} /></GitLabItemsContext.Provider>);
  expect(await screen.findByRole('button', { name: /lint/, pressed: true })).toBeTruthy();
  expect(within(screen.getByRole('group', { name: 'Stage test' })).getByRole('button', { name: /unit/, pressed: false })).toBeTruthy();
  const line = await screen.findByText('ERROR: Job failed: exit code 1');
  expect(line.closest('pre')?.getAttribute('aria-label')).toBe('lint log');
  expect(trace).toHaveBeenCalledWith('thread', 7, undefined, 91, expect.any(AbortSignal));
  expect(screen.getByRole('button', { name: 'Investigate' })).toBeTruthy();

  vi.spyOn(api, 'gitlabWorkItem').mockResolvedValue({ ref: 'epic:3', iid: 3, kind: 'epic', title: 'Readiness', state: 'opened', labels: ['health'], assignees: [], children: [
    { ref: 'issue:14', iid: 14, kind: 'issue', title: 'Health endpoint', state: 'opened' },
    { iid: 9, kind: 'issue', title: 'Elsewhere', state: 'closed', web_url: 'https://gitlab.example.com/other/repo/-/issues/9' },
  ], child_count: 2 });
  rerender(<GitLabItemsContext.Provider value={context()}><GitLabItemView refs={[{ kind: 'epic', id: 3 }]} current={{ kind: 'epic', id: 3 }} onOpen={onOpen} /></GitLabItemsContext.Provider>);
  expect(await screen.findByRole('heading', { name: 'Readiness' })).toBeTruthy();
  expect(screen.getByText('health')).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: /Health endpoint/ }));
  expect(onOpen).toHaveBeenLastCalledWith({ kind: 'issue', id: 14 });
  expect(screen.getByRole('link', { name: /Elsewhere/ }).getAttribute('href')).toBe('https://gitlab.example.com/other/repo/-/issues/9');
});

test('discussion notes render as full Markdown in the list; uploads load from GitLab', async () => {
  mockItems();
  const body = [
    'Two things:',
    '',
    '- return **503**',
    '- log it',
    '',
    '```go',
    'return http.StatusServiceUnavailable',
    '```',
    '',
    '```suggestion:-0+0',
    'w.WriteHeader(503)',
    '```',
    '',
    'See [the docs](https://docs.example.com/health) and ![shot](/uploads/abc/shot.png).',
  ].join('\n');
  const [discussion] = review.discussions;
  vi.spyOn(api, 'gitlabReview').mockResolvedValue({ ...review, discussions: [{ ...discussion, notes: [{ ...discussion.notes[0], body }] }] });
  const current: GitLabRef = { kind: 'mr', id: 2 };
  render(<GitLabItemsContext.Provider value={context(vi.fn(), current)}><GitLabItemView refs={[current]} current={current} onOpen={vi.fn()} /></GitLabItemsContext.Provider>);
  const note = await screen.findByRole('article');
  expect(within(note).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['return 503', 'log it']);
  expect(within(note).getByText('503').tagName).toBe('STRONG');
  expect([...note.querySelectorAll('pre')].map((pre) => pre.textContent)).toEqual(['return http.StatusServiceUnavailable\n', 'w.WriteHeader(503)\n']);
  expect(within(note).getByText('Suggested change')).toBeTruthy();
  expect(within(note).getByRole('link', { name: 'the docs' }).getAttribute('href')).toBe('https://docs.example.com/health');
  // An upload loads from GitLab in place (https, no referrer) and links to it.
  const shot = within(note).getByRole('img', { name: 'shot' });
  expect(shot.getAttribute('src')).toBe('https://gitlab.example.com/group/repo/uploads/abc/shot.png');
  expect(shot.closest('a')?.getAttribute('href')).toBe('https://gitlab.example.com/group/repo/uploads/abc/shot.png');
  expect(within(screen.getAllByLabelText('Discussions').at(-1)!).getByRole('article')).toBe(note);
  expect(note.closest('button')).toBeNull();
});

test('a failed job chip names what failed; a pipeline chip keeps its state word only', () => {
  expect(stateBlocks({ ref: 'job:7', kind: 'job', title: 'test', state: 'failed', failure_kind: 'Tests failed', failure_detail: '2 tests' }).map((b) => b.label)).toEqual(['failed', 'Tests failed · 2 tests']);
  expect(stateBlocks({ ref: 'job:8', kind: 'job', title: 'build', state: 'failed', failure_kind: 'Build failed' }).map((b) => b.label)).toEqual(['failed', 'Build failed']);
  expect(stateBlocks({ ref: 'pipeline:91', kind: 'pipeline', state: 'failed' }).map((b) => b.label)).toEqual(['failed']);
});
