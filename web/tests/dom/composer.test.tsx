import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { api, newRequestId } from '../../src/api';
import { choose, composer, log, openMenu, openTask, renderApp } from './render';

const sendButton = (name: string | RegExp) => screen.getByRole('button', { name });

describe('sending', () => {
  test('Enter sends on an idle task, which has one Send; the composer clears and the task starts working', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), 'Cover TERM=vt100 too');
    expect(sendButton('Send')).toHaveProperty('disabled', false);
    expect(screen.queryByRole('button', { name: 'Send now' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'After this turn' })).toBeNull();
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
    expect(composer().placeholder).toBe('Send now to guide this turn, or after it…');
    await user.type(composer(), 'Also check the resize path');
    expect(sendButton('Send now')).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(await log().findByText('Also check the resize path')).toBeTruthy();
    await user.type(composer(), 'Then update the changelog');
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(await screen.findByText('2 waiting')).toBeTruthy();
    expect(screen.getByTitle('Then update the changelog')).toBeTruthy();
  });

  test('while a turn runs, one Send does what Enter does and its menu the other', async () => {
    const { user, mock } = await openTask('t1');
    expect(screen.queryByRole('button', { name: 'Send' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'After this turn' })).toBeNull();
    await user.type(composer(), 'Steer by button');
    await user.click(sendButton('Send now'));
    expect(await log().findByText('Steer by button')).toBeTruthy();
    await user.type(composer(), 'Queue from the menu');
    const menu = await openMenu(user, 'More send options');
    await user.click(menu.getByRole('menuitem', { name: 'After this turn Ctrl+Enter' }));
    expect(await screen.findByText('2 waiting')).toBeTruthy();
    expect(mock.received.filter((r) => r.route === 'prompt').map((r) => r.body.mode)).toEqual(['steer', 'queue']);
    await waitFor(() => expect(document.activeElement).toBe(composer()));
  });

  test('with After this turn as the default, Send and Enter queue, and Ctrl+Enter and the menu steer', async () => {
    const { user, mock } = await openTask('t1');
    await api.updateWebSettings({ send_default: 'queue' });
    expect(await screen.findByRole('button', { name: 'After this turn' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Send now' })).toBeNull();
    expect(composer().placeholder).toBe('Send after this turn, or now to guide it…');
    await user.type(composer(), 'Queue by Enter');
    await user.keyboard('{Enter}');
    expect(await screen.findByText('2 waiting')).toBeTruthy();
    await user.type(composer(), 'Steer by Ctrl+Enter');
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(await log().findByText('Steer by Ctrl+Enter')).toBeTruthy();
    await user.type(composer(), 'Steer from the menu');
    const menu = await openMenu(user, 'More send options');
    await user.click(menu.getByRole('menuitem', { name: 'Send now Ctrl+Enter' }));
    expect(await log().findByText('Steer from the menu')).toBeTruthy();
    expect(mock.received.filter((r) => r.route === 'prompt').map((r) => r.body.mode)).toEqual(['queue', 'steer', 'steer']);
  });

  test('Stop ends the turn and pauses the queue; Resume sends the queued prompt', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: 'Stop turn' }));
    expect(await log().findByText('Turn stopped.')).toBeTruthy();
    expect(await screen.findByText('Paused')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Resume' }));
    await waitFor(() => expect(screen.queryByText(/waiting$/)).toBeNull());
    expect(await log().findAllByText(/Then describe the new behaviour/)).not.toHaveLength(0);
  });

  test('clearing the queue and cancelling one prompt are confirmed first', async () => {
    const { user } = await openTask('t1');
    await user.click(screen.getByRole('button', { name: /^Cancel waiting message: Then describe/ }));
    const one = await screen.findByRole('alertdialog', { name: 'Cancel this waiting message?' });
    await user.click(within(one).getByRole('button', { name: 'Keep' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(screen.getByText('1 waiting')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Clear' }));
    const all = await screen.findByRole('alertdialog', { name: 'Clear the waiting message?' });
    await user.click(within(all).getByRole('button', { name: 'Clear all' }));
    await waitFor(() => expect(screen.queryByText('1 waiting')).toBeNull());
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

  test('a command that cannot run is dimmed and says why', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), '/compact');
    const row = await within(await screen.findByRole('listbox', { name: 'Commands' })).findByRole('option', { name: /^\/compact/ });
    expect(row.getAttribute('aria-disabled')).toBe('true');
    expect(row.className).toContain('opacity-45');
    expect(await screen.findAllByText('This native command has no supported web handler yet')).not.toHaveLength(0);
  });

  test('a longer command output opens in the side panel, in mono with its lines, not in the composer', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), '/context');
    await within(await screen.findByRole('listbox', { name: 'Commands' })).findByRole('option', { name: /^\/context/ });
    await user.keyboard('{Enter}');
    expect(composer().value).toBe('/context ');
    await user.keyboard('{Enter}');
    const region = await screen.findByRole('region', { name: 'Output of /context' });
    const out = within(region).getByText(/^Context Usage/);
    expect(out.tagName).toBe('PRE');
    expect(out.textContent?.split('\n')).toHaveLength(4);
    expect(out.textContent).toContain('  · · · · · · · · · ·   ○ System Prompt     9.5k   (7%)');
    expect(screen.queryByText('Command result')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Close command output' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Output of /context' })).toBeNull());
  });

  test('beside the conversation, the output panel keeps composer focus on Esc and gives way to Files', async () => {
    const happy = (window as unknown as { happyDOM: { setViewport: (v: { width: number; height: number }) => void } }).happyDOM;
    happy.setViewport({ width: 1440, height: 900 });
    try {
      const { user } = await openTask('t3');
      const run = async () => {
        await user.click(composer());
        await user.type(composer(), '/context');
        await within(await screen.findByRole('listbox', { name: 'Commands' })).findByRole('option', { name: /^\/context/ });
        await user.keyboard('{Enter}{Enter}');
        return screen.findByRole('region', { name: 'Output of /context' });
      };
      const region = await run();
      expect(region.closest('aside')?.getAttribute('aria-label')).toBe('Command output');
      expect(screen.getByText('Output of /context is in the side panel.')).toBeTruthy();
      await user.keyboard('{Escape}');
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Output of /context' })).toBeNull());
      expect(document.activeElement).toBe(composer());
      await run();
      await user.click(screen.getByRole('button', { name: 'Browse files' }));
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Output of /context' })).toBeNull());
    } finally {
      happy.setViewport({ width: 1024, height: 768 });
    }
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
    const { user, mock } = await openTask('t1');
    await choose(user, 'Model: Auto', /Claude Haiku 4\.5/);
    expect(await screen.findByText(/Draft settings apply when its next turn starts\./)).toBeTruthy();
    expect(screen.getByText(/Send now keeps the current turn’s settings\./)).toBeTruthy();
    // A steer is impossible: Send and Enter queue, and the menu says why Send now is not offered.
    expect(composer().placeholder).toBe('Send after this turn, or now to guide it…');
    expect(screen.queryByRole('button', { name: 'Send now' })).toBeNull();
    await user.type(composer(), 'next');
    const menu = await openMenu(user, 'More send options');
    expect(menu.getByRole('menuitem', { name: 'Send now' }).getAttribute('aria-disabled')).toBe('true');
    expect(menu.getByText(/Send now keeps the current turn’s settings\./)).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
    expect(screen.getByRole('button', { name: 'After this turn' })).toBeTruthy();
    await user.click(composer());
    await user.keyboard('{Enter}');
    expect(await screen.findByText('2 waiting')).toBeTruthy();
    expect(mock.received.filter((r) => r.route === 'prompt').map((r) => r.body.mode)).toEqual(['queue']);
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
    // The model picker replaces its skeleton once /api/meta answers, which can be after the snapshot.
    expect((await screen.findByRole('button', { name: /^Model: Auto\. This task is read-only\./ })).getAttribute('aria-disabled')).toBe('true');
    expect(screen.getByRole('button', { name: /^Permissions and execution: Safe\. This task is read-only\./ })).toBeTruthy();
  });

  test('a narrow toolbar folds the lower-priority labels first and keeps the model and the actions', async () => {
    // The environment has no layout: the row overflows until it carries five folds.
    const proto = HTMLElement.prototype;
    const saved = { scroll: Object.getOwnPropertyDescriptor(proto, 'scrollWidth'), client: Object.getOwnPropertyDescriptor(proto, 'clientWidth') };
    const folds = (el: HTMLElement) => (el.dataset.fold ? el.dataset.fold.split(' ').length : 0);
    Object.defineProperty(proto, 'scrollWidth', { configurable: true, get(this: HTMLElement) { if (!this.hasAttribute('data-fold')) return 0; return folds(this) < 5 ? 500 : 400; } });
    Object.defineProperty(proto, 'clientWidth', { configurable: true, get(this: HTMLElement) { return this.hasAttribute('data-fold') ? 400 : 0; } });
    try {
      await openTask('t1');
      const model = await screen.findByRole('button', { name: /^Model: Auto/ });
      const row = model.parentElement!;
      await waitFor(() => expect(row.dataset.fold).toBe('execution credits tuning permissions more'));
      // Each control names the fold that shortens it: the mode loses its execution word, then its label; effort and
      // context its label; both move into More; the model's name goes last and may truncate only to a few letters.
      const mode = document.getElementById('composer-mode')!;
      expect(within(mode).getByText('· Interactive', { exact: false }).className).toContain('in-data-[fold~=execution]:hidden');
      expect(within(mode).getByText('Safe', { exact: false }).className).toContain('in-data-[fold~=permissions]:hidden');
      expect(mode.className).toContain('in-data-[fold~=more]:hidden');
      const effort = document.getElementById('composer-effort-context')!;
      expect(within(effort).getByText('Default').className).toContain('in-data-[fold~=tuning]:hidden');
      expect(effort.className).toContain('in-data-[fold~=more]:hidden');
      expect(document.getElementById('composer-more')!.className).toContain('sm:in-data-[fold~=more]:inline-flex');
      const name = within(model).getByText('Auto');
      expect(name.hasAttribute('data-squeeze')).toBe(true);
      expect(name.className).toContain('in-data-[fold~=model]:hidden');
      expect(row.className).toContain('data-[fold~=wrap]:flex-wrap');
      expect(screen.getByRole('button', { name: 'Send now' })).toBeTruthy();
      expect(screen.getByRole('button', { name: 'More send options' })).toBeTruthy();
    } finally {
      Object.defineProperty(proto, 'scrollWidth', saved.scroll ?? { configurable: true, value: 0 });
      Object.defineProperty(proto, 'clientWidth', saved.client ?? { configurable: true, value: 0 });
    }
  });
});

describe('attachments', () => {
  const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0])], 'shot.png', { type: 'image/png' });

  test('an attached file uploads, shows as a chip and goes with the message', async () => {
    const { user } = await openTask('t3');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    const remove = await screen.findByRole('button', { name: 'Remove build.log' });
    // An upload in flight, with no text yet, is not a message.
    expect(await screen.findByRole('button', { name: 'Send. Wait for the upload to finish' })).toHaveProperty('disabled', true);
    await user.type(composer(), 'See the log');
    await waitFor(() => expect(sendButton('Send')).toHaveProperty('disabled', false), { timeout: 3000 });
    expect(remove).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(await log().findByText('See the log')).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull());
  });

  test('an attachment alone is a message: Enter sends it without the blanks typed, and the transcript shows its chip', async () => {
    const { user, mock } = await openTask('t3');
    expect(sendButton('Send')).toHaveProperty('disabled', true);
    await user.type(composer(), '  ');
    expect(sendButton('Send')).toHaveProperty('disabled', true);
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    await waitFor(() => expect(sendButton('Send')).toHaveProperty('disabled', false), { timeout: 3000 });
    await user.click(composer());
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull());
    const sent = mock.received.filter((r) => r.route === 'prompt');
    expect(sent).toHaveLength(1);
    expect(sent[0].body.text).toBe('');
    expect(sent[0].body.attachments).toHaveLength(1);
    // Chips only: no text, and no Copy for text it does not have.
    const bubble = (await log().findByTitle('Open build.log')).closest<HTMLElement>('[data-history-anchor]')!;
    expect(bubble.textContent).toBe('You: build.log17 B');
    expect(within(bubble).queryByRole('button', { name: 'Copy message' })).toBeNull();
    await waitFor(() => expect(composer().value).toBe(''));
  });

  test('an attachment alone steers a running turn with Enter', async () => {
    const { user, mock } = await openTask('t1');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    await waitFor(() => expect(sendButton('Send now')).toHaveProperty('disabled', false), { timeout: 3000 });
    await user.click(composer());
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull());
    const sent = mock.received.filter((r) => r.route === 'prompt');
    expect(sent).toHaveLength(1);
    expect(sent[0].body).toMatchObject({ text: '', mode: 'steer' });
    expect(sent[0].body.attachments).toHaveLength(1);
    expect(await log().findByTitle('Open build.log')).toBeTruthy();
  });

  test('an attachment alone queues with its name as the label', async () => {
    const { user } = await openTask('t1');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    await waitFor(() => expect(sendButton('Send now')).toHaveProperty('disabled', false), { timeout: 3000 });
    await user.click(composer());
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(await screen.findByText('2 waiting')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Cancel waiting message: build.log' }));
    const dialog = await screen.findByRole('alertdialog', { name: 'Cancel this waiting message?' });
    expect(within(dialog).getByText('It will not be sent; nothing else changes.')).toBeTruthy();
    expect(within(dialog).getByText('build.log')).toBeTruthy();
  });

  test('after a stopped attachment-only turn, Resend puts its upload back', async () => {
    const { user } = await openTask('t3');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['hello from a log\n'], 'build.log', { type: 'text/plain' }));
    await waitFor(() => expect(sendButton('Send')).toHaveProperty('disabled', false), { timeout: 3000 });
    await user.click(composer());
    await user.keyboard('{Enter}');
    await user.click(await screen.findByRole('button', { name: 'Stop turn' }));
    expect(await log().findByText('Turn stopped.')).toBeTruthy();
    await user.click(await screen.findByRole('button', { name: 'Resend last prompt' }));
    expect(await screen.findByRole('button', { name: 'Remove build.log' })).toBeTruthy();
    expect(composer().value).toBe('');
    expect(sendButton('Send')).toHaveProperty('disabled', false);
  });

  test('Resend puts back only the uploads still stored', async () => {
    const { user } = await openTask('t5');
    expect(await log().findByText('old-notes.txt')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Resend last prompt' }));
    expect(await screen.findByRole('button', { name: 'Remove tmux-pane.png' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Remove old-notes.txt' })).toBeNull();
    expect(composer().value).toBe('');
    expect(sendButton('Send')).toHaveProperty('disabled', false);
  });

  test('a message of only file references reads "No text", and Resend offers no older prompt', async () => {
    const { user } = await openTask('t3');
    await api.prompt('t3', '', newRequestId(), 'send', { files: ['internal/vterm/redraw_test.go'] });
    expect(await log().findByText('No text')).toBeTruthy();
    await user.click(await screen.findByRole('button', { name: 'Stop turn' }));
    expect(await log().findByText('Turn stopped.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Resend last prompt' })).toBeNull();
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
