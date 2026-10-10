import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp } from './render';

async function backgroundAI() {
  const rendered = renderApp('#settings');
  // The page sizes asked of GET /api/utility, in order, and the bodies of PATCH /api/settings.
  const reads: string[] = [];
  const patches: unknown[] = [];
  const mocked = window.fetch;
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input instanceof Request ? input.url : input), location.href);
    if (url.pathname === '/api/utility') reads.push(url.searchParams.get('limit') ?? '');
    if (url.pathname === '/api/settings' && init?.method === 'PATCH') patches.push(JSON.parse(String(init.body)));
    return mocked(input, init);
  };
  const card = await waitFor(() => {
    const found = screen.getByRole('region', { name: 'Background AI' });
    within(found).getByRole('meter', { name: 'Background AI calls today' });
    return found;
  });
  return { ...rendered, reads, patches, card: within(card) };
}

/** One purpose's row: its daily-line input and what sits beside it. */
function purposeRow(card: Awaited<ReturnType<typeof backgroundAI>>['card'], label: string) {
  const input = card.getByRole('spinbutton', { name: label }) as HTMLInputElement;
  return { input, row: within(input.parentElement!) };
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

  test('each purpose has its own line under the daily limit', async () => {
    const { card } = await backgroundAI();
    const lines = within(card.getByRole('region', { name: 'Daily line per purpose' }));
    expect(lines.getAllByRole('spinbutton').map((i) => [i.id, (i as HTMLInputElement).value])).toEqual([
      ['utility-purpose-outcome-input', '200'],
      ['utility-purpose-suggest-replies-input', '80'],
      ['utility-purpose-title-input', '40'],
      ['utility-purpose-commit-message-input', '30'],
      ['utility-purpose-configuration-draft-input', '20'],
    ]);
    expect(purposeRow(card, 'Task title').row.getByText('16 today')).toBeTruthy();
    expect(purposeRow(card, 'Commit message').row.getByText('0 today')).toBeTruthy();
    // The day's total is reached, so every purpose is paused with it.
    expect(purposeRow(card, 'Outcome line').row.getByText('Paused today')).toBeTruthy();
    expect(purposeRow(card, 'Outcome line').row.getByText('Paused with the daily limit.')).toBeTruthy();
  });

  test('editing one line saves only that line once typing stops', async () => {
    const { card, user, patches } = await backgroundAI();
    const { input } = purposeRow(card, 'Outcome line');
    await user.clear(input);
    await user.type(input, '150');
    expect(patches).toHaveLength(0);
    // The service replaces the whole map, so the others go back as they are stored.
    await waitFor(() => expect(patches).toEqual([{ utility_purpose_limits: { outcome: 150, 'suggest-replies': 80, title: 40, 'commit-message': 30, 'configuration-draft': 20 } }]), { timeout: 2000 });
    await waitFor(() => expect(purposeRow(card, 'Outcome line').input.value).toBe('150'));
  });

  test('a paused line says why', async () => {
    const { card, user } = await backgroundAI();
    const total = card.getByRole('spinbutton', { name: 'Daily limit (calls)' });
    await user.clear(total);
    await user.type(total, '60');
    await user.click(card.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(card.getByText('40 of 60')).toBeTruthy());
    await waitFor(() => expect(purposeRow(card, 'Outcome line').row.queryByText('Paused today')).toBeNull());
    const replies = purposeRow(card, 'Suggested replies').row;
    expect(replies.getByText('Paused today')).toBeTruthy();
    expect(replies.getByText('Stopped after repeated unusable answers; it resumes at midnight on the server.')).toBeTruthy();

    const { input } = purposeRow(card, 'Commit message');
    await user.clear(input);
    await user.type(input, '0');
    await waitFor(() => expect(purposeRow(card, 'Commit message').row.getByText('Off: its line is 0.')).toBeTruthy(), { timeout: 2000 });
  });
});
