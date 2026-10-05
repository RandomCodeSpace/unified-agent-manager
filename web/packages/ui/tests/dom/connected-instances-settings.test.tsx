import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import { ConnectedInstancesSettings, type ConnectedInstancesSettingsProps } from '../../src/components/ConnectedInstancesSettings';
import type { ConnectedInstance } from '../../src/api';

const connection: ConnectedInstance = {
  id: 'connection-b', instance_id: 'instance-b', label: 'Work server', base_url: 'https://b.example.com', enabled: true,
  generation: 1, has_key: true, version: '1.0.0', protocol_major: 1, capabilities: [], allow_private: false,
};

function setup(overrides: Partial<ConnectedInstancesSettingsProps> = {}) {
  const props = {
    homeInstanceID: 'instance-a', connections: [connection],
    onAdd: vi.fn(async () => {}), onUpdate: vi.fn(async () => {}), onRemove: vi.fn(async () => {}), onRefresh: vi.fn(async () => {}),
    ...overrides,
  };
  const user = userEvent.setup();
  render(<ConnectedInstancesSettings {...props} />);
  return { user, props };
}

describe('Connected instances settings', () => {
  test('adds any number of instances, keeps the key in a password field and clears it after a refused pairing', async () => {
    const onAdd = vi.fn(async () => { throw new Error('The target refused pairing'); });
    const { user } = setup({ onAdd });
    await user.click(screen.getByRole('button', { name: 'Add instance' }));
    const form = within(screen.getByRole('form', { name: 'Add an instance' }));
    await user.type(form.getByLabelText('Name'), 'Personal server');
    await user.type(form.getByLabelText('Web URL'), 'https://c.example.com');
    const key = form.getByLabelText('Access key') as HTMLInputElement;
    expect(key.type).toBe('password');
    await user.type(key, 'test-secret-key');
    await user.click(form.getByRole('switch', { name: 'Allow a private network address' }));
    await user.click(form.getByRole('button', { name: 'Connect instance' }));
    expect((await screen.findByRole('alert')).textContent).toContain('The target refused pairing');
    expect(onAdd).toHaveBeenCalledWith({ label: 'Personal server', base_url: 'https://c.example.com', token: 'test-secret-key', allow_private: true });
    expect(key.value).toBe('');
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(screen.getByRole('region', { name: 'Work server' })).toBeTruthy();
  });

  test('a connection names its version and the features it offers, in one line', () => {
    setup({ connections: [{ ...connection, capabilities: ['events-v1', 'usage-v1', 'terminal-v1'] }] });
    expect(screen.getByText('1.0.0 · Terminal, Usage')).toBeTruthy();
  });

  test('editing a label preserves the credential and avoids re-pairing an unchanged URL', async () => {

    const { user, props } = setup();
    await user.click(screen.getByRole('button', { name: 'Edit connection' }));
    const form = within(screen.getByRole('form', { name: 'Edit Work server' }));
    expect((form.getByLabelText('New access key (optional)') as HTMLInputElement).value).toBe('');
    await user.clear(form.getByLabelText('Name'));
    await user.type(form.getByLabelText('Name'), 'Renamed server');
    await user.click(form.getByRole('button', { name: 'Save connection' }));
    await waitFor(() => expect(props.onUpdate).toHaveBeenCalledWith('connection-b', { label: 'Renamed server', allow_private: false }));
    expect(screen.queryByRole('form')).toBeNull();
  });

  test('disabling a source targets only that connection and confirms an active terminal first', async () => {
    const { user, props } = setup({ activeTerminalConnectionID: connection.id });
    await user.click(screen.getByRole('switch', { name: 'Enable Work server' }));
    expect(props.onUpdate).not.toHaveBeenCalled();
    const dialog = within(await screen.findByRole('alertdialog'));
    expect(dialog.getByText(/active terminal shell will close/)).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Disable connection' }));
    await waitFor(() => expect(props.onUpdate).toHaveBeenCalledWith('connection-b', { enabled: false }));
    expect(props.onRemove).not.toHaveBeenCalled();
  });

  test('a disabled source can be enabled without a destructive confirmation', async () => {
    const { user, props } = setup({ connections: [{ ...connection, enabled: false }], statuses: { [connection.id]: { status: 'online' } } });
    expect(screen.getByRole('status').textContent).toBe('Disabled');
    await user.click(screen.getByRole('switch', { name: 'Enable Work server' }));
    await waitFor(() => expect(props.onUpdate).toHaveBeenCalledWith('connection-b', { enabled: true }));
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  test('removing confirms the precise source and reports failure without hiding the saved record', async () => {
    const onRemove = vi.fn(async () => { throw new Error('Connection changed; refresh and retry'); });
    const { user } = setup({ onRemove, statuses: { [connection.id]: { status: 'auth-required' } } });
    expect(screen.getByRole('status').textContent).toBe('Access needs renewal');
    await user.click(screen.getByRole('button', { name: 'Remove connection' }));
    const dialog = within(await screen.findByRole('alertdialog'));
    expect(dialog.getByText('Remove Work server?')).toBeTruthy();
    expect(dialog.getByText(/Tasks and files on the other instance are not deleted/)).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Remove connection' }));

    expect((await screen.findByRole('alert')).textContent).toContain('Connection changed');
    expect(onRemove).toHaveBeenCalledWith('connection-b');
    expect(screen.getByRole('region', { name: 'Work server' })).toBeTruthy();
  });

  test('a connection on another Copilot account warns with the reason and opens its account settings', async () => {
    const reason = 'Work server is linked to Copilot account mallory; this instance is linked to octo. Both must use the same account.';
    const onOpenAccount = vi.fn();
    const { user } = setup({ onOpenAccount, statuses: { [connection.id]: { status: 'account-mismatch', error: reason } } });
    expect(screen.getByRole('status').textContent).toBe(`Linked to a different Copilot account — ${reason}`);
    await user.click(screen.getByRole('button', { name: 'Open its GitHub Copilot settings' }));
    expect(onOpenAccount).toHaveBeenCalledWith('connection-b');
  });
});
