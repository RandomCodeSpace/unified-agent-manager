import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp } from './render';

async function backgroundAI() {
  const rendered = renderApp('#settings');
  const card = await waitFor(() => {
    const found = screen.getByRole('region', { name: 'Background AI' });
    within(found).getByRole('meter', { name: 'Background AI calls today' });
    return found;
  });
  return { ...rendered, card: within(card) };
}

describe('Background AI', () => {
  test('today against the limit, the paused notice, and the log by day with totals', async () => {
    const { card } = await backgroundAI();
    expect(card.getByText('40 of 40')).toBeTruthy();
    expect(card.getByRole('status').textContent).toMatch(/^Background AI is paused until tomorrow/);
    const today = within(card.getByRole('region', { name: 'Today' }));
    expect(today.getByText('40 calls · 2 skipped · 21.9K in, 926 out tokens · 0.07 credits')).toBeTruthy();
    expect(today.getAllByText('Skipped: daily limit')).toHaveLength(2);
    expect(today.getAllByRole('listitem')).toHaveLength(25);
  });

  test('older calls load page by page until every call is shown', async () => {
    const { card, user } = await backgroundAI();
    for (const shown of [50, 75, 85]) {
      await user.click(card.getByRole('button', { name: 'Show older calls' }));
      await waitFor(() => expect(card.getAllByRole('listitem')).toHaveLength(shown));
    }
    expect(card.queryByRole('button', { name: 'Show older calls' })).toBeNull();
    expect(card.getAllByRole('listitem')).toHaveLength(85);
    const yesterday = within(card.getByRole('region', { name: 'Yesterday' }));
    expect(yesterday.getByText('Failed')).toBeTruthy();
    expect(yesterday.getByText('copilot title: model request timed out')).toBeTruthy();
    expect(yesterday.getAllByText(/^gpt-6-luna · .*≈/).length).toBeGreaterThan(0);
  });

  test('raising the limit lifts the pause', async () => {
    const { card, user } = await backgroundAI();
    const input = card.getByRole('spinbutton', { name: 'Daily limit (calls)' });
    await user.clear(input);
    await user.type(input, '1001');
    expect((card.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
    await user.clear(input);
    await user.type(input, '60');
    await user.click(card.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(card.getByText('40 of 60')).toBeTruthy());
    expect(card.queryByRole('status')).toBeNull();
  });
});
