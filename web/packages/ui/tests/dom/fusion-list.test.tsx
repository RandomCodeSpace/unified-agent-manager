// The strip's list holds Copilot Fusion's recorded phases and the background commands beside the subagents, under
// the message whose reply they started in, with kind filters; a phase opens its reader (mock t8, given a recorded
// Critique turn and a background command in its latest reply).
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { openSubagentIndex, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

test('the list shows Fusion phases and commands with the subagents, filters them by kind, and a phase reads its verdict', async () => {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't8')!;
  const last = [...task.items].reverse().find((i) => i.kind === 'user' && !i.agent_id)!;
  const after = (s: number) => new Date(Date.parse(last.time!) + s * 1000).toISOString();
  task.fusionTurns = [{
    id: 'f1', started_at: after(1), level: 'high', pattern: 'critique', hint: 'Using Critique: a solver will draft, a critic will review.', models: ['kimi-k3', 'gpt-5.6-luna'],
    phases: [
      { id: 'f1:phase:0', kind: 'draft', model: 'kimi-k3', status: 'succeeded', started_at: after(1), ended_at: after(60), tokens: 1200 },
      { id: 'f1:critic', kind: 'critic', model: 'gpt-5.6-luna', effort: 'high', status: 'succeeded', started_at: after(60), ended_at: after(80), tokens: 300, nano_aiu: 66136000, verdict: 'revise', note: 'Close races **Hit**.', defect: 'A hit after Close is counted.' },
      { kind: 'revision', status: 'planned', conditional: true },
    ],
  }];
  task.background_tasks = { known: true, tasks: [{ id: 'bg-1', command: 'go test -race ./...', description: 'Race tests', status: 'running', started_at: after(2) }] };
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const { user } = await openTask('t8');
  const list = await openSubagentIndex(user);
  const kinds = within(list.getByRole('group', { name: 'Show by kind' }));
  await waitFor(() => expect(kinds.getByRole('button', { name: 'Fusion 3' })).toBeTruthy());
  expect(kinds.getByRole('button', { name: 'Commands 1' })).toBeTruthy();
  expect(list.getByRole('searchbox', { name: 'Search agents and commands' }).getAttribute('placeholder')).toMatch(/^Search \d+ items$/);
  expect(list.getByRole('button', { name: 'Background command Race tests, running: show its output' })).toBeTruthy();

  await user.click(kinds.getByRole('button', { name: 'Fusion 3' }));
  expect(list.queryByRole('button', { name: /^Background command/ })).toBeNull();
  expect(list.getByRole('button', { name: 'Fusion Revision, planned: Runs only if the review asks for changes' })).toBeTruthy();
  await user.click(list.getByRole('button', { name: /^Fusion Critic \(GPT-5\.6 Luna · high\), succeeded: Asked for changes/ }));
  // In the list's place: no room beside the sheet here.
  const reader = within(await screen.findByRole('region', { name: 'Critic · GPT-5.6 Luna' }));
  expect(reader.getByText(/ · Fusion High critic · high · 300 tokens · 0\.07 AI units$/)).toBeTruthy();
  expect(reader.getByText('Asked for changes')).toBeTruthy();
  expect(reader.getByText('Hit').tagName).toBe('STRONG');
  expect(reader.getByText('A hit after Close is counted.')).toBeTruthy();
  expect(reader.getByRole('button', { name: 'Show in conversation' })).toBeTruthy();
});

test('a Thread with commands and no subagent opens the list from the strip and keeps it open', async () => {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't1')!;
  task.subagents = [];
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const { user } = await openTask('t1');
  await user.click(await screen.findByRole('button', { name: 'Show the agents and commands' }));
  const list = within(await screen.findByRole('dialog', { name: 'Agents and commands' }));
  expect(await list.findByRole('button', { name: 'Background command Repeat the redraw tests, running: show its output' })).toBeTruthy();
  expect(screen.getByRole('dialog', { name: 'Agents and commands' })).toBeTruthy();
});
