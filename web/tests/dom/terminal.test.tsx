import { screen, waitFor, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { openTask } from './render';

test('without WebGL the terminal dock says why instead of a shell', async () => {
  const { user } = await openTask('t3');
  const toggle = screen.getByRole('button', { name: 'Terminal' });
  await user.click(toggle);
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  expect((await dock.findByRole('alert')).textContent).toBe('The terminal needs WebGL, which this browser has turned off.');
  expect(dock.getByText('/home/user/projects/unified-agent-manager')).toBeTruthy();
  expect(toggle.getAttribute('aria-pressed')).toBe('true');
  // Restart tries again, with the same outcome.
  await user.click(dock.getByRole('button', { name: 'Restart' }));
  expect(await dock.findByRole('alert')).toBeTruthy();
  await user.click(dock.getByRole('button', { name: 'Close terminal' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'Terminal' })).toBeNull());
  expect(document.activeElement).toBe(toggle);
});

test('the dock keeps its height across tasks and resizes from the keyboard', async () => {
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = await screen.findByRole('region', { name: 'Terminal' });
  const handle = within(dock).getByRole('separator');
  handle.focus();
  await user.keyboard('{ArrowUp}{ArrowUp}');
  await waitFor(() => expect(localStorage.getItem('uam.panel.terminal-h') ?? '').not.toBe(''));
  const height = Number(localStorage.getItem('uam.panel.terminal-h'));
  expect(height).toBe(352);
  await user.dblClick(handle);
  await waitFor(() => expect(dock.style.getPropertyValue('--panel-h')).toBe('320px'));
  history.pushState(null, '', '/#task=t4');
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Bump GitHub Actions pins'));
  expect(screen.getByRole('region', { name: 'Terminal' })).toBeTruthy();
});
