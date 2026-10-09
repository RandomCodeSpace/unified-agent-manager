import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api, ApiError, type Interaction, type ResendResult, type RewindPreview, type SnapshotData } from '../../src/api';
import * as data from '../../src/mock/data';
import { composer, openTask, renderApp } from './render';

function fixture() {
  const state = data.seed();
  const source = state.tasks.find(t => t.id === 't3')!;
  source.capabilities.rewind = true;
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const preview: RewindPreview = { user_item_id: 'i5', token: 'token-1', turns: 1, files_available: true, files: { status: 'available', files: 1, additions: 2, deletions: 0, entries: [{ path: `${source.workdir}/cmd/doctor.go`, kind: 'modified', additions: 2 }] } };
  vi.spyOn(api, 'itemBody').mockImplementation(async (_id, itemId) => ({ seq: 1, epoch: 'e', session_id: 't3', item: source.items.find(i => i.id === itemId)! }));
  const read = vi.spyOn(api, 'rewindPreview').mockResolvedValue(preview);
  return { source, preview, read };
}

const sent: ResendResult = {
  rewind: { request_id: 'r', user_item_id: 'i5', mode: 'conversation-and-files', state: 'applied', result: { outcome: 'success', events_removed: 4, restored_files: [], skipped_files: [] } },
  submission: { request_id: 'p', status: 'accepted', time: new Date().toISOString() } as ResendResult['submission'],
};

/** Opens Edit and resend on the second turn (the prompt with attachments). */
async function startEdit() {
  const opened = await openTask('t3');
  await opened.user.type(composer(), 'My other draft');
  await opened.user.click(screen.getAllByRole('button', { name: 'Turn actions' })[1]);
  await opened.user.click(await screen.findByRole('menuitem', { name: 'Edit and resend…' }));
  const strip = within(await screen.findByRole('group', { name: 'Editing a past prompt' }));
  await waitFor(() => expect(composer().value).toContain('Does it handle `TERM=dumb`?'));
  await strip.findByText(/Removes this prompt and its reply · restores 1 file/);
  return { ...opened, strip };
}

describe('edit and resend', () => {
  test('opening parks the draft, prefills the whole prompt and stored attachments; stopping changes nothing', async () => {
    fixture();
    const resend = vi.spyOn(api, 'resend');
    const rewind = vi.spyOn(api, 'rewind');
    const { user, mock, strip } = await startEdit();
    expect(within(composer().closest('form')!).getAllByText(/dumb-terminal\.png/).length).toBeGreaterThan(0);
    expect(strip.getByText(/1 attachment of the original prompt has no stored copy/)).toBeTruthy();
    await user.click(strip.getByRole('button', { name: 'Stop editing' }));
    await waitFor(() => expect(composer().value).toBe('My other draft'));
    expect(screen.queryByRole('group', { name: 'Editing a past prompt' })).toBeNull();
    // Escape in the box stops editing too.
    await user.click(screen.getAllByRole('button', { name: 'Turn actions' })[1]);
    await user.click(await screen.findByRole('menuitem', { name: 'Edit and resend…' }));
    await waitFor(() => expect(composer().value).toContain('Does it handle'));
    composer().focus();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(composer().value).toBe('My other draft'));
    expect(resend).not.toHaveBeenCalled();
    expect(rewind).not.toHaveBeenCalled();
    expect(mock.received.filter(r => r.route === 'prompt')).toHaveLength(0);
  });

  test('send rewinds and sends the edit once, then the other draft comes back', async () => {
    fixture();
    const resend = vi.spyOn(api, 'resend').mockResolvedValue(sent);
    const { user } = await startEdit();
    await user.clear(composer());
    await user.type(composer(), 'Edited: handle TERM=dumb too');
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await waitFor(() => expect(composer().value).toBe('My other draft'));
    expect(resend).toHaveBeenCalledTimes(1);
    const [id, body] = resend.mock.calls[0];
    expect(id).toBe('t3');
    expect(body.rewind).toMatchObject({ user_item_id: 'i5', mode: 'conversation-and-files', token: 'token-1' });
    expect(body.prompt.text).toBe('Edited: handle TERM=dumb too');
    expect(body.prompt.attachments).toEqual(['att-png-seed1', 'att-txt-seed1']);
    expect(body.prompt.request_id).not.toBe(body.rewind.request_id);
    expect(screen.queryByRole('group', { name: 'Editing a past prompt' })).toBeNull();
  });

  test.each([
    ['uncertain', { ...sent, submission: undefined, rewind: { ...sent.rewind, state: 'uncertain', result: undefined } }, /result was lost.*not sent/],
    ['partial', { ...sent, submission: undefined, rewind: { ...sent.rewind, result: { outcome: 'truncation-failed', restored_files: ['/w/a'], skipped_files: [] } } }, /conversation was not rewound\. Your edited prompt was not sent\./],
    ['send rejected', { ...sent, submission: undefined, send_error: 'attachment is missing' }, /not sent: attachment is missing/],
  ] as [string, ResendResult, RegExp][])('%s keeps the edited draft and never resends by itself', async (_name, result, message) => {
    fixture();
    const resend = vi.spyOn(api, 'resend').mockResolvedValue(result);
    const { user, strip } = await startEdit();
    await user.type(composer(), ' edited');
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await strip.findByText(message);
    expect(composer().value).toContain(' edited');
    await new Promise(resolve => setTimeout(resolve, 50));
    expect(resend).toHaveBeenCalledTimes(1);
  });

  test('after a rewind whose send was refused, a fixed edit reuses that rewind and sends as a new prompt', async () => {
    fixture();
    const resend = vi.spyOn(api, 'resend').mockResolvedValueOnce({ ...sent, submission: undefined, send_error: 'attachment is missing' }).mockResolvedValueOnce(sent);
    const { user, strip } = await startEdit();
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await strip.findByText(/not sent: attachment is missing/);
    await user.type(composer(), ' fixed');
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await waitFor(() => expect(composer().value).toBe('My other draft'));
    const [first, second] = resend.mock.calls.map(c => c[1]);
    expect(second.rewind.request_id).toBe(first.rewind.request_id);
    expect(second.prompt.request_id).not.toBe(first.prompt.request_id);
    expect(second.prompt.text).toContain(' fixed');
  });

  test('a fixed edit after an uncertain send keeps neither request', async () => {
    fixture();
    const resend = vi.spyOn(api, 'resend')
      .mockResolvedValueOnce({ ...sent, submission: { request_id: 'p', status: 'uncertain', time: new Date().toISOString() } as ResendResult['submission'] })
      .mockResolvedValueOnce(sent);
    const { user, strip } = await startEdit();
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await strip.findByText(/was rewound, but your edited prompt was not accepted/);
    await user.type(composer(), ' fixed');
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await waitFor(() => expect(resend).toHaveBeenCalledTimes(2));
    const [first, second] = resend.mock.calls.map(c => c[1]);
    expect(second.rewind.request_id).not.toBe(first.rewind.request_id);
  });

  test('a stale preview is read again and the edit waits for a new send', async () => {
    const { read } = fixture();
    const resend = vi.spyOn(api, 'resend').mockRejectedValueOnce(new ApiError(409, 'changed', { code: 'rewind_stale' })).mockResolvedValueOnce(sent);
    const { user } = await startEdit();
    const reads = read.mock.calls.length;
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await screen.findByText(/changed\. Check the strip, then send again/);
    await waitFor(() => expect(read.mock.calls.length).toBeGreaterThan(reads));
    expect(resend).toHaveBeenCalledTimes(1);
    expect(composer().value).toContain('Does it handle');
    await waitFor(() => expect((screen.getByRole('button', { name: 'Rewind and send' }) as HTMLButtonElement).getAttribute('aria-disabled')).toBeNull());
    await user.click(screen.getByRole('button', { name: 'Rewind and send' }));
    await waitFor(() => expect(composer().value).toBe('My other draft'));
    expect(resend.mock.calls[1][1].rewind.request_id).not.toBe(resend.mock.calls[0][1].rewind.request_id);
  });

  test('a plan review arriving mid-edit replaces the edit strip; the two never show together', async () => {
    fixture();
    const opened = renderApp('#task=t3');
    const Source = window.EventSource;
    let stream: EventSource | undefined;
    let snapshot: SnapshotData | undefined;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (String(url).startsWith('/api/events?')) this.addEventListener('snapshot', (event) => {
          stream = event.target as EventSource;
          snapshot = JSON.parse((event as MessageEvent).data) as SnapshotData;
        });
      }
    };
    try {
      await waitFor(() => expect(snapshot?.session?.id).toBe('t3'));
      await waitFor(() => expect(composer()?.disabled).toBe(false));
      await opened.user.click(screen.getAllByRole('button', { name: 'Turn actions' })[1]);
      await opened.user.click(await screen.findByRole('menuitem', { name: 'Edit and resend…' }));
      await screen.findByRole('group', { name: 'Editing a past prompt' });
      const original = snapshot!;
      const plan: Interaction = { id: 'plan-review', kind: 'plan_review', title: 'Plan ready', state: 'pending', time: new Date().toISOString(), plan: { request_id: 'plan-review', summary: 'Keep data', revision: 1, content: '# Plan', actions: ['interactive'], recommended: 'interactive' } };
      act(() => stream!.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify({
        ...original, seq: 1_000_000,
        sessions: original.sessions.map((session) => session.id === 't3' ? { ...session, state: 'awaiting_answer', pending: 1 } : session),
        session: { ...original.session!, state: 'awaiting_answer', pending: 1, capabilities: { ...original.session!.capabilities, plan: true }, interactions: [plan] },
      }) })));
      const form = within(composer().form!);
      await form.findByText('Plan ready');
      expect(screen.queryByRole('group', { name: 'Editing a past prompt' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Rewind and send' })).toBeNull();
      expect(composer().value).toBe('');
    } finally {
      window.EventSource = Source;
    }
  });
});
