import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from './render';

async function account() {
  (await screen.findByRole('button', { name: 'Providers', exact: true })).click();
  return within(await screen.findByRole('region', { name: 'GitHub Copilot' }));
}

afterEach(() => history.replaceState(null, '', '/'));

describe('GitHub Copilot sign-in', () => {
  test('signed out: a banner says so; a refused token says why and the field is cleared; a good one signs in', async () => {
    const { user } = renderApp('?copilot=out#settings');
    const banner = await screen.findByText('GitHub Copilot is signed out. Sign in in Settings.');
    expect(banner).toBeTruthy();
    const card = await account();
    expect(await card.findByText('Signed out')).toBeTruthy();
    const field = card.getByLabelText('Sign in with a token') as HTMLInputElement;
    expect(field.type).toBe('password');
    expect(card.getByRole('link', { name: 'Create one on GitHub' }).getAttribute('href')).toBe('https://github.com/settings/personal-access-tokens/new');
    expect(card.getByText(/every task on this server/)).toBeTruthy();
    expect(card.getByText(/not supported here/)).toBeTruthy();

    await user.type(field, 'not-a-token');
    await user.click(card.getByRole('button', { name: 'Sign in' }));
    expect((await card.findByRole('alert')).textContent).toMatch(/^Could not sign in: Failed to fetch Copilot user info: 401/);
    expect(field.value).toBe('');

    await user.type(field, 'github_pat_example');
    await user.click(card.getByRole('button', { name: 'Sign in' }));
    expect(await card.findByText('Signed in as octocat. Every task on this server uses this account from its next message.')).toBeTruthy();
    await waitFor(() => expect(screen.queryByText('GitHub Copilot is signed out. Sign in in Settings.')).toBeNull());
  });

  test('a stored sign-in signs out only after confirming', async () => {
    const { user } = renderApp('#settings');
    const card = await account();
    expect(await card.findByText('Signed in as octocat')).toBeTruthy();
    expect(card.queryByLabelText('Sign in with a token')).toBeNull();
    await user.click(card.getByRole('button', { name: 'Sign out' }));
    const dialog = within(await screen.findByRole('alertdialog'));
    expect(dialog.getByText(/Every task on this server stops working/)).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Sign out' }));
    expect(await card.findByText('Signed out')).toBeTruthy();
    expect(await screen.findByText('GitHub Copilot is signed out. Sign in in Settings.')).toBeTruthy();
  });

  test('an environment token is named, and signing in and out is off', async () => {
    renderApp('?copilot=env#settings');
    const card = await account();
    expect(await card.findByText(/takes precedence over any sign-in made here/)).toBeTruthy();
    expect(card.getAllByText('GH_TOKEN').length).toBeGreaterThan(0);
    expect(card.queryByLabelText('Sign in with a token')).toBeNull();
    expect(card.queryByRole('button', { name: 'Sign out' })).toBeNull();
    expect(card.queryByRole('button', { name: 'Use another token' })).toBeNull();
  });
});
