// Settings → Session stats: the server's key=value counts, grouped by key prefix.
import { screen, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp } from './render';

describe('Settings → Session stats', () => {
  test('lists every count under its group with the file it was written to', async () => {
    const { user } = renderApp('#settings');
    await screen.findByRole('heading', { level: 1, name: 'Settings' });
    await user.click(screen.getByRole('button', { name: 'Session stats', exact: true }));
    const friction = within(await screen.findByRole('region', { name: 'friction' }));
    expect(friction.getByText('tool_failure_pct').nextSibling?.textContent).toBe('8');
    const sessions = within(screen.getByRole('region', { name: 'sessions' }));
    expect(sessions.getByText('total').nextSibling?.textContent).toBe('214');
    expect(screen.getByText('/home/user/.config/uam/session-stats.txt')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Scan again' })).toBeTruthy();
  });

  test('Copy puts the key=value lines on the clipboard', async () => {
    const { user } = renderApp('#settings');
    // After renderApp: its user-event setup installs a clipboard of its own.
    const written: string[] = [];
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => { written.push(t); } } });
    await screen.findByRole('heading', { level: 1, name: 'Settings' });
    await user.click(screen.getByRole('button', { name: 'Session stats', exact: true }));
    await user.click(await screen.findByRole('button', { name: 'Copy' }));
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeTruthy();
    expect(written).toHaveLength(1);
    expect(written[0]).toMatch(/^scan\.at=.+\n/);
    expect(written[0]).toContain('\nfriction.tool_failure_pct=8\n');
  });
});
