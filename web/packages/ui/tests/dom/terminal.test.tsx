import { screen, waitFor, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { composer, openTask } from './render';

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

test('closing hands the header and focus back at once while the dock slides out; a Terminal press meanwhile opens a new dock', async () => {
  const { user } = await openTask('t3');
  const toggle = screen.getByRole('button', { name: 'Terminal' });
  await user.click(toggle);
  const dock = await screen.findByRole('region', { name: 'Terminal' });
  await within(dock).findByRole('alert');
  await user.click(within(dock).getByRole('button', { name: 'Close terminal' }));
  expect(toggle.getAttribute('aria-pressed')).toBe('false');
  expect(document.activeElement).toBe(toggle);
  expect(dock.isConnected).toBe(true);
  expect(dock.querySelector('[inert]')).not.toBeNull();
  await user.click(toggle);
  const next = await screen.findByRole('region', { name: 'Terminal' });
  expect(next).not.toBe(dock);
  expect(dock.isConnected).toBe(false);
  expect(next.querySelector('[inert]')).toBeNull();
});

test("without a shell, the Terminal button in another Project's Task moves the dock there at once", async () => {
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  expect(await dock.findByRole('alert')).toBeTruthy();
  history.pushState(null, '', '/#task=t6');
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Tidy zsh startup'));
  expect(screen.getByRole('button', { name: 'Terminal' }).getAttribute('aria-pressed')).toBe('true');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  await waitFor(() => expect(within(screen.getByRole('region', { name: 'Terminal' })).getByText('/home/user/dotfiles')).toBeTruthy());
  expect(screen.queryByRole('alertdialog')).toBeNull();
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

test('a drag on the dock edge hands focus back, and typing on the edge never reaches the composer', async () => {
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  await dock.findByRole('alert');
  // The Restart button stands in for the shell, which needs WebGL.
  const restart = dock.getByRole('button', { name: 'Restart' });
  restart.focus();
  const handle = dock.getByRole('separator');
  await user.pointer([{ keys: '[MouseLeft>]', target: handle, coords: { clientY: 400 } }, { target: handle, coords: { clientY: 340 } }, { keys: '[/MouseLeft]', target: handle, coords: { clientY: 340 } }]);
  expect(document.activeElement).toBe(restart);
  handle.focus();
  await user.keyboard('zz');
  expect(composer().value).toBe('');
  expect(document.activeElement).toBe(handle);
});

test('the dock shows with the Task only: Settings hides it and keeps its shell', async () => {
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = await screen.findByRole('region', { name: 'Terminal' });
  await user.click(screen.getByRole('button', { name: 'Settings' }));
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Settings'));
  expect(dock.isConnected).toBe(true);
  expect(dock.classList.contains('hidden')).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Close settings' }));
  await waitFor(() => expect(dock.classList.contains('hidden')).toBe(false));
});
