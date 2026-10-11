import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiError, api, type GitLabReview as Review, type SessionDetail } from '../../src/api';
import { GitLabReview } from '../../src/components/GitLabReview';

const sha = 'c'.repeat(40);
const session = { id: 'task', model: 'selected-model', stage: 'active', state: 'completed', queued: 0, items: [], last_submission: null, turn_timings: [] } as unknown as SessionDetail;
const data: Review = {
  merge_request: { iid: 42, title: 'Checkout timeout', state: 'opened', draft: false, web_url: 'https://gitlab.example/mr/42', source_branch: 'fix/timeout', target_branch: 'main', head_sha: sha },
  checked_at: '2026-10-11T17:00:00Z', truncated: false,
  discussions: [
    { id: 'thread', individual_note: false, notes: [{ id: 1, body: 'Please fix the timeout.', author: { name: 'Maya', username: 'maya' }, created_at: '2026-10-11T16:00:00Z', updated_at: '2026-10-11T16:00:00Z', resolvable: true, resolved: false, system: false, position: { new_path: 'tests/checkout.ts', old_path: 'tests/checkout.ts', new_line: 84, head_sha: sha, base_sha: 'a', start_sha: 'a' } }] },
    { id: 'comment', individual_note: true, notes: [{ id: 2, body: 'Thanks for the update.', author: { name: 'Luca', username: 'luca' }, created_at: '2026-10-11T16:01:00Z', updated_at: '2026-10-11T16:01:00Z', resolvable: false, resolved: false, system: false }] },
  ],
};

afterEach(() => vi.restoreAllMocks());
function draw(selected = session) {
  const read = vi.spyOn(api, 'gitlabReview').mockResolvedValue(structuredClone(data));
  const action = vi.spyOn(api, 'gitlabReviewAction').mockImplementation(async (_id, _iid, body) => ({ request_id: body.request_id, status: body.mode === 'queue' ? 'queued' : 'accepted', time: '2026-10-11T17:00:00Z' }));
  const post = vi.spyOn(api, 'postGitlabComment').mockResolvedValue({ note_id: 3, discussion_id: 'thread' });
  const resolve = vi.spyOn(api, 'resolveGitlabThread').mockResolvedValue({ resolved: true });
  const ui = (current: SessionDetail) => <GitLabReview key={current.id} sessionId={current.id} session={current} iid={42} sha={sha} />;
  const rendered = render(ui(selected));
  return { ...rendered, user: userEvent.setup(), read, action, post, resolve, update: (next: SessionDetail) => rendered.rerender(ui(next)) };
}
function done(requestId: string, text = 'I updated the timeout and reran the test.'): SessionDetail {
  return { ...session, items: [
    { id: 'owner', kind: 'user', text: `UAM GitLab review request: ${requestId}\nDraft reply`, time: '2026-10-11T17:00:00Z' },
    { id: 'answer', kind: 'assistant', text, time: '2026-10-11T17:00:01Z' },
    { id: 'later', kind: 'user', text: 'Unrelated request', time: '2026-10-11T17:00:03Z' },
    { id: 'unrelated', kind: 'assistant', text: 'Do not use this answer.', time: '2026-10-11T17:00:04Z' },
  ], turn_timings: [{ id: 'turn', user_item_id: 'owner', started_at: '2026-10-11T17:00:00Z', ended_at: '2026-10-11T17:00:02Z', state: 'completed' }] };
}

test('review shows anchored thread context, comments, and useful filters', async () => {
  const { user, read } = draw();
  expect(await screen.findByText('tests/checkout.ts:84')).toBeTruthy();
  expect(read).toHaveBeenCalledWith('task', 42, sha, expect.any(AbortSignal));
  expect(screen.getByText('1 unresolved · fix/timeout → main')).toBeTruthy();
  const filters = within(screen.getByRole('group', { name: 'Filter discussions' }));
  await user.click(filters.getByRole('button', { name: 'Resolved' }));
  expect(screen.getByText('No resolved threads.')).toBeTruthy();
  await user.click(filters.getByRole('button', { name: 'All' }));
  await user.click(screen.getByRole('button', { name: /Luca Comment/ }));
  expect(screen.queryByRole('button', { name: 'Resolve thread' })).toBeNull();
  expect(screen.getByRole('button', { name: 'Post comment' })).toBeTruthy();
});

test.each(['Investigate', 'Fix'] as const)('%s sends the selected thread into this task without posting or resolving', async (label) => {
  const { user, action, post, resolve } = draw();
  await user.click(await screen.findByRole('button', { name: label, exact: true }));
  expect(action).toHaveBeenCalledExactlyOnceWith('task', 42, { commit_sha: sha, discussion_id: 'thread', action: label.toLowerCase(), request_id: expect.any(String), mode: 'send' });
  expect(post).not.toHaveBeenCalled();
  expect(resolve).not.toHaveBeenCalled();
  await user.click(screen.getByRole('button', { name: label, exact: true }));
  expect(action).toHaveBeenCalledTimes(1);
});

test('busy tasks queue review work using the same action endpoint', async () => {
  const { user, action } = draw({ ...session, state: 'working' });
  await user.click(await screen.findByRole('button', { name: 'Fix', exact: true }));
  expect(action.mock.calls[0][2].mode).toBe('queue');
  expect(await screen.findByText('Fix queued. Follow progress in the task.')).toBeTruthy();
});

test('an agent draft uses only its exact completed turn, then waits for explicit posting', async () => {
  const { user, action, post, resolve, update } = draw();
  await user.click(await screen.findByRole('button', { name: 'Draft reply' }));
  const requestId = action.mock.calls[0][2].request_id;
  update(done(requestId));
  await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('I updated the timeout and reran the test.'));
  expect(post).not.toHaveBeenCalled();
  expect(resolve).not.toHaveBeenCalled();
  await user.click(screen.getByRole('button', { name: 'Post reply' }));
  expect(post).toHaveBeenCalledExactlyOnceWith('task', 42, { commit_sha: sha, discussion_id: 'thread', body: 'I updated the timeout and reran the test.' });
  expect(resolve).not.toHaveBeenCalled();
});

test('a compact exact-turn answer is fetched before inserting the draft', async () => {
  const body = vi.spyOn(api, 'itemBody').mockResolvedValue({ seq: 1, epoch: 'e', session_id: 'task', item: { id: 'answer', kind: 'assistant', text: 'Full reply body', time: '2026-10-11T17:00:01Z' } });
  const { user, action, update } = draw();
  await user.click(await screen.findByRole('button', { name: 'Draft reply' }));
  const next = done(action.mock.calls[0][2].request_id);
  next.items[1] = { ...next.items[1], text: undefined, compact: { has_text: true, has_reasoning: false } };
  update(next);
  await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('Full reply body'));
  expect(body).toHaveBeenCalledWith('task', 'answer', '', expect.any(AbortSignal));
});

test('mixed timestamp precision and steering within a turn preserve the exact draft reply', async () => {
  const { user, action, update } = draw();
  await user.click(await screen.findByRole('button', { name: 'Draft reply' }));
  const next = done(action.mock.calls[0][2].request_id);
  next.items[0].time = '2026-10-11T17:00:00Z';
  next.items[1].time = '2026-10-11T17:00:00.123Z';
  next.items.splice(1, 0, { id: 'steer', kind: 'user', delivery: 'steer', text: 'Keep it concise', time: '2026-10-11T17:00:00.1Z' });
  next.turn_timings![0].ended_at = '2026-10-11T17:00:00.123456Z';
  update(next);
  await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('I updated the timeout and reran the test.'));
});

test('dirty replies survive refresh and a late agent draft', async () => {
  const { user, action, update, read } = draw();
  await user.click(await screen.findByRole('button', { name: 'Draft reply' }));
  await user.type(screen.getByRole('textbox'), 'My own draft');
  await user.click(screen.getByRole('button', { name: 'Refresh discussions' }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  update(done(action.mock.calls[0][2].request_id));
  expect(await screen.findByText('Draft ready. Your edits were kept.')).toBeTruthy();
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('My own draft');
  await user.click(screen.getByText('Agent draft'));
  await user.click(screen.getByRole('button', { name: 'Use draft' }));
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('I updated the timeout and reran the test.');
});

test('an unrelated or unfinished agent turn cannot become a draft', async () => {
  const { user, action, update } = draw();
  await user.click(await screen.findByRole('button', { name: 'Draft reply' }));
  update(done('unrelated-request'));
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('');
  const unfinished = done(action.mock.calls[0][2].request_id);
  unfinished.turn_timings![0].state = 'working';
  update(unfinished);
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('');
});

test('post and resolve are separate explicit writes, and replies remain scoped to their thread', async () => {
  const { user, post, resolve } = draw();
  await screen.findByRole('button', { name: 'Resolve thread' });
  await user.type(screen.getByRole('textbox'), 'Keep this draft');
  await user.click(screen.getByRole('button', { name: 'Add comment' }));
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('');
  await user.type(screen.getByRole('textbox'), 'A general comment');
  await user.click(screen.getByRole('button', { name: 'Post comment' }));
  expect(post).toHaveBeenCalledWith('task', 42, { commit_sha: sha, body: 'A general comment' });
  await user.click(screen.getByRole('button', { name: 'Back to thread' }));
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('Keep this draft');
  await user.click(screen.getByRole('button', { name: 'Resolve thread' }));
  expect(resolve).toHaveBeenCalledExactlyOnceWith('task', 42, { commit_sha: sha, discussion_id: 'thread', resolved: true });
  expect(post).toHaveBeenCalledTimes(1);
});

test('unknown posting outcome prevents retry until a successful refresh', async () => {
  const { user, post, read } = draw();
  post.mockRejectedValueOnce(new ApiError(503, 'outcome unknown'));
  await screen.findByRole('button', { name: 'Post reply' });
  await user.type(screen.getByRole('textbox'), 'Reply once');
  await user.click(screen.getByRole('button', { name: 'Post reply' }));
  expect(await screen.findByText(/GitLab may have saved this change/)).toBeTruthy();
  expect((screen.getByRole('button', { name: 'Post reply' }) as HTMLButtonElement).disabled).toBe(true);
  read.mockRejectedValueOnce(new Error('still offline'));
  await user.click(screen.getByRole('button', { name: 'Refresh discussions' }));
  expect(await screen.findByText(/still offline/)).toBeTruthy();
  expect((screen.getByRole('button', { name: 'Post reply' }) as HTMLButtonElement).disabled).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Refresh discussions' }));
  await waitFor(() => expect((screen.getByRole('button', { name: 'Post reply' }) as HTMLButtonElement).disabled).toBe(false));
  expect(post).toHaveBeenCalledTimes(1);
});

test('permission failures preserve the reply and never report success', async () => {
  const { user, post } = draw();
  post.mockRejectedValueOnce(new ApiError(403, 'Your GitLab account cannot post here.'));
  await screen.findByRole('button', { name: 'Post reply' });
  await user.type(screen.getByRole('textbox'), 'My reply');
  await user.click(screen.getByRole('button', { name: 'Post reply' }));
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Your GitLab account cannot post here.');
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('My reply');
});

test('closing and reopening the inspector preserves drafts and an uncertain write hold', async () => {
  const { user, post, unmount } = draw();
  post.mockRejectedValueOnce(new ApiError(503, 'outcome unknown'));
  await screen.findByRole('button', { name: 'Post reply' });
  await user.type(screen.getByRole('textbox'), 'Preserve this reply');
  await user.click(screen.getByRole('button', { name: 'Post reply' }));
  expect(await screen.findByRole('alert')).toBeTruthy();
  unmount();
  render(<GitLabReview sessionId="task" session={session} iid={42} sha={sha} />);
  await screen.findByRole('button', { name: 'Post reply' });
  expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('Preserve this reply');
  expect((screen.getByRole('button', { name: 'Post reply' }) as HTMLButtonElement).disabled).toBe(true);
  expect(post).toHaveBeenCalledTimes(1);
});

test('changing tasks cancels an old read and does not leak its discussion', async () => {
  const { read, update } = draw();
  await screen.findByText('tests/checkout.ts:84');
  let complete!: (value: Review) => void;
  read.mockImplementationOnce(() => new Promise((done) => { complete = done; }));
  update({ ...session, id: 'other' });
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  const otherSignal = read.mock.calls[1][3]!;
  read.mockResolvedValueOnce({ ...data, discussions: [] });
  update({ ...session, id: 'third' });
  expect(await screen.findByText('No comments yet.')).toBeTruthy();
  await act(async () => complete(data));
  expect(otherSignal.aborted).toBe(true);
  expect(screen.queryByText('tests/checkout.ts:84')).toBeNull();
});

test('settled tasks show review context but disable actions and outbound writes', async () => {
  const { user, action, post } = draw({ ...session, stage: 'settled' });
  expect(await screen.findByText('Reopen this task to work on the review.')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: 'Fix', exact: true }));
  await user.type(screen.getByRole('textbox'), 'Draft');
  await user.click(screen.getByRole('button', { name: 'Post reply' }));
  expect(action).not.toHaveBeenCalled();
  expect(post).not.toHaveBeenCalled();
});
