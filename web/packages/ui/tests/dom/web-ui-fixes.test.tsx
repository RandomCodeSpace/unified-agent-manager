import { afterEach, describe, expect, test, vi } from 'vitest';
import { renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

describe('sidebar shelves', () => {
  test('the pinned shelf headers sit flush with the foot of the list, past its bottom padding', async () => {
    renderApp();
    const side = await sidebar();
    const archived = side.getByRole('button', { name: /^Archived/ });
    const settled = side.getByRole('button', { name: /^Settled/ });
    // The scroller's 12px bottom padding insets the sticky edge; the headers reach past it, so no row shows under them.
    expect(archived.parentElement!.parentElement!.className).toContain('pb-3');
    expect(archived.className).toContain('-bottom-3');
    expect(settled.className).toContain('bottom-4');
    expect(settled.className).toContain('pointer-coarse:bottom-8');
  });
});
