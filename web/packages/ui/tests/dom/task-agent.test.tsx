import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { choose, composer, openMenu, renderApp, sidebar } from './render';

describe('custom agent', () => {
  test('a new task runs as the chosen agent, which changes only between turns and back to the default', async () => {
    const create = vi.spyOn(api, 'createSession');
    const settings = vi.spyOn(api, 'settings');
    const { user } = renderApp();
    await sidebar();
    const main = within(screen.getByRole('main'));
    await waitFor(() => expect(main.getByRole('button', { name: 'New task' }).hasAttribute('disabled')).toBe(false));
    await user.click(main.getByRole('button', { name: 'New task' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /notes-site/ }));
    expect(await screen.findByRole('textbox', { name: 'Message' })).toBeTruthy();

    // The default agent unless picked; the menu lists the Project's agents with their descriptions.
    const menu = await openMenu(user, 'Agent: Default agent');
    expect(await menu.findByRole('menuitemradio', { name: /Reviewer.*Reviews a change before it is committed/ })).toBeTruthy();
    expect(menu.getByRole('menuitemradio', { name: /Default/ }).getAttribute('aria-checked')).toBe('true');
    await user.keyboard('{Escape}');
    await choose(user, 'Agent: Default agent', /Reviewer/);
    expect(await screen.findByRole('button', { name: 'Agent: Reviewer' })).toBeTruthy();
    await user.type(composer(), 'Review the parser change');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0].agent).toBe('reviewer');

    // The created Task shows its agent; while its turn runs, the agent cannot change.
    const running = await screen.findByRole('button', { name: /^Agent: reviewer\. The agent changes between turns\./ });
    expect(running.getAttribute('aria-disabled')).toBe('true');
    expect(await screen.findByRole('button', { name: 'Agent: reviewer' }, { timeout: 10000 })).toBeTruthy();
    await choose(user, 'Agent: reviewer', /Default/);
    await waitFor(() => expect(settings).toHaveBeenCalledWith(expect.any(String), { agent: '' }));
    expect(await screen.findByRole('button', { name: 'Agent: Default agent' })).toBeTruthy();
  });

  test('a task whose provider cannot select agents shows no agent picker', async () => {
    renderApp('#task=t3');
    await screen.findByRole('region', { name: 'Conversation' });
    await waitFor(() => expect(composer()?.disabled).toBe(false));
    expect(screen.queryByRole('button', { name: /^Agent:/ })).toBeNull();
  });
});
