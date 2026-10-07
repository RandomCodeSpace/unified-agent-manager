import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { api } from '../../src/api';
import { openMenu, renderApp } from './render';

describe('a finished approved epic', () => {
  test('a card added under it waits in Plans to approve, and the epic offers Approve and run again', async () => {
    const { user } = renderApp('#planner=p3');
    const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    await tree.findByRole('treeitem', { name: '#32 Accessible post template, Doing' });
    // #32 finishes under its approval, then the owner adds a follow-up, which is a proposal there.
    await act(() => api.planner.status('cp3-35', 'done', 'Shipped.'));
    await act(() => api.planner.approve('cp3-32', { provider: 'copilot', model: 'gpt-5-mini', effort: '', context_size: 'default', mode: 'safe', parallel: 2, items: [] }));
    await waitFor(() => expect(tree.getByRole('treeitem', { name: /^#32 Accessible post template, Done/ })).toBeTruthy());
    await act(() => api.planner.create({ project_id: 'p3', kind: 'subtask', parent_id: 'cp3-33', title: 'Check alt text in the RSS feed' }));
    await waitFor(() => expect(tree.getByRole('treeitem', { name: /^#32 / }).textContent).toContain('1 to approve'));
    const menu = await openMenu(user, 'Actions for #32');
    expect(menu.getByRole('menuitem', { name: 'Approve and run…' })).toBeTruthy();
    await user.keyboard('{Escape}');
    await user.click(screen.getByRole('button', { name: 'Inbox, 1 pending' }));
    const plans = within(await screen.findByRole('region', { name: 'Plans to approve' }));
    expect(plans.getByRole('button', { name: '#32 Accessible post template' })).toBeTruthy();
    expect(plans.getByText('1 to approve')).toBeTruthy();
  });
});
