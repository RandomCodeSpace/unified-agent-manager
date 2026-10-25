import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { composer, renderApp } from './render';

afterEach(() => { vi.restoreAllMocks(); localStorage.clear(); });

/** Opens a Thread of the `?mock&budget` fixtures (src/mock/data.ts budgetTasks). */
async function openBudgetTask(id: string) {
  const rendered = renderApp(`?budget#task=${id}`);
  await screen.findByRole('region', { name: 'Conversation' });
  return rendered;
}

test('a Thread at its budget is Working, wrapping up in its hover text, with no handoff yet', async () => {
  await openBudgetTask('t-wrap');
  // Wrapping up is a phase of the work, no state: the chip reads Working and says why on hover.
  const header = screen.getByRole('heading', { level: 1 }).parentElement!;
  expect(header.textContent).toContain('Working');
  expect(header.querySelector('[title^="Wrapping up"]')).not.toBeNull();
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
