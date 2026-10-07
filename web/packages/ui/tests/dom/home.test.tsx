import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type SessionSummary } from '../../src/api';
import { Home } from '../../src/components/Home';
import * as data from '../../src/mock/data';
import { renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

describe('Home', () => {
  test('shows the six most recently updated tasks across projects, including settled tasks', async () => {
    const { projects, tasks } = data.seed();
    const sessions: SessionSummary[] = Array.from({ length: 8 }, (_, i) => ({
      ...tasks[0],
      id: `recent-${i + 1}`,
      name: `Recent task ${i + 1}`,
      project_id: projects[i % projects.length].id,
      stage: i === 6 ? 'settled' : 'active',
      state: 'completed',
      subagents_running: 0,
      pending: 0,
      created_at: `2026-09-0${8 - i}T00:00:00Z`,
      updated_at: `2026-10-0${i + 1}T00:00:00Z`,
    }));
    sessions.push(
      { ...sessions[0], id: 'archived', name: 'Archived task', stage: 'archived', updated_at: '2026-10-09T00:00:00Z' },
      { ...sessions[0], id: 'removed-project', name: 'Removed project task', project_id: 'removed', updated_at: '2026-10-10T00:00:00Z' },
    );
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(<Home projects={projects} sessions={sessions} hasNews={() => false} onNewTask={() => {}} onAddProject={() => {}} onSelect={onSelect} />);

    const recent = within(screen.getByRole('region', { name: 'Recent tasks' }));
    const rows = recent.getAllByRole('button');
    expect(rows).toHaveLength(6);
    for (const [index, row] of rows.entries()) {
      expect(within(row).getByText(`Recent task ${8 - index}`)).toBeTruthy();
    }
    expect(within(rows[0]).getByText('dotfiles')).toBeTruthy();
    expect(within(rows[1]).getByText('Settled')).toBeTruthy();
    expect(recent.queryByText('Archived task')).toBeNull();
    expect(recent.queryByText('Removed project task')).toBeNull();
    await user.click(rows[1]);
    expect(onSelect).toHaveBeenCalledWith('recent-7');
  });

  test('New task opens the project picker and composer without creating a task', async () => {
    const create = vi.spyOn(api, 'createSession');
    const { user } = renderApp();
    await sidebar();
    const main = within(screen.getByRole('main'));
    await waitFor(() => expect(main.getByRole('button', { name: 'New task' }).hasAttribute('disabled')).toBe(false));
    await user.click(main.getByRole('button', { name: 'New task' }));
    const palette = within(await screen.findByRole('dialog'));
    await user.click(palette.getByRole('option', { name: /notes-site/ }));
    expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();
    expect(screen.getByText('New task in notes-site')).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
    expect(window.location.hash).toBe('');
  });

  test('a recent task opens its conversation and updates the URL', async () => {
    const { user } = renderApp();
    const recent = within(await screen.findByRole('region', { name: 'Recent tasks' }));
    await user.click(recent.getByRole('button', { name: /Fix re-attach redraw regression/ }));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Fix re-attach redraw regression'));
    expect(window.location.hash).toBe('#task=t1');
    expect(await screen.findByRole('region', { name: 'Conversation' })).toBeTruthy();
  });

  test('a project without tasks offers New task and opens that project\'s composer', async () => {
    const state = data.seed();
    state.projects = [state.projects[2]];
    state.tasks = [];
    vi.spyOn(data, 'seed').mockReturnValue(state);
    let loadMeta!: (meta: typeof state.meta) => void;
    vi.spyOn(api, 'meta').mockReturnValue(new Promise((resolve) => { loadMeta = resolve; }));
    const create = vi.spyOn(api, 'createSession');
    const { user } = renderApp();
    expect(await screen.findByRole('heading', { name: 'What are you working on?' })).toBeTruthy();
    const main = within(screen.getByRole('main'));
    expect(main.queryByRole('region', { name: 'Recent tasks' })).toBeNull();
    expect(main.getByRole('button', { name: 'New task' }).hasAttribute('disabled')).toBe(true);
    await act(async () => loadMeta(state.meta));
    await waitFor(() => expect(main.getByRole('button', { name: 'New task' }).hasAttribute('disabled')).toBe(false));
    await user.click(main.getByRole('button', { name: 'New task' }));
    expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();
    expect(screen.getByText('New task in notes-site')).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
  });

  test('first login offers Add project and opens the existing project dialog', async () => {
    const state = data.seed();
    state.projects = [];
    state.tasks = [];
    vi.spyOn(data, 'seed').mockReturnValue(state);
    const { user } = renderApp();
    expect(await screen.findByRole('heading', { name: 'Start with a project' })).toBeTruthy();
    const main = within(screen.getByRole('main'));
    expect(main.queryByRole('button', { name: 'New task' })).toBeNull();
    expect(main.queryByRole('region', { name: 'Recent tasks' })).toBeNull();
    await user.click(main.getByRole('button', { name: 'Add project' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Add a project' }));
    expect(dialog.getByRole('textbox', { name: 'Directory on the host' })).toBeTruthy();
  });
});
