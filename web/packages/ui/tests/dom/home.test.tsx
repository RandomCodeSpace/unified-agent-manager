import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type SessionSummary } from '../../src/api';
import { Home } from '../../src/components/Home';
import * as data from '../../src/mock/data';
import { renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

describe('Home', () => {
  test('lists only what the sidebar cannot: Threads that need you and Thread budgets, never a recent list', async () => {
    const { projects, tasks } = data.seed();
    const base = { ...tasks[0], state: 'completed', subagents_running: 0, pending: 0, budget: undefined } satisfies SessionSummary;
    const sessions: SessionSummary[] = [
      { ...base, id: 'quiet', name: 'Quiet thread', updated_at: '2026-10-09T00:00:00Z' },
      { ...base, id: 'asks', name: 'Asks a question', state: 'awaiting_answer', pending: 1, updated_at: '2026-10-08T00:00:00Z' },
      { ...base, id: 'settled-asks', name: 'Settled asker', stage: 'settled', state: 'awaiting_answer', pending: 1 },
      { ...base, id: 'spends', name: 'Spends credits', budget: { credits: 25_000, spent: 12_345 } },
      { ...base, id: 'reached', name: 'Out of budget', budget: { credits: 20, spent: 20.4, reached: true } },
    ];
    const onSelect = vi.fn();
    const user = userEvent.setup();
    render(<Home projects={projects} sessions={sessions} hasNews={() => false} onNewTask={() => {}} onAddProject={() => {}} onSelect={onSelect} />);

    expect(screen.queryByRole('region', { name: 'Recent threads' })).toBeNull();
    expect(screen.queryByText('Quiet thread')).toBeNull();
    const needs = within(screen.getByRole('region', { name: 'Needs you' }));
    expect(needs.getAllByRole('button')).toHaveLength(1);
    expect(needs.queryByText('Settled asker')).toBeNull();
    await user.click(needs.getByRole('button', { name: /Asks a question/ }));
    expect(onSelect).toHaveBeenCalledWith('asks');

    const budgets = within(screen.getByRole('region', { name: 'Budgets' }));
    expect(within(budgets.getByRole('button', { name: /Out of budget/ })).getByText('Budget reached')).toBeTruthy();
    // Credits use the one number format: compact from 10K, the exact value in the title.
    const spent = within(budgets.getByRole('button', { name: /Spends credits/ })).getByText('12.3K');
    expect(spent.getAttribute('title')).toBe('12,345');
  });

  test('with connected instances, a Thread that needs you on another instance shows on Home', () => {
    const { projects, tasks } = data.seed();
    const asks = { ...tasks[0], id: 'far', name: 'Waiting on the peer', state: 'awaiting_permission', pending: 1 } satisfies SessionSummary;
    const machine = (id: string, sessions: SessionSummary[]) => ({ id, short: id, label: id, hasNews: () => false, state: { projects, sessions } });
    render(<Home projects={projects} sessions={[]} hasNews={() => false} onNewTask={() => {}} onAddProject={() => {}} onSelect={() => {}}
      machines={[machine('here', []), machine('peer', [asks])] as never} />);
    const needs = within(screen.getByRole('region', { name: 'Needs you' }));
    expect(needs.getByRole('button', { name: /Waiting on the peer.*peer/ })).toBeTruthy();
  });

  test('New task opens the project picker and composer without creating a task', async () => {
    const create = vi.spyOn(api, 'createSession');
    const { user } = renderApp();
    await sidebar();
    const main = within(screen.getByRole('main'));
    await waitFor(() => expect(main.getByRole('button', { name: 'New thread' }).hasAttribute('disabled')).toBe(false));
    await user.click(main.getByRole('button', { name: 'New thread' }));
    const palette = within(await screen.findByRole('dialog'));
    await user.click(palette.getByRole('option', { name: /notes-site/ }));
    expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();
    expect(screen.getByText('New thread in notes-site')).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
    expect(window.location.hash).toBe('');
  });

  test('a Thread that needs you opens its conversation and updates the URL', async () => {
    const { user } = renderApp();
    const needs = within(await screen.findByRole('region', { name: 'Needs you' }));
    await user.click(needs.getByRole('button', { name: /Tidy zsh startup/ }));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Tidy zsh startup'));
    expect(window.location.hash).toMatch(/^#task=/);
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
    expect(main.queryByRole('region', { name: 'Needs you' })).toBeNull();
    expect(main.getByRole('button', { name: 'New thread' }).hasAttribute('disabled')).toBe(true);
    await act(async () => loadMeta(state.meta));
    await waitFor(() => expect(main.getByRole('button', { name: 'New thread' }).hasAttribute('disabled')).toBe(false));
    await user.click(main.getByRole('button', { name: 'New thread' }));
    expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();
    expect(screen.getByText('New thread in notes-site')).toBeTruthy();
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
    expect(main.queryByRole('button', { name: 'New thread' })).toBeNull();
    expect(main.queryByRole('region', { name: 'Needs you' })).toBeNull();
    await user.click(main.getByRole('button', { name: 'Add project' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Choose a project folder' }));
    expect(await dialog.findByRole('textbox', { name: 'Folder path' })).toBeTruthy();
  });
});
