// The app renders on every streamed delta of a reply; the Task's Plan panel must not render its
// card details for any of them. A file of its own: it counts the checklist's renders by mocking
// the module, which holds for every test in the file.
import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { openTask } from './render';

const renders = vi.hoisted(() => ({ checklist: 0 }));

vi.mock('../../src/components/planner/CardPanel', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../src/components/planner/CardPanel')>();
  // The real component, run as the body of this one: each of its renders counts.
  return {
    ...real,
    Checklist: (props: Parameters<typeof real.Checklist>[0]) => {
      renders.checklist++;
      return real.Checklist(props);
    },
  };
});

test('a streamed reply renders none of the Task’s Plan panel', async () => {
  const { user } = await openTask('t6');
  const task = await api.createSession({ project_id: 'p1', provider: 'copilot', request_id: 'streamed-reply', prompt: 'Tidy the transcript code' });
  // The new Task works on a subtask, so its panel opens on that card's details and checklist.
  await api.planner.attach('cp1-22', { task_id: task.id });
  window.location.hash = `#task=${task.id}`;
  await user.click(await screen.findByRole('button', { name: /^This task works on #22/ }));
  const panel = within(await screen.findByRole('dialog', { name: 'Plan' }));
  await panel.findByRole('region', { name: '#22 details' });
  // Settled before the reply starts streaming (600 ms in).
  await new Promise((resolve) => setTimeout(resolve, 200));
  renders.checklist = 0;
  // Reasoning and the reply stream in word by word, then the turn ends.
  // The panel is the overlay sheet here, so the conversation under it is hidden from the accessibility tree.
  await waitFor(() => expect(within(screen.getByRole('log', { hidden: true })).getByText('Tests pass. Anything else?')).toBeTruthy(), { timeout: 15000 });
  expect(renders.checklist).toBe(0);
});
