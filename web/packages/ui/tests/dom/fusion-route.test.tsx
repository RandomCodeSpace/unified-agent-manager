// A Copilot Fusion turn shows its route on the turn's head (a Standard turn, the model it picked), a row per phase at the conversation's foot, and the
// reviewing phase on the strip (mock t8, a working Thread, given the route of a recorded Critique turn).
import { screen, waitFor } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { log, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString();

test('a Critique turn names its route on the head and lists its phases, the reviewing one on the strip too', async () => {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't8')!;
  task.turn_activity = {
    ...task.turn_activity!,
    fusion: {
      level: 'high', pattern: 'critique', hint: 'Using Critique: a solver will draft a result, a critic will review it.', models: ['kimi-k3', 'gpt-5.6-luna'],
      phases: [
        { id: 'f:phase:0', kind: 'draft', role: 'solver', model: 'kimi-k3', status: 'succeeded', started_at: ago(90), ended_at: ago(20), tokens: 148298 },
        { id: 'f:critic', kind: 'critic', role: 'critic', model: 'gpt-5.6-luna', effort: 'high', status: 'running', started_at: ago(20) },
        { kind: 'revision', role: 'solver', status: 'planned', conditional: true },
      ],
    },
  };
  vi.spyOn(data, 'seed').mockReturnValue(state);
  await openTask('t8');
  const route = await waitFor(() => log().getByText('Fusion High · Critique · 2 models'));
  expect(route.getAttribute('title')).toMatch(/^Using Critique/);
  const foot = await screen.findByRole("region", { name: /at work$/ });
  expect(foot.querySelector('[aria-label^="Fusion Draft (Kimi K3), succeeded: Done"]')).not.toBeNull();
  expect(foot.querySelector('[aria-label^="Fusion Critic (GPT-5.6 Luna · high), running: Reviewing"]')).not.toBeNull();
  expect(foot.querySelector('[aria-label="Fusion Revision, planned: Runs only if the review asks for changes"]')).not.toBeNull();
  await waitFor(() => expect(screen.getByText(/^Critic reviewing, /, { selector: '.sr-only' })).toBeTruthy());
});

test('a Fusion Standard turn names the model it picked and why on the head, with no phase rows', async () => {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't8')!;
  task.turn_activity = {
    ...task.turn_activity!,
    fusion: { level: 'standard', pattern: 'single', hint: 'Copilot Fusion selected Kimi K3 since the task has medium complexity.', models: ['kimi-k3'], phases: [] },
  };
  vi.spyOn(data, 'seed').mockReturnValue(state);
  await openTask('t8');
  const route = await waitFor(() => log().getByText('Fusion Standard · Kimi K3'));
  expect(route.getAttribute('title')).toMatch(/^Copilot Fusion selected Kimi K3/);
  expect(screen.queryByRole('region', { name: 'Fusion at work' })).toBeNull();
});
