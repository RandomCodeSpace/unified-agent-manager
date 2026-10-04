import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp } from './render';

async function backgroundAI() {
  const rendered = renderApp('#settings');
  // The page sizes asked of GET /api/utility, in order.
  const reads: string[] = [];
  const mocked = window.fetch;
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input instanceof Request ? input.url : input), location.href);
    if (url.pathname === '/api/utility') reads.push(url.searchParams.get('limit') ?? '');
    return mocked(input, init);
  };
  const card = await waitFor(() => {
    const found = screen.getByRole('region', { name: 'Background AI' });
    within(found).getByRole('meter', { name: 'Background AI calls today' });
    return found;
  });
  return { ...rendered, reads, card: within(card) };
}

/** Opens the log and waits for its first page. */
async function openLog(card: Awaited<ReturnType<typeof backgroundAI>>['card'], user: Awaited<ReturnType<typeof backgroundAI>>['user']) {
  await user.click(card.getByRole('button', { name: 'Show log · 40 calls today' }));
  await waitFor(() => expect(card.getAllByRole('listitem')).toHaveLength(25));
}

describe('Background AI', () => {
  test('the log starts collapsed and is read only once opened', async () => {
    const { card, user, reads } = await backgroundAI();
    expect(card.getByText('40 of 40')).toBeTruthy();
    expect(card.getByRole('status').textContent).toMatch(/^Background AI is paused until tomorrow/);
    expect(card.getByRole('spinbutton', { name: 'Daily limit (calls)' })).toBeTruthy();
    const toggle = card.getByRole('button', { name: 'Show log · 40 calls today' });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(card.queryByRole('region', { name: 'Today' })).toBeNull();
    expect(card.queryAllByRole('listitem')).toHaveLength(0);
    expect(card.queryByRole('button', { name: 'Show older calls' })).toBeNull();
    expect(reads.length).toBeGreaterThan(0);
    expect(reads.every((limit) => limit === '1')).toBe(true);

    await openLog(card, user);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(toggle.textContent).toBe('Hide log');
    expect(reads.at(-1)).toBe('25');

    await user.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(card.queryAllByRole('listitem')).toHaveLength(0);
  });

  test('today against the limit and the log by day with totals', async () => {
    const { card, user } = await backgroundAI();
    await openLog(card, user);
    const today = within(card.getByRole('region', { name: 'Today' }));
    expect(today.getByText('40 calls · 2 skipped · 21.9K in, 926 out tokens · 0.07 credits')).toBeTruthy();
    expect(today.getAllByText('Skipped: daily limit')).toHaveLength(2);
    expect(today.getAllByRole('listitem')).toHaveLength(25);
    // A title made with the Task's own model says so; other calls name the Utility model alone.
    expect(today.getAllByText(/^gpt-6-luna \(the task's model\) · /)).toHaveLength(1);
    expect(today.getAllByText(/^gpt-6-luna · /).length).toBeGreaterThan(0);
  });

  test('older calls load page by page until every call is shown', async () => {
    const { card, user } = await backgroundAI();
    await openLog(card, user);
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
