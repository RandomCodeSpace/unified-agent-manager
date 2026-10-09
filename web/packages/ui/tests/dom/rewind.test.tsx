import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api, ApiError, type RewindPreview, type RewindReceipt } from '../../src/api';
import * as data from '../../src/mock/data';
import { openTask } from './render';

function fixture(change?: (task: ReturnType<typeof data.seed>['tasks'][number]) => void) {
  const state = data.seed();
  const source = state.tasks.find(t => t.id === 't3')!;
  source.capabilities.rewind = true;
  change?.(source);
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const preview: RewindPreview = {
    user_item_id: 'i1', token: 'token-1', turns: 2, files_available: true,
    files: { status: 'available', files: 2, additions: 3, deletions: 459, entries: [
      { path: `${source.workdir}/cmd/doctor.go`, kind: 'deleted', deletions: 458 },
      { path: '/elsewhere/notes.md', kind: 'modified', additions: 3, deletions: 1 },
    ] },
  };
  return { source, preview };
}

async function openRewind(id = 't3') {
  const { user, mock } = await openTask(id);
  const trigger = screen.getAllByRole('button', { name: 'Turn actions' })[0];
  await user.click(trigger);
  await user.click(await screen.findByRole('menuitem', { name: 'Rewind to before this prompt…' }));
  const panel = await screen.findByRole('dialog', { name: 'Rewind to before this prompt?' });
  return { user, mock, trigger, panel };
}

describe('native rewind', () => {
  test('preview is anchored and undimmed, shows the inverse effect, and cancel changes nothing', async () => {
    const { preview } = fixture();
    const read = vi.spyOn(api, 'rewindPreview').mockResolvedValue(preview);
    const rewind = vi.spyOn(api, 'rewind');
    const { user, mock, trigger, panel } = await openRewind();
    expect(panel.getAttribute('aria-modal')).not.toBe('true');
    expect(screen.getByRole('region', { name: 'Conversation' }).hasAttribute('inert')).toBe(false);
    const p = within(panel);
    await p.findByText(/the 1 after it/);
    expect(read).toHaveBeenCalledWith('t3', 'i1', expect.anything());
    expect(p.getByText('restored').parentElement?.textContent).toContain('+458');
    expect(p.getByText('outside the project')).toBeTruthy();
    expect(p.getByText(/protected and stay as they are/)).toBeTruthy();
    await user.click(p.getByRole('radio', { name: 'Conversation only' }));
    expect(p.getByText(/2 edited files stay on disk/)).toBeTruthy();
    await user.click(p.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Rewind to before this prompt?' })).toBeNull());
    expect(rewind).not.toHaveBeenCalled();
    expect(mock.received.filter(r => r.route === 'prompt')).toHaveLength(0);
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  test('confirm sends the bound preview once and shows structured per-file outcomes', async () => {
    const { preview } = fixture();
    vi.spyOn(api, 'rewindPreview').mockResolvedValue(preview);
    const receipt: RewindReceipt = { request_id: 'r', user_item_id: 'i1', mode: 'conversation-and-files', state: 'applied', result: {
      outcome: 'truncation-failed', error: 'disk full', restored_files: ['/w/cmd/doctor.go'], skipped_files: [{ path: '/w/notes.md', reason: 'user-modified' }],
    } };
    const rewind = vi.spyOn(api, 'rewind').mockResolvedValue(receipt);
    const { user, panel } = await openRewind();
    const p = within(panel);
    await user.click(await p.findByRole('button', { name: 'Rewind' }));
    await p.findByText('Files were restored, but the conversation was not rewound.');
    expect(rewind).toHaveBeenCalledTimes(1);
    const [, body] = rewind.mock.calls[0];
    expect(body).toMatchObject({ user_item_id: 'i1', mode: 'conversation-and-files', token: 'token-1' });
    expect(body.request_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(p.getByText('disk full')).toBeTruthy();
    expect(p.getByText(/changed after Copilot’s last write/)).toBeTruthy();
    expect(p.queryByRole('button', { name: 'Rewind' })).toBeNull();
  });

  test('a stale preview is read again and needs a new confirmation', async () => {
    const { preview } = fixture();
    const rewind = vi.spyOn(api, 'rewind');
    const read = vi.spyOn(api, 'rewindPreview').mockImplementation(async () => (rewind.mock.calls.length ? { ...preview, token: 'token-2' } : preview));
    rewind.mockRejectedValueOnce(new ApiError(409, 'The files changed', { code: 'rewind_stale' }))
      .mockResolvedValueOnce({ request_id: 'r', user_item_id: 'i1', mode: 'conversation-and-files', state: 'done', result: { outcome: 'session-busy', restored_files: [], skipped_files: [] } });
    const { user, panel } = await openRewind();
    const p = within(panel);
    await user.click(await p.findByRole('button', { name: 'Rewind' }));
    await p.findByText('The files changed');
    await waitFor(() => expect(read.mock.calls.at(-1)).toBeDefined());
    await waitFor(() => expect(p.getByRole('button', { name: 'Rewind' }).hasAttribute('disabled')).toBe(false));
    expect(rewind).toHaveBeenCalledTimes(1);
    await user.click(await p.findByRole('button', { name: 'Rewind' }));
    await p.findByText('Nothing changed: the conversation was busy.');
    expect(rewind.mock.calls[1][1].token).toBe('token-2');
    expect(rewind.mock.calls[1][1].request_id).not.toBe(rewind.mock.calls[0][1].request_id);
  });

  test('an uncertain result is shown plainly and the hold reconciles by rereading only', async () => {
    const { preview } = fixture();
    vi.spyOn(api, 'rewindPreview').mockResolvedValue({ ...preview, files_available: false, files_reason: 'file-change-tracking-disabled' });
    const rewind = vi.spyOn(api, 'rewind').mockResolvedValue({ request_id: 'r', user_item_id: 'i1', mode: 'conversation', state: 'uncertain' });
    const { user, panel } = await openRewind();
    const p = within(panel);
    await p.findByText(/does not track file changes/);
    expect(p.queryByRole('radio')).toBeNull();
    await user.click(await p.findByRole('button', { name: 'Rewind' }));
    await waitFor(() => expect(rewind).toHaveBeenCalled());
    await waitFor(() => expect(document.body.textContent).toContain('result was lost'));
    expect(rewind.mock.calls[0][1].mode).toBe('conversation');
  });

  test('a held Task offers only the explicit reread', async () => {
    fixture(task => { task.rewind = { request_id: 'held-request', state: 'uncertain', mode: 'conversation' }; });
    const reconcile = vi.spyOn(api, 'reconcileRewind').mockResolvedValue({ request_id: 'held-request', user_item_id: 'i1', mode: 'conversation', state: 'done' });
    const rewind = vi.spyOn(api, 'rewind');
    const { user } = await openTask('t3');
    await screen.findByText(/result was lost/);
    await user.click(screen.getAllByRole('button', { name: 'Turn actions' })[0]);
    expect(screen.queryByRole('menuitem', { name: 'Rewind to before this prompt…' })).toBeNull();
    await user.keyboard('{Escape}');
    await user.click(screen.getByRole('button', { name: 'Reread conversation' }));
    await waitFor(() => expect(reconcile).toHaveBeenCalledWith('t3', 'held-request'));
    expect(rewind).not.toHaveBeenCalled();
  });
});
