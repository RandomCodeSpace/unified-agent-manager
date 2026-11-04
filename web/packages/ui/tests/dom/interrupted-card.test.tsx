import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { InterruptedCard } from '../../src/components/Handoff';

const waiting = { text: 'echo y > b.txt', waiting: true };
const started = { text: 'sleep 60', waiting: false };

test('a call waiting on permission says it never ran, and Ask again sends once', async () => {
  const user = userEvent.setup();
  let finish = () => {};
  const send = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
  const draft = vi.fn();
  render(<InterruptedCard lead="Stopped." call={waiting} onRunAgain={draft} onAskAgain={send} />);
  expect(screen.getByText(/Not run: it was waiting for your permission\./)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Run again' })).toBeNull();
  const ask = screen.getByRole('button', { name: 'Ask again' });
  await user.click(ask);
  await user.click(ask);
  expect(send).toHaveBeenCalledTimes(1);
  expect(draft).not.toHaveBeenCalled();
  finish();
});

test('Ask again is disabled with the reason at a reached budget, and a refused send shows why', async () => {
  const user = userEvent.setup();
  const send = vi.fn(() => Promise.reject(new Error('Thread is busy')));
  const { rerender } = render(<InterruptedCard lead="Stopped." call={waiting} onAskAgain={send} blocked="The budget is reached." />);
  expect((screen.getByRole('button', { name: 'Ask again' }) as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText('The budget is reached.')).toBeTruthy();
  rerender(<InterruptedCard lead="Stopped." call={waiting} onAskAgain={send} />);
  await user.click(screen.getByRole('button', { name: 'Ask again' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('Thread is busy'));
});

test('a started call keeps the draft-only Run again; a read-only Thread gets no button', async () => {
  const user = userEvent.setup();
  const send = vi.fn(() => Promise.resolve());
  const draft = vi.fn(() => Promise.resolve());
  const { rerender } = render(<InterruptedCard lead="Stopped." call={started} onRunAgain={draft} onAskAgain={send} />);
  // The label may be shortened; the tooltip holds what the card has.
  expect(screen.getByTitle('sleep 60')).toBeTruthy();
  expect(screen.getByText(/This call had started and did not finish/)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Ask again' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Run again' }));
  expect(draft).toHaveBeenCalledTimes(1);
  // A drafted request leaves Run again usable.
  await waitFor(() => expect((screen.getByRole('button', { name: 'Run again' }) as HTMLButtonElement).disabled).toBe(false));
  expect(send).not.toHaveBeenCalled();
  rerender(<InterruptedCard lead="Stopped." call={waiting} />);
  expect(screen.queryByRole('button')).toBeNull();
});
