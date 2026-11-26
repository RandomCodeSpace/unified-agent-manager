// A failed Thread reads as one plain sentence with raw detail behind a disclosure,
// uses "Error" consistently, and offers the real fix before Resend for known causes.
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

test('a rate limit reads as one plain sentence, folds raw detail into Details, and keeps Resend only', async () => {
  withFailed((t) => {
    t.failure_kind = 'rate_limit';
    t.state_detail = '429 user_weekly_rate_limited';
    t.items = t.items.filter((i) => i.kind !== 'notice');
  });
  const { user } = await openTask('t4');
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toBe('Rate limit reached; wait a moment, then send your message again.');
  expect(screen.queryByText(/^Turn failed/)).toBeNull();
  const details = log().getByText('Details').closest('details')!;
  expect(details.open).toBe(false);
  await user.click(log().getByText('Details'));
  expect(details.open).toBe(true);
  expect(within(details).getByText('429 user_weekly_rate_limited')).toBeTruthy();
  expect(screen.queryByRole('button', { name: /Sign in in Settings|Check account in Settings/ })).toBeNull();
  expect(screen.getByRole('button', { name: 'Resend last prompt' })).toBeTruthy();
  // The header chip and the Thread's row say the cause on hover; the state stays "Error".
  const state = await screen.findByRole('group', { name: 'State: Error' });
  expect(within(state).getByText('Error').closest('[title]')?.getAttribute('title')).toBe('Rate limit reached');
  await user.hover(row().querySelector('button[data-nav]')!);
  await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Rate limit reached'));
});

test('mock t4 (401 Bad credentials) shows one sentence, folds the raw notice into Details, and offers Sign in in Settings before Resend', async () => {
  const { user } = await openTask('t4');
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toBe('Sign in to your account in Settings, then send your message again.');
  // The raw provider notice is folded into Details rather than shown inline or deleted.
  const details = log().getByText('Details').closest('details')!;
  expect(details.open).toBe(false);
  expect(within(details).getByText('Error: provider process exited (code 1).')).toBeTruthy();
  // Sign in in Settings comes before Resend last prompt and opens Settings at GitHub Copilot.
  const signIn = screen.getByRole('button', { name: 'Sign in in Settings' });
  const resend = screen.getByRole('button', { name: 'Resend last prompt' });
  expect(signIn.compareDocumentPosition(resend) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  await user.click(signIn);
  expect(await screen.findByRole('heading', { name: 'GitHub Copilot' })).toBeTruthy();
});

test('a backend-classified authentication failure offers Sign in in Settings and opens the account section', async () => {
  withFailed((t) => {
    t.failure_kind = 'authentication';
    t.state_detail = 'GitHub Copilot is signed out.';
    t.items = t.items.filter((i) => i.kind === 'user');
  });
  const { user } = await openTask('t4');
  expect((await screen.findByRole('alert')).textContent).toBe('Sign in to your account in Settings, then send your message again.');
  const signIn = screen.getByRole('button', { name: 'Sign in in Settings' });
  const resend = screen.getByRole('button', { name: 'Resend last prompt' });
  expect(signIn.compareDocumentPosition(resend) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  await user.click(signIn);
  expect(await screen.findByRole('heading', { name: 'GitHub Copilot' })).toBeTruthy();
});

test('a failure with no known cause uses the Error vocabulary, folds detail into Details, and keeps Resend only', async () => {
  withFailed((t) => {
    t.failure_kind = 'other';
    t.state_detail = 'Provider process exited (code 1)';
    t.items = t.items.filter((i) => i.kind === 'user');
  });
  await openTask('t4');
  expect((await screen.findByRole('alert')).textContent).toBe('Stopped with an error.');
  expect(screen.queryByText(/^Turn failed/)).toBeNull();
  const details = log().getByText('Details').closest('details')!;
  expect(details.open).toBe(false);
  expect(within(details).getByText('Provider process exited (code 1)')).toBeTruthy();
  expect(screen.queryByRole('button', { name: /Sign in in Settings|Check account in Settings/ })).toBeNull();
  expect(screen.getByRole('button', { name: 'Resend last prompt' })).toBeTruthy();
});
