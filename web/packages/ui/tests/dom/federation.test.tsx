import { StrictMode } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { UamApp } from '@uam/ui';
import { install } from '../../src/mock/install';
import { createApiClient, type ConnectedInstance } from '../../src/api';

const capabilities = ['workload-grants-v1', 'expected-instance-v1', 'local-workload-v1', 'events-v1', 'files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'routines-v1', 'usage-v1', 'notices-v1'];
const record = (id: string, label: string): ConnectedInstance => ({ id, instance_id: `instance-${id}`, label, base_url: `https://${id}.example`, enabled: true, generation: 1, has_key: true, version: 'test', protocol_major: 1, capabilities, allow_private: false });

function federated(hash = '', records = [record('b', 'Workstation B'), record('c', 'Workstation C')], strict = true) {
  history.replaceState(null, '', `/${hash}`);
  const owners = ['', 'b', 'c'].map(id => {
    const mock = install();
    return { id, mock, fetch: window.fetch, EventSource: window.EventSource };
  });
  const calls: { owner: string; path: string; method: string }[] = [];
  const streams: { owner: string; path: string; stream: EventSource }[] = [];
  let registry = records;
  let expired = false;
  const route = (input: string) => {
    const url = new URL(input, window.location.origin);
    const match = /^\/api\/connected\/([^/]+)(\/api\/.*)$/.exec(url.pathname);
    const owner = match?.[1] ?? '';
    if (match) { url.pathname = match[2]; url.searchParams.delete('uam_generation'); }
    return { owner, path: url.pathname + url.search };
  };
  window.fetch = async (input, init) => {
    const full = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    if (expired && ['/api/connections', '/api/auth'].includes(new URL(full, window.location.origin).pathname)) return new Response(JSON.stringify({ error: 'Sign in required' }), { status: 401 });
    if (new URL(full, window.location.origin).pathname === '/api/connections') return new Response(JSON.stringify({ instance_id: 'home-a', connections: registry }));
    const { owner, path } = route(full);
    calls.push({ owner, path, method: init?.method ?? 'GET' });
    return owners.find(entry => entry.id === owner)!.fetch(path, init);
  };
  window.EventSource = new Proxy(owners[0].EventSource, { construct(_target, args: [string]) {
    const { owner, path } = route(args[0]);
    const stream = new (owners.find(entry => entry.id === owner)!.EventSource)(path);
    streams.push({ owner, path, stream });
    return stream;
  } });
  const user = userEvent.setup();
  const view = render(strict ? <StrictMode><UamApp /></StrictMode> : <UamApp />);
  return { user, calls, streams, owners, ...view, expireHome() { expired = true; act(() => document.dispatchEvent(new Event('visibilitychange'))); }, replaceRegistry(next: ConnectedInstance[]) { registry = next; act(() => document.dispatchEvent(new Event('visibilitychange'))); }, setRegistry(next: ConnectedInstance[]) { registry = next; } };
}

async function taskRows() {
  return within(await screen.findByRole('navigation', { name: 'Tasks' }));
}

test('combines colliding task IDs and sends a selected task reply only to its owning instance', async () => {
  const { user, owners, streams } = federated();
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await screen.findByRole('region', { name: 'Conversation' });
  const box = await screen.findByRole('textbox', { name: 'Message' });
  await waitFor(() => expect(box).toHaveProperty('disabled', false));
  await user.type(box, 'Only B receives this');
  await user.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(owners[1].mock.received.some(call => call.body.text === 'Only B receives this')).toBe(true));
  expect(owners[0].mock.received).toHaveLength(0);
  expect(owners[2].mock.received).toHaveLength(0);
  expect(window.location.hash).toContain('task=t3&home=home-a&instance=instance-b&connection=b&generation=1');
  for (const owner of ['', 'b', 'c']) expect(streams.filter(entry => entry.owner === owner && !entry.path.startsWith('/api/events/detail') && !entry.path.startsWith('/api/connected-notifications') && entry.stream.readyState !== 2)).toHaveLength(1);
});

test('owner switching restores separate drafts and keeps configuration requests on the selected source', async () => {
  const { user, calls } = federated();
  const b = createApiClient(record('b', 'Workstation B'));
  localStorage.setItem(b.storageKey('uam.draft.t3'), JSON.stringify({ text: 'B draft' }));
  localStorage.setItem('uam.draft.t3', JSON.stringify({ text: 'A draft' }));
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  expect(await screen.findByRole('textbox', { name: 'Message' })).toHaveProperty('value', 'B draft');
  expect(screen.queryByRole('combobox', { name: 'Active instance' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  await chooseInstance(user, /^Workstation B/);
  await screen.findByRole('heading', { name: 'Settings · Workstation B', level: 1 });
  await user.click(screen.getByRole('button', { name: 'Agents', exact: true }));
  await waitFor(() => expect(calls.some(call => call.owner === 'b' && call.path.startsWith('/api/configuration'))).toBe(true));
  expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/configuration'))).toBe(false);
});

test('membership sweeps remain owner-scoped and skip unchanged remote Task selection', async () => {
  const b = createApiClient(record('b', 'Workstation B'));
  const c = createApiClient(record('c', 'Workstation C'));
  for (const client of [b, c]) {
    localStorage.setItem(client.storageKey('uam.draft.gone'), 'stale draft');
    localStorage.setItem(`uam.review.${client.cacheKey('gone')}`, 'stale review');
  }
  localStorage.setItem('uam.history-archive', JSON.stringify({ [b.cacheKey('gone')]: 1, [c.cacheKey('gone')]: 1 }));
  const { user, streams } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  expect(localStorage.getItem(b.storageKey('uam.draft.gone'))).toBe('stale draft');
  await user.click(rows.getByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await waitFor(() => expect(localStorage.getItem(b.storageKey('uam.draft.gone'))).toBeNull());
  expect(localStorage.getItem(`uam.review.${b.cacheKey('gone')}`)).toBeNull();
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ [c.cacheKey('gone')]: 1 });
  expect(localStorage.getItem(c.storageKey('uam.draft.gone'))).toBe('stale draft');
  expect(localStorage.getItem(`uam.review.${c.cacheKey('gone')}`)).toBe('stale review');

  const keys = vi.spyOn(Object, 'keys');
  const reads = vi.spyOn(localStorage, 'getItem');
  try {
    const remoteRows = await taskRows();
    await user.click(remoteRows.getByRole('button', { name: /Bump GitHub Actions pins/, description: 'Workstation B' }));
    await waitFor(() => expect(streams.some(({ owner, path, stream }) => owner === 'b' && path.startsWith('/api/events?session=t4&') && stream.readyState === 1)).toBe(true));
    await act(async () => {});
    expect(keys.mock.calls.filter(([object]) => object === localStorage)).toHaveLength(0);
    expect(reads.mock.calls.filter(([key]) => key === 'uam.history-archive')).toHaveLength(0);
  } finally {
    keys.mockRestore();
    reads.mockRestore();
  }
});

test('a remote deep link with a stale generation opens the Task on the connection at its current generation', async () => {
  const { calls } = federated('#task=t3&home=home-a&instance=instance-b&connection=b&generation=99');
  await screen.findByRole('region', { name: 'Conversation' });
  await waitFor(() => expect(calls.some(call => call.owner === 'b' && call.path.startsWith('/api/sessions/t3'))).toBe(true));
  expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/sessions/t3'))).toBe(false);
  // The fragment is rewritten to the generation in use; the stale one is only a cache-buster.
  await waitFor(() => expect(window.location.hash).toBe('#task=t3&home=home-a&instance=instance-b&connection=b&generation=1'));
  expect(screen.queryByText(/This connection has changed/)).toBeNull();
});

test('a remote deep link to a replaced instance stays unavailable and never opens a colliding local task', async () => {
  const { calls } = federated('#task=t3&home=home-a&instance=instance-x&connection=b&generation=1');
  expect(await screen.findByText('The connected instance no longer matches this link.')).toBeTruthy();
  expect(screen.queryByRole('region', { name: 'Conversation' })).toBeNull();
  expect(calls.some(call => call.path.startsWith('/api/sessions/t3'))).toBe(false);
});

test('disabling the selected owner falls back to this instance with the sidebar intact and closes its streams', async () => {
  const records = [record('b', 'Workstation B')];
  const { user, replaceRegistry, streams } = federated('', records);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await screen.findByRole('region', { name: 'Conversation' });
  replaceRegistry([{ ...records[0], enabled: false, generation: 2 }]);
  // Home, not a blank page or a full-screen notice; the Task list stays, without the disabled machine's rows.
  await screen.findByRole('heading', { name: 'What are you working on?', level: 1 });
  expect(screen.queryByText(/This connection is disabled|This connection is unavailable/)).toBeNull();
  expect(screen.queryByRole('textbox', { name: 'Message' })).toBeNull();
  expect(window.location.hash).toBe('');
  const list = await taskRows();
  expect(list.getByRole('button', { name: /Doctor: add terminal line/, description: 'This instance' })).toBeTruthy();
  // The machine's rows leave through the list's view transition, after Home is on screen.
  await waitFor(() => expect(list.queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' })).toBeNull());
  await waitFor(() => expect(streams.filter(entry => entry.owner === 'b').every(entry => entry.stream.readyState === 2)).toBe(true));
});

test('re-pairing the selected owner keeps its Task open at the new generation', async () => {
  const records = [record('b', 'Workstation B')];
  const { user, replaceRegistry, calls } = federated('', records);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await screen.findByRole('region', { name: 'Conversation' });
  replaceRegistry([{ ...records[0], generation: 2 }]);
  await waitFor(() => expect(window.location.hash).toBe('#task=t3&home=home-a&instance=instance-b&connection=b&generation=2'));
  // The view remounts on the connection's new generation and shows the same Task.
  await screen.findByRole('region', { name: 'Conversation' });
  expect(screen.queryByText(/This connection is unavailable/)).toBeNull();
  expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/sessions/t3'))).toBe(false);
});

test('core-only peers render their task and name unavailable optional features in Settings', async () => {
  const { user } = federated('', [{ ...record('b', 'Core only'), capabilities: capabilities.slice(0, 4) }]);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Core only' }));
  await screen.findByRole('region', { name: 'Conversation' });
  expect(screen.queryByRole('combobox', { name: 'Active instance' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  expect(await screen.findByText(/Unavailable on Core only:.*files.*terminal.*configuration/)).toBeTruthy();
  // A section the peer cannot serve explains why instead of offering a form that fails.
  const shell = within(await screen.findByRole('region', { name: 'Shell access' }));
  expect(shell.getByText('Core only does not offer the terminal to connected instances. Update UAM on Core only to manage it here.')).toBeTruthy();
  expect(shell.queryByRole('switch', { name: 'Terminal' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Skills', exact: true }));
  expect(within(await screen.findByRole('region', { name: 'Skills' })).getByText(/does not offer agents, skills, hooks and instructions/)).toBeTruthy();
});

async function chooseInstance(user: ReturnType<typeof userEvent.setup>, name: RegExp) {
  await user.click(await screen.findByRole('combobox', { name: 'Active instance' }));
  await user.click(await screen.findByRole('option', { name }));
}

test('Settings of a selected peer read and write only that peer and name it in the header', async () => {
  const { user, calls, owners } = federated();
  await user.click(await screen.findByRole('button', { name: 'Settings', exact: true }));
  await screen.findByRole('heading', { name: 'Settings · This instance', level: 1 });
  expect(screen.getByRole('combobox', { name: 'Active instance' }).textContent).toContain('This instance');
  // Home's Settings reads its account, CLI and MCP servers once its catalog and settings arrive, which can be after
  // its heading shows; they go before the switch. From the switch on only B's Settings are mounted: no read or write
  // may reach home or C.
  await waitFor(() => expect(['/api/providers/copilot/account', '/api/providers/copilot/cli', '/api/mcp'].every(path => calls.some(call => call.owner === '' && call.path === path))).toBe(true));
  const switched = calls.length;
  await chooseInstance(user, /^Workstation B/);
  await screen.findByRole('heading', { name: 'Settings · Workstation B', level: 1 });
  expect(screen.getByRole('combobox', { name: 'Active instance' }).textContent).toContain('Workstation B');
  // Connections are administered by the home instance only, and its sign-in is not B's to end.
  expect(screen.queryByRole('button', { name: 'Connected instances' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Log out' })).toBeNull();
  const settingsCall = (call: { path: string }) => /^\/api\/(settings|mcp|configuration|providers|usage|utility)/.test(call.path);
  const suggest = await screen.findByRole('switch', { name: 'Suggest replies' });
  await user.click(suggest);
  await waitFor(() => expect(calls.slice(switched).some(call => call.owner === 'b' && call.method === 'PATCH' && call.path === '/api/settings')).toBe(true));
  // B's own settings changed; home and C kept theirs.
  const stored = async (index: number) => (await (await owners[index].fetch('/api/settings')).json()).suggest_replies;
  expect(await stored(1)).toBe(false);
  expect(await stored(0)).not.toBe(false);
  expect(await stored(2)).not.toBe(false);
  await user.click(screen.getByRole('button', { name: 'MCP servers', exact: true }));
  await user.click(screen.getByRole('button', { name: 'Agents', exact: true }));
  await user.click(screen.getByRole('button', { name: 'Models', exact: true }));
  await waitFor(() => {
    const paths = calls.slice(switched).filter(call => call.owner === 'b').map(call => call.path);
    for (const prefix of ['/api/mcp', '/api/configuration', '/api/usage/prices', '/api/providers/copilot/account']) expect(paths.some(path => path.startsWith(prefix))).toBe(true);
  });
  expect(calls.slice(switched).filter(call => call.owner !== 'b' && settingsCall(call))).toEqual([]);
  await user.click(screen.getByRole('button', { name: 'This browser', exact: true }));
  expect(screen.getByText('Kept in this browser, not on Workstation B: these apply whichever instance is on screen.')).toBeTruthy();
});

test.each(['', 'b'])('settle and reopen remain responsive with connected navigation for owner %s', async (owner) => {
  const { user, calls } = federated();
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: owner ? 'Workstation B' : 'This instance' }));
  await screen.findByRole('region', { name: 'Conversation' });
  await user.click(await screen.findByRole('button', { name: 'Task actions' }));
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Settle' }));
  await screen.findByText('Settled. Reopen this task to continue the same conversation.');
  await user.click(screen.getByRole('button', { name: 'Task actions' }));
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Reopen' }));
  await waitFor(() => expect(screen.queryByText(/^Settled\. Reopen/)).toBeNull());
  expect(calls.filter(call => /\/(settle|reopen)$/.test(call.path)).map(call => call.owner)).toEqual([owner, owner]);
});


test('editing an unrelated connection preserves the selected owner client and open streams', async () => {
  const records = [record('b', 'Workstation B'), record('c', 'Workstation C')];
  const { user, streams, replaceRegistry } = federated('', records);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await screen.findByRole('region', { name: 'Conversation' });
  const before = streams.filter(entry => entry.owner === 'b' && entry.stream.readyState !== 2);
  expect(before.length).toBeGreaterThan(0);
  replaceRegistry([records[0], { ...records[1], enabled: false, generation: 2 }]);
  await waitFor(() => expect(within(screen.getByRole('navigation', { name: 'Tasks' })).queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation C' })).toBeNull());
  expect(before.every(entry => entry.stream.readyState !== 2)).toBe(true);
  expect(streams.filter(entry => entry.owner === 'b' && entry.stream.readyState !== 2)).toEqual(before);
});

test('escaped ownership fields cannot briefly open a same-ID local task before validation', async () => {
  const { calls, streams } = federated('#task=t3&%68ome=home-a&%69nstance=instance-x&%63onnection=b&%67eneration=1');
  await screen.findByText('The connected instance no longer matches this link.');
  expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/sessions/t3'))).toBe(false);
  expect(streams.some(entry => entry.owner === '' && new URL(entry.path, 'https://home.test').searchParams.get('session') === 't3')).toBe(false);
});


test('home auth loss clears connected sources even while an invalid route has unmounted the active app', async () => {
  const { expireHome, streams } = federated('#task=t3&home=home-a&instance=instance-x&connection=b&generation=1');
  await screen.findByText('The connected instance no longer matches this link.');
  expireHome();
  await screen.findByRole('heading', { name: 'Sign in to UAM' });
  expect(screen.queryByRole('navigation', { name: 'Tasks' })).toBeNull();
  await waitFor(() => expect(streams.every(entry => entry.stream.readyState === 2)).toBe(true));
});


test('combined badge preserves the home unread-failure count beside remote permission requests', async () => {
  localStorage.setItem('uam.viewedSince', JSON.stringify('2000-01-01T00:00:00Z'));
  const { user } = federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  // Each instance has seven permission/question tasks; home's old read mark adds its failed and interrupted tasks.
  await waitFor(() => expect(document.title).toBe('(16) UAM'));
  await user.click(rows.getByRole('button', { name: /Bump GitHub Actions pins/, description: 'This instance' }));
  await screen.findByRole('region', { name: 'Conversation' });
  await waitFor(() => expect(document.title).toBe('(15) UAM - Bump GitHub Actions pins'));
});

test('task navigation shows compact instance names without extra filters and removes disabled sources', async () => {
  const records = [record('b', 'Workstation B')];
  const { user, replaceRegistry } = federated('', records);
  const rows = await taskRows();
  const remote = await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  expect(within(remote).getByText('Workstation B').title).toBe('Workstation B');
  expect(within(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'This instance' })).getByText('Local')).toBeTruthy();
  expect(screen.queryByRole('combobox', { name: 'Task instance filter' })).toBeNull();
  expect(screen.queryByRole('combobox', { name: 'Task project filter' })).toBeNull();
  expect(screen.queryByText(/Counts may be incomplete/)).toBeNull();
  expect(screen.queryByText(/^Workstation B: /)).toBeNull();
  replaceRegistry([{ ...records[0], enabled: false, generation: 2 }]);
  await waitFor(() => expect(rows.queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' })).toBeNull());
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'This instance' }));
  await screen.findByRole('region', { name: 'Conversation' });
  expect(window.location.hash).toBe('#task=t3');
});

test('turning a connection off removes its rows at once, before the registry is read again', async () => {
  const records = [record('b', 'Workstation B')];
  const { user, setRegistry } = federated('', records);
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  await user.click(await screen.findByRole('button', { name: 'Connected instances' }));
  const harness = window.fetch;
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  let toggled = false;
  window.fetch = async (input, init) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, window.location.origin);
    if (url.pathname === '/api/connections/b' && init?.method === 'PATCH') {
      const next = { ...records[0], enabled: false };
      setRegistry([next]);
      toggled = true;
      return new Response(JSON.stringify(next));
    }
    // The registry re-read waits on the server's account checks; the switch's own result must not wait for it.
    if (toggled && url.pathname === '/api/connections') await held;
    return harness(input, init);
  };
  const section = within(await screen.findByRole('region', { name: 'Workstation B' }));
  await user.click(section.getByRole('switch', { name: 'Enable Workstation B' }));
  await waitFor(() => expect(rows.queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' })).toBeNull());
  expect(rows.getByRole('button', { name: /Doctor: add terminal line/, description: 'This instance' })).toBeTruthy();
  expect(section.getByRole('status').textContent).toBe('Disabled');
  release();
});

test('leaving the instance whose terminal is open asks in the app’s dialog, never the browser’s', async () => {
  // happy-dom has no confirm(); a browser's would block the test, so a stub stands in and must stay uncalled.
  const native = vi.fn(() => true);
  vi.stubGlobal('confirm', native);
  const { user } = federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'This instance' }));
  await screen.findByRole('region', { name: 'Conversation' });
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  await screen.findByRole('region', { name: 'Terminal' });
  await user.click(rows.getByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  const dialog = within(await screen.findByRole('alertdialog', { name: 'Switch instances?' }));
  expect(native).not.toHaveBeenCalled();
  // Cancel keeps the instance and its shell.
  await user.click(dialog.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  expect(window.location.hash).toBe('#task=t3');
  expect(screen.getByRole('region', { name: 'Terminal' })).toBeTruthy();
  await user.click(rows.getByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await user.click(within(await screen.findByRole('alertdialog', { name: 'Switch instances?' })).getByRole('button', { name: 'Switch and close terminal' }));
  await waitFor(() => expect(window.location.hash).toContain('connection=b'));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'Terminal' })).toBeNull());
  expect(native).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});

test('Alt+J and Alt+K walk the Tasks that need you across machines, in the list’s order', async () => {
  federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  const keys = Array.from(rows.getByRole('list', { name: 'Unsettled tasks' }).querySelectorAll<HTMLElement>('[data-task-row]')).filter((el) => within(el).queryByText('Input')).map((el) => el.dataset.taskRow!);
  expect(keys.length).toBeGreaterThan(2);
  // Both machines hold the same Tasks, so the list alternates: this instance's row, then Workstation B's.
  expect(keys[0]).not.toMatch(/^uam:/);
  expect(keys[1]).toBe(`uam:b:${keys[0]}`);
  expect(keys[2]).not.toMatch(/^uam:/);
  const remote = (id: string) => `#task=${id}&home=home-a&instance=instance-b&connection=b&generation=1`;
  const press = (code: 'KeyJ' | 'KeyK') => fireEvent.keyDown(document.body, { code, key: code === 'KeyJ' ? 'j' : 'k', altKey: true });
  press('KeyJ');
  await waitFor(() => expect(window.location.hash).toBe(`#task=${keys[0]}`));
  press('KeyJ');
  await waitFor(() => expect(window.location.hash).toBe(remote(keys[0])));
  await screen.findByRole('region', { name: 'Conversation' });
  press('KeyJ');
  await waitFor(() => expect(window.location.hash).toBe(`#task=${keys[2]}`));
  await screen.findByRole('region', { name: 'Conversation' });
  press('KeyK');
  await waitFor(() => expect(window.location.hash).toBe(remote(keys[0])));
});

test('another machine’s Task, Terminal and Changes name the machine after their title; this instance’s do not', async () => {
  const { user } = federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Fix re-attach redraw regression/, description: 'This instance' }));
  await screen.findByRole('region', { name: 'Conversation' });
  const titleRow = () => within(screen.getByRole('heading', { level: 1 }).parentElement!);
  expect(titleRow().queryByText('Workstation B')).toBeNull();
  await user.click(await (await taskRows()).findByRole('button', { name: /Fix re-attach redraw regression/, description: 'Workstation B' }));
  await waitFor(() => expect(window.location.hash).toContain('connection=b'));
  await screen.findByRole('region', { name: 'Conversation' });
  expect(titleRow().getByText('Workstation B')).toBeTruthy();
  expect(titleRow().getByText('Workstation B').classList.contains('sr-only')).toBe(false);
  await user.click(screen.getByRole('button', { name: 'Terminal' }));
  // The terminal's chunk loads lazily: its header follows the region.
  expect(await within(await screen.findByRole('region', { name: 'Terminal' })).findByText('Workstation B')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: /^Open changes/ }));
  const changes = await screen.findByRole('dialog', { name: 'Changes' });
  expect(within(changes).getByText('Workstation B')).toBeTruthy();
  await user.click(within(changes).getByRole('button', { name: 'Close changes' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull());
});

test('choosing the open remote task again still selects it (the composer takes focus, the drawer closes)', async () => {
  const { user } = federated();
  const rows = await taskRows();
  const remote = await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(remote);
  const box = await screen.findByRole('textbox', { name: 'Message' });
  await waitFor(() => expect(box).toHaveProperty('disabled', false));
  const again = (await taskRows()).getByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  again.focus();
  const hash = window.location.hash;
  const length = history.length;
  await user.click(again);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'Message' })));
  expect(window.location.hash).toBe(hash);
  expect(history.length).toBe(length);
});

test('the Project filter lists every machine’s Projects under its name and filters to one on that machine', async () => {
  const { user } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getByRole('button', { name: 'Project filter: all projects' }));
  const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
  const groups = list.getAllByRole('group');
  expect(groups.map((group) => group.getAttribute('aria-labelledby') && document.getElementById(group.getAttribute('aria-labelledby')!)?.textContent)).toEqual(['This instance', 'Workstation B', 'Workstation C']);
  const b = within(list.getByRole('group', { name: 'Workstation B' }));
  const project = b.getAllByRole('option')[0];
  const name = project.querySelector('.truncate')?.textContent ?? '';
  await user.click(project);
  await waitFor(() => expect(screen.getByRole('button', { name: `Project filter: ${name}` })).toBeTruthy());
  // Only B's Tasks of that Project remain.
  await waitFor(() => expect(rows.queryAllByRole('button', { description: 'This instance' })).toHaveLength(0));
  expect(rows.queryAllByRole('button', { description: 'Workstation C' })).toHaveLength(0);
  expect(rows.getAllByRole('button', { description: 'Workstation B' }).length).toBeGreaterThan(0);
});

test('Add project on another machine browses and adds there, and the Project shows under that machine', async () => {
  const { user, calls } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getAllByRole('button', { name: 'Add project' })[0]);
  const dialog = within(await screen.findByRole('dialog', { name: 'Add a project' }));
  await user.click(dialog.getByRole('combobox', { name: 'Machine' }));
  await user.click(await screen.findByRole('option', { name: /^Workstation B/ }));
  const before = calls.length;
  await user.click(dialog.getByRole('button', { name: 'Browse' }));
  const folders = within(await dialog.findByRole('listbox', { name: 'Folders' }));
  await user.dblClick(await folders.findByRole('option', { name: /^projects/ }));
  await user.click(await folders.findByRole('option', { name: /^archive/ }));
  await user.click(dialog.getByRole('button', { name: 'Use this folder' }));
  await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'Remote archive');
  await user.click(dialog.getByRole('button', { name: 'Add project' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
  const made = calls.slice(before).filter((call) => call.path.startsWith('/api/fs') || (call.path === '/api/projects' && call.method === 'POST'));
  expect(made.length).toBeGreaterThan(1);
  expect(made.every((call) => call.owner === 'b')).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Project filter: all projects' }));
  const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
  await waitFor(() => expect(within(list.getByRole('group', { name: 'Workstation B' })).getByRole('option', { name: /Remote archive/ })).toBeTruthy());
  expect(within(list.getByRole('group', { name: 'This instance' })).queryByRole('option', { name: /Remote archive/ })).toBeNull();
});

test('the New task palette lists every machine and another machine’s Project opens its draft there', async () => {
  const { user } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(document.getElementById('new-task')!);
  const palette = within(await screen.findByRole('dialog', { name: 'New task' }));
  const b = within(palette.getByRole('group', { name: 'Workstation B' }));
  expect(palette.getByRole('group', { name: 'This instance' })).toBeTruthy();
  await user.click(b.getAllByRole('option')[0]);
  await waitFor(() => expect(window.location.hash).toContain('connection=b'));
  expect(await screen.findByRole('heading', { name: 'New task', level: 1 })).toBeTruthy();
  expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();
});

test('Settle on another machine’s row runs on that machine only, and its shelf counts it', async () => {
  const { user, calls } = federated();
  const nav = await taskRows();
  const row = await nav.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  const shelf = () => Number(/(\d+)/.exec(nav.getByRole('button', { name: /^Settled/ }).textContent ?? '')?.[1] ?? 0);
  const settled = shelf();
  expect(nav.getByRole('button', { name: /^Archived/ })).toBeTruthy();
  const settle = row.closest('[data-task-row]')!.querySelector<HTMLElement>('[data-settle]')!;
  await user.click(settle);
  await waitFor(() => expect(shelf()).toBe(settled + 1));
  expect(calls.filter((call) => /\/(settle|stage)$/.test(call.path) || call.path.includes('/stage')).map((call) => call.owner)).toEqual(['b']);
  expect(window.location.hash).not.toContain('connection=b');
});

test('a page load opens one stream, reads the catalogs once and reports what it shows once per stream', async () => {
  // Without StrictMode's development double mount: the requests a production page makes.
  const { user, calls, streams, owners } = federated('', [record('b', 'Workstation B')], false);
  // As on a real page, the registry answers after this instance's stream has sent its first snapshot.
  const answer = window.fetch;
  window.fetch = async (input, init) => {
    if (String(input).endsWith('/api/connections')) await new Promise((r) => setTimeout(r, 200));
    return answer(input, init);
  };
  const rows = await taskRows();
  const remote = await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await new Promise((r) => setTimeout(r, 300));
  // The connections arrive after this instance's stream opened: it stays open.
  expect(streams.filter(entry => entry.owner === '' && entry.path.startsWith('/api/events?'))).toHaveLength(1);
  expect(calls.filter(call => call.owner === '' && call.path === '/api/meta')).toHaveLength(1);
  expect(calls.filter(call => call.path.endsWith('/viewing')).map(call => call.path).sort()).toEqual(['/api/connected-notifications/viewing', '/api/viewing']);
  // A later change to the custom models, part of the model lists, reads the catalogs again.
  await act(async () => { await owners[0].fetch('/api/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ custom_models: [] }) }); });
  await waitFor(() => expect(calls.filter(call => call.owner === '' && call.path === '/api/meta')).toHaveLength(2));
  // Its snapshot still reached the machine list: on B, this instance's rows show before its own stream there opens.
  const Live = window.EventSource;
  window.EventSource = new Proxy(Live, { construct(target, args: [string]) {
    const stream = new target(args[0]);
    if (args[0].startsWith('/api/events?')) stream.close();
    return stream;
  } });
  await user.click(remote);
  await waitFor(() => expect(window.location.hash).toContain('connection=b'));
  expect((await taskRows()).getByRole('button', { name: /Explain how the CPR probe decides the glyph set/, description: 'This instance' })).toBeTruthy();
});

test('a first connection added while the page is open restarts the stream once, so the machine list starts from a snapshot', async () => {
  const { user, owners, streams, replaceRegistry } = federated('', [], false);
  await (await taskRows()).findByRole('button', { name: /Explain how the CPR probe decides the glyph set/ });
  // Renamed while there is no machine list to keep it in.
  await act(async () => { await owners[0].fetch('/api/sessions/t2', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'Renamed before pairing' }) }); });
  await (await taskRows()).findByRole('button', { name: /Renamed before pairing/ });
  replaceRegistry([record('b', 'Workstation B')]);
  const remote = await (await taskRows()).findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await waitFor(() => expect(streams.filter(entry => entry.owner === '' && entry.path.startsWith('/api/events?'))).toHaveLength(2));
  // On B, this instance's rows come from that snapshot: its own stream there never opens here.
  const Live = window.EventSource;
  window.EventSource = new Proxy(Live, { construct(target, args: [string]) {
    const stream = new target(args[0]);
    if (args[0].startsWith('/api/events?')) stream.close();
    return stream;
  } });
  await user.click(remote);
  await waitFor(() => expect(window.location.hash).toContain('connection=b'));
  expect((await taskRows()).getByRole('button', { name: /Renamed before pairing/, description: 'This instance' })).toBeTruthy();
});

test('switching the instance in Settings keeps the section and shows the known state at once', async () => {
  const { user } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  await user.click(await screen.findByRole('button', { name: 'Agents', exact: true }));
  await chooseInstance(user, /^Workstation B/);
  await screen.findByRole('heading', { name: 'Settings · Workstation B', level: 1 });
  expect(screen.getByRole('button', { name: 'Agents', exact: true }).getAttribute('aria-current')).toBe('page');
  expect(screen.queryByText('Loading tasks…')).toBeNull();
  expect(screen.queryByRole('main', { busy: true })).toBeNull();
  for (const machine of ['This instance', 'Workstation B', 'Workstation C']) expect((await taskRows()).getByRole('button', { name: /Doctor: add terminal line/, description: machine })).toBeTruthy();
});

/** The Usage popover's summary value under `label`. */
function usageSummary(label: 'Estimated cost' | 'Cache saving' | 'Total tokens') {
  const name = within(screen.getByRole('region', { name: 'Tokens' })).getByText(label, { exact: true });
  return (label === 'Total tokens' ? name : name.parentElement!).nextElementSibling!.textContent;
}

async function openUsage(user: ReturnType<typeof userEvent.setup>) {
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(rows.getByRole('button', { name: 'Usage', exact: true }));
  return within(await screen.findByRole('dialog', { name: 'Usage' }));
}

test('Usage sums every machine’s tokens, each priced by its own catalog, and filters to one machine', async () => {
  const { user, owners } = federated('', [record('b', 'Workstation B')]);
  // Only B prices gpt-6-luna: its cost counts in All machines and on B, never on this instance.
  await act(async () => { await owners[1].fetch('/api/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token_prices: { copilot: { 'gpt-6-luna': { input: 1, output: 2 } } } }) }); });
  const popover = await openUsage(user);
  const machine = popover.getByRole('combobox', { name: 'Machine' });
  expect(machine.textContent).toBe('All machines');
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$3.77'));
  await waitFor(() => expect(usageSummary('Cache saving')).toBe('$5.02'));
  expect(usageSummary('Total tokens')).toBe('2.6M');
  expect(popover.queryByText(/not included/)).toBeNull();
  await user.click(machine);
  expect((await screen.findAllByRole('option')).map((option) => option.textContent)).toEqual(['All machines', 'This instance', 'Workstation B']);
  await user.click(screen.getByRole('option', { name: 'Workstation B' }));
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$1.90'));
  expect(usageSummary('Total tokens')).toBe('1.3M');
  expect(screen.getByRole('dialog', { name: 'Usage' })).toBeTruthy();
  await user.click(popover.getByRole('combobox', { name: 'Machine' }));
  await user.click(await screen.findByRole('option', { name: 'This instance' }));
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$1.87'));
  expect(usageSummary('Cache saving')).toBe('$2.51');
});

test('Usage names each machine that cannot report and totals the others', async () => {
  const { user, calls } = federated('', [record('b', 'Workstation B'), { ...record('c', 'Workstation C'), capabilities: capabilities.filter((family) => family !== 'usage-v1') }]);
  const harness = window.fetch;
  window.fetch = async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    return url.startsWith('/api/connected/b/api/usage/tokens') ? new Response(JSON.stringify({ error: 'Usage is unavailable' }), { status: 500 }) : harness(input, init);
  };
  const popover = await openUsage(user);
  expect(await popover.findByText('Workstation B not included: could not read usage')).toBeTruthy();
  expect(popover.getByText('Workstation C not included: needs an update')).toBeTruthy();
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$1.87'));
  expect(usageSummary('Total tokens')).toBe('1.3M');
  expect(calls.some((call) => call.owner === 'c' && call.path.startsWith('/api/usage'))).toBe(false);
});

test('Usage leaves out a machine on another Copilot account', async () => {
  const { user, calls } = federated('', [{ ...record('b', 'Workstation B'), status: 'account_mismatch', reason: 'Workstation B is linked to Copilot account mallory.' }]);
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B, unavailable: linked to a different Copilot account' });
  await user.click(rows.getByRole('button', { name: 'Usage', exact: true }));
  const popover = within(await screen.findByRole('dialog', { name: 'Usage' }));
  expect(await popover.findByText('Workstation B not included: linked to a different Copilot account')).toBeTruthy();
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$1.87'));
  expect(calls.some((call) => call.owner === 'b' && call.path.startsWith('/api/usage'))).toBe(false);
});

test('a connection on another Copilot account keeps its rows, blocks its Tasks and opens its account from Connected instances', async () => {
  const reason = 'Workstation B is linked to Copilot account mallory; this instance is linked to octo. Both must use the same account.';
  const { user } = federated('', [{ ...record('b', 'Workstation B'), status: 'account_mismatch', reason }]);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B, unavailable: linked to a different Copilot account' }));
  expect(await screen.findByRole('heading', { name: 'Copilot is signed in to another account' })).toBeTruthy();
  expect(screen.getByText(reason)).toBeTruthy();
  expect(screen.queryByRole('region', { name: 'Conversation' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  await chooseInstance(user, /^This instance/);
  await screen.findByRole('heading', { name: 'Settings · This instance', level: 1 });
  await user.click(await screen.findByRole('button', { name: 'Connected instances' }));
  const section = within(await screen.findByRole('region', { name: 'Workstation B' }));
  expect(section.getByRole('status').textContent).toBe(`Linked to a different Copilot account — ${reason}`);
  await user.click(section.getByRole('button', { name: 'Open its GitHub Copilot settings' }));
  await screen.findByRole('heading', { name: 'Settings · Workstation B', level: 1 });
  await waitFor(() => expect(document.activeElement?.id).toBe('account-copilot-title'));
});
