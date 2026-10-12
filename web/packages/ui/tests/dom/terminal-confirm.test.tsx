import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { openTask } from './render';

// A connected shell needs WebGL and xterm.js's canvas, which the environment lacks: both are stand-ins,
// every member a no-op, while the shell itself is the mock's fake socket.
vi.mock('@xterm/xterm', () => ({ Terminal: stub({ cols: 80, rows: 24, options: {}, modes: { mouseTrackingMode: 'none' } }) }));
vi.mock('@xterm/addon-fit', () => ({ FitAddon: stub({}) }));
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: stub({}) }));
function stub(known: Record<string, unknown>) {
  return function () {
    return new Proxy({ ...known }, { get: (t, key) => (key in t ? t[key as string] : () => ({ dispose() {} })) });
  };
}

const getContext = HTMLCanvasElement.prototype.getContext;
beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, kind: string, ...rest: unknown[]) {
    return kind === 'webgl2' ? { getExtension: () => null } : getContext.call(this, kind as '2d', ...(rest as []));
  } as typeof HTMLCanvasElement.prototype.getContext;
});
afterEach(() => {
  HTMLCanvasElement.prototype.getContext = getContext;
});

async function openConnected() {
  const rendered = await openTask('t3');
  await rendered.user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  await waitFor(() => expect(dock.getByText('Connected')).toBeTruthy());
  return rendered;
}

test('the header Terminal button confirms before it closes a connected shell', async () => {
  const { user } = await openConnected();
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  let confirm = within(await screen.findByRole('alertdialog'));
  expect(confirm.getByText('Close the terminal?')).toBeTruthy();
  expect(confirm.getByText('This ends the shell and whatever runs in it.')).toBeTruthy();
  await user.click(confirm.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  expect(screen.getByRole('region', { name: 'Terminal' })).toBeTruthy();

  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  confirm = within(await screen.findByRole('alertdialog'));
  await user.click(confirm.getByRole('button', { name: 'Close terminal' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'Terminal' })).toBeNull());
});

test("in another Project's Task, the Terminal button moves a connected shell there once confirmed", async () => {
  const { user } = await openConnected();
  history.pushState(null, '', '/#task=t6');
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Tidy zsh startup'));
  expect(screen.getByRole('button', { name: 'Terminal' }).getAttribute('aria-pressed')).toBe('true');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const confirm = within(await screen.findByRole('alertdialog'));
  expect(confirm.getByText('Move the terminal?')).toBeTruthy();
  expect(confirm.getByText('This ends the shell and whatever runs in it, then starts a new shell in dotfiles.')).toBeTruthy();
  await user.click(confirm.getByRole('button', { name: 'Move terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  await waitFor(() => expect(dock.getByText('/home/user/dotfiles')).toBeTruthy());
  await waitFor(() => expect(dock.getByText('Connected')).toBeTruthy());
});
