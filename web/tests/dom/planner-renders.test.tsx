// The app renders on every streamed delta of a reply; the planner panel over the Task must not
// render its views for any of them. A file of its own: it counts TreeView's renders by mocking
// the module, which holds for every test in the file.
import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { log, openTask } from './render';

const renders = vi.hoisted(() => ({ tree: 0 }));

vi.mock('../../src/components/planner/TreeView', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../src/components/planner/TreeView')>();
  // The real component, run as the body of this one: each of its renders counts, from a parent or from its context.
  return {
    ...real,
    TreeView: (props: Parameters<typeof real.TreeView>[0]) => {
      renders.tree++;
      return real.TreeView(props);
    },
  };
});

test('a streamed reply renders none of the Task’s planner panel', async () => {
  await openTask('t6');
  const task = await api.createSession({ project_id: 'p1', provider: 'copilot', request_id: 'streamed-reply', prompt: 'Tidy the transcript code' });
  window.location.hash = `#task=${task.id}`;
  const panel = within(await screen.findByRole('region', { name: 'Planner pop-out' }));
  await panel.findAllByRole('treeitem');
  // Settled before the reply starts streaming (600 ms in).
  await new Promise((resolve) => setTimeout(resolve, 200));
  renders.tree = 0;
  // Reasoning and the reply stream in word by word, then the turn ends.
  await waitFor(() => expect(log().getByText('Tests pass. Anything else?')).toBeTruthy(), { timeout: 15000 });
  expect(renders.tree).toBe(0);
});
