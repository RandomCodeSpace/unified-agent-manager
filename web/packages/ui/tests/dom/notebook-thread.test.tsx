// The notebook layout's Thread chrome (components/notebook): the live card in the running turn's
// cell, a waiting question in that cell, the settings line under the title and the composer as
// the next cell. The current layout draws none of it.
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type ChangeFile, type Interaction, type Item, type SessionDetail, type Subagent, type Todo, type TurnTiming } from '../../src/api';
import { InteractionCard } from '../../src/components/Interactions';
import { InNotebookSlot } from '../../src/components/notebook/LiveCard';
import { NotebookSlots, useNotebookSlotRef } from '../../src/components/notebook/slots';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import { SubagentScope } from '../../src/components/Subagents';
import { applyLayout } from '../../src/lib/layout';
import { nextTurn } from '../../src/lib/tasks';
import { choose, openTask } from './render';

afterEach(() => applyLayout('classic'));

const since = '2026-11-10T12:00:00Z';
const timing = { id: 'turn', user_item_id: 'u1', state: 'working', started_at: since } as TurnTiming;
const todos = [
  { id: 'a', title: 'Plan the pages', status: 'done' },
  { id: 'b', title: 'Build the index page', status: 'in_progress' },
  { id: 'c', title: 'Sign in to the registry', status: 'blocked', note: 'No token on this machine' },
] as Todo[];
const items = [
  { id: 'u1', kind: 'user', time: since, text: 'Build the site' },
  { id: 'call-1', kind: 'tool', time: since, tool: { name: 'task', status: 'running' } },
  { id: 'cmd', kind: 'tool', time: since, tool: { name: 'bash', status: 'running' } },
] as Item[];
const subagent = { id: 's1', name: 'explore', label: 'Survey the templates', status: 'running', parent_tool_call_id: 'call-1', started_at: since } as Subagent;
const working = {
  id: 't', state: 'working', items, interactions: [], subagents: [subagent], turn_timings: [timing],
  turn_activity: { todos: { known: true, touched: true, todos, counts: { total: 3, done: 1, in_progress: 1, blocked: 1 } }, phase: { kind: 'local', since, tool: 'bash', detail: 'go test ./...' } },
} as unknown as SessionDetail;
const question: Interaction = { id: 'q', kind: 'question', title: 'Two questions', state: 'pending', time: since, questions: [{ text: 'Which terminals?', custom: true }, { text: 'Anything else?', custom: true }] };
const waiting = { ...working, state: 'awaiting_answer', subagents: [], interactions: [question], turn_activity: { ...working.turn_activity, phase: { kind: 'waiting', since, detail: 'Which terminals?' } } } as unknown as SessionDetail;
const files = [{ path: 'site/index.html', status: 'modified', additions: 4, deletions: 1 }] as ChangeFile[];

/** Stands in for the running turn's cell, which gives the Thread its two slots. */
function Cell({ slots }: Readonly<{ slots: boolean }>) {
  const live = useNotebookSlotRef('live');
  const asks = useNotebookSlotRef('asks');
  return slots ? <><div data-testid="asks" ref={asks} /><div data-testid="live" ref={live} /></> : null;
}

function strip(session: SessionDetail, slots: boolean, onReview = () => {}) {
  return render(
    <ApiContext.Provider value={{ ...api, todoHistory: () => Promise.reject(new Error('offline')) }}>
      <SubagentScope session={session} agents={{}} agentSteps={{}} snapshotSeq={0} onLocate={() => {}} onJumpToReply={() => {}} onShow={() => {}}>
        <NotebookSlots>
          <Cell slots={slots} />
          <div data-testid="strip">
            <StatusLine line={statusLine(session, session.state === 'working', false)} session={session} hidden={false} jump={false} onJump={() => {}} changed={files} onReview={onReview} />
          </div>
        </NotebookSlots>
      </SubagentScope>
    </ApiContext.Provider>,
  );
}

const stripButtons = () => within(screen.getByTestId('strip')).queryAllByRole('button').map((b) => b.textContent);

test('with the running cell on screen, Now, Plan, Subagents and the changed files stand in the live card and the strip draws none of them', async () => {
  applyLayout('notebook');
  const onReview = vi.fn();
  strip(working, true, onReview);
  const card = within(await screen.findByRole('region', { name: 'This turn so far' }));
  expect(screen.getByTestId('live').contains(screen.getByRole('region', { name: 'This turn so far' }))).toBe(true);
  expect(within(card.getByRole('region', { name: 'Now' })).getByText('Running go test ./...', { ignore: '.sr-only' })).toBeTruthy();
  // The list, each row's stage in a word.
  const plan = within(card.getByRole('region', { name: 'Plan' }));
  expect(plan.getByText('1 of 3')).toBeTruthy();
  expect(plan.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['In progressBuild the index page', 'BlockedSign in to the registryNo token on this machine', 'DonePlan the pages']);
  expect(within(card.getByRole('region', { name: 'Subagents' })).getByRole('button', { name: /^Survey the templates \(explore\), running/ })).toBeTruthy();
  fireEvent.click(within(card.getByRole('region', { name: 'Changed so far' })).getByRole('button', { name: 'Review' }));
  expect(onReview).toHaveBeenCalledTimes(1);
  // One home per fact: the strip keeps no list, no progress line and no subagents.
  expect(stripButtons()).toEqual([]);
  expect(document.querySelectorAll('[data-progress-words]')).toHaveLength(1);
  // A section folds; the list's reader opens from its word.
  const fold = plan.getByRole('button', { name: /^Plan/ });
  fireEvent.click(fold);
  expect(fold.getAttribute('aria-expanded')).toBe('false');
  fireEvent.click(plan.getByRole('button', { name: 'Open the list' }));
  expect(await screen.findByRole('dialog', { name: 'Todo' })).toBeTruthy();
});

test('a Thread that waits on an answer draws no Now: the card does not restate the wait', async () => {
  applyLayout('notebook');
  strip(waiting, true);
  const card = within(await screen.findByRole('region', { name: 'This turn so far' }));
  expect(card.getByRole('region', { name: 'Plan' })).toBeTruthy();
  expect(card.queryByRole('region', { name: 'Now' })).toBeNull();
  expect(screen.queryByText('Needs answer')).toBeNull();
});

test('with no cell on screen the strip draws as before, and the current layout never draws the card', () => {
  applyLayout('notebook');
  const none = strip(working, false);
  expect(screen.queryByRole('region', { name: 'This turn so far' })).toBeNull();
  expect(screen.getByRole('button', { name: /^Todo 1 of 3 done.*Show the list$/ })).toBeTruthy();
  expect(screen.getByRole('button', { name: /^Subagents: 1.*Show the subagents$/ })).toBeTruthy();
  expect(screen.getByText('Running go test ./...', { ignore: '.sr-only' })).toBeTruthy();
  none.unmount();
  applyLayout('classic');
  strip(working, true);
  expect(screen.queryByRole('region', { name: 'This turn so far' })).toBeNull();
  expect(screen.getByTestId('live').childElementCount).toBe(0);
  expect(screen.getByRole('button', { name: /^Todo 1 of 3 done.*Show the list$/ })).toBeTruthy();
  expect(screen.getByRole('button', { name: /^Subagents: 1.*Show the subagents$/ })).toBeTruthy();
});

function asks(slots: boolean) {
  return render(
    <NotebookSlots>
      <Cell slots={slots} />
      <div data-testid="place">
        <InNotebookSlot name="asks">
          <InteractionCard session={{ id: 't', capabilities: { permissions: true, questions: true } } as unknown as SessionDetail} interaction={question} onUpdate={() => {}} />
        </InNotebookSlot>
      </div>
    </NotebookSlots>,
  );
}

test('a pending question renders in the cell when it gives the slot, and in its own place when it does not', async () => {
  applyLayout('notebook');
  const inCell = asks(true);
  expect(await within(screen.getByTestId('asks')).findByText('Which terminals?')).toBeTruthy();
  expect(screen.getByTestId('place').childElementCount).toBe(0);
  inCell.unmount();
  const noCell = asks(false);
  expect(within(screen.getByTestId('place')).getByText('Which terminals?')).toBeTruthy();
  noCell.unmount();
  applyLayout('classic');
  asks(true);
  expect(within(screen.getByTestId('place')).getByText('Which terminals?')).toBeTruthy();
  expect(screen.getByTestId('asks').childElementCount).toBe(0);
});

test('in a Thread without cells the question stays under the conversation', async () => {
  applyLayout('notebook');
  await openTask('t2');
  expect(await within(screen.getByRole('log')).findByText('Which terminals should the explanation cover?')).toBeTruthy();
});

test('the notebook layout moves the settings controls to a labelled line under the title, where the model still changes; the composer is the next cell', async () => {
  applyLayout('notebook');
  const { user } = await openTask('t3');
  const line = within(await screen.findByRole('group', { name: 'Thread settings' }));
  const model = await line.findByRole('button', { name: 'Model: Auto' });
  expect(line.getByText('Model')).toBeTruthy();
  expect(line.getByText('Mode')).toBeTruthy();
  expect(model.closest('form')).toBeNull();
  await choose(user, 'Model: Auto', /GPT-5 mini/);
  expect(await line.findByRole('button', { name: 'Model: GPT-5 mini' })).toBeTruthy();
  // Writing and sending stay in the composer.
  const form = document.getElementById('composer-text')!.closest('form')!;
  expect(within(form).getByRole('button', { name: /^Attach files/ })).toBeTruthy();
  expect(within(form).getByRole('button', { name: /^Send/ })).toBeTruthy();
  expect(within(form).queryByRole('button', { name: /^Model:/ })).toBeNull();
  expect(screen.getByText('next').previousElementSibling?.textContent).toMatch(/^Turn \d+$/);
});

test('the current layout keeps the controls in the composer, with no settings line and no cell gutter', async () => {
  await openTask('t3');
  const model = await screen.findByRole('button', { name: 'Model: Auto' });
  expect(model.closest('form')).toBe(document.getElementById('composer-text')!.closest('form'));
  expect(screen.queryByRole('group', { name: 'Thread settings' })).toBeNull();
  expect(screen.queryByText('next')).toBeNull();
  await waitFor(() => expect(screen.queryByText('Model')).toBeNull());
});

test('the next turn takes the number after the turns so far and the queued messages', () => {
  expect(nextTurn({})).toBe(1);
  expect(nextTurn({ turn_timings: [timing, timing], queued: 1 })).toBe(4);
  // Turns without timings are counted by their messages; a subagent's are not turns.
  expect(nextTurn({ items: [{ id: 'u', kind: 'user', time: since }, { id: 's', kind: 'user', time: since, agent_id: 'x' }] as Item[] })).toBe(2);
});
