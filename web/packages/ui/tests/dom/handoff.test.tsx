import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiError, api } from '../../src/api';
import { composer, renderApp } from './render';

afterEach(() => { vi.restoreAllMocks(); localStorage.clear(); });

/** Opens a Thread of the `?mock&budget` fixtures (src/mock/data.ts budgetTasks). */
async function openBudgetTask(id: string) {
  const rendered = renderApp(`?budget#task=${id}`);
  await screen.findByRole('region', { name: 'Conversation' });
  return rendered;
}

test('a Thread at its budget is Working, with no handoff yet', async () => {
  await openBudgetTask('t-wrap');
  // Wrapping up is a phase of the work, no state: the header reads Working.
  screen.getByRole('group', { name: 'State: Working' });
  const row = within(screen.getByRole('navigation', { name: 'Threads' })).getByRole('button', { name: /Port the pager tests/ });
  expect(within(row).getByText('Working')).toBeTruthy();
  expect(screen.queryByRole('region', { name: 'Handoff' })).toBeNull();
});

test('a handoff shows uam facts under the reply, and Continue only opens a new Thread draft with it', async () => {
  const { mock, user } = await openBudgetTask('t-handoff');
  const card = within(await screen.findByRole('region', { name: 'Handoff' }));
  expect(card.getByText(/The reply above is the agent’s handoff/)).toBeTruthy();
  expect(card.getByText(/Move the warning tones \(in progress\)/)).toBeTruthy();
  expect(card.getByText(/25\.6 spent; budget 25/)).toBeTruthy();
  // The card names whose facts they are; the facts' own heading is left out.
  expect(card.queryByRole('heading', { name: 'Facts' })).toBeNull();

  const fetch = vi.spyOn(window, 'fetch');
  await user.click(card.getByRole('button', { name: 'Continue in a new Thread' }));
  await screen.findByRole('heading', { name: 'New thread' });
  await waitFor(() => expect(composer().value).toContain('Continue the work from this handoff. It comes from the Thread "Split the dashboard tone table"'));
  const text = composer().value;
  expect(text).toContain('## In progress\nWarning tones: half moved.');
  expect(text).toContain('- [ ] Move the warning tones (in progress)');
  // Nothing starts: no Thread is created and no prompt is sent until the owner sends the draft.
  expect(fetch.mock.calls.some(([url, init]) => String(url).endsWith('/api/sessions') && init?.method === 'POST')).toBe(false);
  expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);
});

test('under a credit budget the composer warns when a prompt may use more than is left, and Send stays enabled', async () => {
  const { user } = await openBudgetTask('t-near');
  const note = await screen.findByText('Estimate: a whole prompt on this model often uses about 4.0 AI credits; the budget has 1.5 left.');
  expect(note.getAttribute('role')).toBe('status');
  await user.type(composer(), 'One more fixture');
  expect(screen.getByRole('button', { name: 'Send' }).getAttribute('aria-disabled')).toBeNull();
  expect(screen.queryByText(/Budget reached/)).toBeNull();
});

test('a Thread at its budget shows no estimate, disables Send with the reason beside the composer, keeps the draft and opens the budget', async () => {
  const { user, mock } = await openBudgetTask('t-handoff');
  await screen.findByRole('region', { name: 'Handoff' });
  expect(screen.queryByText(/often uses about/)).toBeNull();
  const note = await screen.findByText(/^Budget reached\. Your draft is kept; raise or remove the budget to send it\./);
  expect(note.getAttribute('role')).toBe('status');
  await user.type(composer(), 'One more fixture');
  const send = screen.getByRole('button', { name: 'Send. The budget is reached' });
  expect(send.getAttribute('aria-disabled')).toBe('true');
  await user.click(send);
  await user.keyboard('{Enter}');
  expect(mock.received.filter((r) => r.route === 'prompt')).toHaveLength(0);
  expect(composer().value).toBe('One more fixture');
  await user.click(screen.getByRole('button', { name: 'Change budget' }));
  const tab = await screen.findByRole('tab', { name: 'Usage' });
  expect(tab.getAttribute('aria-selected')).toBe('true');
  expect(await screen.findByRole('region', { name: 'Budget' })).toBeTruthy();
});

test('a Thread wrapping up at its budget says so and refuses sends', async () => {
  const { user } = await openBudgetTask('t-wrap');
  await screen.findByText(/^Budget reached: the Thread is wrapping up and writing its handoff\. Your draft is kept/);
  expect(screen.queryByText(/often uses about/)).toBeNull();
  await user.type(composer(), 'Keep going');
  expect(screen.getAllByRole('button', { name: /The budget is reached/ }).length).toBeGreaterThan(0);
});

test('a budget_reached refusal the client did not foresee keeps the draft and shows the budget note', async () => {
  const { user } = await openBudgetTask('t-near');
  const prompt = vi.spyOn(api, 'prompt').mockRejectedValueOnce(new ApiError(409, 'This Thread reached its budget. Raise or remove the budget to send more.', { code: 'budget_reached' }));
  await user.type(composer(), 'Raced the limit');
  await user.click(screen.getByRole('button', { name: 'Send' }));
  await screen.findByText(/^Budget reached\. Your draft is kept/);
  expect(prompt).toHaveBeenCalledTimes(1);
  expect(composer().value).toBe('Raced the limit');
  expect(screen.getByRole('button', { name: 'Send. The budget is reached' }).getAttribute('aria-disabled')).toBe('true');
  expect(screen.queryByRole('alert')).toBeNull();
});
