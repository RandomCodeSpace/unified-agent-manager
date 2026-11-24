// Attachments beyond the paper clip: what is refused, paste, drop, and putting a past prompt back.
// A message of only attachments has no case: its turn sometimes never starts (COVERAGE.md, CMP-24).
import type { ChatCompletionRequest } from '@copilotkit/aimock';
import type { Locator, Page } from '@playwright/test';
import { attach, PNG, useVisionMock, type Upload } from './composer-helpers';
import { conversation, expect, startThread, test, threadState } from './harness';

const text = (name: string, body = `${name} body\n`): Upload => ({ name, mimeType: 'text/plain', buffer: Buffer.from(body) });
const chip = (page: Page, name: string) => page.getByRole('button', { name: `Remove ${name}` });
const form = (page: Page) => page.locator('form').filter({ has: page.getByRole('textbox', { name: 'Message' }) });
/** The refusal chip of the file named `name`. */
const refusal = (page: Page, name: string) => form(page).getByRole('alert').filter({ hasText: name });

/** A DataTransfer in the page holding `files` and, when given, plain text: what a paste or a drop carries. */
const transfer = (page: Page, files: Upload[], plain?: string) => page.evaluateHandle(({ files, plain }) => {
  const dt = new DataTransfer();
  for (const f of files) dt.items.add(new File([Uint8Array.from(atob(f.b64), (c) => c.charCodeAt(0))], f.name, { type: f.mimeType }));
  if (plain !== undefined) dt.setData('text/plain', plain);
  return dt;
}, { files: files.map((f) => ({ name: f.name, mimeType: f.mimeType, b64: f.buffer.toString('base64') })), plain });

/** Pastes into `box`; false when the composer took the paste for itself (an upload), true when the browser's own paste goes on. */
const paste = async (box: Locator, files: Upload[], plain?: string) =>
  box.evaluate((el, dt) => el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })), await transfer(box.page(), files, plain));

/** The parts of the last user message, without the CLI's own date stamp. */
const userParts = (req: ChatCompletionRequest) => {
  const content = [...req.messages].reverse().find((m) => m.role === 'user')!.content;
  const parts = typeof content === 'string' ? [content] : (content as { text?: string }[]).map((p) => p.text ?? '');
  return parts.filter((p) => p !== '' && !p.startsWith('<current_datetime>'));
};

test('the service types an upload by its bytes; SVG, archives, oversized files and a sixth file are refused with the reason', async ({ page, uam, project, model }) => {
  const restore = await useVisionMock(uam);
  try {
    model.reply('upload rules', 'Ready.');
    const id = (await startThread(page, project, 'upload rules')).split('#task=')[1];
    await expect(threadState(page, 'Finished')).toBeVisible();
    const drop = async (name: string) => {
      await chip(page, name).click();
      await expect(chip(page, name)).toHaveCount(0);
    };

    // Text bytes named .png are stored as text; PNG bytes named .txt as an image.
    await attach(page, [{ name: 'words.png', mimeType: 'image/png', buffer: Buffer.from('plain words\n') }, { name: 'pixels.txt', mimeType: 'text/plain', buffer: PNG }]);
    await expect(form(page).getByText('12 B · Text', { exact: true })).toBeVisible();
    await expect(form(page).getByText(`${PNG.length} B · Image`, { exact: true })).toBeVisible();
    await drop('words.png');
    await drop('pixels.txt');

    // A zip named .txt passes the browser's check by name; the service reads its bytes and refuses it.
    const zip = Buffer.concat([Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x14, 0, 0, 0, 8, 0]), Buffer.alloc(64, 0xff)]);
    await attach(page, [
      { name: 'archive.txt', mimeType: 'text/plain', buffer: zip },
      { name: 'bundle.zip', mimeType: 'application/zip', buffer: zip },
      { name: 'drawing.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>') },
      { name: 'huge.png', mimeType: 'image/png', buffer: Buffer.concat([PNG, Buffer.alloc(3 * 1024 * 1024)]) },
      { name: 'vector.txt', mimeType: 'text/plain', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>') },
      { name: 'huge.txt', mimeType: 'text/plain', buffer: Buffer.alloc(256 * 1024 + 1, 'a') },
    ]);
    await expect(refusal(page, 'archive.txt')).toContainText(/only png, jpeg, gif and webp images, PDF files and UTF-8 text files can be attached/i);
    await expect(refusal(page, 'bundle.zip')).toContainText('Only png, jpeg, gif and webp images, PDF files and text files can be attached');
    await expect(refusal(page, 'drawing.svg')).toContainText('SVG images cannot be attached');
    await expect(refusal(page, 'huge.png')).toContainText('Images can be at most 3 MiB');
    await expect(refusal(page, 'vector.txt')).toContainText('SVG images cannot be attached');
    await expect(refusal(page, 'huge.txt')).toContainText('Text files can be at most 256 KiB');
    // A refused file blocks Send until it is removed.
    await page.getByRole('textbox', { name: 'Message' }).fill('would not go');
    await expect(page.getByRole('button', { name: 'Send. Remove the attachment that was refused' })).toHaveAttribute('aria-disabled', 'true');
    for (const name of ['archive.txt', 'bundle.zip', 'drawing.svg', 'huge.png', 'vector.txt', 'huge.txt']) await drop(name);
    await expect(page.getByRole('button', { name: 'Send', exact: true })).toBeEnabled();

    // Five go; the sixth is refused and the paper clip says why.
    await attach(page, ['one', 'two', 'three', 'four', 'five', 'six'].map((n) => text(`${n}.txt`)));
    for (const n of ['one', 'two', 'three', 'four', 'five']) await expect(form(page).getByText(`${n}.txt`, { exact: true }).first()).toBeVisible();
    await expect(refusal(page, 'six.txt')).toContainText('A prompt carries at most 5 attachments');
    await expect(page.getByRole('button', { name: /^Attach files\. A message carries at most 5 attachments\./ })).toBeVisible();
    // A custom model takes no PDF, so the composer never uploads one: the service's own limit is asked directly.
    const pdf = (extra: number) => page.request.post(`/api/sessions/${id}/attachments?name=doc.pdf`, { headers: { 'Content-Type': 'application/octet-stream' }, data: Buffer.concat([Buffer.from('%PDF-1.4\n'), Buffer.alloc(extra, 0x20)]) });
    const big = await pdf(10 * 1024 * 1024);
    expect(big.status()).toBe(413);
    expect(await big.text()).toContain('can be at most 10 MiB');
    expect(await (await pdf(64)).text()).toContain('does not accept PDF files');
    expect(model.served()).toHaveLength(1);
  } finally {
    await restore();
  }
});

test('pasting an image uploads it; a paste over 8 KiB becomes pasted-text.txt, and Keep as text puts it in the message', async ({ page, uam, project, model }) => {
  const restore = await useVisionMock(uam);
  try {
    model.reply('paste things', 'Ready.');
    await startThread(page, project, 'paste things');
    await expect(threadState(page, 'Finished')).toBeVisible();
    const box = page.getByRole('textbox', { name: 'Message' });

    expect(await paste(box, [{ name: 'clip.png', mimeType: 'image/png', buffer: PNG }])).toBe(false);
    await expect(chip(page, 'clip.png')).toBeVisible();
    await expect(form(page).getByText(`${PNG.length} B · Image`, { exact: true })).toBeVisible();
    await chip(page, 'clip.png').click();

    // A short paste is left to the browser: no chip.
    expect(await paste(box, [], 'a short paste')).toBe(true);
    const long = `${'0123456789abcdef'.repeat(512)}\nthe end`;
    expect(Buffer.byteLength(long)).toBeGreaterThan(8 * 1024);
    await box.fill('before ');
    expect(await paste(box, [], long)).toBe(false);
    await expect(chip(page, 'pasted-text.txt')).toBeVisible();
    await expect(form(page).getByText('8 KB · Text', { exact: true })).toBeVisible();
    await expect(box).toHaveValue('before ');
    await expect(chip(page, 'a short paste')).toHaveCount(0);

    await page.getByRole('button', { name: 'Keep pasted-text.txt as text' }).click();
    await expect(chip(page, 'pasted-text.txt')).toHaveCount(0);
    await expect(box).toHaveValue(`before ${long}`);
    await expect(box).toBeFocused();
    expect(model.served()).toHaveLength(1);
  } finally {
    await restore();
  }
});

test('dragging files over the composer shows what can be attached; dropping uploads them', async ({ page, uam, project, model }) => {
  const restore = await useVisionMock(uam);
  try {
    model.reply('drop things', 'Ready.');
    await startThread(page, project, 'drop things');
    await expect(threadState(page, 'Finished')).toBeVisible();

    const dt = await transfer(page, [text('dropped.txt'), { name: 'dropped.png', mimeType: 'image/png', buffer: PNG }]);
    await expect(page.getByText('Drop to attach', { exact: true })).toHaveCount(0);
    await form(page).dispatchEvent('dragenter', { dataTransfer: dt });
    await expect(page.getByText('Drop to attach', { exact: true })).toBeVisible();
    // The overlay's second line is the model's gate when it narrows the kinds, else the limits (the paper clip's tip has those).
    await expect(page.getByText('Mock takes images and text files, not PDF', { exact: true })).toBeVisible();
    await form(page).dispatchEvent('dragover', { dataTransfer: dt });
    await form(page).dispatchEvent('drop', { dataTransfer: dt });
    await expect(page.getByText('Drop to attach', { exact: true })).toHaveCount(0);
    await expect(chip(page, 'dropped.txt')).toBeVisible();
    await expect(chip(page, 'dropped.png')).toBeVisible();
    await expect(form(page).getByText('17 B · Text', { exact: true })).toBeVisible();
    await expect(form(page).getByText(`${PNG.length} B · Image`, { exact: true })).toBeVisible();
    expect(model.served()).toHaveLength(1);
  } finally {
    await restore();
  }
});

test('after a failed turn, Resend last prompt puts the prompt and its upload back in the box without sending', async ({ page, project, model }) => {
  model.reply('before the failure', 'Ready.');
  model.fail('this one fails', 429, 'rate limited by the test');
  await startThread(page, project, 'before the failure');
  await expect(threadState(page, 'Finished')).toBeVisible();

  await attach(page, [text('evidence.txt')]);
  await expect(chip(page, 'evidence.txt')).toBeVisible();
  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('this one fails');
  await box.press('Enter');
  await expect(threadState(page, 'Error')).toBeVisible({ timeout: 40_000 });
  await expect(box).toHaveValue('');
  await expect(chip(page, 'evidence.txt')).toHaveCount(0);
  const calls = model.served().length;

  await page.getByRole('button', { name: 'Resend last prompt' }).click();
  await expect(box).toHaveValue('this one fails');
  await expect(box).toBeFocused();
  await expect(chip(page, 'evidence.txt')).toBeVisible();
  await expect(form(page).getByText('18 B · Text', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Resend last prompt' })).toHaveCount(0);
  await expect(threadState(page, 'Error')).toBeVisible();
  expect(model.served()).toHaveLength(calls);
});

test('Edit and resend fills the past prompt and its upload under the editing strip; sending rewinds and sends the edit', async ({ page, project, model }) => {
  model.reply('opening question', 'Opening answer.');
  model.reply('second question, revised', 'Revised answer.');
  model.reply('second question', 'Second answer.');
  await startThread(page, project, 'opening question');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const conv = conversation(page);
  const box = page.getByRole('textbox', { name: 'Message' });
  await attach(page, [text('context.txt')]);
  await expect(chip(page, 'context.txt')).toBeVisible();
  await box.fill('second question');
  await box.press('Enter');
  await expect(conv.getByText('Second answer.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await box.fill('a draft set aside');
  await conv.getByRole('button', { name: 'Turn actions' }).nth(1).click();
  await page.getByRole('menuitem', { name: 'Edit and resend…' }).click();
  const strip = page.getByRole('group', { name: 'Editing a past prompt' });
  await expect(strip).toContainText('Editing your prompt from');
  await expect(strip).toContainText('Removes this prompt and its reply');
  await expect(strip).toContainText('Nothing changes until you send. Your other draft comes back when you finish editing.');
  await expect(box).toHaveValue('second question');
  await expect(chip(page, 'context.txt')).toBeVisible();

  // Stopping changes nothing and brings the draft back.
  await strip.getByRole('button', { name: 'Stop editing' }).click();
  await expect(strip).toHaveCount(0);
  await expect(box).toHaveValue('a draft set aside');
  await expect(chip(page, 'context.txt')).toHaveCount(0);
  await expect(conv.getByText('Second answer.')).toBeVisible();

  await conv.getByRole('button', { name: 'Turn actions' }).nth(1).click();
  await page.getByRole('menuitem', { name: 'Edit and resend…' }).click();
  await expect(box).toHaveValue('second question');
  await box.fill('second question, revised');
  await page.getByRole('button', { name: 'Rewind and send' }).click();
  await expect(conv.getByText('Revised answer.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText('Second answer.')).toHaveCount(0);
  await expect(conv.getByText('Opening answer.')).toBeVisible();
  await expect(strip).toHaveCount(0);
  await expect(box).toHaveValue('a draft set aside');

  expect(model.served().map((s) => s.fixture)).toEqual(['reply opening question', 'reply second question', 'reply second question, revised']);
  const revised = model.served()[2].request;
  // The rewound prompt and its reply are gone from what the model reads; the upload went again.
  expect(JSON.stringify(revised.messages)).not.toContain('Second answer.');
  expect(userParts(revised).join('\n')).toContain('context.txt body');
});
