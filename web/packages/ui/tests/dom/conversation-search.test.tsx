// Sidebar search also finds words in the Threads' conversations (mock /api/search; the service asks the Copilot CLI's index).
import { screen, waitFor, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

test('a word only a conversation holds lists its Thread under In conversations, marked, and opens it', async () => {
  const { user } = renderApp();
  const side = await sidebar();
  const box = side.getByRole('searchbox', { name: 'Search threads' });
  await user.type(box, 'contrast');
  // No Thread is named for it; its conversation is.
  expect(await side.findByText('No thread names match')).toBeTruthy();
  const found = within(await side.findByRole('region', { name: 'In conversations' }));
  const hit = await found.findByRole('button', { name: /Accessibility pass on the post template/ });
  expect(within(hit).getByText('contrast', { selector: 'mark' })).toBeTruthy();
  await user.click(hit);
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Accessibility pass on the post template'));
});

test('short words search names only', async () => {
  const { user } = renderApp();
  const side = await sidebar();
  await user.type(side.getByRole('searchbox', { name: 'Search threads' }), 'co');
  expect(side.queryByRole('region', { name: 'In conversations' })).toBeNull();
});
