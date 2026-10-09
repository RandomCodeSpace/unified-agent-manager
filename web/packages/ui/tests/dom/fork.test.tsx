import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api, ApiError } from '../../src/api';
import { saveDensity } from '../../src/lib/density';
import * as data from '../../src/mock/data';
import { composer, openTask } from './render';

function fixture() {
  const state = data.seed();
  const source = state.tasks.find(t => t.id === 't3')!;
  source.capabilities.fork = true;
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const forked = () => {
    const target = { ...source, id: 'native-branch', name: 'Native branch', fork_of: source.id, stage: 'active' as const, open: false, state: 'closed' as const, pending: 0, interactions: [], queue: [], items: source.items.map(it => ({ ...it })) };
    state.tasks.push(target);
    return target;
  };
  return { state, source, forked };
}

describe('native task branching', () => {
  test('reply foot opens an undimmed picker; cancel creates nothing and returns focus', async () => {
    fixture();
    const fork = vi.spyOn(api, 'fork');
    const { user, mock } = await openTask('t3');
    const trigger = screen.getAllByRole('button', { name: 'Turn actions' })[0];
    await user.click(trigger);
    await user.click(await screen.findByRole('menuitem', { name: 'Branch from here' }));
    const picker = await screen.findByRole('dialog', { name: 'Branch from here' });
    expect(picker.getAttribute('aria-modal')).not.toBe('true');
    expect(screen.getByRole('region', { name: 'Conversation' }).hasAttribute('inert')).toBe(false);
    expect(within(picker).getByText(/same project and current files/)).toBeTruthy();
    await user.click(within(picker).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Branch from here' })).toBeNull());
    expect(fork).not.toHaveBeenCalled();
    expect(mock.received.filter(r => r.route === 'prompt')).toHaveLength(0);
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  test.each(['compact', 'detailed'] as const)('%s branches the selected prefix once and keeps the source draft', async density => {
    const { source, forked } = fixture();
    saveDensity(density);
    const fork = vi.spyOn(api, 'fork').mockImplementation(async () => forked());
    const { user, mock } = await openTask(source.id);
    await user.type(composer(), 'Draft stays on the source');
    const trigger = screen.getAllByRole('button', { name: 'Turn actions' })[0];
    await user.click(trigger);
    await user.click(await screen.findByRole('menuitem', { name: 'Branch from here' }));
    const picker = within(await screen.findByRole('dialog', { name: 'Branch from here' }));
    await user.click(picker.getByRole('button', { name: 'Branch task' }));
    await screen.findByRole('heading', { level: 1, name: 'Native branch' });
    expect(fork).toHaveBeenCalledTimes(1);
    const [sourceId, request] = fork.mock.calls[0];
    expect(sourceId).toBe(source.id);
    expect(source.items.some(it => it.kind === 'user' && !it.delivery && it.id === request.user_item_id)).toBe(true);
    expect(request.model).toBe(source.model);
    expect(request.request_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(mock.received.filter(r => r.route === 'prompt')).toHaveLength(0);
    await user.click(await screen.findByRole('button', { name: 'the source task' }));
    await waitFor(() => expect(composer().value).toBe('Draft stays on the source'));
  });

  test('Default is an explicit target choice and a transport retry keeps its request identity', async () => {
    const { forked } = fixture();
    const fork = vi.spyOn(api, 'fork').mockRejectedValueOnce(new Error('Connection lost')).mockImplementationOnce(async () => forked());
    const { user } = await openTask('t3');
    await user.click(screen.getAllByRole('button', { name: 'Turn actions' })[0]);
    await user.click(await screen.findByRole('menuitem', { name: 'Branch from here' }));
    let picker = within(await screen.findByRole('dialog', { name: 'Branch from here' }));
    await user.click(picker.getByRole('combobox', { name: 'Branch model' }));
    await user.click(await screen.findByRole('option', { name: 'Default', exact: true }));
    await user.click(picker.getByRole('button', { name: 'Branch task' }));
    await picker.findByRole('alert');
    picker = within(screen.getByRole('dialog', { name: 'Branch from here' }));
    await user.click(picker.getByRole('button', { name: 'Branch task' }));
    await screen.findByRole('heading', { level: 1, name: 'Native branch' });
    expect(fork).toHaveBeenCalledTimes(2);
    expect(fork.mock.calls[0][1]).toEqual(fork.mock.calls[1][1]);
    expect(fork.mock.calls[0][1].model).toBe('');
  });

  test('an unresolved native result disables blind refork and preserves the request', async () => {
    fixture();
    const fork = vi.spyOn(api, 'fork').mockRejectedValue(new ApiError(409, 'Branch outcome is unknown', { code: 'fork_uncertain' }));
    const { user } = await openTask('t3');
    await user.click(screen.getAllByRole('button', { name: 'Turn actions' })[0]);
    await user.click(await screen.findByRole('menuitem', { name: 'Branch from here' }));
    const picker = within(await screen.findByRole('dialog', { name: 'Branch from here' }));
    await user.click(picker.getByRole('button', { name: 'Branch task' }));
    await picker.findByText('Branch outcome is unknown');
    const confirm = picker.getByRole('button', { name: 'Branch task' }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    await user.click(confirm);
    expect(fork).toHaveBeenCalledTimes(1);
    await user.click(picker.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Branch from here' })).toBeNull());
  });

  test('Dismiss clears only the unresolved request for this reply and model, never reforking', async () => {
    fixture();
    const fork = vi.spyOn(api, 'fork').mockRejectedValue(new ApiError(409, 'Branch outcome is unknown', { code: 'fork_uncertain' }));
    const dismiss = vi.spyOn(api, 'dismissFork').mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValueOnce(undefined);
    const { user } = await openTask('t3');
    await user.click(screen.getAllByRole('button', { name: 'Turn actions' })[0]);
    await user.click(await screen.findByRole('menuitem', { name: 'Branch from here' }));
    const picker = within(await screen.findByRole('dialog', { name: 'Branch from here' }));
    expect(picker.queryByRole('button', { name: 'Dismiss' })).toBeNull();
    await user.click(picker.getByRole('button', { name: 'Branch task' }));
    await picker.findByText(/A Copilot session may still exist/);
    await user.click(picker.getByRole('button', { name: 'Dismiss' }));
    await picker.findByText('Connection lost');
    await user.click(picker.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Branch from here' })).toBeNull());
    expect(dismiss).toHaveBeenCalledTimes(2);
    const request = fork.mock.calls[0][1];
    expect(dismiss.mock.calls[1]).toEqual(['t3', { user_item_id: request.user_item_id, model: request.model }]);
    expect(fork).toHaveBeenCalledTimes(1);
  });

  test('a saved result that could not be added offers Add the existing branch, then Dismiss', async () => {
    const { forked } = fixture();
    const saved = new ApiError(404, 'could not add the existing branch: unknown project', { code: 'fork_saved' });
    const fork = vi.spyOn(api, 'fork').mockRejectedValueOnce(saved).mockRejectedValueOnce(saved).mockImplementationOnce(async () => forked());
    const dismiss = vi.spyOn(api, 'dismissFork');
    const { user } = await openTask('t3');
    await user.click(screen.getAllByRole('button', { name: 'Turn actions' })[0]);
    await user.click(await screen.findByRole('menuitem', { name: 'Branch from here' }));
    const picker = within(await screen.findByRole('dialog', { name: 'Branch from here' }));
    await user.click(picker.getByRole('button', { name: 'Branch task' }));
    await picker.findByText(/The Copilot session for this branch exists/);
    expect(picker.getByRole('button', { name: 'Dismiss' })).toBeTruthy();
    await user.click(picker.getByRole('button', { name: 'Add the existing branch' }));
    await picker.findByText(/could not add the existing branch/);
    await user.click(picker.getByRole('button', { name: 'Add the existing branch' }));
    await screen.findByRole('heading', { level: 1, name: 'Native branch' });
    expect(fork).toHaveBeenCalledTimes(3);
    expect(new Set(fork.mock.calls.map(call => JSON.stringify(call[1]))).size).toBe(1);
    expect(dismiss).not.toHaveBeenCalled();
  });
});
