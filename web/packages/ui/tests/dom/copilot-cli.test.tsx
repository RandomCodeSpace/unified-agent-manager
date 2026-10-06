import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, ApiError } from '../../src/api';
import { renderApp, type User } from './render';

const NOTICE = 'Copilot CLI update available';
// An update ends at the first poll after it finished: 2 s after it started, in the mock.
const POLLED = { timeout: 8000 };

/** The Copilot CLI group in Settings → Providers. */
async function cli(user: User) {
  await user.click(await screen.findByRole('button', { name: 'Providers', exact: true }));
  const card = within(await screen.findByRole('region', { name: 'GitHub Copilot' }));
  return within(await card.findByRole('group', { name: 'Copilot CLI' }));
}

afterEach(() => {
  history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('Copilot CLI version and update', () => {
  test('up to date: the version shows, and neither Settings nor its Providers tab carries a notice', async () => {
    const { user } = renderApp('#settings');
    const group = await cli(user);
    expect(await group.findByText('1.0.92')).toBeTruthy();
    expect(group.getByText(/^Up to date · Checked/)).toBeTruthy();
    expect(group.getByRole('button', { name: 'Check again' })).toBeTruthy();
    expect(group.queryByRole('button', { name: /^Update to/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Settings', description: NOTICE })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Providers', description: NOTICE })).toBeNull();
    expect(screen.queryByText(NOTICE)).toBeNull();
  });

  test('an update on offer puts a dot on Settings and on Providers, said in words, and names both versions', async () => {
    const { user } = renderApp('?cli=outdated#settings');
    const settings = await screen.findByRole('button', { name: 'Settings', description: NOTICE });
    expect(settings.querySelector('.bg-warning[aria-hidden="true"]')).toBeTruthy();
    const tab = screen.getByRole('button', { name: 'Providers', description: NOTICE });
    expect(tab.querySelector('.bg-warning[aria-hidden="true"]')).toBeTruthy();
    const group = await cli(user);
    expect(await group.findByText('1.0.89')).toBeTruthy();
    expect(group.getByText(/^1\.0\.92 available · Checked/)).toBeTruthy();
    expect(group.getByRole('button', { name: 'Update to 1.0.92' })).toBeTruthy();
  });

  test('Update runs on the server until it is done, then the dots clear', async () => {
    const { user } = renderApp('?cli=outdated#settings');
    const group = await cli(user);
    await user.click(await group.findByRole('button', { name: 'Update to 1.0.92' }));
    expect(await group.findByText('Updating to 1.0.92…')).toBeTruthy();
    expect(group.getByText('Copilot tasks keep running; new ones use the current version until it restarts.')).toBeTruthy();
    expect(group.queryByRole('button', { name: 'Check again' })).toBeNull();
    expect(await group.findByText('Copilot CLI updated to 1.0.92.', {}, POLLED)).toBeTruthy();
    expect(group.getByText('1.0.92')).toBeTruthy();
    expect(group.getByText(/^Up to date/)).toBeTruthy();
    expect(group.queryByText(/^Still running/)).toBeNull();
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Settings', description: NOTICE })).toBeNull());
    expect(screen.getByRole('button', { name: 'Settings' }).querySelector('.bg-warning')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Providers', description: NOTICE })).toBeNull();
  });

  test('an update already running is picked up when Settings opens again', async () => {
    const { user } = renderApp('?cli=hold#settings');
    await user.click(await (await cli(user)).findByRole('button', { name: 'Update to 1.0.92' }));
    await (await cli(user)).findByText('Updating to 1.0.92…');
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Copilot CLI' })).toBeNull());
    await user.click(screen.getByRole('button', { name: 'Settings' }));
    expect(await (await cli(user)).findByText('Updating to 1.0.92…')).toBeTruthy();
  });

  test('a failed update says why and offers Retry', async () => {
    const { user } = renderApp('?cli=fail#settings');
    const group = await cli(user);
    await user.click(await group.findByRole('button', { name: 'Update to 1.0.92' }));
    expect((await group.findByRole('alert', {}, POLLED)).textContent).toMatch(/^Could not update to 1\.0\.92: npm install -g @github\/copilot@1\.0\.92 failed/);
    expect(group.getByRole('button', { name: 'Retry' })).toBeTruthy();
    expect(group.queryByRole('button', { name: 'Update to 1.0.92' })).toBeNull();
  });

  test('an update while a Task works installs it and says the CLI restarts once none is busy', async () => {
    const { user } = renderApp('?cli=busy#settings');
    const group = await cli(user);
    await user.click(await group.findByRole('button', { name: 'Update to 1.0.92' }));
    expect(await group.findByText('Copilot CLI updated to 1.0.92.', {}, POLLED)).toBeTruthy();
    expect(group.getByText('1.0.92')).toBeTruthy();
    expect(group.getByText('Still running 1.0.89; restarts when no Copilot task is working or waiting.')).toBeTruthy();
    expect(group.queryByRole('button', { name: /^Update to/ })).toBeNull();
  });

  test('a refused update shows the server message', async () => {
    vi.spyOn(api, 'updateProviderCli').mockRejectedValue(new ApiError(409, 'no update of the GitHub Copilot CLI is available'));
    const { user } = renderApp('?cli=outdated#settings');
    const group = await cli(user);
    await user.click(await group.findByRole('button', { name: 'Update to 1.0.92' }));
    expect((await group.findByRole('alert')).textContent).toBe('Could not update: no update of the GitHub Copilot CLI is available');
    expect(group.getByRole('button', { name: 'Update to 1.0.92' })).toBeTruthy();
  });

  test('a CLI UAM cannot update says why, with no Update and no notice', async () => {
    const { user } = renderApp('?cli=manual#settings');
    const group = await cli(user);
    expect(await group.findByText(/^UAM cannot update it here: copilot at \/usr\/local\/bin\/copilot is not an npm global install/)).toBeTruthy();
    expect(group.queryByRole('button', { name: /^Update to/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Settings', description: NOTICE })).toBeNull();
  });

  test('a release the SDK refused is named, not offered', async () => {
    const { user } = renderApp('?cli=incompatible#settings');
    const group = await cli(user);
    expect(await group.findByText(/^1\.0\.95 needs a newer UAM · Checked/)).toBeTruthy();
    expect(group.queryByRole('button', { name: /^Update to/ })).toBeNull();
  });
});
