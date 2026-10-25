import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { renderApp } from './render';

test('Enable waits for a check of the origin in the field, which shows the server’s warnings', async () => {
  const save = vi.spyOn(api, 'updateWebSettings');
  try {
    const { user } = renderApp('#settings');
    const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
    await user.click(rail.getByRole('button', { name: 'Diagram editor' }));
    const section = within(await screen.findByRole('region', { name: 'Diagram editor' }));
    await user.click(await section.findByRole('radio', { name: 'draw.io' }));
    const origin = section.getByRole('textbox', { name: 'Origin' });
    const enable = section.getByRole('switch', { name: 'Enable draw.io' });
    expect(enable.hasAttribute('data-disabled')).toBe(true);
    // Typed with the spelling the server normalises away: the check and the saved setting use its form.
    await user.type(origin, 'HTTPS://Embed.diagrams.net/');
    expect(enable.hasAttribute('data-disabled')).toBe(true);
    await user.click(section.getByRole('button', { name: 'Check' }));
    expect(await section.findByText(/Diagrams are edited in a page from embed.diagrams.net/)).toBeTruthy();
    expect(section.getByRole('status').textContent).toContain('Public embed');
    await waitFor(() => expect(enable.hasAttribute('data-disabled')).toBe(false));
    await user.click(enable);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ drawio: { origin: 'https://embed.diagrams.net', enabled: true } }));
    await waitFor(() => expect(enable.getAttribute('aria-checked')).toBe('true'));
    // Another origin in the field: the switch waits for its own check.
    await user.type(origin, '/x');
    expect(enable.hasAttribute('data-disabled')).toBe(true);
  } finally {
    save.mockRestore();
  }
});

test('a failed check shows the server’s error and keeps Enable off', async () => {
  const { user } = renderApp('#settings');
  const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
  await user.click(rail.getByRole('button', { name: 'Diagram editor' }));
  const section = within(await screen.findByRole('region', { name: 'Diagram editor' }));
  await user.click(await section.findByRole('radio', { name: 'draw.io' }));
  await user.type(section.getByRole('textbox', { name: 'Origin' }), 'http://drawio.example.test');
  await user.click(section.getByRole('button', { name: 'Check' }));
  expect((await section.findByRole('alert')).textContent).toContain('mixed content');
  expect(section.getByRole('switch', { name: 'Enable draw.io' }).hasAttribute('data-disabled')).toBe(true);
});
