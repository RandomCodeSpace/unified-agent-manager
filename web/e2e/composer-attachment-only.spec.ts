// A message of only attachments (CMP-24): it sends, steers and queues, and blank text beside it is not sent.
import path from 'node:path';
import type { ChatCompletionRequest } from '@copilotkit/aimock';
import type { Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState } from './harness';

const upload = (name: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(`${name} body\n`) });
const chip = (page: Page, name: string) => page.getByRole('button', { name: `Remove ${name}` });

async function attach(page: Page, name: string) {
  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: /^Attach files/ }).click();
  await (await chooser).setFiles([upload(name)]);
  await expect(chip(page, name)).toBeVisible();
  // Enter is refused while the upload runs (Wait for the upload to finish).
  await expect(page.getByText(/^Uploading…/)).toHaveCount(0);
}

/** The text parts of the last user message, without the CLI's own date stamp. */
const userParts = (req: ChatCompletionRequest) => {
  const content = [...req.messages].reverse().find((m) => m.role === 'user')!.content;
  const parts = typeof content === 'string' ? [content] : (content as { text?: string }[]).map((p) => p.text ?? '');
  return parts.filter((p) => p !== '' && !p.startsWith('<current_datetime>'));
};

test('an attachment with blank text sends on an idle Thread; the blanks are not sent', async ({ page, project, model }) => {
  model.reply('opening words', 'Opened.');
  model.reply('idle.txt body', 'Read idle.txt.');
  await startThread(page, project, 'opening words');
  await expect(threadState(page, 'Finished')).toBeVisible();

  await attach(page, 'idle.txt');
  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('   ');
  await box.press('Enter');
  await expect(conversation(page).getByText('Read idle.txt.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(chip(page, 'idle.txt')).toHaveCount(0);
  const sent = model.served().find((s) => s.fixture === 'reply idle.txt body')!.request;
  expect(userParts(sent).some((p) => p.trim() === '')).toBe(false);
  expect(userParts(sent).join('\n')).toContain('idle.txt body');
});

test('an attachment alone steers a running turn', async ({ page, project, model, workdir }) => {
  model.on({ userMessage: 'hold for a steer' }, { toolCalls: [{ id: 'call_steer', name: 'create', arguments: JSON.stringify({ path: path.join(workdir, 'steer.txt'), file_text: 'steer\n' }) }] }, {}, 'tools for hold for a steer');
  model.reply('steer.txt body', 'Steered by steer.txt.');
  await startThread(page, project, 'hold for a steer');
  const allow = conversation(page).getByRole('button', { name: 'Allow once' });
  await expect(allow).toBeVisible();

  await attach(page, 'steer.txt');
  await page.getByRole('textbox', { name: 'Message' }).press('Enter');
  await expect(chip(page, 'steer.txt')).toHaveCount(0);
  await allow.click();
  await expect(conversation(page).getByText('Steered by steer.txt.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(model.served().map((s) => s.fixture)).toEqual(['tools for hold for a steer', 'reply steer.txt body']);
});

test('an attachment alone queued with After this turn runs its own turn after a tool turn', async ({ page, project, model, workdir }) => {
  model.toolRound('hold for the queue', [{ id: 'call_queue', name: 'create', arguments: { path: path.join(workdir, 'held.txt'), file_text: 'held\n' } }], 'Held job done.');
  model.reply('queued.txt body', 'Read queued.txt.');
  await startThread(page, project, 'hold for the queue');
  const allow = conversation(page).getByRole('button', { name: 'Allow once' });
  await expect(allow).toBeVisible();

  await attach(page, 'queued.txt');
  await page.getByRole('textbox', { name: 'Message' }).press('Control+Enter');
  await expect(page.getByText('1 waiting', { exact: true })).toBeVisible();
  await expect(chip(page, 'queued.txt')).toHaveCount(0);
  await allow.click();
  await expect(conversation(page).getByText('Read queued.txt.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByText(/\d+ waiting/)).toHaveCount(0);
  expect(model.served().map((s) => s.fixture)).toEqual(['tools for hold for the queue', 'result of call_queue', 'reply queued.txt body']);
});
