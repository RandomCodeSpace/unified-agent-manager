// The message box itself: its size, focus, the new Thread draft, prompt history, the Enter setting and a settled Thread's row.
import fs from 'node:fs';
import path from 'node:path';
import { attach, sidebarRow } from './composer-helpers';
import { conversation, expect, newThread, send, startThread, test, threadState, type Project } from './harness';

test('a long message grows the box to its cap, then scrolls inside it; Send and the toolbar stay in view', async ({ page, project, model }) => {
  model.reply('size check', 'Sized.');
  await startThread(page, project, 'size check');
  await expect(threadState(page, 'Finished')).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  const height = () => box.evaluate((el) => el.getBoundingClientRect().height);
  const empty = await height();
  await box.fill(Array.from({ length: 6 }, (_, i) => `line ${i + 1}`).join('\n'));
  const six = await height();
  expect(six).toBeGreaterThan(empty);
  // Six lines fit: nothing to scroll yet.
  expect(await box.evaluate((el) => el.scrollHeight <= el.clientHeight)).toBe(true);

  await box.fill(Array.from({ length: 80 }, (_, i) => `line ${i + 1}`).join('\n'));
  const cap = (page.viewportSize()!.height * 40) / 100;
  await expect.poll(height).toBeGreaterThan(six);
  expect(await height()).toBeLessThanOrEqual(cap + 1);
  expect(await height()).toBeGreaterThan(cap - 2);
  expect(await box.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
  // The caret's line is the one in view: the box scrolled inside itself, the page did not.
  expect(await box.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
  expect(await page.evaluate(() => document.documentElement.scrollHeight <= window.innerHeight)).toBe(true);
  for (const name of ['Send', /^Attach files/, 'Model: Mock']) await expect(page.getByRole('button', { name, exact: typeof name === 'string' })).toBeInViewport({ ratio: 1 });
});

test('typing with focus outside the box focuses it and keeps the first character', async ({ page, project, model }) => {
  model.reply('focus check', 'Focused.');
  await startThread(page, project, 'focus check');
  await expect(threadState(page, 'Finished')).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await conversation(page).getByText('Focused.').click();
  await expect(box).not.toBeFocused();
  await page.keyboard.type('hello there');
  await expect(box).toBeFocused();
  await expect(box).toHaveValue('hello there');
});

test('a New thread pane keeps its typed text per Project across another Thread and a reload; its attachments are not kept', async ({ page, uam, project, model }) => {
  model.reply('seed thread', 'Seeded.').title('seed thread', 'Seed row');
  const dir = path.join(uam.home, 'projects', `other-${path.basename(project.dir)}`);
  fs.mkdirSync(dir, { recursive: true });
  const other = (await uam.api('POST', '/api/projects', { dir })) as Project;
  await startThread(page, project, 'seed thread');
  await expect(conversation(page).getByText('Seeded.')).toBeVisible();

  await newThread(page, project);
  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('kept for later');
  await attach(page, [{ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('some notes\n') }]);
  const chip = page.getByRole('button', { name: 'Remove notes.txt' });
  await expect(chip).toBeVisible();
  // The draft is written shortly after the last change.
  await expect.poll(() => page.evaluate((k) => localStorage.getItem(k) ?? '', `uam.draft.new.${project.id}`)).toContain('kept for later');

  await sidebarRow(page, 'Seed row').click();
  await expect(conversation(page).getByText('Seeded.')).toBeVisible();
  await expect(box).toHaveValue('');
  await newThread(page, project);
  await expect(box).toHaveValue('kept for later');
  await expect(chip).toHaveCount(0);

  // Another Project's pane has its own draft.
  await newThread(page, other);
  await expect(box).toHaveValue('');

  await page.reload();
  await expect(page.getByRole('navigation', { name: 'Threads' }).or(page.getByRole('navigation', { name: 'Sidebar' })).first()).toBeVisible();
  await newThread(page, project);
  await expect(box).toHaveValue('kept for later');
  await expect(chip).toHaveCount(0);
  expect(model.served().map((s) => s.fixture).filter((f) => f.startsWith('reply'))).toEqual(['reply seed thread']);
});

test('Up recalls earlier prompts newest first; Down past the newest and Esc bring the draft back', async ({ page, project, model }) => {
  model.reply('first prompt', 'First answer.');
  model.reply('second prompt', 'Second answer.');
  await startThread(page, project, 'first prompt');
  await expect(conversation(page).getByText('First answer.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await send(page, 'second prompt');
  await expect(conversation(page).getByText('Second answer.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('my draft');
  await box.press('ArrowUp');
  await expect(box).toHaveValue('second prompt');
  await box.press('ArrowUp');
  await expect(box).toHaveValue('first prompt');
  // Nothing older: Up stays on the oldest.
  await box.press('ArrowUp');
  await expect(box).toHaveValue('first prompt');
  await box.press('ArrowDown');
  await expect(box).toHaveValue('second prompt');
  await box.press('ArrowDown');
  await expect(box).toHaveValue('my draft');

  await box.press('ArrowUp');
  await expect(box).toHaveValue('second prompt');
  await box.press('Escape');
  await expect(box).toHaveValue('my draft');
  expect(model.served()).toHaveLength(2);
});

test('with the send default After this turn, Enter queues and Ctrl+Enter sends now', async ({ page, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { send_default: 'queue' });
  try {
    // The turn waits on a permission request while both messages are sent.
    model.on({ userMessage: 'hold job' }, { toolCalls: [{ id: 'call_swap', name: 'create', arguments: JSON.stringify({ path: path.join(workdir, 'swap.txt'), file_text: 'swap\n' }) }] }, {}, 'tools for hold job');
    model.reply('steer this in', 'Steered in.');
    model.reply('queued by enter', 'Queued one ran.');
    await startThread(page, project, 'hold job');
    const allow = conversation(page).getByRole('button', { name: 'Allow once' });
    await expect(allow).toBeVisible();

    const box = page.getByRole('textbox', { name: 'Message' });
    await expect(page.getByRole('button', { name: 'After this turn', exact: true })).toBeVisible();
    await box.fill('queued by enter');
    await box.press('Enter');
    await expect(box).toHaveValue('');
    await expect(page.getByText('1 waiting', { exact: true })).toBeVisible();
    await expect(conversation(page).getByText('queued by enter')).toHaveCount(0);

    await box.fill('steer this in');
    const more = page.getByRole('button', { name: 'More send options' });
    await more.click();
    await expect(page.getByRole('menuitem', { name: /Send now/ })).toBeVisible();
    await page.keyboard.press('Escape');
    // The closing menu hands focus back to its button; a key pressed before that goes to the menu.
    await expect(page.getByRole('menuitem', { name: /Send now/ })).toHaveCount(0);
    await expect(more).toBeFocused();
    await box.press('Control+Enter');
    await expect(box).toHaveValue('');
    await expect(conversation(page).getByText('steer this in')).toBeVisible();
    await expect(page.getByText('1 waiting', { exact: true })).toBeVisible();

    await allow.click();
    await expect(conversation(page).getByText('Queued one ran.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    // The steer joined the running turn (its request carries the tool result); the queued message ran as the next turn.
    expect(model.served().map((s) => s.fixture)).toEqual(['tools for hold job', 'reply steer this in', 'reply queued by enter']);
    expect(model.served()[1].request.messages.some((m) => m.role === 'tool' && m.tool_call_id === 'call_swap')).toBe(true);
  } finally {
    await uam.api('PATCH', '/api/settings', { send_default: 'steer' });
  }
});

test('a settled Thread\'s composer is one row with Reopen; Reopen gives the box back, focused', async ({ page, uam, project, model }) => {
  model.reply('settle me', 'Ready to settle.');
  const url = await startThread(page, project, 'settle me');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const box = page.getByRole('textbox', { name: 'Message' });
  const form = page.locator('form').filter({ has: page.getByRole('button', { name: 'Model: Mock' }).or(page.getByRole('button', { name: 'Reopen', exact: true })) });
  const open = (await form.boundingBox())!.height;

  await uam.api('POST', `/api/sessions/${url.split('#task=')[1]}/settle`);
  const reopen = form.getByRole('button', { name: 'Reopen', exact: true });
  await expect(reopen).toBeVisible();
  await expect(form.getByText('Settled', { exact: true })).toBeVisible();
  // One row: no box, no Send, no Attach.
  await expect(box).toHaveCount(0);
  await expect(form.getByRole('button', { name: 'Send', exact: true })).toHaveCount(0);
  await expect(form.getByRole('button', { name: /^Attach files/ })).toHaveCount(0);
  const settled = (await form.boundingBox())!.height;
  expect(settled).toBeLessThan(open - 40);
  expect(settled).toBeLessThanOrEqual(64);

  await reopen.click();
  await expect(box).toBeFocused();
  await expect(form.getByRole('button', { name: 'Send', exact: true })).toBeVisible();
  await page.keyboard.type('back again');
  await expect(box).toHaveValue('back again');
});
