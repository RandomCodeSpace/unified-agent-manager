import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

const header = () => screen.getByRole('heading', { level: 1 });

describe('app shell', () => {
  test('lists the projects and tasks, and says how to begin', async () => {
    // A service that predates the planner: the count is the Tasks' alone.
    renderApp('?planner=unset');
    const side = await sidebar();
    expect(await screen.findByText('Open a task from the sidebar, or start a new one there.')).toBeTruthy();
    expect(side.getByRole('button', { name: /Fix re-attach redraw regression/ })).toBeTruthy();
    expect(side.getByRole('button', { name: /Archived 2/ })).toBeTruthy();
    expect(side.getByRole('status').textContent).toContain('Connected');
    // Seven tasks wait for the user: two for permission, five for an answer.
    expect(document.title).toBe('(7) UAM');
  });

  test('opening a task from the sidebar shows it and keeps it in the URL', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: /Completed.*Doctor: add terminal line/ }));
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

  test('Ctrl+B hides the sidebar and brings it back, remembered per browser', async () => {
    const { user } = renderApp();
    await sidebar();
    const aside = document.querySelector('aside')!;
    await user.keyboard('{Control>}b{/Control}');
    await waitFor(() => expect(aside.getAttribute('aria-hidden')).toBe('true'));
    expect(localStorage.getItem('uam.sidebar')).toBe('false');
    await user.click(screen.getByRole('button', { name: 'Show sidebar' }));
    await waitFor(() => expect(aside.getAttribute('aria-hidden')).toBe('false'));
    expect(localStorage.getItem('uam.sidebar')).toBe('true');
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
    expect(screen.getByRole('button', { name: 'Send' })).toHaveProperty('disabled', true);
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toHaveProperty('disabled', false));
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
    expect(await screen.findByText('Open a task from the sidebar, or start a new one there.')).toBeTruthy();
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
