import { screen, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { openTask } from './render';

// The terminal's lazy chunk never lands here, so the dock keeps what it shows while it loads.
vi.mock('../../src/components/Terminal', () => new Promise(() => {}));

test('while its chunk loads, the dock is the terminal header saying Connecting…', async () => {
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  expect(dock.getByText('Terminal')).toBeTruthy();
  expect(dock.getByText('/home/user/projects/unified-agent-manager')).toBeTruthy();
  expect(dock.getByRole('status').textContent).toBe('Connecting…');
});
