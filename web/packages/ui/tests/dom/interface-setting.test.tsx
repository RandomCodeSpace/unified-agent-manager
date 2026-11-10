import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { applyLayout } from '../../src/lib/layout';
import { renderApp } from './render';

afterEach(() => applyLayout('classic'));

test('the Interface setting stores the layout and puts it on the page', async () => {
  const { user } = renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  await user.click(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: 'Appearance & alerts' }));
  const choice = within(await screen.findByRole('radiogroup', { name: 'Interface' }));
  expect(choice.getByRole('radio', { name: 'Current' }).getAttribute('aria-checked')).toBe('true');
  expect(document.documentElement.dataset.layout).toBe('classic');

  await user.click(choice.getByRole('radio', { name: 'Notebook (beta)' }));
  await waitFor(() => expect(choice.getByRole('radio', { name: 'Notebook (beta)' }).getAttribute('aria-checked')).toBe('true'));
  expect(localStorage.getItem('uam.layout')).toBe('notebook');
  expect(document.documentElement.dataset.layout).toBe('notebook');

  await user.click(choice.getByRole('radio', { name: 'Current' }));
  await waitFor(() => expect(choice.getByRole('radio', { name: 'Current' }).getAttribute('aria-checked')).toBe('true'));
  expect(localStorage.getItem('uam.layout')).toBe('classic');
  expect(document.documentElement.dataset.layout).toBe('classic');
});

test('Find a setting finds the Interface row by its words', async () => {
  // A wide screen: the rail stands beside the page and marks the first match current.
  const original = window.matchMedia.bind(window);
  const media = vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === '(min-width: 768px)' ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
  try {
    const { user } = renderApp('#settings');
    await screen.findByRole('heading', { level: 1, name: 'Settings' });
    await user.type(screen.getByRole('searchbox', { name: 'Find a setting' }), 'notebook');
    await waitFor(() => expect(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: 'Appearance & alerts' }).getAttribute('aria-current')).toBe('page'));
  } finally {
    media.mockRestore();
  }
});
