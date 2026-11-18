// A whole reply that ends a working Thread's transcript is marked up at once: a Copilot Fusion reply
// arrives whole while the critic still reviews (mock t8, a working Thread). A reply still arriving in
// deltas keeps its last block plain until it is whole.
import { waitFor } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { log, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

const REPLY = 'Done.\n\n> [!WARNING]\n> Check the dark theme after.';

async function openWithReply(partial: boolean) {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't8')!;
  task.items = [...(task.items ?? []), { id: 'r-last', kind: 'assistant', time: new Date().toISOString(), text: REPLY, ...(partial ? { partial } : {}) }];
  vi.spyOn(data, 'seed').mockReturnValue(state);
  await openTask('t8');
  return waitFor(() => log().getByText(/Check the dark theme after\./));
}

test('a whole reply at the foot of a working turn shows its alert as a callout', async () => {
  const line = await openWithReply(false);
  expect(line.closest<HTMLElement>('[data-callout]')?.dataset.callout).toBe('warning');
  expect(log().queryByText(/\[!WARNING\]/)).toBeNull();
});

test('a reply still arriving keeps its last block as written', async () => {
  const line = await openWithReply(true);
  expect(line.closest('[data-callout]')).toBeNull();
  expect(line.textContent).toMatch(/^\[!WARNING\]/);
});
