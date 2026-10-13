import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { UsageButton } from '../../src/components/UsageButton';
import { tokenPriceFixture, tokenUsageFixture } from '../../src/mock/token-usage';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

// Its own file: the popover's module must not have been loaded by another test.
test('the popover code loads once the page is idle, so the first opening shows without a loading state', async () => {
  const idle = vi.fn((run: () => void) => { run(); return 1; });
  vi.stubGlobal('requestIdleCallback', idle);
  vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
  vi.spyOn(api, 'tokenPrices').mockResolvedValue(tokenPriceFixture());
  render(<AppContext.Provider value={{ usage: { quotas: [], stale: false }, meta: null } as unknown as AppContextValue}><UsageButton /></AppContext.Provider>);
  expect(idle).toHaveBeenCalled();
  await import('../../src/components/Usage');
  // Rendered in the opening's own commit: no fallback, no wait for the module.
  act(() => { fireEvent.click(screen.getByRole('button', { name: 'Usage' })); });
  expect(screen.getByRole('group', { name: 'Usage period' })).toBeTruthy();
});
