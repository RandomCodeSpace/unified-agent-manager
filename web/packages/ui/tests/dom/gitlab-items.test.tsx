import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type GitLabPipelineState, type GitLabReview as Review, type Item, type TurnTiming } from '../../src/api';
import { GitLabItemView } from '../../src/components/GitLabItems';
import { GitLabItemsContext, type GitLabItems } from '../../src/components/GitLabStrip';
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

test('a reply that ended a turn which touched GitLab ends in a strip of its items with their states; a press opens one', async () => {
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
  await waitFor(() => expect(within(strip).getAllByRole('link').map((a) => a.textContent)).toEqual(['!2draft·pipeline running', 'Pipeline #91failed', 'lintfailed', '#14open']));
  expect(read).toHaveBeenCalledExactlyOnceWith('thread', ['mr:2', 'pipeline:91', 'job:7', 'issue:14']);
  // The reply's own link stays a plain link.
  expect(screen.getByRole('link', { name: 'the issue' }).getAttribute('href')).toBe(`${P}/issues/14`);
  await user.click(within(strip).getByRole('link', { name: /lint/ }));
  expect(open).toHaveBeenCalledExactlyOnceWith([{ kind: 'mr', id: 2 }, { kind: 'pipeline', id: 91 }, { kind: 'job', id: 7 }, { kind: 'issue', id: 14 }], { kind: 'job', id: 7 });
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

test('discussion notes render as full Markdown in the list; uploads link to GitLab and never load', async () => {
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
  expect(note.querySelector('img')).toBeNull();
  expect(within(note).getByRole('link', { name: /shot/ }).getAttribute('href')).toBe('https://gitlab.example.com/group/repo/uploads/abc/shot.png');
  expect(within(screen.getAllByLabelText('Discussions').at(-1)!).getByRole('article')).toBe(note);
  expect(note.closest('button')).toBeNull();
});
