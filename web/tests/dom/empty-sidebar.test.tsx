import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import * as data from '../../src/mock/data';
import { renderApp } from './render';

const seed = data.seed;

afterEach(() => vi.restoreAllMocks());

function workspace(projects = true, tasks = false, url = '') {
  const state = seed();
  if (!projects) state.projects = [];
  if (!tasks) state.tasks = [];
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const rendered = renderApp(url);
  const Source = window.EventSource;
  const streams: EventSource[] = [];
  window.EventSource = class extends Source {
    constructor(url: string | URL, options?: EventSourceInit) {
      super(url, options);
      streams.push(this);
    }
  };
  return { ...rendered, state, streams };
}

const rail = () => screen.findByRole('navigation', { name: 'Sidebar' });
const taskList = () => screen.findByRole('navigation', { name: 'Tasks' });

describe('empty-workspace sidebar', () => {
  test.each([false, true])('starts collapsed when projects exist: %s, without overwriting the preference', async (projects) => {
    localStorage.setItem('uam.sidebar', 'true');
    workspace(projects);
    const nav = within(await rail());
    expect(nav.getByRole('status').textContent).toContain('Connected');
    expect(screen.queryByRole('navigation', { name: 'Tasks' })).toBeNull();
    expect(localStorage.getItem('uam.sidebar')).toBe('true');
  });

  test('waits for a snapshot before treating the workspace as empty', async () => {
    workspace(false, false, '?slow=300');
    expect(await taskList()).toBeTruthy();
    expect(screen.queryByRole('navigation', { name: 'Sidebar' })).toBeNull();
    expect(await rail()).toBeTruthy();
    expect(localStorage.getItem('uam.sidebar')).toBeNull();
  });

  test('a manual expansion survives another empty snapshot, and the toggle still collapses it', async () => {
    const { user, state, streams } = workspace();
    await user.click(within(await rail()).getByRole('button', { name: /^Show sidebar/ }));
    await taskList();
    act(() => streams.at(-1)!.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify({ seq: 100, projects: state.projects, sessions: [], settings: state.settings, session: null }) })));
    expect(await taskList()).toBeTruthy();
    expect(screen.queryByRole('navigation', { name: 'Sidebar' })).toBeNull();
    await user.click(within(await taskList()).getByRole('button', { name: /^Hide sidebar/ }));
    expect(await rail()).toBeTruthy();
    expect(localStorage.getItem('uam.sidebar')).toBe('false');
  });

  test.each([false, true])('a populated Home respects the saved open preference: %s', async (open) => {
    localStorage.setItem('uam.sidebar', JSON.stringify(open));
    workspace(true, true);
    const nav = within(await (open ? taskList() : rail()));
    await waitFor(() => expect(nav.getByRole('status').textContent).toContain('Connected'));
    expect(window.location.hash).toBe('');
    expect(!!screen.queryByRole('navigation', { name: 'Tasks' })).toBe(open);
  });

  test('the rail opens a draft while empty, then creating the first task restores the saved preference', async () => {
    localStorage.setItem('uam.sidebar', 'true');
    const { user } = workspace();
    await user.click(within(await rail()).getByRole('button', { name: 'New task' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /notes-site/ }));
    expect(await screen.findByRole('heading', { name: 'New task' })).toBeTruthy();
    expect(await rail()).toBeTruthy();
    await act(async () => { await api.createSession({ project_id: 'p3', provider: 'copilot', request_id: 'empty-sidebar-first-task' }); });
    expect(await taskList()).toBeTruthy();
    expect(localStorage.getItem('uam.sidebar')).toBe('true');
  });

  test('removing the final task collapses the sidebar without changing the preference', async () => {
    localStorage.setItem('uam.sidebar', 'true');
    const { state } = workspace(true, true);
    const archived = state.tasks.find((task) => task.stage === 'archived')!;
    state.tasks = [archived];
    const nav = within(await taskList());
    await waitFor(() => expect(nav.getByRole('status').textContent).toContain('Connected'));
    await act(async () => { await api.deleteSession(archived.id); });
    expect(await rail()).toBeTruthy();
    expect(localStorage.getItem('uam.sidebar')).toBe('true');
  });

  test('signing in again reapplies the empty default after a manual expansion', async () => {
    const { user } = workspace(false);
    await user.click(within(await rail()).getByRole('button', { name: /^Show sidebar/ }));
    await taskList();
    vi.spyOn(window, 'fetch').mockResolvedValueOnce(new Response('{}', { status: 401 }));
    await act(async () => { await expect(api.meta()).rejects.toThrow(); });
    // The existing service mock has no login route; only its successful auth result is needed here.
    vi.spyOn(api, 'login').mockResolvedValueOnce(undefined);
    await user.type(await screen.findByLabelText('Access token'), 'mock-token');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Sign in to UAM' })).toBeNull());
    expect(await rail()).toBeTruthy();
    expect(localStorage.getItem('uam.sidebar')).toBe('true');
  });
});
