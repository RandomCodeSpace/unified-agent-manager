import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { choose, composer, log, openMenu, openTask, renderApp } from './render';

const sendButton = (name: string | RegExp) => screen.getByRole('button', { name });

describe('sending', () => {
  test('Enter sends on an idle task; the composer clears and the task starts working', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), 'Cover TERM=vt100 too');
    expect(sendButton('Send')).toHaveProperty('disabled', false);
    await user.keyboard('{Enter}');
    expect(await log().findByText('Cover TERM=vt100 too')).toBeTruthy();
    await waitFor(() => expect(composer().value).toBe(''));
    expect(await screen.findByRole('button', { name: 'Stop turn' })).toBeTruthy();
    await waitFor(() => expect(localStorage.getItem('uam.draft.t3')).toBeNull());
  });

  test('Shift+Enter adds a line instead of sending', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), 'one{Shift>}{Enter}{/Shift}two');
    expect(composer().value).toBe('one\ntwo');
  });

  test('while a turn runs, Enter steers it and Ctrl+Enter queues a follow-up', async () => {
    const { user } = await openTask('t1');
    expect(composer().placeholder).toBe('Steer this turn, or queue a follow-up…');
    await user.type(composer(), 'Also check the resize path');
    expect(sendButton('Steer')).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(await log().findByText('Also check the resize path')).toBeTruthy();
    await user.type(composer(), 'Then update the changelog');
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(await screen.findByText('2 queued')).toBeTruthy();
    expect(screen.getByTitle('Then update the changelog')).toBeTruthy();
  });

  test('Stop ends the turn and pauses the queue; Resume sends the queued prompt', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: 'Stop turn' }));
    expect(await log().findByText('Turn stopped.')).toBeTruthy();
    expect(await screen.findByText('Paused')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Resume' }));
    await waitFor(() => expect(screen.queryByText(/queued$/)).toBeNull());
    expect(await log().findAllByText(/Then describe the new behaviour/)).not.toHaveLength(0);
  });

  test('clearing the queue and cancelling one prompt are confirmed first', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: /^Cancel queued prompt: Then describe/ }));
    const one = await screen.findByRole('alertdialog', { name: 'Cancel this queued prompt?' });
    await user.click(within(one).getByRole('button', { name: 'Keep' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(screen.getByText('1 queued')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Clear' }));
    const all = await screen.findByRole('alertdialog', { name: 'Clear the queued prompt?' });
    await user.click(within(all).getByRole('button', { name: 'Clear queue' }));
    await waitFor(() => expect(screen.queryByText('1 queued')).toBeNull());
  });

  test('after a failed turn the last prompt can be put back to edit', async () => {
    const { user } = await openTask('t4');
    expect(screen.getByText('Error: provider process exited (code 1).')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Resend last prompt' }));
    expect(composer().value).toBe('Bump every action in .github/workflows to the current release and keep the SHA pins.');
    expect(screen.queryByRole('button', { name: 'Resend last prompt' })).toBeNull();
  });

  test('Up recalls earlier prompts and Escape restores the draft', async () => {
    const { user } = await openTask('t3');
    await user.click(composer());
    await user.keyboard('{ArrowUp}');
    expect(composer().value).toMatch(/^Does it handle `TERM=dumb`\?/);
    await user.keyboard('{ArrowUp}');
    expect(composer().value).toBe('Add a line to `uam doctor` that reports the detected terminal and glyph set.');
    await user.keyboard('{Escape}');
    expect(composer().value).toBe('');
  });

  test('an unsent draft is kept per task across a task switch', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), 'half a thought');
    await waitFor(() => expect(localStorage.getItem('uam.draft.t3')).toContain('half a thought'));
    history.pushState(null, '', '/#task=t4');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Bump GitHub Actions pins'));
    expect(composer().value).toBe('');
    history.pushState(null, '', '/#task=t3');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(composer().value).toBe('half a thought'));
    await user.click(screen.getByRole('button', { name: 'Send' }));
  });
});

describe('pickers', () => {
  test('/ lists the commands; picking one runs it', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), '/');
    const list = within(await screen.findByRole('listbox', { name: 'Commands' }));
    expect(await list.findByRole('option', { name: /^\/review/ })).toBeTruthy();
    expect(list.getByRole('option', { name: /^\/autopilot, aliases \/goal/ })).toBeTruthy();
    await user.type(composer(), 'rev');
    await waitFor(() => expect(list.getAllByRole('option')).toHaveLength(1));
    await user.keyboard('{Enter}');
    expect(composer().value).toBe('/review ');
    expect(sendButton('Run /review')).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(await log().findByText('/review')).toBeTruthy();
  });

  test('$ lists skills only, and Escape closes the list', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), '$');
    const list = within(await screen.findByRole('listbox', { name: 'Skills' }));
    expect(await list.findByRole('option', { name: /^\/commit/ })).toBeTruthy();
    expect(list.queryByRole('option', { name: /^\/review/ })).toBeNull();
    await user.keyboard('{ArrowDown}{ArrowUp}{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('an unknown command says so and a second Enter sends it as text', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), '/nothing');
    expect(await screen.findByText(/No command matches “\/nothing”/)).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('@ searches the project files and adds a file reference chip', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), 'Look at @redraw_');
    const list = within(await screen.findByRole('listbox', { name: 'Files' }));
    const option = await list.findByRole('option', { name: 'File internal/vterm/redraw_test.go' });
    await user.click(option);
    expect(composer().value).toBe('Look at @internal/vterm/redraw_test.go ');
    expect(screen.getByRole('button', { name: 'Remove file reference internal/vterm/redraw_test.go' })).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(await log().findByText(/Look at/)).toBeTruthy();
  });

  test('@ in a project without Git explains why there is nothing to list', async () => {
    const { user } = await openTask('t6');
    await user.type(composer(), '@');
    expect(await screen.findByText('This directory is not in a Git working tree')).toBeTruthy();
  });
});

describe('settings in the toolbar', () => {
  test('choosing a model on an idle task saves it on the task', async () => {
    const { user } = await openTask('t3');
    await choose(user, 'Model: Auto', /GPT-5 mini/);
    expect(await screen.findByRole('button', { name: 'Model: GPT-5 mini' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Effort and context size: Default' })).toBeTruthy();
  });

  test('on a running task a new model is a draft setting for the next turn', async () => {
    const { user } = await openTask('t1');
    await choose(user, 'Model: Auto', /Claude Haiku 4\.5/);
    expect(await screen.findByText(/Draft settings apply when its next turn starts\./)).toBeTruthy();
    expect(screen.getByText(/Steering keeps the current settings\./)).toBeTruthy();
    await user.type(composer(), 'next');
    await user.click(screen.getByRole('button', { name: 'Queue next turn' }));
    expect(await screen.findByText('2 queued')).toBeTruthy();
    // Queued with its settings, the draft settings are spent.
    await waitFor(() => expect(screen.queryByText(/Draft settings apply/)).toBeNull());
  });

  test('effort and context size come from the chosen model', async () => {
    const { user } = await openTask('t2');
    await choose(user, /^Effort and context size: Default · 200K/, 'high');
    expect(await screen.findByRole('button', { name: /^Effort and context size: high · 200K/ })).toBeTruthy();
    await choose(user, /^Effort and context size: high/, /Long context · 1M/);
    expect(await screen.findByRole('button', { name: /^Effort and context size: high · 1M/ })).toBeTruthy();
  });

  test('Yolo with Autopilot is confirmed before it applies', async () => {
    const { user } = await openTask('t1');
    await choose(user, 'Permissions and execution: Safe · Interactive', /^Yolo/);
    expect(await screen.findByRole('button', { name: 'Permissions and execution: Yolo · Interactive' })).toBeTruthy();
    const menu = await openMenu(user, 'Permissions and execution: Yolo · Interactive');
    const autopilot = await menu.findByRole('menuitemradio', { name: /^Autopilot/ });
    // The execution controls wait for the command list that carries /autopilot.
    await waitFor(() => expect(autopilot.hasAttribute('aria-disabled')).toBe(false));
    await user.click(autopilot);
    const confirm = await screen.findByRole('alertdialog', { name: 'Switch to Yolo with Autopilot?' });
    await user.click(within(confirm).getByRole('button', { name: 'Switch' }));
    expect(await screen.findByRole('button', { name: 'Permissions and execution: Yolo · Autopilot' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Stop autopilot' })).toBeTruthy();
  });

  test('a read-only task explains why nothing can change', async () => {
    renderApp('#task=t12');
    expect(await screen.findByText('Archived. This task is read-only.')).toBeTruthy();
    expect(screen.getByRole('button', { name: /^Model: Auto\. This task is read-only\./ }).getAttribute('aria-disabled')).toBe('true');
    expect(screen.getByRole('button', { name: /^Permissions and execution: Safe\. This task is read-only\./ })).toBeTruthy();
  });
});

describe('attachments', () => {
  const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0])], 'shot.png', { type: 'image/png' });

  test('an attached file uploads, shows as a chip and goes with the message', async () => {
    const { user } = await openTask('t3');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    const remove = await screen.findByRole('button', { name: 'Remove build.log' });
    await waitFor(() => expect(sendButton('Send')).toHaveProperty('disabled', true));
    await user.type(composer(), 'See the log');
    await waitFor(() => expect(sendButton('Send')).toHaveProperty('disabled', false), { timeout: 3000 });
    expect(remove).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(await log().findByText('See the log')).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull());
  });

  test('a model without image input refuses an image before it uploads', async () => {
    const { user } = await openTask('t9');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, png());
    expect(await screen.findByText(/Kimi K3 does not accept images/i)).toBeTruthy();
    expect(screen.getByRole('button', { name: /Remove the attachment that was refused/ })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Remove shot.png' }));
    expect(screen.queryByText(/does not accept images/i)).toBeNull();
  });
});

describe('background tasks', () => {
  test('a running background shell can be stopped after confirming', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: 'Background tasks: 1 running' }));
    const dialog = await screen.findByRole('dialog', { name: 'Background tasks' });
    expect(within(dialog).getByText('Repeat the redraw tests')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Stop background task: Repeat the redraw tests' }));
    const confirm = await screen.findByRole('alertdialog');
    await user.click(within(confirm).getByRole('button', { name: /Stop/ }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Background tasks: 1 running' })).toBeNull());
  });
});
