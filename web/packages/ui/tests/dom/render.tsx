// Renders the real app the way main.tsx does under `vite dev ?mock`: the in-browser fake of the
// service (src/mock/install.ts) replaces fetch, EventSource, WebSocket and XMLHttpRequest first.
import { StrictMode } from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect } from 'vitest';
import { UamApp } from '@uam/ui';
import { install } from '../../src/mock/install';

export function renderApp(hash = '') {
  if (hash) history.replaceState(null, '', `/${hash}`);
  const mock = install();
  const user = userEvent.setup();
  const view = render(
    <StrictMode>
      <UamApp />
    </StrictMode>,
  );
  return { user, mock, ...view };
}

export type User = ReturnType<typeof renderApp>['user'];

/** The sidebar landmark once the first snapshot has listed the Projects. */
export async function sidebar() {
  const aside = within(await screen.findByRole('navigation', { name: 'Tasks' }));
  await aside.findByRole('button', { name: /Fix re-attach redraw regression/ });
  return aside;
}

/** Renders the app on one Task and waits until its conversation and composer are on screen. */
export async function openTask(id: string) {
  const rendered = renderApp(`#task=${id}`);
  await screen.findByRole('region', { name: 'Conversation' });
  await waitFor(() => expect(composer()?.disabled).toBe(false));
  return rendered;
}

/** The composer's message box (a combobox while the `/`, `$` or `@` picker is open). */
export const composer = () => document.getElementById('composer-text') as HTMLTextAreaElement;

/** The transcript's log region. */
export const log = () => within(screen.getByRole('log'));

/** Opens a menu from its trigger and returns the menu. */
export async function openMenu(user: User, trigger: string | RegExp) {
  const button = await screen.findByRole('button', { name: trigger });
  // A menu that just closed finishes its exit first; a press during it would not reopen it.
  await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
  await user.click(button);
  return within(await screen.findByRole('menu'));
}

/** Picks a radio item in a menu (radio items keep the menu open), then closes the menu with Escape. */
export async function choose(user: User, trigger: string | RegExp, item: string | RegExp) {
  const menu = await openMenu(user, trigger);
  await user.click(await menu.findByRole('menuitemradio', { name: item }));
  await user.keyboard('{Escape}');
  await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
}
