import { screen, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { openMenu, renderApp } from './render';

afterEach(() => vi.restoreAllMocks());

test('the Thread header has no GitLab button or menu item; GitLab opens from the conversation', async () => {
  const read = vi.spyOn(api, 'gitlabPipeline').mockResolvedValue({ supported: true, auto_investigate: false, can_investigate: false });
  const { user } = renderApp('?gitidle#task=t3');
  const header = (await screen.findByRole('heading', { level: 1 })).closest('header')!;
  expect(within(header).queryByRole('button', { name: /GitLab/ })).toBeNull();
  const menu = await openMenu(user, 'Thread actions');
  expect(menu.queryByRole('menuitem', { name: /GitLab/ })).toBeNull();
  expect(read).not.toHaveBeenCalled();
});

test('a narrow header folds no GitLab item into the menu either', async () => {
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return this.tagName === 'HEADER' ? 520 : 0;
  });
  const { user } = renderApp('?gitidle#task=t3');
  await screen.findByRole('region', { name: 'Conversation' });
  const menu = await openMenu(user, 'Thread actions');
  expect(menu.getByRole('menuitem', { name: 'Rename' })).toBeTruthy();
  expect(menu.queryByRole('menuitem', { name: /GitLab/ })).toBeNull();
});
