import { StrictMode } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { UamApp } from '@uam/ui';
import { install } from '../../src/mock/install';
import { createApiClient, type ConnectedInstance, type SessionSummary } from '../../src/api';

// Which Tasks' sidebar rows rendered: each row builds its menu items once per render.
const rowRenders = vi.hoisted(() => [] as string[]);
vi.mock('../../src/components/taskActions', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../src/components/taskActions')>();
  return { ...actual, taskMenuItems: (...args: Parameters<typeof actual.taskMenuItems>) => {
    if (args[2] === 'row') rowRenders.push(args[0].id);
    return actual.taskMenuItems(...args);
  } };
});

const capabilities = ['workload-grants-v1', 'expected-instance-v1', 'local-workload-v1', 'events-v1', 'files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'routines-v1', 'usage-v1', 'notices-v1', 'unarchive-v1'];
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
  return within(await screen.findByRole('navigation', { name: 'Threads' }));
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
  expect(screen.getAllByRole('button', { name: /^Instance:/ })).toHaveLength(1);
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
  expect(list.getByRole('button', { name: /Doctor: add terminal line/, description: 'Main Server' })).toBeTruthy();
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
  expect(screen.getAllByRole('button', { name: /^Instance:/ })).toHaveLength(1);
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  expect(await screen.findByText(/Unavailable on Core only:.*files.*terminal.*configuration/)).toBeTruthy();
  // A section the peer cannot serve explains why instead of offering a form that fails.
  await user.click(screen.getByRole('button', { name: 'UAM and access', exact: true }));
  const shell = within(await screen.findByRole('region', { name: 'Shell access' }));
  expect(shell.getByText('Core only does not offer the terminal to connected instances. Update UAM on Core only to manage it here.')).toBeTruthy();
  expect(shell.queryByRole('switch', { name: 'Terminal' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Skills', exact: true }));
  expect(within(await screen.findByRole('region', { name: 'Skills' })).getByText(/does not offer agents, skills, hooks and instructions/)).toBeTruthy();
});

/** The instance switcher in the Settings header (the sidebar has its own). */
async function settingsSwitcher() {
  const header = (await screen.findByRole('heading', { name: /^Settings/, level: 1 })).closest('header')!;
  return within(header).getByRole('button', { name: /^Instance:/ });
}

async function chooseInstance(user: ReturnType<typeof userEvent.setup>, name: RegExp) {
  await user.click(await settingsSwitcher());
  await user.click(within(await screen.findByRole('dialog', { name: 'Instances' })).getByRole('button', { name }));
}

test('Settings of a selected peer read and write only that peer and name it in the header', async () => {
  const { user, calls, owners } = federated();
  await user.click(await screen.findByRole('button', { name: 'Settings', exact: true }));
  await screen.findByRole('heading', { name: 'Settings · Main Server', level: 1 });
  expect((await settingsSwitcher()).getAttribute('aria-label')).toBe('Instance: Main Server');
  // Home's Settings mounts Background AI from the start (the rail's dot reads it); its read goes once home's settings
  // arrive, which can be after its heading shows, so it goes before the switch. The other pages mount on their first
  // visit. From the switch on only B's Settings are mounted: no read or write may reach home or C.
  await waitFor(() => expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/utility'))).toBe(true));
  const switched = calls.length;
  await chooseInstance(user, /^Workstation B/);
  await screen.findByRole('heading', { name: 'Settings · Workstation B', level: 1 });
  expect((await settingsSwitcher()).getAttribute('aria-label')).toBe('Instance: Workstation B');
  // Connections are administered by the home instance only, and its sign-in is not B's to end.
  expect(screen.queryByRole('button', { name: 'Connected instances' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Log out' })).toBeNull();
  const settingsCall = (call: { path: string }) => /^\/api\/(settings|mcp|configuration|providers|usage|utility)/.test(call.path);
  await user.click(await screen.findByRole('button', { name: 'Composer', exact: true }));
  const suggest = await screen.findByRole('switch', { name: 'Suggest replies' });
  await user.click(suggest);
  await waitFor(() => expect(calls.slice(switched).some(call => call.owner === 'b' && call.method === 'PATCH' && call.path === '/api/settings')).toBe(true));
  // B's own settings changed; home and C kept theirs.
  const stored = async (index: number) => (await (await owners[index].fetch('/api/settings')).json()).suggest_replies;
  expect(await stored(1)).toBe(false);
  expect(await stored(0)).not.toBe(false);
  expect(await stored(2)).not.toBe(false);
  // Each page reads from B on its first visit: the account too, now that its page mounts when visited.
  await user.click(screen.getByRole('button', { name: 'MCP servers', exact: true }));
  await user.click(screen.getByRole('button', { name: 'Agents', exact: true }));
  await user.click(screen.getByRole('button', { name: 'Token costs', exact: true }));
  await user.click(screen.getByRole('button', { name: 'GitHub Copilot', exact: true }));
  await waitFor(() => {
    const paths = calls.slice(switched).filter(call => call.owner === 'b').map(call => call.path);
    for (const prefix of ['/api/mcp', '/api/configuration', '/api/usage/prices', '/api/providers/copilot/account']) expect(paths.some(path => path.startsWith(prefix))).toBe(true);
  });
  expect(calls.slice(switched).filter(call => call.owner !== 'b' && settingsCall(call))).toEqual([]);
  await user.click(screen.getByRole('button', { name: 'Appearance & alerts', exact: true }));
  expect(screen.getByText('Saved in this browser only.')).toBeTruthy();
  // The groups saved on the instance name it once, in the rail.
  expect(screen.getByRole('group', { name: 'Threads · on Workstation B' })).toBeTruthy();
  expect(screen.queryByRole('group', { name: /^This browser · on/ })).toBeNull();
});

test.each(['', 'b'])('settle and reopen remain responsive with connected navigation for owner %s', async (owner) => {
  const { user, calls } = federated();
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: owner ? 'Workstation B' : 'Main Server' }));
  await screen.findByRole('region', { name: 'Conversation' });
  await user.click(await screen.findByRole('button', { name: 'Thread actions' }));
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Settle…' }));
  await user.click(within(await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' })).getByRole('button', { name: 'Settle anyway' }));
  await screen.findByText('Settled.');
  await user.click(screen.getByRole('button', { name: 'Thread actions' }));
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Reopen' }));
  await waitFor(() => expect(screen.queryByText(/^Settled\. Reopen/)).toBeNull());
  // Known unsaved work asks without a request: the forced settle and the reopen.
  expect(calls.filter(call => /\/(settle|reopen)$/.test(call.path)).map(call => call.owner)).toEqual([owner, owner]);
});

test('Unarchive from a remote search result restores only its owner and opens it as Finished', async () => {
  const { user, calls, owners } = federated();
  const rows = await taskRows();
  await user.type(rows.getByRole('searchbox', { name: 'Search threads' }), 'Node 24');
  const task = await rows.findByRole('button', { name: /Pin GitHub Actions to Node 24/, description: 'Workstation B' });
  fireEvent.contextMenu(task);
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Unarchive' }));
  await waitFor(() => expect(window.location.hash).toContain('task=t12&home=home-a&instance=instance-b&connection=b&generation=1'));
  const restoredRows = await taskRows();
  await restoredRows.findByRole('button', { name: /Finished.*Pin GitHub Actions to Node 24/, description: 'Workstation B' });
  expect(calls.filter(call => call.path.endsWith('/unarchive')).map(call => [call.owner, call.method])).toEqual([['b', 'POST']]);
  for (const owner of owners) expect(owner.mock.received).toHaveLength(0);
  for (const label of ['Main Server', 'Workstation C']) expect(restoredRows.getByRole('button', { name: /Archived.*Pin GitHub Actions to Node 24/, description: label })).toBeTruthy();
});

test('Unarchive is hidden for a connected instance that does not advertise it', async () => {
  const peer = { ...record('b', 'Older server'), capabilities: capabilities.filter(c => c !== 'unarchive-v1') };
  const { user, calls } = federated('', [peer]);
  const rows = await taskRows();
  await user.type(rows.getByRole('searchbox', { name: 'Search threads' }), 'Node 24');
  fireEvent.contextMenu(await rows.findByRole('button', { name: /Pin GitHub Actions to Node 24/, description: 'Older server' }));
  const menu = within(await screen.findByRole('menu'));
  expect(menu.queryByRole('menuitem', { name: 'Unarchive' })).toBeNull();
  expect(menu.getByRole('menuitem', { name: 'Delete' })).toBeTruthy();
  expect(calls.some(call => call.path.endsWith('/unarchive'))).toBe(false);
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
  await waitFor(() => expect(within(screen.getByRole('navigation', { name: 'Threads' })).queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation C' })).toBeNull());
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
  expect(screen.queryByRole('navigation', { name: 'Threads' })).toBeNull();
  await waitFor(() => expect(streams.every(entry => entry.stream.readyState === 2)).toBe(true));
});


test('combined badge preserves the home unread-failure count beside remote permission requests', async () => {
  localStorage.setItem('uam.viewedSince', JSON.stringify('2000-01-01T00:00:00Z'));
  const { user } = federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  // Each instance has seven permission/question tasks; home's old read mark adds its failed and interrupted tasks.
  await waitFor(() => expect(document.title).toBe('(16) UAM'));
  await user.click(rows.getByRole('button', { name: /Bump GitHub Actions pins/, description: 'Main Server' }));
  await screen.findByRole('region', { name: 'Conversation' });
  await waitFor(() => expect(document.title).toBe('(15) UAM - Bump GitHub Actions pins'));
});

test('task navigation uses machine text initials and removes disabled sources', async () => {
  const records = [record('b', 'Workstation B')];
  const { user, replaceRegistry } = federated('', records);
  const rows = await taskRows();
  const remote = await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  const remoteBadge = within(remote).getByTitle('Workstation B');
  expect(remoteBadge.textContent).toBe('WB');
  expect(remoteBadge.getAttribute('aria-hidden')).toBe('true');
  expect(within(remote).queryByText('Workstation B')).toBeNull();
  const local = await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Main Server' });
  expect(within(local).getByTitle('Main Server').textContent).toBe('MS');
  expect(within(local).queryByText('Main Server')).toBeNull();
  const filter = screen.getByRole('button', { name: /^Instance: All instances/ });
  expect(filter.closest('header')).toBeTruthy();
  expect(filter.textContent).toBe('');
  await user.click(filter);
  const reader = within(await screen.findByRole('dialog', { name: 'Instances' }));
  const badge = within(reader.getByRole('button', { name: /^Workstation B,/ })).getByTitle('Workstation B');
  expect(badge.textContent).toBe(remoteBadge.textContent);
  await user.keyboard('{Escape}');
  expect(document.activeElement).toBe(filter);
  expect(screen.queryByRole('combobox', { name: 'Thread instance filter' })).toBeNull();
  expect(screen.queryByRole('combobox', { name: 'Thread project filter' })).toBeNull();
  expect(screen.queryByText(/Counts may be incomplete/)).toBeNull();
  expect(screen.queryByText(/^Workstation B: /)).toBeNull();
  replaceRegistry([{ ...records[0], enabled: false, generation: 2 }]);
  await waitFor(() => expect(rows.queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' })).toBeNull());
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Main Server' }));
  await screen.findByRole('region', { name: 'Conversation' });
  expect(window.location.hash).toBe('#task=t3');
});

test('a streamed update to one Thread re-renders its row alone, though every machine is rebuilt', async () => {
  const { streams, owners } = federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  const tasks = await (await owners[0].fetch('/api/sessions')).json() as SessionSummary[];
  const task = tasks.find((t) => t.id === 't1')!;
  // Both machines' snapshots and catalogs settle first.
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 600)); });
  rowRenders.length = 0;
  const stream = streams.filter((s) => s.owner === '' && s.path.startsWith('/api/events?')).at(-1)!.stream;
  await act(async () => { stream.dispatchEvent(new MessageEvent('session', { data: JSON.stringify({ seq: 1_000_000, session: { ...task, name: 'Renamed on the stream' } }) })); });
  await rows.findByRole('button', { name: /Renamed on the stream/, description: 'Main Server' });
  expect(new Set(rowRenders)).toEqual(new Set(['t1']));
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
  expect(rows.getByRole('button', { name: /Doctor: add terminal line/, description: 'Main Server' })).toBeTruthy();
  expect(section.getByRole('status').textContent).toBe('Disabled');
  release();
});

test('leaving the instance whose terminal is open asks in the app’s dialog, never the browser’s', async () => {
  // happy-dom has no confirm(); a browser's would block the test, so a stub stands in and must stay uncalled.
  const native = vi.fn(() => true);
  vi.stubGlobal('confirm', native);
  const { user } = federated('', [record('b', 'Workstation B')]);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Main Server' }));
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
  const keys = Array.from(rows.getByRole('list', { name: 'Unsettled threads' }).querySelectorAll<HTMLElement>('[data-task-row]')).filter((el) => within(el).queryByText('Input')).map((el) => el.dataset.taskRow!);
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
  await user.click(await rows.findByRole('button', { name: /Fix re-attach redraw regression/, description: 'Main Server' }));
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

test('the instance switcher scopes the Task list, Home and the Project filter to one instance and puts it on screen', async () => {
  const { user, calls } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation C' });
  expect(await screen.findByText('Across all instances')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: /^Instance: All instances/ }));
  const reader = within(await screen.findByRole('dialog', { name: 'Instances' }));
  expect(reader.getByRole('button', { name: /^All instances(,|$)/ }).getAttribute('aria-current')).toBe('true');
  expect(within(reader.getByRole('region', { name: 'Main Server' })).getByRole('button', { name: /^Main Server, / })).toBeTruthy();
  expect(within(reader.getByRole('region', { name: 'Connected' })).getAllByRole('button').map((row) => row.getAttribute('aria-label')?.split(',')[0])).toEqual(['Workstation B', 'Workstation C']);
  await user.click(reader.getByRole('button', { name: /^Workstation B,/ }));
  // B's Tasks alone, without the instance's name on each row, and B on screen (its view mounts afresh): Home says so.
  await screen.findByRole('button', { name: /^Instance: Workstation B/ });
  const scoped = await taskRows();
  await waitFor(() => expect(scoped.queryAllByRole('button', { description: 'Main Server' })).toHaveLength(0));
  expect(scoped.queryAllByRole('button', { description: 'Workstation C' })).toHaveLength(0);
  const row = await scoped.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  expect(within(row).queryByText('Workstation B')).toBeNull();
  expect(within(row).queryByTitle('Workstation B')).toBeNull();
  const filter = screen.getByRole('button', { name: /^Instance: Workstation B/ });
  expect(filter.closest('header')).toBeTruthy();
  expect(within(filter).getByTitle('Workstation B').textContent).toBe('WB');
  expect(await screen.findByText('On Workstation B')).toBeTruthy();
  await waitFor(() => expect(calls.some((call) => call.owner === 'b' && call.path === '/api/meta')).toBe(true));
  // The Project filter offers B's Projects alone, and All projects keeps the scope.
  await user.click(screen.getByRole('button', { name: 'Project filter: all projects' }));
  const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
  expect(list.getAllByRole('group').map((group) => document.getElementById(group.getAttribute('aria-labelledby')!)?.textContent)).toEqual(['Workstation B']);
  await user.keyboard('{Escape}');
  // All instances brings every instance's Tasks back; B stays on screen.
  await user.click(screen.getByRole('button', { name: /^Instance: Workstation B/ }));
  await user.click(within(await screen.findByRole('dialog', { name: 'Instances' })).getByRole('button', { name: /^All instances,/ }));
  await scoped.findByRole('button', { name: /Doctor: add terminal line/, description: 'Main Server' });
  expect(scoped.getByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation C' })).toBeTruthy();
  expect(await screen.findByText('Across all instances')).toBeTruthy();
});

test('Alt+I opens the instance switcher, and Connect another instance opens Settings at Connected instances', async () => {
  const { user } = federated();
  await screen.findByRole('button', { name: /^Instance: All instances/ });
  await user.keyboard('{Alt>}i{/Alt}');
  const reader = within(await screen.findByRole('dialog', { name: 'Instances' }));
  await user.click(reader.getByRole('button', { name: 'Connect another instance' }));
  await screen.findByRole('heading', { name: 'Settings · Main Server', level: 1 });
  expect(await screen.findByRole('region', { name: 'Workstation B' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Connected instances' }).getAttribute('aria-current')).toBe('page');
});

test('the Project filter lists every machine’s Projects under its name and filters to one on that machine', async () => {
  const { user } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getByRole('button', { name: 'Project filter: all projects' }));
  const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
  const groups = list.getAllByRole('group');
  expect(groups.map((group) => group.getAttribute('aria-labelledby') && document.getElementById(group.getAttribute('aria-labelledby')!)?.textContent)).toEqual(['Main Server', 'Workstation B', 'Workstation C']);
  const b = within(list.getByRole('group', { name: 'Workstation B' }));
  const project = b.getAllByRole('option')[0];
  const name = project.querySelector('.truncate')?.textContent ?? '';
  await user.click(project);
  await waitFor(() => expect(screen.getByRole('button', { name: `Project filter: ${name}` })).toBeTruthy());
  // Only B's Tasks of that Project remain.
  await waitFor(() => expect(rows.queryAllByRole('button', { description: 'Main Server' })).toHaveLength(0));
  expect(rows.queryAllByRole('button', { description: 'Workstation C' })).toHaveLength(0);
  expect(rows.getAllByRole('button', { description: 'Workstation B' }).length).toBeGreaterThan(0);
});

test('Add project on another machine browses and adds there, and the Project shows under that machine', async () => {
  const { user, calls } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getAllByRole('button', { name: 'Add project' })[0]);
  const dialog = within(await screen.findByRole('dialog', { name: 'Choose a project folder' }));
  await user.click(dialog.getByRole('combobox', { name: 'Instance' }));
  await user.click(await screen.findByRole('option', { name: /^Workstation B/ }));
  const before = calls.length;
  const folders = within(await dialog.findByRole('listbox', { name: 'Folders' }));
  await user.dblClick(await folders.findByRole('option', { name: /^projects/ }));
  await user.click(await folders.findByRole('option', { name: /^archive/ }));
  await user.click(dialog.getByRole('button', { name: 'Continue' }));
  await user.type(dialog.getByRole('textbox', { name: 'Project name' }), 'Remote archive');
  await user.click(dialog.getByRole('button', { name: 'Add project' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
  const made = calls.slice(before).filter((call) => call.path.startsWith('/api/fs') || (call.path === '/api/projects' && call.method === 'POST'));
  expect(made.length).toBeGreaterThan(1);
  expect(made.every((call) => call.owner === 'b')).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Project filter: all projects' }));
  const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
  await waitFor(() => expect(within(list.getByRole('group', { name: 'Workstation B' })).getByRole('option', { name: /Remote archive/ })).toBeTruthy());
  expect(within(list.getByRole('group', { name: 'Main Server' })).queryByRole('option', { name: /Remote archive/ })).toBeNull();
});

test('the New task palette lists every machine and another machine’s Project opens its draft there', async () => {
  const { user } = federated();
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(document.getElementById('new-task')!);
  const palette = within(await screen.findByRole('dialog', { name: 'New thread' }));
  const b = within(palette.getByRole('group', { name: 'Workstation B' }));
  expect(palette.getByRole('group', { name: 'Main Server' })).toBeTruthy();
  await user.click(b.getAllByRole('option')[0]);
  await waitFor(() => expect(window.location.hash).toContain('connection=b'));
  expect(await screen.findByRole('heading', { name: 'New thread', level: 1 })).toBeTruthy();
  expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();
});

test('Settle on another machine’s row runs on that machine only, and its shelf counts it', async () => {
  const { user, calls } = federated();
  const nav = await taskRows();
  const row = await nav.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  const shelf = () => Number(/(\d+)/.exec(nav.getByRole('button', { name: /^Settled/ }).textContent ?? '')?.[1] ?? 0);
  // Each machine's list arrives on its own stream: count the shelf once every machine's rows are in.
  for (const machine of ['Main Server', 'Workstation C']) await nav.findByRole('button', { name: /Doctor: add terminal line/, description: machine });
  const settled = shelf();
  expect(nav.queryByRole('button', { name: /^Archived/ })).toBeNull();
  const settle = row.closest('[data-task-row]')!.querySelector<HTMLElement>('[data-settle]')!;
  // Its folder has unsaved git work: Settle asks first, and the forced settle goes to that machine.
  expect(settle.textContent).toBe('Settle…');
  expect(row.contains(settle)).toBe(false);
  expect(within(row).getByText('Finished')).toBeTruthy();
  await user.hover(row);
  await user.click(settle);
  const guard = within(await screen.findByRole('alertdialog', { name: 'Settle with unsaved work?' }));
  expect(guard.queryByRole('button', { name: 'Open Changes' })).toBeNull();
  await user.click(guard.getByRole('button', { name: 'Settle anyway' }));
  await waitFor(() => expect(shelf()).toBe(settled + 1));
  expect(calls.filter((call) => /\/(settle|stage)$/.test(call.path) || call.path.includes('/stage')).map((call) => call.owner)).toEqual(['b']);
  expect(window.location.hash).not.toContain('connection=b');
});

test('Archive and a clean Settle on another machine’s rows run on that machine', async () => {
  const { user, calls } = federated();
  const nav = await taskRows();
  const remote = () => calls.filter((call) => /\/(settle|archive)$/.test(call.path)).map((call) => `${call.owner} ${call.path.split('/').pop()}`);
  // A clean Thread settles at once.
  const clean = await nav.findByRole('button', { name: /Bump dependencies/, description: 'Workstation B' });
  await user.hover(clean);
  await user.click(clean.closest('[data-task-row]')!.querySelector<HTMLElement>('[data-settle]')!);
  await waitFor(() => expect(remote()).toEqual(['b settle']));
  // Known unsaved work: Archive asks first.
  const dirty = await nav.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  fireEvent.contextMenu(dirty);
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Archive' }));
  await user.click(within(await screen.findByRole('alertdialog', { name: 'Archive with unsaved work?' })).getByRole('button', { name: 'Archive anyway' }));
  await waitFor(() => expect(remote()).toEqual(['b settle', 'b archive']));
  // A clean Thread's Archive keeps today's dialog.
  const other = await nav.findByRole('button', { name: /Draft release notes/, description: 'Workstation B' });
  fireEvent.contextMenu(other);
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Archive' }));
  await user.click(within(await screen.findByRole('alertdialog', { name: 'Archive this thread?' })).getByRole('button', { name: 'Archive thread' }));
  await waitFor(() => expect(remote()).toEqual(['b settle', 'b archive', 'b archive']));
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
  expect((await taskRows()).getByRole('button', { name: /Explain how the CPR probe decides the glyph set/, description: 'Main Server' })).toBeTruthy();
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
  expect((await taskRows()).getByRole('button', { name: /Renamed before pairing/, description: 'Main Server' })).toBeTruthy();
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
  expect(screen.queryByText('Loading threads…')).toBeNull();
  expect(screen.queryByRole('main', { busy: true })).toBeNull();
  for (const machine of ['Main Server', 'Workstation B', 'Workstation C']) expect((await taskRows()).getByRole('button', { name: /Doctor: add terminal line/, description: machine })).toBeTruthy();
});

/** The Usage popover's summary value under `label`. */
function usageSummary(label: 'Estimated cost' | 'Cache saving' | 'Total tokens') {
  const name = within(screen.getByRole('region', { name: 'Tokens' })).getByText(label, { exact: true });
  return (label === 'Total tokens' ? name : name.parentElement!).nextElementSibling!.textContent;
}

async function openUsage(user: ReturnType<typeof userEvent.setup>) {
  const rows = await taskRows();
  await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(rows.getByRole('button', { name: 'Usage and AI', exact: true }));
  return within(await screen.findByRole('dialog', { name: 'Usage' }));
}

test('Usage sums every machine’s tokens, each priced by its own catalog, and filters to one machine', async () => {
  const { user, owners } = federated('', [record('b', 'Workstation B')]);
  // Only B prices gpt-6-luna: its cost counts in All instances and on B, never on this server.
  await act(async () => { await owners[1].fetch('/api/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token_prices: { copilot: { 'gpt-6-luna': { input: 1, output: 2 } } } }) }); });
  const popover = await openUsage(user);
  const machine = popover.getByRole('combobox', { name: 'Instance' });
  expect(machine.textContent).toBe('All instances');
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$3.77'));
  await waitFor(() => expect(usageSummary('Cache saving')).toBe('$5.02'));
  expect(usageSummary('Total tokens')).toBe('2.6M');
  expect(popover.queryByText(/not included/)).toBeNull();
  await user.click(machine);
  expect((await screen.findAllByRole('option')).map((option) => option.textContent)).toEqual(['All instances', 'Main Server', 'Workstation B']);
  await user.click(screen.getByRole('option', { name: 'Workstation B' }));
  await waitFor(() => expect(usageSummary('Estimated cost')).toBe('$1.90'));
  expect(usageSummary('Total tokens')).toBe('1.3M');
  expect(screen.getByRole('dialog', { name: 'Usage' })).toBeTruthy();
  await user.click(popover.getByRole('combobox', { name: 'Instance' }));
  await user.click(await screen.findByRole('option', { name: 'Main Server' }));
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
  await user.click(rows.getByRole('button', { name: 'Usage and AI', exact: true }));
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
  await chooseInstance(user, /^Main Server/);
  await screen.findByRole('heading', { name: 'Settings · Main Server', level: 1 });
  await user.click(await screen.findByRole('button', { name: 'Connected instances' }));
  const section = within(await screen.findByRole('region', { name: 'Workstation B' }));
  expect(section.getByRole('status').textContent).toBe(`Linked to a different Copilot account — ${reason}`);
  await user.click(section.getByRole('button', { name: 'Open its GitHub Copilot settings' }));
  await screen.findByRole('heading', { name: 'Settings · Workstation B', level: 1 });
  await waitFor(() => expect(document.activeElement?.id).toBe('account-copilot-title'));
});

test('project location belongs to the selected instance and older peers omit the setting', async () => {
  const current = { ...record('b', 'Workstation B'), capabilities: [...capabilities, 'project-location-v1'] };
  const { user, calls, owners } = federated('', [current, record('c', 'Workstation C')]);
  await user.click(await screen.findByRole('button', { name: 'Settings', exact: true }));
  await chooseInstance(user, /^Workstation B/);
  await user.click(await screen.findByRole('button', { name: 'UAM and access', exact: true }));
  const input = await screen.findByRole('textbox', { name: 'Default project location' });
  await user.type(input, '/home/user/projects');
  const before = calls.length;
  await user.click(screen.getByRole('button', { name: 'Browse', exact: true }));
  await screen.findByRole('option', { name: /^archive/ });
  await user.click(screen.getByRole('button', { name: 'New folder' }));
  await user.type(await screen.findByRole('textbox', { name: 'New folder name' }), 'team-projects');
  await user.click(screen.getByRole('button', { name: 'Create folder' }));
  await screen.findByRole('option', { name: 'team-projects' });
  await user.click(screen.getByRole('button', { name: 'Use this folder' }));
  expect(input).toHaveProperty('value', '/home/user/projects/team-projects');
  const fsCalls = calls.slice(before).filter(call => call.path.startsWith('/api/fs/dirs'));
  expect(fsCalls.some(call => call.method === 'POST')).toBe(true);
  expect(fsCalls.every(call => call.owner === 'b')).toBe(true);
  expect(screen.queryByRole('button', { name: 'Save project location' })).toBeNull();
  await waitFor(() => expect(input).toHaveProperty('readOnly', false));
  const starts = await Promise.all(owners.map(async owner => (await (await owner.fetch('/api/fs/dirs')).json()).path));
  expect(starts).toEqual(['/home/user', '/home/user/projects/team-projects', '/home/user']);
  expect(calls.filter(call => call.path === '/api/settings' && call.method === 'PATCH').map(call => call.owner)).toEqual(['b']);
  await user.click(screen.getAllByRole('button', { name: 'Add project' })[0]);
  const dialog = within(await screen.findByRole('dialog', { name: 'Choose a project folder' }));
  const folders = within(await dialog.findByRole('listbox', { name: 'Folders' }));
  await dialog.findByText('No folders here');
  expect(dialog.getByRole('button', { name: 'team-projects' }).getAttribute('aria-current')).toBe('location');
  expect(folders.queryByRole('option', { name: /^dotfiles/ })).toBeNull();
  await user.click(dialog.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Choose a project folder' })).toBeNull());
  await chooseInstance(user, /^Workstation C/);
  await screen.findByRole('heading', { name: 'Settings · Workstation C', level: 1 });
  expect(screen.queryByRole('textbox', { name: 'Default project location' })).toBeNull();
  await chooseInstance(user, /^Main Server/);
  expect(await screen.findByRole('textbox', { name: 'Default project location' })).toHaveProperty('value', '');
  await chooseInstance(user, /^Workstation B/);
  expect(await screen.findByRole('textbox', { name: 'Default project location' })).toHaveProperty('value', '/home/user/projects/team-projects');
});

test('Add project clears the previous instance folder and name and opens the new instance default', async () => {
  const { user, owners, calls } = federated();
  for (const [index, path] of [[1, '/home/user/projects'], [2, '/home/user/dotfiles']] as const) {
    await owners[index].fetch('/api/settings', { method: 'PATCH', body: JSON.stringify({ default_project_dir: path }) });
  }
  await (await taskRows()).findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' });
  await user.click(screen.getAllByRole('button', { name: 'Add project' })[0]);
  const dialog = within(await screen.findByRole('dialog', { name: 'Choose a project folder' }));
  await user.click(dialog.getByRole('combobox', { name: 'Instance' }));
  await user.click(await screen.findByRole('option', { name: /^Workstation B/ }));
  const folders = within(await dialog.findByRole('listbox', { name: 'Folders' }));
  await user.click(await folders.findByRole('option', { name: /^archive/ }));
  await user.click(dialog.getByRole('button', { name: 'Continue' }));
  expect(dialog.getByText('/home/user/projects/archive')).toBeTruthy();
  await user.type(dialog.getByRole('textbox', { name: 'Project name' }), 'Only on B');
  await user.click(dialog.getByRole('button', { name: 'Back' }));
  await user.click(dialog.getByRole('combobox', { name: 'Instance' }));
  await user.click(await screen.findByRole('option', { name: /^Workstation C/ }));
  await dialog.findByText('No folders here');
  expect(dialog.getByRole('textbox', { name: 'Folder path' })).toHaveProperty('value', '/home/user/dotfiles');
  expect(dialog.queryByText('/home/user/projects/archive')).toBeNull();
  await user.click(dialog.getByRole('button', { name: 'Continue' }));
  expect(dialog.getByRole('textbox', { name: 'Project name' })).toHaveProperty('value', '');
  expect(dialog.getByText('/home/user/dotfiles')).toBeTruthy();
  expect(calls.filter(call => call.path === '/api/projects' && call.method === 'POST')).toHaveLength(0);
});

test('Add project without folder capabilities accepts a typed path and creates only on that instance', async () => {
  const peer = { ...record('b', 'Core only'), capabilities: capabilities.filter(capability => capability !== 'files-v1') };
  const { user, calls } = federated('', [peer]);
  await (await taskRows()).findByRole('button', { name: /Doctor: add terminal line/, description: 'Core only' });
  await user.click(screen.getAllByRole('button', { name: 'Add project' })[0]);
  const dialog = within(await screen.findByRole('dialog', { name: 'Choose a project folder' }));
  await user.click(dialog.getByRole('combobox', { name: 'Instance' }));
  await user.click(await screen.findByRole('option', { name: /^Core only/ }));
  expect(dialog.queryByRole('listbox', { name: 'Folders' })).toBeNull();
  expect(dialog.getByText(/does not offer folder browsing/)).toBeTruthy();
  const path = dialog.getByRole('textbox', { name: 'Directory on the host' });
  await user.type(path, 'relative/path');
  expect(dialog.getByRole('button', { name: 'Continue' })).toHaveProperty('disabled', true);
  await user.clear(path);
  await user.type(path, '/home/user/projects/scratch');
  await user.click(dialog.getByRole('button', { name: 'Continue' }));
  expect(dialog.getByText('/home/user/projects/scratch')).toBeTruthy();
  await user.type(dialog.getByRole('textbox', { name: 'Project name' }), 'Core scratch');
  await user.click(dialog.getByRole('button', { name: 'Add project' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add a project' })).toBeNull());
  expect(calls.filter(call => call.path.startsWith('/api/fs') && call.owner === 'b')).toHaveLength(0);
  expect(calls.filter(call => call.path === '/api/projects' && call.method === 'POST').map(call => call.owner)).toEqual(['b']);
});
