import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { renderApp, sidebar } from './render';

async function account() {
  (await screen.findByRole('button', { name: 'Providers', exact: true })).click();
  return within(await screen.findByRole('region', { name: 'GitHub Copilot' }));
}

afterEach(() => {
  history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

const SIGNED_IN = 'Signed in as octocat. Every task on this server uses this account from its next message.';

describe('GitHub Copilot sign-in', () => {
  test('signed out: a banner says so; a refused token says why and the field is cleared; a good one signs in', async () => {
    const { user } = renderApp('?copilot=out&device=off#settings');
    const banner = await screen.findByText('GitHub Copilot is signed out. Sign in in Settings.');
    expect(banner).toBeTruthy();
    const card = await account();
    expect(await card.findByText('Signed out')).toBeTruthy();
    expect(card.queryByRole('button', { name: 'Sign in with GitHub' })).toBeNull();
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
    expect(await card.findByText(SIGNED_IN)).toBeTruthy();
    await waitFor(() => expect(screen.queryByText('GitHub Copilot is signed out. Sign in in Settings.')).toBeNull());
  });

  test('Sign in with GitHub shows the code and the link, copies the code, and signs in once GitHub approves', async () => {
    const { user } = renderApp('?copilot=out#settings');
    const card = await account();
    const start = await card.findByRole('button', { name: 'Sign in with GitHub' });
    expect(card.getByLabelText('Or sign in with a token')).toBeTruthy();
    expect(card.queryByText(/not supported here/)).toBeNull();

    const opened: unknown[][] = [];
    const open = window.open;
    window.open = (...args: unknown[]) => { opened.push(args); return null; };
    const written: string[] = [];
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => { written.push(t); } } });
    await user.click(start);
    const code = within(await card.findByRole('group', { name: 'Device code' }));
    expect(code.getByText('B4F2-9C1D')).toBeTruthy();
    // The click copies the code, then opens GitHub's device page.
    await waitFor(() => expect(opened).toEqual([['https://github.com/login/device', '_blank', 'noopener,noreferrer']]));
    expect(written).toEqual(['B4F2-9C1D']);
    window.open = open;
    expect(card.getByText('The code is copied. GitHub opened in a new tab: paste the code there and approve Copilot CLI. No tab? Use Open GitHub. This page updates when you are done.')).toBeTruthy();
    const copy = code.getByRole('button', { name: 'Copy code' });
    await waitFor(() => expect(document.activeElement).toBe(copy));
    const link = card.getByRole('link', { name: 'Open GitHub' });
    expect(link.getAttribute('href')).toBe('https://github.com/login/device');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
    expect(card.getByText('Waiting for approval on GitHub…').getAttribute('role')).toBe('status');
    expect(card.queryByLabelText('Or sign in with a token')).toBeNull();

    await user.click(copy);
    expect(await code.findByRole('button', { name: 'Copied' })).toBeTruthy();

    expect(await card.findByText(SIGNED_IN)).toBeTruthy();
    expect(card.queryByRole('group', { name: 'Device code' })).toBeNull();
    expect(card.queryByRole('button', { name: 'Sign in with GitHub' })).toBeNull();
    await waitFor(() => expect(screen.queryByText('GitHub Copilot is signed out. Sign in in Settings.')).toBeNull());
  });

  test('with no keychain on the server, a second code comes with a notice and then signs in', async () => {
    const open = window.open;
    window.open = () => null;
    const { user } = renderApp('?copilot=out&device=keychain#settings');
    const card = await account();
    await user.click(await card.findByRole('button', { name: 'Sign in with GitHub' }));
    expect(await card.findByText('B4F2-9C1D')).toBeTruthy();
    expect(await card.findByText('K7Q3-2M8P', {}, { timeout: 5000 })).toBeTruthy();
    expect(card.getByText(/^This server has no system keychain/)).toBeTruthy();
    expect(card.getByText('Enter the code on GitHub and approve Copilot CLI. This page updates when you are done.')).toBeTruthy();
    expect(await card.findByText(SIGNED_IN, {}, { timeout: 5000 })).toBeTruthy();
    window.open = open;
  });

  test('a failed device sign-in says why and starts again with Try again', async () => {
    const { user } = renderApp('?copilot=out&device=fail#settings');
    const card = await account();
    await user.click(await card.findByRole('button', { name: 'Sign in with GitHub' }));
    expect(await card.findByText('B4F2-9C1D')).toBeTruthy();
    expect((await card.findByRole('alert')).textContent).toMatch(/^Could not sign in: the code expired before it was approved on GitHub/);
    await user.click(card.getByRole('button', { name: 'Try again' }));
    expect(await card.findByText('B4F2-9C1D')).toBeTruthy();
    expect(card.queryByRole('alert')).toBeNull();
  });

  test('Cancel ends the device sign-in and returns to the start', async () => {
    const cancel = vi.spyOn(api, 'cancelDeviceSignIn');
    const { user } = renderApp('?copilot=out&device=hold#settings');
    const card = await account();
    await user.click(await card.findByRole('button', { name: 'Sign in with GitHub' }));
    expect(await card.findByText('B4F2-9C1D')).toBeTruthy();
    await user.click(card.getByRole('button', { name: 'Cancel' }));
    expect(cancel).toHaveBeenCalledWith('copilot');
    expect(await card.findByRole('button', { name: 'Sign in with GitHub' })).toBeTruthy();
    expect(card.queryByText('B4F2-9C1D')).toBeNull();
    expect(card.getByLabelText('Or sign in with a token')).toBeTruthy();
  });

  test('a device sign-in already waiting shows its code on open and finishes', async () => {
    renderApp('?copilot=out&device=waiting#settings');
    const card = await account();
    expect(within(await card.findByRole('group', { name: 'Device code' })).getByText('B4F2-9C1D')).toBeTruthy();
    expect(card.queryByRole('button', { name: 'Sign in with GitHub' })).toBeNull();
    expect(await card.findByText(SIGNED_IN)).toBeTruthy();
  });

  test('with device sign-in on offer, the token form still signs in', async () => {
    const { user } = renderApp('?copilot=out#settings');
    const card = await account();
    await user.type(await card.findByLabelText('Or sign in with a token'), 'github_pat_example');
    await user.click(card.getByRole('button', { name: 'Sign in' }));
    expect(await card.findByText(SIGNED_IN)).toBeTruthy();
    expect(card.queryByRole('button', { name: 'Sign in with GitHub' })).toBeNull();
  });

  test('a stored sign-in signs out only after confirming', async () => {
    const { user } = renderApp('#settings');
    const card = await account();
    expect(await card.findByText('Signed in as octocat')).toBeTruthy();
    expect(card.queryByLabelText('Sign in with a token')).toBeNull();
    expect(card.queryByRole('button', { name: 'Sign in with GitHub' })).toBeNull();
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
    expect(card.queryByRole('button', { name: 'Sign in with GitHub' })).toBeNull();
  });
});

describe('one linked Copilot account', () => {
  const MISMATCH = 'GitHub Copilot is signed in as octocat, but this server is linked to monalisa. Sign in as monalisa, or unlink the account.';

  const NOT_LINKED = 'This server is linked to Copilot account monalisa. Sign in with that account.';

  test('the linked account shows; Unlink asks first, then clears the link and signs out', async () => {
    const unlink = vi.spyOn(api, 'unlink');
    const { user } = renderApp('#settings');
    const card = await account();
    expect(await card.findByText('Linked account: octocat')).toBeTruthy();
    expect(card.queryByText(MISMATCH)).toBeNull();
    await user.click(card.getByRole('button', { name: 'Unlink' }));
    const dialog = within(await screen.findByRole('alertdialog'));
    expect(dialog.getByText('Unlink octocat?')).toBeTruthy();
    expect(dialog.getByText('Copilot signs out on this server, and the next sign-in links its account. Every task on this server uses that account.')).toBeTruthy();
    expect(unlink).not.toHaveBeenCalled();
    await user.click(dialog.getByRole('button', { name: 'Unlink' }));
    expect(unlink).toHaveBeenCalledWith('copilot');
    expect(await card.findByText('Unlinked octocat and signed out. The next sign-in links its account.')).toBeTruthy();
    expect(card.queryByText(/^Linked account:/)).toBeNull();
    expect(card.getByText('Signed out')).toBeTruthy();
    expect(await screen.findByText('GitHub Copilot is signed out. Sign in in Settings.')).toBeTruthy();
  });

  test('signed in as another account than the linked one: a warning, with Unlink and Sign out', async () => {
    renderApp('?mismatch=1#settings');
    const card = await account();
    expect(await card.findByText(MISMATCH)).toBeTruthy();
    expect(card.getByText('Linked account: monalisa')).toBeTruthy();
    expect(card.getByRole('button', { name: 'Unlink' })).toBeTruthy();
    expect(card.getByRole('button', { name: 'Sign out' })).toBeTruthy();
  });

  test('signed out but linked: the start says which account; another one is refused with the server reason', async () => {
    const { user } = renderApp('?copilot=out&linked=monalisa#settings');
    const card = await account();
    expect(await card.findByText('Use the GitHub account monalisa; this server is linked to it.')).toBeTruthy();
    await user.click(card.getByRole('button', { name: 'Sign in with GitHub' }));
    expect((await card.findByRole('alert')).textContent).toBe(`Could not sign in: ${NOT_LINKED}Try again`);
  });

  test('a token for another account than the linked one shows the 409 message', async () => {
    const { user } = renderApp('?copilot=out&device=off&linked=monalisa#settings');
    const card = await account();
    await user.type(await card.findByLabelText('Sign in with a token'), 'github_pat_example');
    await user.click(card.getByRole('button', { name: 'Sign in' }));
    expect((await card.findByRole('alert')).textContent).toBe(`Could not sign in: ${NOT_LINKED}`);
    expect(card.getByText('Signed out')).toBeTruthy();
  });
});

describe('the app while Copilot is signed in to another account', () => {
  const blocked = () => screen.findByRole('heading', { name: 'Copilot is signed in to another account' });

  test('the block shows on open, and Tasks and New task open nothing', async () => {
    const { user } = renderApp('?mismatch=1');
    const aside = await sidebar();
    expect(await blocked()).toBeTruthy();
    expect(screen.getByText(/^GitHub Copilot is signed in as octocat, but this server is linked to monalisa\./)).toBeTruthy();
    await user.click(aside.getByRole('button', { name: /Fix re-attach redraw regression/ }));
    expect(await blocked()).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Conversation' })).toBeNull();
    await user.click(aside.getByRole('button', { name: 'New task' }));
    await user.keyboard('{Alt>}n{/Alt}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Message' })).toBeNull();
    expect(await blocked()).toBeTruthy();
  });

  test('Open Settings lands on the Copilot account; after Unlink the block lifts', async () => {
    const { user } = renderApp('?mismatch=1');
    await blocked();
    await user.click(screen.getByRole('button', { name: 'Open Settings' }));
    const card = within(await screen.findByRole('region', { name: 'GitHub Copilot' }));
    await waitFor(() => expect(document.activeElement?.id).toBe('account-copilot-title'));
    expect(await card.findByText('Linked account: monalisa')).toBeTruthy();
    await user.click(card.getByRole('button', { name: 'Unlink' }));
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Unlink' }));
    expect(await card.findByText(/^Unlinked monalisa and signed out\./)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Copilot is signed in to another account' })).toBeNull());
    expect(await screen.findByText('GitHub Copilot is signed out. Sign in in Settings.')).toBeTruthy();
  });
});
