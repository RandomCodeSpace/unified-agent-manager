// Leaving a Thread and coming back: the Since you left line, and Settings or Routines over the Thread, which is not leaving.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, openHome, send, startThread, test, threadState } from './harness';
import { line, lines, offsetOf, settledScrollTop, threadId, topItem, yolo } from './transcript-helpers';

test('a Thread that worked while you were away says what happened, and Jump to where you stopped shows the first new message', async ({ page, project, model, uam, workdir }) => {
  fs.writeFileSync(path.join(workdir, 'package.json'), JSON.stringify({ name: 'fixture', scripts: { test: 'exit 0' } }));
  model.reply('before you go', 'See you later.');
  const away = (n: number) => `away line ${String(n).padStart(3, '0')}`;
  model.on({ toolCallId: 'call_test' }, { content: Array.from({ length: 60 }, (_, i) => away(i + 1)).join('\n\n') }, { chunkSize: 100_000 }, 'result of call_test');
  model.on({ userMessage: 'work while I am away' }, {
    toolCalls: [
      { id: 'call_note', name: 'create', arguments: JSON.stringify({ path: path.join(workdir, 'notes.txt'), file_text: 'note\n' }) },
      { id: 'call_test', name: 'bash', arguments: JSON.stringify({ command: 'npm test', description: 'Run the tests' }) },
    ],
  }, {}, 'tools for work while I am away');
  const url = await startThread(page, project, 'before you go');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await yolo(page, uam.api);
  const conv = conversation(page);
  const since = page.getByRole('status').filter({ hasText: 'Since you left' });

  // The reader goes Home, then the next prompt arrives from elsewhere and the turn runs while they are away.
  const id = threadId(page);
  await openHome(page);
  await uam.api('POST', `/api/sessions/${id}/prompt`, { text: 'work while I am away', request_id: crypto.randomUUID() });
  // The row read Finished before the prompt too: it counts once the turn's last model call was answered.
  await expect.poll(() => model.served().some((s) => s.fixture === 'result of call_test')).toBe(true);
  const row = page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${project.name} .*Finished`) });
  await expect(row).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'notes.txt'), 'utf8')).toBe('note\n');

  await row.click();
  await expect(page).toHaveURL(url);
  await expect(since).toHaveText(/^Since you left \(<1 min\): the agent finished, ran the tests, and changed 1 file\.\s*Jump to where you stopped/);
  // Opened at the newest text; the jump goes back to the first thing not seen, the prompt sent while away.
  const first = conv.getByText('work while I am away', { exact: true });
  await expect(conv.getByText(away(60), { exact: true })).toBeInViewport();
  await expect(first).not.toBeInViewport();
  await since.getByRole('button', { name: 'Jump to where you stopped' }).click();
  await expect(since).toHaveCount(0);
  await expect(first).toBeInViewport();
  await expect(conv.getByText(away(60), { exact: true })).not.toBeInViewport();

  // Seen now: leaving and coming back with nothing new shows no line.
  await openHome(page);
  await row.click();
  await expect(conv.getByText(away(60), { exact: true })).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(since).toHaveCount(0);
});

test('Settings and Routines over a Thread give it back at its scroll position with its open rows, and are not leaving', async ({ page, project, model, uam }) => {
  model.toolRound('long answer with steps', [{ id: 'call_seq', name: 'bash', arguments: { command: 'seq 1 3', description: 'Count to three' } }], lines(80));
  model.reply('say more', 'More, said while Routines was open.', { latency: 2000 });
  await startThread(page, project, 'long answer with steps');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(conv.getByText(line(80), { exact: true })).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  // Open the turn's steps and one call in them, then scroll to the middle.
  const counts = conv.getByRole('button', { name: /^1 command\. Show the activity of this turn$/ });
  await counts.click();
  const call = conv.getByRole('button', { name: 'Ran seq 1 3 , ok' });
  await call.click();
  await expect(call).toHaveAttribute('aria-expanded', 'true');
  // The reader's place: the paragraph at the top of the view and its offset.
  const toMiddle = async () => {
    await conv.evaluate((el) => { el.scrollTop = Math.round((el.scrollHeight - el.clientHeight) / 2); });
    expect(await settledScrollTop(page)).toBeGreaterThan(0);
    return topItem(page);
  };
  const place = await toMiddle();
  expect(place.text).toMatch(/^stream line \d{3}$/);
  const backAsLeft = async () => {
    await expect(conv).toBeVisible();
    await settledScrollTop(page);
    const offset = await offsetOf(page, place.text);
    expect(offset, `${place.text} stays where it was`).not.toBeNull();
    expect(Math.abs(offset! - place.offset)).toBeLessThanOrEqual(2);
    await expect(counts).toHaveAttribute('aria-expanded', 'true');
    await expect(call).toHaveAttribute('aria-expanded', 'true');
    await expect(conv.getByText('$ seq 1 3')).toBeAttached();
  };

  const sidebar = page.getByRole('navigation', { name: 'Threads' });
  await sidebar.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
  await expect(conv).toBeHidden();
  await page.getByRole('button', { name: 'Close settings' }).click();
  await backAsLeft();

  await sidebar.getByRole('button', { name: 'Routines', exact: true }).click();
  await expect(page.getByRole('button', { name: 'New routine', exact: true })).toBeVisible();
  await expect(conv).toBeHidden();
  await page.keyboard.press('Escape');
  await backAsLeft();

  // A turn that ends while Routines covers the Thread is not news when it comes back.
  await send(page, 'say more');
  await expect(threadState(page, 'Working')).toBeVisible();
  await sidebar.getByRole('button', { name: 'Routines', exact: true }).click();
  await expect(conv).toBeHidden();
  await expect(page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${project.name} .*Finished`) })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(conv.getByText('More, said while Routines was open.')).toBeVisible();
  await expect(counts).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByRole('status').filter({ hasText: 'Since you left' })).toHaveCount(0);
});
