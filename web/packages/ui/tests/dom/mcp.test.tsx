import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { openMenu, openTask, renderApp } from './render';

describe('MCP servers', () => {
  test('Settings lists servers with their secrets as names only, and a command server needs Terminal', async () => {
    const { user } = renderApp('#settings');
    await user.click(await screen.findByRole('button', { name: 'MCP servers', exact: true }));
    const section = within(await screen.findByRole('region', { name: 'MCP servers' }));
    const list = within(await section.findByRole('list', { name: 'MCP servers' }));
    expect(list.getByText('/opt/mcp/echo-server --stdio')).toBeTruthy();
    expect(list.getByText('ECHO_TOKEN=')).toBeTruthy();
    expect(list.getByText('Built in')).toBeTruthy();
    // Terminal on: the form offers a command server.
    await user.click(section.getByRole('button', { name: 'Add server' }));
    expect(section.getByRole('radio', { name: 'Command' })).toBeTruthy();
    await user.click(section.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'General', exact: true }));
    await user.click(screen.getByRole('switch', { name: 'Terminal' }));
    await waitFor(() => expect((screen.getByRole('switch', { name: 'Terminal' }) as HTMLElement).getAttribute('aria-checked')).toBe('false'));
    await user.click(screen.getByRole('button', { name: 'MCP servers', exact: true }));
    await user.click(section.getByRole('button', { name: 'Add server' }));
    expect(section.queryByRole('radio', { name: 'Command' })).toBeNull();
    expect(section.getByText(/needs Settings → Shell access → Terminal on/)).toBeTruthy();
    await user.type(section.getByRole('textbox', { name: 'Name' }), 'notes');
    await user.type(section.getByRole('textbox', { name: 'Address' }), 'https://notes.example.com/mcp');
    await user.click(section.getByRole('button', { name: 'Add server' }));
    expect(await list.findByText('https://notes.example.com/mcp')).toBeTruthy();
  });

  test("the built-in GitHub server is off by default, and its switch is UAM's own setting for every task, though discovery leaves it out", async () => {
    const { user } = renderApp('#settings');
    await user.click(await screen.findByRole('button', { name: 'MCP servers', exact: true }));
    const section = within(await screen.findByRole('region', { name: 'MCP servers' }));
    const list = within(await section.findByRole('list', { name: 'MCP servers' }));
    const github = list.getByRole('switch', { name: 'Use github-mcp-server in tasks' });
    const row = within(github.closest('li')!);
    expect(github.getAttribute('aria-checked')).toBe('false');
    expect(row.getByText('Off')).toBeTruthy();
    expect(document.getElementById(github.getAttribute('aria-describedby')!)?.textContent).toMatch(/not shared with the copilot command\. Off by default/);
    // Only the built-in GitHub server gets the setting's switch; other built-in servers stay read-only.
    expect(list.queryAllByRole('switch', { name: /in tasks$/ })).toHaveLength(1);
    await user.click(github);
    await waitFor(() => expect(github.getAttribute('aria-checked')).toBe('true'));
    expect(row.queryByText('Off')).toBeNull();
  });

  test('a Task leaves the built-in GitHub server off until Settings turns it on, and can still turn it on for itself', async () => {
    const { user } = await openTask('t1');
    const menu = await openMenu(user, 'Task actions');
    await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
    const servers = within(await dialog.findByRole('list', { name: "This task's MCP servers" }));
    const github = servers.getByRole('switch', { name: 'Use github-mcp-server in this task' });
    expect(github.getAttribute('aria-checked')).toBe('false');
    await user.click(github);
    await waitFor(() => expect(github.getAttribute('aria-checked')).toBe('true'));
  });

  test('a stored header value stays write-only when the server is edited', async () => {
    const { user } = renderApp('#settings');
    await user.click(await screen.findByRole('button', { name: 'MCP servers', exact: true }));
    const section = within(await screen.findByRole('region', { name: 'MCP servers' }));
    await section.findByRole('list', { name: 'MCP servers' });
    await user.click(section.getAllByRole('button', { name: 'Edit' })[0]);
    const form = within(section.getByRole('form', { name: 'Edit MCP server docs-search' }));
    const value = form.getByLabelText('Headers 1 value') as HTMLInputElement;
    expect(value.value).toBe('');
    expect(value.placeholder).toBe('•••• set');
    expect(form.getByText(/Stored values are never shown/)).toBeTruthy();
  });

  test('a Task shows each server state and signs in by pasting the address the browser ended on', async () => {
    const { user } = await openTask('t1');
    const menu = await openMenu(user, 'Task actions');
    await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
    const servers = within(await dialog.findByRole('list', { name: "This task's MCP servers" }));
    expect(servers.getByText(/failed to spawn MCP server process/)).toBeTruthy();
    expect(servers.getByText('Needs sign-in')).toBeTruthy();
    await user.click(servers.getByRole('button', { name: 'Restart' }));
    expect(await dialog.findByText(/restart the MCP server/)).toBeTruthy();
    await user.click(servers.getByRole('button', { name: 'Sign in' }));
    const form = within(await dialog.findByRole('form', { name: 'Sign in to tracker' }));
    expect(form.getByRole('link', { name: /Open the sign-in page/ }).getAttribute('href')).toContain('tracker.example.com/authorize');
    const address = form.getByRole('textbox', { name: 'Address the browser ended on' });
    await user.type(address, 'http://127.0.0.1:41234/?code=c&state=other');
    await user.click(form.getByRole('button', { name: 'Finish sign-in' }));
    expect(await form.findByText(/belongs to another sign-in/)).toBeTruthy();
    await user.clear(address);
    await user.type(address, 'http://127.0.0.1:41234/?code=c&state=mock-state');
    await user.click(form.getByRole('button', { name: 'Finish sign-in' }));
    await waitFor(() => expect(dialog.queryByRole('form', { name: 'Sign in to tracker' })).toBeNull());
    expect(servers.queryByText('Needs sign-in')).toBeNull();
    // Off for this task only (the built-in GitHub server is already off: Settings leaves it off).
    const docs = servers.getByRole('switch', { name: 'Use docs-search in this task' });
    await user.click(docs);
    expect(await within(docs.closest('li')!).findByText('Off for this task')).toBeTruthy();
  });
});
