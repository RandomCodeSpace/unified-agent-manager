import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import type { AppContextValue } from '../../src/components/common';
import { AppContext } from '../../src/components/common';
import { ComposerUsage } from '../../src/components/ComposerUsage';
import type { SessionDetail } from '../../src/api';

const session = { provider: 'copilot', context: { used: 50_000, limit: 200_000 }, capabilities: {} } as unknown as SessionDetail;

function draw(compact_threshold?: number, open?: number) {
  const app = { settings: { send_default: 'steer', terminal: false, compact_threshold }, usage: null } as unknown as AppContextValue;
  return render(<AppContext.Provider value={app}><ComposerUsage session={{ ...session, compact_threshold: open }} /></AppContext.Provider>);
}

test('the context ring marks where compaction starts', async () => {
  const view = draw(60);
  // The tick sits at 60% of the way round (the ring starts at the top, the svg is turned a quarter back).
  const mark = view.container.querySelector('[data-compact-mark]')!;
  expect(Number(mark.getAttribute('x2'))).toBeCloseTo(8 + 7.75 * Math.cos(2 * Math.PI * 0.6));
  await userEvent.click(screen.getByRole('button', { name: /25%/ }));
  expect((await screen.findByText(/Compacts at/)).textContent).toBe('Compacts at 60% (120K tokens)');
  view.unmount();
  draw();
  await userEvent.click(screen.getByRole('button', { name: /25%/ }));
  expect((await screen.findByText(/Compacts at/)).textContent).toBe('Compacts at 80% (160K tokens)');
});

test('an open conversation shows the threshold it opened with, not a later setting', async () => {
  // Settings now say 50%; the conversation opened at 80% and keeps it until it reopens.
  draw(50, 80);
  await userEvent.click(screen.getByRole('button', { name: /25%/ }));
  expect((await screen.findByText(/Compacts at/)).textContent).toBe('Compacts at 80% (160K tokens)');
});
