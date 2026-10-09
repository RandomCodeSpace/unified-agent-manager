import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { openMenu, openTask, renderApp } from './render';

describe('MCP servers', () => {
  test.each(['Sign in', 'Sign in again'])('a public callback finishes in its own tab for %s', async (action) => {
    const { user } = renderApp('#task=t1');
    const Source = window.EventSource;
    let stream: EventSource | undefined;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (String(url).startsWith('/api/events?session=t1')) this.addEventListener('snapshot', (e) => { stream = e.target as EventSource; });
      }
    };
    await screen.findByRole('region', { name: 'Conversation' });
    await waitFor(() => expect(stream).toBeDefined());
    const original = window.fetch;
    const fetch = vi.spyOn(window, 'fetch');
    fetch.mockImplementation((input, init) => String(input).endsWith('/sign-in') ? Promise.resolve(new Response(JSON.stringify({ url: 'https://authorize.example/login', callback: true }))) : original(input, init));
    try {
      const menu = await openMenu(user, 'Task actions');
      await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
      const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
      await dialog.findByRole('list', { name: "This task's MCP servers" });
      await user.click(dialog.getByRole('button', { name: action, exact: true }));
      const name = action === 'Sign in' ? 'tracker' : 'docs-search';
      const form = within(await dialog.findByRole('form', { name: `Sign in to ${name}` }));
      expect(form.getByText(/Finish in that tab, then return to this task/)).toBeTruthy();
      expect(form.queryByRole('textbox', { name: 'Address the browser ended on' })).toBeNull();
      expect(form.queryByText(/server machine instead/)).toBeNull();
      const send = (seq: number, status: string) => act(() => stream!.dispatchEvent(new MessageEvent('mcp_status', { data: JSON.stringify({ seq, session_id: 't1', mcp_status: { supported: true, ready: true, servers: [{ name, status, remote: true, sign_in: true }] } }) })));
      send(1_000_001, 'pending');
      expect(dialog.getByRole('form', { name: `Sign in to ${name}` })).toBeTruthy();
      send(1_000_002, 'connected');
      await waitFor(() => expect(dialog.queryByRole('form', { name: `Sign in to ${name}` })).toBeNull());
      expect(dialog.getByRole('button', { name: 'Apply current settings' })).toHaveProperty('disabled', false);
      expect(fetch.mock.calls.filter(([url]) => String(url).endsWith('/sign-in/finish'))).toHaveLength(0);
    } finally {
      fetch.mockRestore();
    }
  });

  test('an older service still reads one expanded server through its existing rich endpoint', async () => {
    const { user } = await openTask('t1');
    const original = window.fetch;
    const fetch = vi.spyOn(window, 'fetch');
    fetch.mockImplementation((input, init) => String(input).endsWith('/tools') ? Promise.resolve(new Response('{"error":"not found"}', { status: 404 })) : original(input, init));
    try {
      const menu = await openMenu(user, 'Task actions');
      await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
      const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
      await dialog.findByRole('list', { name: "This task's MCP servers" });
      fetch.mockClear();
      await user.click(dialog.getByRole('button', { name: 'Tools', exact: true }));
      const tools = within(await dialog.findByRole('list', { name: 'docs-search tools' }));
      expect(tools.getByText('Search the documentation.')).toBeTruthy();
      const reads = fetch.mock.calls.filter(([url]) => String(url).includes('/mcp'));
      expect(reads).toHaveLength(1);
      expect(String(reads[0][0])).toBe('/api/sessions/t1/mcp');
      expect(dialog.queryByRole('list', { name: 'tracker tools' })).toBeNull();
    } finally {
      fetch.mockRestore();
    }
  });

  test('completed sign-in is permanently released when native status connects', async () => {
    const { user } = renderApp('#task=t1');
    const Source = window.EventSource;
    let stream: EventSource | undefined;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (String(url).startsWith('/api/events?session=t1')) this.addEventListener('snapshot', (e) => { stream = e.target as EventSource; });
      }
    };
    await screen.findByRole('region', { name: 'Conversation' });
    await waitFor(() => expect(stream).toBeDefined());
    const menu = await openMenu(user, 'Task actions');
    await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
    await dialog.findByRole('list', { name: "This task's MCP servers" });
    await user.click(dialog.getByRole('button', { name: 'Sign in', exact: true }));
    await dialog.findByRole('form', { name: 'Sign in to tracker' });
    const send = (seq: number, status: string) => act(() => stream!.dispatchEvent(new MessageEvent('mcp_status', { data: JSON.stringify({ seq, session_id: 't1', mcp_status: { supported: true, ready: true, servers: [{ name: 'tracker', status, remote: true }] } }) })));
    send(1_000_001, 'connected');
    await waitFor(() => expect(dialog.queryByRole('form', { name: 'Sign in to tracker' })).toBeNull());
    send(1_000_002, 'pending');
    await dialog.findByText('Starting…');
    expect(dialog.queryByRole('form', { name: 'Sign in to tracker' })).toBeNull();
    expect(dialog.queryByRole('link', { name: /Open the sign-in page/ })).toBeNull();
    expect(dialog.getByRole('button', { name: 'Apply current settings' })).toHaveProperty('disabled', false);
  });

  test('"Sign in again" is offered only for a server that uses a sign-in, not for every connected remote one', async () => {
    const { user } = renderApp('#task=t1');
    const Source = window.EventSource;
    let stream: EventSource | undefined;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (String(url).startsWith('/api/events?session=t1')) this.addEventListener('snapshot', (e) => { stream = e.target as EventSource; });
      }
    };
    await screen.findByRole('region', { name: 'Conversation' });
    await waitFor(() => expect(stream).toBeDefined());
    const menu = await openMenu(user, 'Task actions');
    await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
    await dialog.findByRole('list', { name: "This task's MCP servers" });
    act(() => stream!.dispatchEvent(new MessageEvent('mcp_status', { data: JSON.stringify({ seq: 1_000_001, session_id: 't1', mcp_status: { supported: true, ready: true, servers: [{ name: 'plain-remote', status: 'connected', remote: true }, { name: 'oauth-remote', status: 'connected', remote: true, sign_in: true }] } }) })));
    await dialog.findByText('plain-remote');
    expect(dialog.getAllByRole('button', { name: 'Sign in again' })).toHaveLength(1);
    expect(within(dialog.getByText('oauth-remote').closest('li')!).getByRole('button', { name: 'Sign in again' })).toBeTruthy();
  });

  test('native task status updates without polling, and tools load only for the expanded server', async () => {
    // Leave mock network/animation timeouts real, while making interval polling observable.
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const { user } = renderApp('#task=t1');
    const Source = window.EventSource;
    let stream: EventSource | undefined;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (String(url).startsWith('/api/events?session=t1')) this.addEventListener('snapshot', (e) => { stream = e.target as EventSource; });
      }
    };
    await screen.findByRole('region', { name: 'Conversation' });
    await waitFor(() => expect(stream).toBeDefined());
    const fetch = vi.spyOn(window, 'fetch');
    try {
      const menu = await openMenu(user, 'Task actions');
      await user.click(menu.getByRole('menuitem', { name: 'MCP servers…' }));
      const dialog = within(await screen.findByRole('dialog', { name: 'MCP servers' }));
      await dialog.findByRole('list', { name: "This task's MCP servers" });
      const calls = () => fetch.mock.calls.filter(([url, init]) => new URL(String(url), window.location.origin).pathname === '/api/sessions/t1/mcp' && (!init?.method || init.method === 'GET'));
      const initialReads = calls().length;
      expect(initialReads).toBeGreaterThan(0);
      const send = (seq: number, status: string) => act(() => stream!.dispatchEvent(new MessageEvent('mcp_status', { data: JSON.stringify({ seq, session_id: 't1', mcp_status: { supported: true, ready: true, servers: [{ name: 'docs-search', status, remote: true }] } }) })));
      send(1_000_001, 'pending');
      expect(await dialog.findByText('Starting…')).toBeTruthy();
      send(1_000_002, 'connected');
      expect(await dialog.findByText('Connected')).toBeTruthy();
      expect(dialog.getByRole('button', { name: 'Tools', exact: true })).toBeTruthy();
      expect(fetch.mock.calls.filter(([url]) => String(url).includes('/tools'))).toHaveLength(0);
      await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
      expect(calls()).toHaveLength(initialReads);
      await user.click(dialog.getByRole('button', { name: 'Tools', exact: true }));
      const tools = within(await dialog.findByRole('list', { name: 'docs-search tools' }));
      expect(tools.getByText('Search the documentation.')).toBeTruthy();
      expect(dialog.getByRole('button', { name: '2 tools' })).toBeTruthy();
      const toolReads = fetch.mock.calls.filter(([url]) => String(url).includes('/mcp/servers/') && String(url).endsWith('/tools'));
      expect(toolReads).toHaveLength(1);
      expect(String(toolReads[0][0])).toBe('/api/sessions/t1/mcp/servers/docs-search/tools');
      expect(calls()).toHaveLength(initialReads);
      await user.click(dialog.getByRole('button', { name: 'Refresh', exact: true }));
      await waitFor(() => expect(calls()).toHaveLength(initialReads + 1));
    } finally {
      fetch.mockRestore();
      vi.useRealTimers();
    }
  });

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
