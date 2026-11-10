// Browser storage that refuses access (full quota, blocked site data) changes nothing on screen.
// Its own file: happy-dom binds the storage methods once the app has run, so the spies come first.
import { render, screen, waitFor } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import type { Item } from '../../src/api';
import { Thinking } from '../../src/components/Transcript';
import { openTask, sidebar } from './render';

test('a thought renders closed when session storage refuses reads', () => {
  const refused = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new DOMException('Access is denied.', 'SecurityError'); });
  const thought: Item = { id: 'think', kind: 'reasoning', time: '2026-10-08T12:00:00Z', text: 'Weighing it.' };
  render(<Thinking item={thought} streaming={false} endedAt="2026-10-08T12:00:02Z" />);
  expect(screen.getByRole('button', { name: 'Thought for 2s' }).getAttribute('aria-expanded')).toBe('false');
  expect(refused).toHaveBeenCalled();
  refused.mockRestore();
});

test('a refused write leaves the first render, an open Thread and the shelves working', async () => {
  const refused = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('The quota has been exceeded.', 'QuotaExceededError'); });
  const { user } = await openTask('t1');
  const settled = (await sidebar()).getByRole('button', { name: /^Settled/ });
  const open = settled.getAttribute('aria-expanded');
  await user.click(settled);
  await waitFor(() => expect(settled.getAttribute('aria-expanded')).not.toBe(open));
  expect(refused).toHaveBeenCalled();
  expect(screen.queryByText('UAM could not show this page')).toBeNull();
  refused.mockRestore();
});
