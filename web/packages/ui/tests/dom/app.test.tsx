import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

const header = () => screen.getByRole('heading', { level: 1 });

describe('app shell', () => {
  test('lists the projects and tasks, with the Home launcher in the main pane', async () => {
    renderApp();
    const side = await sidebar();
    expect(await screen.findByRole('heading', { name: 'What are you working on?' })).toBeTruthy();
    expect(side.getByRole('button', { name: /Fix re-attach redraw regression/ })).toBeTruthy();
    expect(side.getByRole('button', { name: /Archived 2/ })).toBeTruthy();
    expect(side.getByRole('status').textContent).toContain('Connected');
    // Seven tasks wait for the user: two for permission, five for an answer.
    expect(document.title).toBe('(7) UAM');
  });

  test('a link to an unknown view lands on the home view', async () => {
    renderApp('#unknown=p1');
    await sidebar();
    expect(await screen.findByRole('heading', { name: 'What are you working on?' })).toBeTruthy();
    await waitFor(() => expect(window.location.hash).toBe(''));
  });

  test('opening a task from the sidebar shows it and keeps it in the URL', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: /unified-agent-manager.*Doctor: add terminal line/ }));
    await waitFor(() => expect(header().textContent).toBe('Doctor: add terminal line'));
    expect(window.location.hash).toBe('#task=t3');
    const log = within(screen.getByRole('log'));
    expect(await log.findByText(/Does it handle/)).toBeTruthy();
    // The composer takes focus on a fine pointer.
    await waitFor(() => expect(document.activeElement?.id).toBe('composer-text'));
  });

  test('a #task= link selects that task, and a hash change moves to another', async () => {
    renderApp('#task=t4');
    await waitFor(() => expect(header().textContent).toBe('Bump GitHub Actions pins'));
    history.pushState(null, '', '/#task=t10');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(header().textContent).toBe('Bump dependencies'));
  });

  test('a stream the service refuses shows the reconnecting banner once it lasts', async () => {
    renderApp('#task=gone');
    expect(await screen.findByLabelText('Loading conversation')).toBeTruthy();
    await screen.findAllByText('Connection lost, reconnecting. Work continues on the server.');
    const statuses = within(screen.getByRole('main')).getAllByRole('status');
    expect(statuses.some((s) => s.textContent?.includes('Connection lost, reconnecting.'))).toBe(true);
  });

  test('Ctrl+B collapses the sidebar to its rail and brings it back, remembered per browser', async () => {
    const { user } = renderApp();
    await sidebar();
    const list = document.querySelector('aside > [aria-hidden]')!;
    await user.keyboard('{Control>}b{/Control}');
    await waitFor(() => expect(list.getAttribute('aria-hidden')).toBe('true'));
    expect(localStorage.getItem('uam.sidebar')).toBe('false');
    await user.click(within(screen.getByRole('navigation', { name: 'Sidebar' })).getByRole('button', { name: /^Show sidebar/ }));
    await waitFor(() => expect(list.getAttribute('aria-hidden')).toBe('false'));
    expect(localStorage.getItem('uam.sidebar')).toBe('true');
    expect(screen.queryByRole('navigation', { name: 'Sidebar' })).toBeNull();
  });

  test('the gear opens Settings in the pane, kept in the URL as #settings', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Settings' }));
    await waitFor(() => expect(header().textContent).toBe('Settings'));
    expect(window.location.hash).toBe('#settings');
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Settings' })).toBeNull());
    expect(window.location.hash).toBe('');
  });

  test('moving between views adds history entries, and Back and an edited fragment show their view', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const start = history.length;
    await user.click(side.getByRole('button', { name: /unified-agent-manager.*Doctor: add terminal line/ }));
    await waitFor(() => expect(window.location.hash).toBe('#task=t3'));
    await user.click(side.getByRole('button', { name: 'Settings' }));
    await waitFor(() => expect(window.location.hash).toBe('#settings'));
    expect(history.length).toBe(start + 2);
    // Back: the browser restores the fragment and reports it.
    history.replaceState(null, '', '/#task=t3');
    window.dispatchEvent(new PopStateEvent('popstate'));
    await waitFor(() => expect(header().textContent).toBe('Doctor: add terminal line'));
    expect(history.length).toBe(start + 2);
    // An edited fragment opens Settings or Routines.
    history.pushState(null, '', '/#settings');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(header().textContent).toBe('Settings'));
    history.pushState(null, '', '/#routines');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(header().textContent).toBe('Routines'));
  });

  test('a fragment the app does not know is replaced, not added', async () => {
    renderApp('#nothing-here');
    await sidebar();
    const start = history.length;
    await waitFor(() => expect(window.location.hash).toBe(''));
    expect(history.length).toBe(start);
  });

  test('Esc closes Settings, the × closes Routines, and focus goes back to the button that opened them', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const gear = side.getByRole('button', { name: 'Settings' });
    await user.click(gear);
    await waitFor(() => expect(header().textContent).toBe('Settings'));
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Settings' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(gear));
    const routines = side.getByRole('button', { name: 'Routines' });
    await user.click(routines);
    await waitFor(() => expect(header().textContent).toBe('Routines'));
    await user.click(screen.getByRole('button', { name: /^Close/ }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Routines' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(routines));
  });
});

describe('sidebar list keyboard', () => {
  const navRows = () => Array.from(document.querySelectorAll<HTMLElement>('nav[aria-label="Tasks"] [data-nav]'));

  test('the list is one tab stop; arrows and End reach the Archived shelf past a closed one', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const stops = () => navRows().filter((el) => el.tabIndex === 0);
    expect(stops()).toHaveLength(1);
    const settles = document.querySelectorAll<HTMLElement>('nav[aria-label="Tasks"] [data-settle]');
    expect(settles.length).toBeGreaterThan(0);
    for (const settle of settles) expect(settle.tabIndex).toBe(-1);
    stops()[0].focus();
    await user.keyboard('{End}');
    const archived = side.getByRole('button', { name: /^Archived/ });
    expect(document.activeElement).toBe(archived);
    await user.keyboard('{ArrowUp}');
    expect(document.activeElement).toBe(side.getByRole('button', { name: /^Settled/ }));
    await user.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(archived);
    // The stop follows focus.
    await waitFor(() => expect(stops()).toEqual([archived]));
  });

  test('a row\'s Settle is reached with Right and left with Left', async () => {
    const { user } = renderApp();
    await sidebar();
    const settle = document.querySelector<HTMLElement>('nav[aria-label="Tasks"] [data-settle]')!;
    const row = settle.closest('[data-task-row]')!.querySelector<HTMLElement>('[data-nav]')!;
    row.focus();
    await user.keyboard('{ArrowRight}');
    expect(document.activeElement).toBe(settle);
    await user.keyboard('{ArrowLeft}');
    expect(document.activeElement).toBe(row);
  });

  test('search counts its matches in words, and a search with none offers to clear it', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const box = side.getByRole('searchbox', { name: 'Search tasks' });
    await user.type(box, 'Doctor: add terminal');
    expect(await side.findByText('1 matching task')).toBeTruthy();
    await user.clear(box);
    await user.type(box, 'zzzz-no-such-task');
    expect(side.getByText('No matching tasks')).toBeTruthy();
    await user.click(side.getByRole('button', { name: 'Clear search' }));
    expect((box as HTMLInputElement).value).toBe('');
    expect(document.activeElement).toBe(box);
  });
});

describe('collapsed sidebar rail', () => {
  /** The app with the sidebar collapsed, once the Projects have loaded onto the rail. */
  async function rail() {
    localStorage.setItem('uam.sidebar', 'false');
    const rendered = renderApp();
    const nav = within(await screen.findByRole('navigation', { name: 'Sidebar' }));
    await nav.findByRole('button', { name: 'New task' });
    return { ...rendered, nav };
  }

  test('its mark shows the sidebar, carries the Needs you count, and focus follows the toggle', async () => {
    const { user, nav } = await rail();
    expect(screen.queryByRole('navigation', { name: 'Tasks' })).toBeNull();
    await user.click(nav.getByRole('button', { name: 'Show sidebar, 7 need you' }));
    const side = await sidebar();
    await waitFor(() => expect(document.activeElement?.id).toBe('sidebar-hide'));
    await user.click(side.getByRole('button', { name: /^Hide sidebar/ }));
    await waitFor(() => expect(document.activeElement?.id).toBe('sidebar-show'));
    expect(document.activeElement?.closest('nav')?.getAttribute('aria-label')).toBe('Sidebar');
  });

  test('New task opens the palette and the chosen project\'s draft', async () => {
    const { user, nav } = await rail();
    await user.click(nav.getByRole('button', { name: 'New task' }));
    const palette = await screen.findByRole('dialog');
    await user.click(within(palette).getByRole('option', { name: /notes-site/ }));
    await waitFor(() => expect(header().textContent).toBe('New task'));
    expect(screen.getByText('New task in notes-site')).toBeTruthy();
  });

  test('Projects opens the project filter, and the choice is the sidebar\'s', async () => {
    const { user, nav } = await rail();
    await user.click(nav.getByRole('button', { name: 'Project filter: all projects' }));
    const search = await screen.findByRole('combobox', { name: 'Search projects' });
    await user.type(search, 'notes');
    await user.keyboard('{Enter}');
    expect(await nav.findByRole('button', { name: 'Project filter: notes-site' })).toBeTruthy();
    expect(localStorage.getItem('uam.projectFilter')).toBe('"p3"');
    await user.click(nav.getByRole('button', { name: /^Show sidebar/ }));
    expect(await screen.findByRole('button', { name: 'Project filter: notes-site' })).toBeTruthy();
  });

  test('Add project opens the Add project dialog', async () => {
    const { user, nav } = await rail();
    await user.click(nav.getByRole('button', { name: 'Add project' }));
    expect(await screen.findByRole('dialog', { name: 'Add a project' })).toBeTruthy();
  });

  test('Settings and the connection sit at its foot', async () => {
    const { user, nav } = await rail();
    expect(nav.getByRole('status').textContent).toContain('Connected');
    await user.click(nav.getByRole('button', { name: 'Settings' }));
    await waitFor(() => expect(header().textContent).toBe('Settings'));
    expect(nav.getByRole('button', { name: 'Settings' }).getAttribute('aria-pressed')).toBe('true');
  });
});

describe('new task', () => {
  test('New task picks a project, and the first Send creates the task and opens it', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'New task' }));
    const palette = await screen.findByRole('dialog');
    await user.click(within(palette).getByRole('option', { name: /notes-site/ }));
    await waitFor(() => expect(header().textContent).toBe('New task'));
    expect(screen.getByText('New task in notes-site')).toBeTruthy();
    const box = screen.getByRole('textbox', { name: 'Message' });
    await waitFor(() => expect(document.activeElement).toBe(box));
    await user.type(box, 'Add a sitemap');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(window.location.hash).toMatch(/^#task=t\d+$/));
    const log = within(await screen.findByRole('log'));
    expect(await log.findByText('Add a sitemap')).toBeTruthy();
  });

  test('a first message of only an attachment creates the task and sends it without text', async () => {
    const { user, mock } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'New task' }));
    const palette = await screen.findByRole('dialog');
    await user.click(within(palette).getByRole('option', { name: /notes-site/ }));
    await waitFor(() => expect(header().textContent).toBe('New task'));
    expect(screen.getByRole('button', { name: 'Send' }).getAttribute('aria-disabled')).toBe('true');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' }).getAttribute('aria-disabled')).toBeNull());
    await user.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(window.location.hash).toMatch(/^#task=t\d+$/));
    const log = within(await screen.findByRole('log'));
    expect(await log.findByTitle('Open build.log')).toBeTruthy();
    const sent = mock.received.filter((r) => r.route === 'prompt');
    expect(sent).toHaveLength(1);
    expect(sent[0].body.text).toBe('');
    expect(sent[0].body.attachments).toHaveLength(1);
  });

  test('Alt+N opens the palette; Escape closes it', async () => {
    const { user } = renderApp();
    await sidebar();
    await user.keyboard('{Alt>}n{/Alt}');
    const palette = await screen.findByRole('dialog');
    expect(within(palette).getAllByRole('option').length).toBeGreaterThanOrEqual(3);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test('palette numbers stay with their project while searching, and no match offers Add project', async () => {
    const { user } = renderApp();
    await sidebar();
    await user.keyboard('{Alt>}n{/Alt}');
    const palette = within(await screen.findByRole('dialog'));
    const last = palette.getAllByRole('option').at(-1)!;
    const name = last.querySelector('.text-ink')!.textContent!;
    const key = within(last).getByText(/^Alt\+\d$/).textContent;
    const search = palette.getByRole('combobox', { name: 'Search projects' });
    await user.type(search, name);
    expect(palette.getAllByRole('option')).toHaveLength(1);
    expect(within(palette.getByRole('option')).getByText(/^Alt\+\d$/).textContent).toBe(key);
    await user.type(search, 'zzzz');
    await user.click(palette.getByRole('button', { name: 'Add project' }));
    expect(await screen.findByRole('dialog', { name: 'Add a project' })).toBeTruthy();
  });

  test('Alt+N in the terminal stays with the shell', async () => {
    renderApp();
    await sidebar();
    // xterm.js lets a key it does not handle bubble from its textarea, as with Option+N on macOS.
    const term = document.body.appendChild(document.createElement('div'));
    term.className = 'xterm';
    const input = term.appendChild(document.createElement('textarea'));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Dead', code: 'KeyN', altKey: true, bubbles: true }));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole('dialog')).toBeNull();
    term.remove();
  });
});

describe('task lifecycle', () => {
  async function openMenu(user: ReturnType<typeof renderApp>['user']) {
    await user.click(await screen.findByRole('button', { name: 'Task actions' }));
    return screen.findByRole('menu');
  }

  test('settle then reopen from the header menu', async () => {
    const { user } = renderApp('#task=t3');
    await waitFor(() => expect(header().textContent).toBe('Doctor: add terminal line'));
    await user.click(within(await openMenu(user)).getByRole('menuitem', { name: 'Settle' }));
    expect(await screen.findByText('Settled. Reopen this task to continue the same conversation.')).toBeTruthy();
    await user.click(within(await openMenu(user)).getByRole('menuitem', { name: 'Reopen' }));
    await waitFor(() => expect(screen.queryByText(/^Settled\. Reopen/)).toBeNull());
  });

  test('archive asks first, then the task is read-only; delete removes it', async () => {
    const { user } = renderApp('#task=t10');
    await waitFor(() => expect(header().textContent).toBe('Bump dependencies'));
    await user.click(within(await openMenu(user)).getByRole('menuitem', { name: 'Archive' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Archive this task?' });
    await user.click(within(confirm).getByRole('button', { name: 'Archive task' }));
    expect(await screen.findByText('Archived. This task is read-only.')).toBeTruthy();
    await user.click(within(await openMenu(user)).getByRole('menuitem', { name: 'Delete' }));
    const del = await screen.findByRole('alertdialog', { name: /Delete “Bump dependencies”\?/ });
    await user.click(within(del).getByRole('button', { name: 'Delete task' }));
    expect(await screen.findByRole('heading', { name: 'What are you working on?' })).toBeTruthy();
    expect(window.location.hash).toBe('');
    expect(screen.queryByRole('button', { name: /Bump dependencies/ })).toBeNull();
  });

  test('close conversation confirms first', async () => {
    const { user } = renderApp('#task=t3');
    await waitFor(() => expect(header().textContent).toBe('Doctor: add terminal line'));
    await user.click(within(await openMenu(user)).getByRole('menuitem', { name: 'Close conversation' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Close this conversation?' });
    await user.click(within(confirm).getByRole('button', { name: 'Close conversation' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    await user.click(await screen.findByRole('button', { name: 'Task actions' }));
    const menu = await screen.findByRole('menu');
    expect(within(menu).queryByRole('menuitem', { name: 'Close conversation' })).toBeNull();
  });

  test('rename in the header saves the new name', async () => {
    const { user } = renderApp('#task=t3');
    await waitFor(() => expect(header().textContent).toBe('Doctor: add terminal line'));
    await user.click(screen.getByRole('button', { name: 'Rename task' }));
    const input = await screen.findByRole('textbox', { name: 'Task name' });
    await user.clear(input);
    await user.type(input, 'Doctor terminal row{Enter}');
    await waitFor(() => expect(header().textContent).toBe('Doctor terminal row'));
  });

  test('a busy task cannot be settled and says why', async () => {
    const { user } = renderApp('#task=t1');
    await waitFor(() => expect(header().textContent).toBe('Fix re-attach redraw regression'));
    const menu = await openMenu(user);
    expect(within(menu).getByRole('menuitem', { name: 'Settle' }).getAttribute('aria-disabled')).toBe('true');
    expect(within(menu).getAllByText(/Stop the turn, answer what is waiting/).length).toBeGreaterThan(0);
  });
});
