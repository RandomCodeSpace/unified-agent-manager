import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import { ComposerTools } from '../../src/components/ComposerTools';

test('Tools keeps its popup without Snapshot and returns focus on Escape', async () => {
  const user = userEvent.setup();
  render(<ComposerTools />);
  const trigger = screen.getByRole('button', { name: 'Tools' });
  await user.click(trigger);
  expect(await screen.findByRole('menu', { name: 'Tools' })).toBeTruthy();
  expect(screen.getByText('No tools available.')).toBeTruthy();
  expect(screen.queryByText('Snapshot')).toBeNull();
  expect(screen.queryByRole('menuitem')).toBeNull();
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
  expect(document.activeElement).toBe(trigger);
});
