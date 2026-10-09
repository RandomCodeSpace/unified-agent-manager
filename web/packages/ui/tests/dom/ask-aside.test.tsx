import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type AsideAnswer, type SessionDetail } from '../../src/api';
import { AskAside } from '../../src/components/AskAside';

afterEach(() => { vi.restoreAllMocks(); });
const session = { id: 'task-1', conversation_id: 'conv-1', provider: 'copilot', model: 'auto', open: true, capabilities: { aside: true } } as unknown as SessionDetail;

function draw(selected = session, client = api) {
  const tree = (current: SessionDetail, owner = client) => <ApiContext.Provider value={owner}><AskAside session={current} /></ApiContext.Provider>;
  const view = render(tree(selected));
  return { ...view, select: (current: SessionDetail) => view.rerender(tree(current)), selectOwner: (owner: typeof api) => view.rerender(tree(selected, owner)) };
}

async function ask(text: string) {
  await userEvent.click(screen.getByRole('button', { name: 'Ask aside' }));
  const field = await screen.findByRole('textbox', { name: 'Aside question' });
  await waitFor(() => expect(document.activeElement).toBe(field));
  await userEvent.type(field, text);
  await userEvent.keyboard('{Enter}');
}

test('an aside shows its answer in the reader only, and closing drops it', async () => {
  const send = vi.spyOn(api, 'askAside').mockResolvedValue({ text: 'It **passed**.' });
  draw();
  expect(send).not.toHaveBeenCalled();
  await ask('  did the tests pass?  ');
  expect(await screen.findByText('passed')).toBeTruthy();
  expect(screen.getByText('did the tests pass?')).toBeTruthy();
  expect(send).toHaveBeenCalledTimes(1);
  expect(send.mock.calls[0].slice(0, 2)).toEqual(['task-1', 'did the tests pass?']);
  expect((screen.getByRole('textbox', { name: 'Aside question' }) as HTMLTextAreaElement).value).toBe('');
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  await waitFor(() => expect(screen.queryByText('passed')).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Ask aside' })));
  await userEvent.click(screen.getByRole('button', { name: 'Ask aside' }));
  await screen.findByRole('textbox', { name: 'Aside question' });
  expect(screen.queryByText('passed')).toBeNull();
});

test('one question waits at a time; the wait is words, not a spinner', async () => {
  let deliver!: (a: AsideAnswer) => void;
  const send = vi.spyOn(api, 'askAside').mockImplementation(() => new Promise((resolve) => { deliver = resolve; }));
  draw();
  await ask('first');
  expect(await screen.findByText('Waiting for the answer…')).toBeTruthy();
  expect(document.querySelector('[aria-busy="true"]')).toBeNull();
  await userEvent.type(screen.getByRole('textbox', { name: 'Aside question' }), 'second{Enter}');
  expect(send).toHaveBeenCalledTimes(1);
  expect((screen.getByRole('button', { name: 'Ask' }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => deliver({ text: 'first answer', truncated: true }));
  expect(await screen.findByText('first answer')).toBeTruthy();
  expect(screen.getByText(/only its start is shown/)).toBeTruthy();
});

test('closing, a changed selection and a changed API owner abort the wait and ignore a late answer', async () => {
  let deliver!: (a: AsideAnswer) => void;
  const send = vi.spyOn(api, 'askAside').mockImplementation(() => new Promise((resolve) => { deliver = resolve; }));
  const view = draw();
  await ask('q');
  const first = send.mock.calls[0][2]!;
  await userEvent.click(screen.getByRole('button', { name: 'Close', exact: true }));
  expect(first.aborted).toBe(true);
  await ask('q');
  const second = send.mock.calls[1][2]!;
  view.select({ ...session, model: 'next' });
  expect(second.aborted).toBe(true);
  await act(async () => deliver({ text: 'obsolete answer' }));
  expect(screen.queryByText('obsolete answer')).toBeNull();
  expect(screen.queryByRole('dialog', { name: 'Ask aside' })).toBeNull();
  const remote = vi.fn(() => new Promise<AsideAnswer>(() => {}));
  view.unmount();
  const next = draw(session, api);
  await ask('q');
  const third = send.mock.calls[2][2]!;
  next.selectOwner({ ...api, askAside: remote });
  expect(third.aborted).toBe(true);
  expect(screen.queryByRole('dialog', { name: 'Ask aside' })).toBeNull();
});

test('a closed or inactive Task never asks and never reopens', async () => {
  const send = vi.spyOn(api, 'askAside');
  const view = draw({ ...session, open: false });
  await userEvent.click(screen.getByRole('button', { name: 'Ask aside' }));
  expect(await screen.findByText(/needs the Task's conversation open/)).toBeTruthy();
  expect(screen.queryByRole('textbox', { name: 'Aside question' })).toBeNull();
  view.select({ ...session, stage: 'settled' });
  await userEvent.click(screen.getByRole('button', { name: 'Ask aside' }));
  await screen.findByText(/needs the Task's conversation open/);
  expect(send).not.toHaveBeenCalled();
});

test('oversized errors are bounded and the phone reader uses the owning API', async () => {
  vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
  const local = vi.spyOn(api, 'askAside');
  const remote = vi.fn(async () => { throw new Error('Connected failure: ' + 'x'.repeat(2_000_000)); });
  draw(session, { ...api, askAside: remote });
  await ask('q');
  await waitFor(() => expect(screen.getByRole('status').textContent?.startsWith('Connected failure: ')).toBe(true));
  expect(screen.getByRole('status').textContent?.length).toBe(512);
  expect(remote).toHaveBeenCalledTimes(1);
  expect(local).not.toHaveBeenCalled();
  await userEvent.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Ask aside' })).toBeNull());
});

test('a long question collapses behind "Show the whole question"', async () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(400);
  vi.spyOn(api, 'askAside').mockResolvedValue({ text: 'short answer' });
  try {
    draw();
    await ask('a long question');
    expect(await screen.findByText('short answer')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Show the whole question' }).getAttribute('aria-expanded')).toBe('false');
  } finally {
    vi.unstubAllGlobals();
  }
});
