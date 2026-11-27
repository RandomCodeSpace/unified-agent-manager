// The Thread header's branch place (DESIGN.md Thread header): the branch's merge request, or its pipeline, takes the branch link's slot.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type GitLabItemSummary, type GitLabPipelineState } from '../../src/api';
import { GitLabItemsContext } from '../../src/components/GitLabStrip';
import { BranchGitLab } from '../../src/components/Task';
import { openMenu, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

const P = 'https://gitlab.example.com/group/repo/-';
const origin = { host: 'gitlab.example.com', path: 'group/repo' };
const mr = (iid: number, state = 'opened') => ({ iid, title: `Change ${iid}`, state, draft: false, web_url: `${P}/merge_requests/${iid}` });
const base = (extra: Partial<GitLabPipelineState>): GitLabPipelineState => ({ supported: true, auto_investigate: false, can_investigate: false, origin, ...extra });

/** The item reads answer from `states` by reference; each test uses its own Thread id, as the summaries are shared. */
function items(states: Record<string, Partial<GitLabItemSummary>>) {
  return vi.spyOn(api, 'gitlabItems').mockImplementation(async (_id, refs) => ({ items: refs.map((ref) => ({ ref, kind: ref.split(':')[0], ...states[ref] })) }));
}

function show(id: string, state: GitLabPipelineState, compact = false, open = vi.fn()) {
  render(<ApiContext.Provider value={api}><GitLabItemsContext.Provider value={{ sessionId: id, origin, state, open }}>
    <BranchGitLab compact={compact} />
  </GitLabItemsContext.Provider></ApiContext.Provider>);
  return open;
}

const chip = () => document.getElementById('branch-gitlab')!;

test('no merge request and no pipeline: nothing takes the branch link\'s place', () => {
  const read = items({});
  show('none', base({}));
  expect(document.getElementById('branch-gitlab')).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('a pipeline alone shows #id and its state, and opens the panel on it', async () => {
  items({ 'pipeline:91': { state: 'running', web_url: `${P}/pipelines/91` } });
  const open = show('pipeline', base({ pipeline: { id: 91, status: 'running', sha: 'a'.repeat(40), ref: 'demo', web_url: `${P}/pipelines/91` } }));
  await waitFor(() => expect(chip().getAttribute('aria-label')).toBe('Pipeline #91: running'));
  expect(chip().textContent).toContain('#91');
  await userEvent.setup().click(chip());
  expect(open).toHaveBeenCalledExactlyOnceWith([{ kind: 'pipeline', id: 91 }], { kind: 'pipeline', id: 91 });
});

test('one merge request shows !iid with the reply chip\'s blocks, read live; a plain press opens the panel', async () => {
  items({ 'mr:128': { state: 'opened', pipeline_state: 'running', merge_status: 'not_approved', web_url: `${P}/merge_requests/128` } });
  const open = show('one', base({ merge_requests: [mr(128)] }));
  await waitFor(() => expect(chip().getAttribute('aria-label')).toBe('Merge request !128: open, pipeline running, needs approval'));
  expect(chip().getAttribute('href')).toBe(`${P}/merge_requests/128`);
  expect(within(chip()).getByTitle('needs approval').dataset.tone).toBe('warning');
  expect(chip().textContent).not.toContain('+');
  await userEvent.setup().click(chip());
  expect(open).toHaveBeenCalledExactlyOnceWith([{ kind: 'mr', id: 128 }], { kind: 'mr', id: 128 });
});

test('several merge requests: one chip for the most urgent open one, +N, every one in the tooltip and the panel', async () => {
  items({
    'mr:5': { title: 'To main', state: 'opened', merge_status: 'mergeable', web_url: `${P}/merge_requests/5` },
    'mr:6': { title: 'To release', state: 'opened', conflicts: true, web_url: `${P}/merge_requests/6` },
    'mr:7': { title: 'Old', state: 'merged', web_url: `${P}/merge_requests/7` },
  });
  const open = show('many', base({ merge_requests: [mr(5), mr(6), mr(7, 'merged')] }));
  await waitFor(() => expect(chip().getAttribute('aria-label')).toBe('Merge request !6: open, merge conflict. 2 more merge requests'));
  expect(chip().textContent).toContain('!6+2');
  const user = userEvent.setup();
  await user.hover(chip());
  const tip = await screen.findByRole('tooltip');
  expect([...tip.querySelectorAll('div')].map((d) => d.textContent).filter((t) => t?.startsWith('!'))).toEqual(['!6 · To release · open, merge conflict', '!5 · To main · open, ready to merge', '!7 · Old · merged']);
  await user.click(chip());
  expect(open).toHaveBeenCalledExactlyOnceWith([{ kind: 'mr', id: 6 }, { kind: 'mr', id: 5 }, { kind: 'mr', id: 7 }], { kind: 'mr', id: 6 });
});

test('every merge request merged or closed: the newest one shows its state', async () => {
  items({ 'mr:3': { state: 'closed' }, 'mr:4': { state: 'merged' } });
  show('done', base({ merge_requests: [mr(3, 'closed'), mr(4, 'merged')] }));
  await waitFor(() => expect(chip().getAttribute('aria-label')).toBe('Merge request !4: merged. 1 more merge request'));
});

test('folded (a phone) the chip keeps the glyph, !iid, +N and one dot in the most urgent tone; the words stay in its name', async () => {
  items({ 'mr:5': { state: 'opened', pipeline_state: 'failed' }, 'mr:6': { state: 'opened' } });
  show('phone', base({ merge_requests: [mr(5), mr(6)] }), true);
  await waitFor(() => expect(chip().querySelector('[data-tone]')?.getAttribute('data-tone')).toBe('error'));
  expect(chip().getAttribute('aria-label')).toBe('Merge request !5: open, pipeline failed. 1 more merge request');
  expect(chip().textContent).toBe('!5+1');
  expect(chip().querySelectorAll('[data-tone]')).toHaveLength(1);
});

test('in the header the merge request chip takes the branch link\'s place and the branch link moves to the actions menu', async () => {
  vi.spyOn(api, 'gitlabOrigin').mockResolvedValue(base({ merge_requests: [mr(42)] }));
  items({ 'mr:42': { state: 'opened', web_url: `${P}/merge_requests/42` } });
  const { user } = await openTask('t20');
  const header = screen.getByRole('group', { name: /^State: / });
  await waitFor(() => expect(header.querySelector('#branch-gitlab')?.getAttribute('aria-label')).toBe('Merge request !42: open'));
  expect(header.querySelector('#branch-link')).toBeNull();
  const menu = await openMenu(user, 'Thread actions');
  expect(menu.getByRole('menuitem', { name: /on the web|is not pushed/ })).toBeTruthy();
});

test('in the header with no merge request and no pipeline the branch link stays as it is', async () => {
  vi.spyOn(api, 'gitlabOrigin').mockResolvedValue(base({}));
  await openTask('t20');
  const header = screen.getByRole('group', { name: /^State: / });
  await waitFor(() => expect(header.querySelector('#branch-link')).not.toBeNull());
  expect(header.querySelector('#branch-gitlab')).toBeNull();
});
