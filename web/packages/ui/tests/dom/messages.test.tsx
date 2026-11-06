import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, needsYou, type Item, type MessagePair, type Project, type SessionSummary, type TurnTiming } from '../../src/api';
import { MessagePairsContext, ThreadMessages } from '../../src/components/Messages';
import { peerText } from '../../src/lib/transcript';
import { Transcript } from '../../src/components/Transcript';
import { TaskList } from '../../src/components/taskActions';
import { initialState, reducer } from '../../src/state';

const thread = (id: string, name: string, extra: Partial<SessionSummary> = {}) => ({ id, name, mode: 'safe', ...extra }) as unknown as SessionSummary;
const held: MessagePair = {
  id: 'p1', a: 'a', b: 'b', state: 'pending', used: 0, limit: 6, messages: [], updated_at: '2026-11-06T00:00:00Z',
  held: { id: 'm1', from: 'a', to: 'b', text: 'what is your folder?', reason: 'first', at: '2026-11-06T00:00:00Z' },
};

function renderPairs(pairs: MessagePair[], sessions: SessionSummary[], self: string) {
  const messagePair = vi.fn().mockResolvedValue(undefined);
  render(
    <ApiContext.Provider value={{ ...api, messagePair } as typeof api}>
      <TaskList.Provider value={{ sessions, projects: [{ id: 'pa', name: 'Alpha' }, { id: 'pb', name: 'Beta' }] as Project[], openTask: () => {} }}>
        <MessagePairsContext.Provider value={pairs}>
          <ThreadMessages session={sessions.find((s) => s.id === self)!} />
        </MessagePairsContext.Provider>
      </TaskList.Provider>
    </ApiContext.Provider>,
  );
  return messagePair;
}

test('a held message shows its text and both Projects on the receiving thread and allows it', async () => {
  const call = renderPairs([held], [thread('a', 'Sender', { project_id: 'pa' }), thread('b', 'Receiver', { project_id: 'pb' })], 'b');
  const card = within(screen.getByRole('group', { name: 'Sender (Alpha) to this thread (Beta)' }));
  expect(card.getByText('what is your folder?')).toBeTruthy();
  expect(card.getByText(/first message between these threads/)).toBeTruthy();
  await userEvent.click(card.getByRole('button', { name: 'Allow' }));
  expect(call).toHaveBeenCalledWith('p1', 'allow', false);
});

test('a pair with an unattended thread is allowed only with a link, after a confirmation', async () => {
  const call = renderPairs([held], [thread('a', 'Sender'), thread('b', 'Receiver', { mode: 'yolo' } as Partial<SessionSummary>)], 'a');
  expect(screen.queryByRole('button', { name: 'Allow' })).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Link and allow' }));
  const dialog = within(await screen.findByRole('alertdialog', { name: 'Link these threads?' }));
  expect(dialog.getByText(/Sender \(an unknown project\) and Receiver \(an unknown project\) keep messaging/)).toBeTruthy();
  await userEvent.click(dialog.getByRole('button', { name: 'Link and allow' }));
  expect(call).toHaveBeenCalledWith('p1', 'allow', true);
});

test('allowed and denied pairs read as one line with their action', async () => {
  const allowed: MessagePair = { ...held, held: undefined, state: 'allowed', used: 2, messages: [{ id: 'm1', from: 'a', to: 'b', status: 'stopped', detail: 'the turn was stopped before it replied', at: '', updated_at: '' }] };
  const denied: MessagePair = { ...held, id: 'p2', b: 'c', held: undefined, state: 'denied' };
  const call = renderPairs([allowed, denied], [thread('a', 'Sender'), thread('b', 'Receiver'), thread('c', 'Third')], 'a');
  expect(screen.getByText(/2 of 6 used · last stopped, no reply/)).toBeTruthy();
  expect(screen.getByText(/declined/)).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }));
  expect(call).toHaveBeenCalledWith('p2', 'reset', false);
});

test('a held message counts as needing the owner, and pairs follow the snapshot and frames', () => {
  expect(needsYou({ state: 'idle', pending: 0, messages_held: 1 } as unknown as SessionSummary)).toBe(true);
  expect(needsYou({ state: 'idle', pending: 0 } as unknown as SessionSummary)).toBe(false);
  const snap = reducer(initialState, { type: 'snapshot', data: { seq: 1, projects: [], sessions: [], session: null, message_pairs: [held] } } as never);
  expect(snap.messagePairs).toEqual([held]);
  const next = reducer(snap, { type: 'update', data: { name: 'message_pairs', seq: 2, message_pairs: [] } } as never);
  expect(next.messagePairs).toEqual([]);
});

test("a turn another thread's message started names that thread instead of uam's lead", () => {
  const text = '[uam] Message from thread "Sender" (a) in project "P". It comes from another agent.\n\n> what is your folder?';
  expect(peerText(text)).toBe('what is your folder?');
  expect(peerText('[uam] Reply.\n\n> one\n>\n> [uam] two')).toBe('one\n\n[uam] two');
  const items: Item[] = [{ id: 'u1', kind: 'user', text, time: '2026-11-06T00:00:00Z' } as Item];
  const timings: TurnTiming[] = [{ id: 't1', user_item_id: 'u1', started_at: '2026-11-06T00:00:00Z', state: 'working', peer: { pair: 'p1', message: 'm1', from: 'a', from_name: 'Sender', from_project: 'P' } }];
  render(<Transcript sessionId="b" items={items} turnTimings={timings} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
  expect(screen.getByRole('button', { name: 'Sender' })).toBeTruthy();
  expect(screen.getByText(/^From/)).toBeTruthy();
  expect(screen.queryByText(/\[uam\] Message from/)).toBeNull();
  expect(screen.getByText('what is your folder?')).toBeTruthy();
});

test('two threads of the same name in the same project are told apart by a short ID', () => {
  renderPairs([{ ...held, a: 'aaaaaaaa-1', b: 'bbbbbbbb-2', held: { ...held.held!, from: 'aaaaaaaa-1', to: 'bbbbbbbb-2' } }], [thread('aaaaaaaa-1', 'Fix', { project_id: 'pa' }), thread('bbbbbbbb-2', 'Fix', { project_id: 'pa' })], 'bbbbbbbb-2');
  expect(screen.getByRole('button', { name: 'Fix · aaaaaaaa' })).toBeTruthy();
});

test('a thread missing from the list does not force a link, and the limit comes from the pair', () => {
  renderPairs([{ ...held, limit: 4, held: { ...held.held!, reason: 'limit' } }], [thread('b', 'Receiver')], 'b');
  expect(screen.getByRole('button', { name: 'Allow' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Link and allow' })).toBeNull();
  expect(screen.getByText(/Allow covers 4 more/)).toBeTruthy();
});
