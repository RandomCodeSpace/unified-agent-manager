import { screen, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, ApiError } from '../../src/api';
import { renderApp } from './render';

const NOTICE = 'UAM v0.16.0 installed; restart to run it';

/** The Version group of Settings → General → UAM. */
async function card() {
  const region = within(await screen.findByRole('region', { name: 'UAM' }));
  return within(await region.findByRole('group', { name: 'Version' }));
}

afterEach(() => {
  history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('UAM version and restart', () => {
  test('the installed version running: the version shows, no Restart and no notice', async () => {
    renderApp('#settings');
    const group = await card();
    expect(await group.findByText('dev-mock')).toBeTruthy();
    expect(group.getByText('The installed version is running.')).toBeTruthy();
    expect(group.queryByRole('button', { name: 'Restart' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Settings', description: NOTICE })).toBeNull();
    expect(screen.queryByRole('button', { name: 'General', description: 'Restart available' })).toBeNull();
  });

  test('a newer version installed puts a dot on Settings and General, names both versions and offers Restart', async () => {
    const { user } = renderApp('?uam=installed#settings');
    const settings = await screen.findByRole('button', { name: 'Settings', description: NOTICE });
    expect(settings.querySelector('.bg-warning[aria-hidden="true"]')).toBeTruthy();
    const tab = screen.getByRole('button', { name: 'General', description: 'Restart available' });
    expect(tab.querySelector('.bg-warning[aria-hidden="true"]')).toBeTruthy();
    const group = await card();
    expect(await group.findByText('UAM v0.16.0 is installed (running dev-mock).')).toBeTruthy();
    await user.click(group.getByRole('button', { name: 'Restart' }));
    expect(await group.findByText('Restarting onto UAM v0.16.0…')).toBeTruthy();
  });

  test('a restart while a task works waits, and says so', async () => {
    const { user } = renderApp('?uam=busy#settings');
    const group = await card();
    await user.click(await group.findByRole('button', { name: 'Restart' }));
    expect(await group.findByText('Restarts when no task is working or waiting.')).toBeTruthy();
    expect(group.getByText('UAM v0.16.0 is installed (running dev-mock).')).toBeTruthy();
    expect(group.queryByRole('button', { name: 'Restart' })).toBeNull();
  });

  test('an installed binary that cannot be read says why, with no Restart and no notice', async () => {
    renderApp('?uam=error#settings');
    const group = await card();
    expect(await group.findByText('Could not read the installed UAM: run uam version: exit status 2')).toBeTruthy();
    expect(group.queryByRole('button', { name: 'Restart' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Settings', description: NOTICE })).toBeNull();
  });

  test('a refused restart shows the server message', async () => {
    vi.spyOn(api, 'restartService').mockRejectedValue(new ApiError(409, 'cannot restart: run uam version: exit status 2'));
    const { user } = renderApp('?uam=installed#settings');
    const group = await card();
    await user.click(await group.findByRole('button', { name: 'Restart' }));
    expect((await group.findByRole('alert')).textContent).toBe('Could not restart: cannot restart: run uam version: exit status 2');
  });
});
