import { screen, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { openTask } from './render';

// A shell that cannot start: the terminal itself fails to build once WebGL is there.
vi.mock('@xterm/xterm', () => ({ Terminal: class { constructor() { throw new Error('no terminal'); } } }));

const getContext = HTMLCanvasElement.prototype.getContext;
const fonts = document.fonts;
afterEach(() => {
  HTMLCanvasElement.prototype.getContext = getContext;
  Object.defineProperty(document, 'fonts', { configurable: true, value: fonts });
});

/** WebGL present, so the terminal gets past its probe. */
function withWebgl() {
  HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, kind: string, ...rest: unknown[]) {
    return kind === 'webgl2' ? ({ getExtension: () => null } as unknown as WebGL2RenderingContext) : getContext.call(this, kind as '2d', ...(rest as []));
  } as typeof HTMLCanvasElement.prototype.getContext;
}

test('while the shell starts, the dock body says so rather than standing empty', async () => {
  Object.defineProperty(document, 'fonts', { configurable: true, value: { load: () => new Promise(() => {}) } });
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  expect(await dock.findByText('Starting a shell…')).toBeTruthy();
  expect(dock.getByText('Connecting…')).toBeTruthy();
  expect(dock.queryByRole('alert')).toBeNull();
});

test('a shell that cannot open fills the dock body with the error and Retry', async () => {
  withWebgl();
  const { user } = await openTask('t3');
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  const dock = within(await screen.findByRole('region', { name: 'Terminal' }));
  const alert = within(await dock.findByRole('alert'));
  expect(alert.getByText('Could not open a terminal.')).toBeTruthy();
  expect(dock.queryByText('Starting a shell…')).toBeNull();
  await user.click(alert.getByRole('button', { name: 'Retry' }));
  expect(await dock.findByRole('alert')).toBeTruthy();
});
