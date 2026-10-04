import { StrictMode } from 'react';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import { UamApp } from '@uam/ui';
import { install } from '../../src/mock/install';
import { createApiClient, type ConnectedInstance } from '../../src/api';

const capabilities = ['workload-grants-v1', 'expected-instance-v1', 'local-workload-v1', 'events-v1', 'files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'planner-v1', 'routines-v1', 'usage-v1', 'notices-v1'];
const record = (id: string, label: string): ConnectedInstance => ({ id, instance_id: `instance-${id}`, label, base_url: `https://${id}.example`, enabled: true, generation: 1, has_key: true, version: 'test', protocol_major: 1, capabilities, allow_private: false });

function federated(hash = '', records = [record('b', 'Workstation B'), record('c', 'Workstation C')]) {
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
  const view = render(<StrictMode><UamApp /></StrictMode>);
  return { user, calls, streams, owners, ...view, expireHome() { expired = true; act(() => document.dispatchEvent(new Event('visibilitychange'))); }, replaceRegistry(next: ConnectedInstance[]) { registry = next; act(() => document.dispatchEvent(new Event('visibilitychange'))); } };
}

async function taskRows() {
  return within(await screen.findByRole('list', { name: 'Tasks across instances' }));
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
  await user.selectOptions(screen.getByRole('combobox', { name: 'Active instance' }), 'b');
  await screen.findByRole('heading', { name: 'Settings', level: 1 });
  await user.click(screen.getByRole('button', { name: 'Agents', exact: true }));
  await waitFor(() => expect(calls.some(call => call.owner === 'b' && call.path.startsWith('/api/configuration'))).toBe(true));
  expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/configuration'))).toBe(false);
});

test('a stale remote deep link stays unavailable and never opens a colliding local task', async () => {
  const { calls } = federated('#task=t3&home=home-a&instance=instance-b&connection=b&generation=99');
  expect(await screen.findByText('This connection has changed. Open the task from its current instance.')).toBeTruthy();
  expect(screen.queryByRole('region', { name: 'Conversation' })).toBeNull();
  expect(calls.some(call => call.path.startsWith('/api/sessions/t3'))).toBe(false);
});

test('disabling the selected owner closes its streams and shows an explicit unavailable view', async () => {
  const records = [record('b', 'Workstation B')];
  const { user, replaceRegistry, streams } = federated('', records);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation B' }));
  await screen.findByRole('region', { name: 'Conversation' });
  replaceRegistry([{ ...records[0], enabled: false, generation: 2 }]);
  await screen.findByText(/This connection is disabled|This connection is unavailable/);
  await waitFor(() => expect(streams.filter(entry => entry.owner === 'b').every(entry => entry.stream.readyState === 2)).toBe(true));
  expect(screen.queryByRole('textbox', { name: 'Message' })).toBeNull();
});

test('core-only peers render their task and name unavailable optional features in Settings', async () => {
  const { user } = federated('', [{ ...record('b', 'Core only'), capabilities: capabilities.slice(0, 4) }]);
  const rows = await taskRows();
  await user.click(await rows.findByRole('button', { name: /Doctor: add terminal line/, description: 'Core only' }));
  await screen.findByRole('region', { name: 'Conversation' });
  expect(screen.queryByRole('combobox', { name: 'Active instance' })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Settings', exact: true }));
  expect(screen.getByText(/Unavailable on this instance:.*files.*terminal.*configuration/)).toBeTruthy();
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
  await waitFor(() => expect(within(screen.getByRole('list', { name: 'Tasks across instances' })).queryByRole('button', { name: /Doctor: add terminal line/, description: 'Workstation C' })).toBeNull());
  expect(before.every(entry => entry.stream.readyState !== 2)).toBe(true);
  expect(streams.filter(entry => entry.owner === 'b' && entry.stream.readyState !== 2)).toEqual(before);
});

test('escaped ownership fields cannot briefly open a same-ID local task before validation', async () => {
  const { calls, streams } = federated('#task=t3&%68ome=home-a&%69nstance=instance-b&%63onnection=b&%67eneration=99');
  await screen.findByText('This connection has changed. Open the task from its current instance.');
  expect(calls.some(call => call.owner === '' && call.path.startsWith('/api/sessions/t3'))).toBe(false);
  expect(streams.some(entry => entry.owner === '' && new URL(entry.path, 'https://home.test').searchParams.get('session') === 't3')).toBe(false);
});


test('home auth loss clears connected sources even while an invalid route has unmounted the active app', async () => {
  const { expireHome, streams } = federated('#task=t3&home=home-a&instance=instance-b&connection=b&generation=99');
  await screen.findByText('This connection has changed. Open the task from its current instance.');
  expireHome();
  await screen.findByRole('heading', { name: 'Sign in to UAM' });
  expect(screen.queryByRole('list', { name: 'Tasks across instances' })).toBeNull();
  await waitFor(() => expect(streams.every(entry => entry.stream.readyState === 2)).toBe(true));
});


test('combined badge preserves the home unread-failure count beside remote permission requests', async () => {
  localStorage.setItem('uam.viewedSince', JSON.stringify('2000-01-01T00:00:00Z'));
  const { user } = federated('?planner=unset', [record('b', 'Workstation B')]);
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
