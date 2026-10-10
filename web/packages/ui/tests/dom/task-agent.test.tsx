import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { composer, renderApp, sidebar, type User } from './render';

/** Opens Tools on its Agent tab and returns the panel. */
async function agentTab(user: User) {
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull());
  await user.click(await screen.findByRole('button', { name: /^Tools/ }));
  const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
  const tab = panel.getByRole('tab', { name: 'Agent' });
  if (tab.getAttribute('aria-selected') !== 'true') await user.click(tab);
  return panel;
}

async function close(user: User) {
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Tools' })).toBeNull());
}

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

    // The default agent unless picked; the tab lists the Project's agents with their descriptions.
    let panel = await agentTab(user);
    expect(await panel.findByRole('radio', { name: /Reviewer.*Reviews a change before it is committed/ })).toBeTruthy();
    expect(panel.getByRole('radio', { name: /Default/ }).getAttribute('aria-checked')).toBe('true');
    await user.click(panel.getByRole('radio', { name: /Reviewer/ }));
    await waitFor(() => expect(panel.getByRole('radio', { name: /Reviewer/ }).getAttribute('aria-checked')).toBe('true'));
    await close(user);
    await user.type(composer(), 'Review the parser change');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0].agent).toBe('reviewer');

    // While the created Task's turn runs, the agent cannot change; between turns it can, back to the default.
    // Creating the Task replaces the new-task composer, which closes a panel opened meanwhile: open until it stays.
    const shown = async (check: (p: ReturnType<typeof within>) => void) => waitFor(async () => {
      if (!screen.queryByRole('dialog', { name: 'Tools' })) await user.click(screen.getByRole('button', { name: /^Tools/ }));
      const p = within(screen.getByRole('dialog', { name: 'Tools' }));
      check(p);
    }, { timeout: 10000 });
    await shown((p) => expect(p.getByText('The agent changes between turns.', { selector: 'p' })).toBeTruthy());
    await close(user);
    await shown((p) => expect(p.queryByText('The agent changes between turns.', { selector: 'p' })).toBeNull());
    panel = within(screen.getByRole('dialog', { name: 'Tools' }));
    expect((await panel.findByRole('radio', { name: /Reviewer/ })).getAttribute('aria-checked')).toBe('true');
    await user.click(panel.getByRole('radio', { name: /Default/ }));
    await waitFor(() => expect(settings).toHaveBeenCalledWith(expect.any(String), { agent: '' }));
    await waitFor(() => expect(panel.getByRole('radio', { name: /Default/ }).getAttribute('aria-checked')).toBe('true'));
  });

  test('a task whose provider cannot select agents has no Agent tab, and one with no tools has no Tools button', async () => {
    const { user } = renderApp('#task=t2');
    await screen.findByRole('region', { name: 'Conversation' });
    // t2 offers none of the tools: no context report, native usage or custom agents.
    expect(screen.queryByRole('button', { name: /^Tools/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Agent:/ })).toBeNull();
    history.pushState(null, '', '/#task=t7');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    // t7 reports its context but cannot select agents: Tools shows Context alone.
    await user.click(await screen.findByRole('button', { name: /^Tools\. Context .*%/ }));
    const panel = within(await screen.findByRole('dialog', { name: 'Tools' }));
    expect(panel.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Context']);
  });
});
