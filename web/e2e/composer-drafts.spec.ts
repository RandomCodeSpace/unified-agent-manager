// What the composer holds before it is sent: drafts and attached files.
import type { Page } from '@playwright/test';
import { useVisionMock } from './composer-helpers';
import { conversation, expect, lastUserText, newThread, send, startThread, test, threadState } from './harness';

// A 2×2 PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAEElEQVR4nGP4z8AARAwQCgAf7gP9i18U1AAAAABJRU5ErkJggg==', 'base64');

/** Chooses `files` with the composer's Attach files button. */
async function attach(page: Page, files: { name: string; mimeType: string; buffer: Buffer }[]) {
  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: /^Attach files/ }).click();
  await (await chooser).setFiles(files);
}

const row = (page: Page, title: string) => page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button').filter({ hasText: title });

test('a Thread\'s unsent draft and its attached file survive a switch to another Thread and a reload', async ({ page, project, model }) => {
  model.reply('draft thread A', 'Reply in A.').title('draft thread A', 'Draft A');
  model.reply('draft thread B', 'Reply in B.').title('draft thread B', 'Draft B');
  await startThread(page, project, 'draft thread A');
  await expect(conversation(page).getByText('Reply in A.')).toBeVisible();
  const a = page.url();
  await newThread(page, project);
  await send(page, 'draft thread B');
  await expect(conversation(page).getByText('Reply in B.')).toBeVisible();
  await expect(row(page, 'Draft A')).toBeVisible();

  await row(page, 'Draft A').click();
  await expect(page).toHaveURL(a);
  const box = page.getByRole('textbox', { name: 'Message' });
  await box.fill('half a thought');
  await attach(page, [{ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('some notes\n') }]);
  const chip = page.getByRole('button', { name: 'Remove notes.txt' });
  await expect(chip).toBeVisible();
  // The draft is written shortly after the last change; wait until it holds the finished upload.
  const id = a.split('#task=')[1];
  await expect.poll(() => page.evaluate((k) => localStorage.getItem(k) ?? '', `uam.draft.${id}`)).toMatch(/half a thought.*notes\.txt|notes\.txt.*half a thought/);

  await row(page, 'Draft B').click();
  await expect(conversation(page).getByText('Reply in B.')).toBeVisible();
  await expect(box).toHaveValue('');
  await expect(chip).toHaveCount(0);
  await row(page, 'Draft A').click();
  await expect(conversation(page).getByText('Reply in A.')).toBeVisible();
  await expect(box).toHaveValue('half a thought');
  await expect(chip).toBeVisible();

  await page.reload();
  await expect(conversation(page).getByText('Reply in A.')).toBeVisible();
  await expect(box).toHaveValue('half a thought');
  await expect(chip).toBeVisible();
  expect(model.served().map((s) => s.fixture).filter((f) => f.startsWith('reply'))).toEqual(['reply draft thread A', 'reply draft thread B']);
});

test('attached files go with the message; the transcript shows the image and the file, also after a reload', async ({ page, uam, project, model }) => {
  const restore = await useVisionMock(uam);
  try {
    model.reply('first look', 'Ready.');
    model.reply('see attached', 'Got them.');
    await startThread(page, project, 'first look');
    await expect(threadState(page, 'Finished')).toBeVisible();

    await attach(page, [
      { name: 'shot.png', mimeType: 'image/png', buffer: PNG },
      { name: 'log.txt', mimeType: 'text/plain', buffer: Buffer.from('log line alpha\nlog line beta\n') },
    ]);
    await expect(page.getByRole('button', { name: 'Remove shot.png' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Remove log.txt' })).toBeVisible();
    const box = page.getByRole('textbox', { name: 'Message' });
    await box.fill('see attached');
    await box.press('Enter');
    await expect(conversation(page).getByText('Got them.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Remove shot.png' })).toHaveCount(0);

    const req = model.served().find((s) => s.fixture === 'reply see attached')!.request;
    const user = [...req.messages].reverse().find((m) => m.role === 'user')!;
    expect(JSON.stringify(user.content)).toContain('image_url');
    expect(JSON.stringify(req.messages)).toContain('log.txt');
    expect(lastUserText(req)).toContain('see attached');

    const conv = conversation(page);
    const thumb = conv.getByRole('img', { name: 'shot.png' });
    await expect(thumb).toBeVisible();
    await expect(conv.getByText('log.txt')).toBeVisible();
    await conv.getByRole('button', { name: 'Open shot.png' }).click();
    const viewer = page.getByRole('dialog');
    await expect(viewer.getByRole('img', { name: 'shot.png' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(viewer).toBeHidden();

    await page.reload();
    await expect(thumb).toBeVisible();
    await expect.poll(() => thumb.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(2);
    await expect(conv.getByText('log.txt')).toBeVisible();
  } finally {
    await restore();
  }
});
