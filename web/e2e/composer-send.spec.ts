// Sending while idle and while a turn runs: Enter, Shift+Enter, After this turn, Send now and Stop.
import path from 'node:path';
import { conversation, expect, lastUserText, startThread, test, threadState } from './harness';

const slow = (word: string, lines: number) => Array.from({ length: lines }, (_, i) => `${word} line ${i + 1}`).join('\n\n');

test('Shift+Enter adds a line and Enter sends both lines as one message', async ({ page, project, model }) => {
  model.reply('opening words', 'Opened.');
  model.reply('line one', 'Hello back.');
  await startThread(page, project, 'opening words');
  await expect(threadState(page, 'Finished')).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await box.click();
  await page.keyboard.type('line one');
  await page.keyboard.press('Shift+Enter');
  await page.keyboard.type('line two');
  await expect(box).toHaveValue('line one\nline two');
  await expect(page.getByRole('button', { name: 'Send', exact: true })).toHaveCount(1);
  await page.keyboard.press('Enter');

  await expect(conversation(page).getByText('Hello back.')).toBeVisible();
  await expect(box).toHaveValue('');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const sent = model.served().map((s) => lastUserText(s.request)).find((t) => t.includes('line one'));
  expect(sent).toContain('line one\nline two');
});

test('After this turn queues two prompts; they run one turn each, oldest first', async ({ page, project, model, workdir }) => {
  // The first turn waits on a permission request, so it is still running however slowly the prompts are typed.
  model.toolRound('first job', [{ id: 'call_hold', name: 'create', arguments: { path: path.join(workdir, 'held.txt'), file_text: 'held\n' } }], 'First job done.');
  model.reply('queued one', 'Reply to queued one.');
  model.reply('queued two', 'Reply to queued two.');
  await startThread(page, project, 'first job');
  const allow = conversation(page).getByRole('button', { name: 'Allow once' });
  await expect(allow).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  for (const q of ['queued one', 'queued two']) {
    await box.fill(q);
    await box.press('Control+Enter');
    await expect(box).toHaveValue('');
  }
  await expect(page.getByText('2 waiting', { exact: true })).toBeVisible();
  await expect(page.getByText('Sent after this turn', { exact: true })).toBeVisible();
  expect(model.served().map((s) => s.fixture)).toEqual(['tools for first job']);

  await allow.click();
  await expect(conversation(page).getByText('Reply to queued two.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByText(/\d+ waiting/)).toHaveCount(0);
  expect(model.served().map((s) => s.fixture)).toEqual(['tools for first job', 'result of call_hold', 'reply queued one', 'reply queued two']);
  const order = ['first job', 'First job done.', 'queued one', 'Reply to queued one.', 'queued two', 'Reply to queued two.'];
  const text = await conversation(page).innerText();
  const at = order.map((t) => text.indexOf(t));
  expect(at.every((i) => i >= 0), `${order} in ${text}`).toBe(true);
  expect([...at].sort((a, b) => a - b)).toEqual(at);
});

test('Stop pauses the waiting prompt and gives focus back to the box; Resume sends it', async ({ page, project, model }) => {
  model.reply('long job', slow('long', 60), { latency: 250, chunkSize: 4 });
  model.reply('next one', 'Reply to next one.');
  await startThread(page, project, 'long job');
  await expect(conversation(page).getByText('long line 1', { exact: true })).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('next one');
  await page.getByRole('button', { name: 'More send options' }).click();
  await page.getByRole('menuitem', { name: /After this turn/ }).click();
  await expect(page.getByText('1 waiting', { exact: true })).toBeVisible();
  await page.getByRole('heading', { level: 1 }).click();

  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(page.getByText('Paused', { exact: true })).toBeVisible();
  await expect(page.getByText('1 waiting', { exact: true })).toBeVisible();
  await expect(box).toBeFocused();
  expect(model.served().map((s) => s.fixture)).toEqual(['reply long job']);

  await page.getByRole('button', { name: 'Resume' }).click();
  await expect(conversation(page).getByText('Reply to next one.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByText(/\d+ waiting/)).toHaveCount(0);
});

test('Enter during a turn steers it: the message reaches the model within the same turn', async ({ page, project, model, workdir }) => {
  // The turn waits on a permission request while the steer is sent, then goes on once it is allowed.
  model.on({ userMessage: 'do two steps' }, { toolCalls: [{ id: 'call_step', name: 'create', arguments: JSON.stringify({ path: path.join(workdir, 'step.txt'), file_text: 'step\n' }) }] }, {}, 'tools for do two steps');
  // The steer arrives as a user message after the tool result, so it is the last user message of the next request.
  model.reply('also mention bananas', 'Bananas mentioned, both steps done.');
  await startThread(page, project, 'do two steps');
  const allow = conversation(page).getByRole('button', { name: 'Allow once' });
  await expect(allow).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('also mention bananas');
  // Enter's action while a turn runs is Send now, which keeps the turn going.
  await box.press('Enter');
  await expect(box).toHaveValue('');
  await expect(conversation(page).getByText('also mention bananas')).toBeVisible();
  expect(model.served().map((s) => s.fixture)).toEqual(['tools for do two steps']);

  await allow.click();
  await expect(conversation(page).getByText('Bananas mentioned, both steps done.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conversation(page).getByText('You (accepted, not delivered yet):')).toHaveCount(0);
  await expect(conversation(page).getByText('Not delivered')).toHaveCount(0);
  const steered = model.served().find((s) => s.fixture === 'reply also mention bananas')!.request;
  expect(steered.messages.some((m) => m.role === 'tool' && m.tool_call_id === 'call_step')).toBe(true);
  expect(model.served().map((s) => s.fixture)).toEqual(['tools for do two steps', 'reply also mention bananas']);
});

test('a steer the turn never took up reads Not delivered after Stop', async ({ page, project, model }) => {
  model.reply('stream on', slow('steady', 60), { latency: 250, chunkSize: 4 });
  await startThread(page, project, 'stream on');
  await expect(conversation(page).getByText('steady line 1', { exact: true })).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('too late to matter');
  await box.press('Enter');
  await expect(box).toHaveValue('');
  await expect(conversation(page).getByText('too late to matter')).toBeVisible();
  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(conversation(page).getByText('Not delivered', { exact: true })).toBeVisible();
  expect(model.served().map((s) => s.fixture)).toEqual(['reply stream on']);
});

test('typing while a reply streams keeps every character and the caret', async ({ page, project, model }) => {
  model.reply('stream while typing', slow('typing', 80), { latency: 150, chunkSize: 4 });
  await startThread(page, project, 'stream while typing');
  await expect(conversation(page).getByText('typing line 1', { exact: true })).toBeVisible();

  const box = page.getByRole('textbox', { name: 'Message' });
  await box.click();
  const a = 'The quick brown fox jumps over the lazy dog, 0123456789 times!';
  const b = 'Pack my box with five dozen liquor jugs; sphinx of black quartz.';
  await page.keyboard.type(a, { delay: 15 });
  await page.keyboard.press('Shift+Enter');
  await page.keyboard.type(b, { delay: 15 });
  for (let i = 0; i < 20; i++) await page.keyboard.press('ArrowLeft');
  await page.keyboard.type('XYZ', { delay: 15 });
  const expected = `${a}\n${b.slice(0, b.length - 20)}XYZ${b.slice(b.length - 20)}`;
  // Still streaming: every key landed during the reply.
  await expect(threadState(page, 'Working')).toBeVisible();
  await expect(box).toHaveValue(expected);
  expect(await box.evaluate((el: HTMLTextAreaElement) => [el.selectionStart, el.selectionEnd])).toEqual([expected.length - 20, expected.length - 20]);
  expect(model.served()).toHaveLength(1);

  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(box).toHaveValue(expected);
});
