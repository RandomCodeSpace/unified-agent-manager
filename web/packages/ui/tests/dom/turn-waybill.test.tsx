import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import type { Item, TurnTiming } from '../../src/api';
import { Transcript } from '../../src/components/Transcript';

const at = (s: number) => `2026-10-09T12:00:${String(s).padStart(2, '0')}Z`;
const items: Item[] = [
  { id: 'owner', kind: 'user', text: 'Fix it', time: at(0) },
  { id: 'answer', kind: 'assistant', text: 'Fixed.', time: at(5) },
];
const luna: TurnTiming = { id: 'luna', user_item_id: 'owner', started_at: at(0), ended_at: at(54), paused_ms: 6000, state: 'completed', model: 'gpt-6-luna', input_tokens: 128412, cache_read_tokens: 116894, output_tokens: 1214, calls: 3, generation_ms: 31200, nano_aiu: 421_500_000, premium_cost: 1 };
const flash: TurnTiming = { id: 'flash', user_item_id: 'owner', started_at: at(0), ended_at: at(6), state: 'completed', model: 'ollama/deepseek-v4.1-flash', input_tokens: 16838, cache_read_tokens: 8514, output_tokens: 61, calls: 1, generation_ms: 1825 };

const show = (timing: TurnTiming) => render(<Transcript sessionId="task" items={items} turnTimings={[timing]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
const sections = (dialog: HTMLElement) => Object.fromEntries([...dialog.querySelectorAll('dt')].map(dt => [dt.textContent, dt.nextElementSibling?.textContent]));

test('the foot reads as before, now as a dialog button, and opens nothing until pressed', () => {
  show(luna);
  const button = screen.getByRole('button', { name: /^1\.2K tokens\s+39 tok\/s$/ });
  expect(button.getAttribute('aria-haspopup')).toBe('dialog');
  expect(button.getAttribute('aria-expanded')).toBe('false');
  expect(button.title).toBe('128,412 tokens in · 1,214 out · 31s generating');
  expect(button.parentElement!.textContent).toBe(' · 1.2K tokens · 39 tok/s');
  expect(screen.queryByRole('dialog')).toBeNull();
});

test('a priced turn opens and closes by keyboard: weight, carrier, postage with its premium request, route with paused time', async () => {
  const user = userEvent.setup();
  show(luna);
  const button = screen.getByRole('button', { name: /1\.2K tokens/ });
  button.focus();
  await user.keyboard('{Enter}');
  const dialog = await screen.findByRole('dialog', { name: "This turn's waybill" });
  expect(button.getAttribute('aria-expanded')).toBe('true');
  expect(sections(dialog)).toEqual({
    Weight: '128.4K in · 116.9K cache reads (91%) · 1.2K out',
    Carrier: 'gpt-6-luna',
    Postage: '0.42 AI credits · 1 premium request',
    Route: '3 model calls · 31s generating · 6s paused',
  });
  expect(dialog.textContent).toContain('took 48s');
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(button));
});

test('a turn without credits reads Unpriced, with no premium line and no paused time', async () => {
  const user = userEvent.setup();
  show(flash);
  await user.click(screen.getByRole('button', { name: /61 tokens/ }));
  const dialog = await screen.findByRole('dialog', { name: "This turn's waybill" });
  const read = sections(dialog);
  expect(read.Postage).toBe('Unpriced · the provider reported no cost for this turn');
  expect(read.Route).toBe('1 model call · 2s generating');
  expect(within(dialog).queryByText(/premium/)).toBeNull();
});

test('credits with no premium cost show no premium line', async () => {
  const user = userEvent.setup();
  show({ ...luna, premium_cost: 0 });
  await user.click(screen.getByRole('button', { name: /1\.2K tokens/ }));
  const dialog = await screen.findByRole('dialog', { name: "This turn's waybill" });
  expect(sections(dialog).Postage).toBe('0.42 AI credits');
});
