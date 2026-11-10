// A failed Thread names its cause in plain words, with what to do, wherever its failure shows (mock t4, its kind changed).
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { log, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

/** The mock with t4 changed by `change`. */
function withFailed(change: (task: ReturnType<typeof data.seed>['tasks'][number]) => void) {
  const state = data.seed();
  change(state.tasks.find((t) => t.id === 't4')!);
  vi.spyOn(data, 'seed').mockReturnValue(state);
}

const row = () => document.querySelector<HTMLElement>('nav[aria-label="Threads"] [data-task-row="t4"]')!;

test('a rate limit leads with the cause and the action; the provider\'s own text follows once', async () => {
  withFailed((t) => {
    t.failure_kind = 'rate_limit';
    t.state_detail = '429 user_weekly_rate_limited';
    t.items = t.items.filter((i) => i.kind !== 'notice');
  });
  const { user } = await openTask('t4');
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toBe('Rate limit reached. Wait, then send your message again.429 user_weekly_rate_limited');
  expect(screen.queryByText(/^Turn failed/)).toBeNull();
  // The header chip and the Thread's row say the cause on hover; the state stays "Error".
  const state = await screen.findByRole('group', { name: 'State: Error' });
  expect(within(state).getByText('Error').closest('[title]')?.getAttribute('title')).toBe('Rate limit reached');
  await user.hover(row().querySelector('button[data-nav]')!);
  await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Rate limit reached'));
});

test('the provider\'s notice in the conversation is not repeated under the cause', async () => {
  await openTask('t4');
  expect(log().getByText('Error: provider process exited (code 1).')).toBeTruthy();
  expect((await screen.findByRole('alert')).textContent).toBe('Provider error. Send your message again.');
});

test('a failure with no known cause keeps the provider\'s own words as the line', async () => {
  withFailed((t) => {
    t.failure_kind = 'other';
    t.items = t.items.filter((i) => i.kind !== 'notice');
  });
  await openTask('t4');
  expect((await screen.findByRole('alert')).textContent).toBe('Turn failed: Provider process exited (code 1)');
});
