// Permission requests: what the card shows, session approval, yolo.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { pickMode, script } from './activity-helpers';

test('a file write asks with its diff and the path once; a long reason is cut', async ({ page, project, model, workdir }) => {
  const reason = `Because ${'the marker must exist '.repeat(30)}END-OF-REASON`;
  script(model, { userMessage: 'write then run' }, [
    [{ id: 'call_write', name: 'create', arguments: { path: path.join(workdir, 'proposed.txt'), file_text: 'first proposed line\nsecond proposed line\n' } }],
    [{ id: 'call_reason', name: 'bash', arguments: { command: 'printf r > reasoned.txt', description: reason } }],
    'Wrote and ran.',
  ]);
  await startThread(page, project, 'write then run');
  // Regression: the CLI's patch starts with a blank line, and the card showed it raw, headers and all.
  const card = conversation(page).getByRole('group', { name: 'Write file' });
  const change = card.getByRole('region', { name: 'Proposed change' });
  await expect(change.getByRole('cell', { name: '+first proposed line' })).toBeVisible();
  await expect(change.getByRole('cell', { name: '+second proposed line' })).toBeVisible();
  // The path stands once, as the diff's caption, with none of the patch's own headers.
  await expect(card.getByText(path.join(workdir, 'proposed.txt'))).toHaveCount(1);
  await expect(card).not.toContainText('+++ b/');
  expect(fs.existsSync(path.join(workdir, 'proposed.txt'))).toBe(false);
  await card.getByRole('button', { name: 'Allow once' }).click();

  const shell = conversation(page).getByRole('group', { name: 'Run shell command' });
  const shown = shell.getByText(/^Because the marker must exist/);
  await expect(shown).toBeVisible();
  const text = (await shown.textContent()) ?? '';
  expect(text).not.toContain('END-OF-REASON');
  expect(text.length).toBeGreaterThan(250);
  expect(text.length).toBeLessThanOrEqual(310);
  await shell.getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Wrote and ran.')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'proposed.txt'), 'utf8')).toBe('first proposed line\nsecond proposed line\n');
});

test('Allow for this session, where Copilot offers it, becomes the main button after two Allow once, and then covers the next request', async ({ page, project, model, workdir }) => {
  const write = (n: number) => [{ id: `call_w${n}`, name: 'create', arguments: { path: path.join(workdir, `s${n}.txt`), file_text: `s${n}\n` } }];
  script(model, { userMessage: 'four writes' }, [write(1), write(2), write(3), write(4), 'Four written.']);
  // The CLI ends the turn on a denial without asking the model again.
  model.on({ userMessage: 'fetch a page' }, { toolCalls: [{ id: 'call_fetch', name: 'web_fetch', arguments: JSON.stringify({ url: 'https://example.invalid/page' }) }] }, {}, 'fetch');
  await startThread(page, project, 'fetch a page');
  const conv = conversation(page);
  // Copilot offers no session approval for a URL, and uam adds none.
  const fetch = conv.getByRole('group', { name: 'Fetch URL' });
  await expect(fetch.getByRole('button', { name: 'Allow once' })).toBeVisible();
  await expect(fetch.getByRole('button', { name: 'Allow for this session' })).toHaveCount(0);
  await fetch.getByRole('button', { name: 'Deny' }).click();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'four writes');
  const card = (n: number) => conv.getByRole('group', { name: 'Write file' }).filter({ hasText: `s${n}.txt` });
  for (const n of [1, 2]) {
    await expect(card(n).getByRole('button', { name: 'Allow for this session' })).toBeVisible();
    // The main button stands last, at the right.
    await expect(card(n).getByRole('button').last()).toHaveText('Allow once');
    await card(n).getByRole('button', { name: 'Allow once' }).click();
  }
  await expect(card(3).getByRole('button', { name: 'Allow once' })).toBeVisible();
  await expect(card(3).getByRole('button').last()).toHaveText('Allow for this session');
  await card(3).getByRole('button', { name: 'Allow for this session' }).click();
  // The session approval covers the fourth write: no card.
  await expect(conv.getByText('Four written.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 's4.txt'), 'utf8')).toBe('s4\n');
});

test('switching to yolo allows the waiting request; later requests run with no card and read auto-approved', async ({ page, project, model, workdir }) => {
  model.toolRound('wait for yolo', [{ id: 'call_wait', name: 'bash', arguments: { command: 'printf one > one.txt', description: 'Write one' } }], 'One written.');
  model.toolRound('run in yolo', [{ id: 'call_yolo', name: 'bash', arguments: { command: 'printf two > two.txt', description: 'Write two' } }], 'Two written.');
  await startThread(page, project, 'wait for yolo');
  const cards = conversation(page).getByRole('group', { name: 'Run shell command' });
  await expect(cards).toBeVisible();
  await expect(threadState(page, 'Needs permission')).toBeVisible();
  await pickMode(page, 'Yolo');
  await expect(conversation(page).getByText('One written.')).toBeVisible();
  await expect(cards).toHaveCount(0);
  expect(fs.readFileSync(path.join(workdir, 'one.txt'), 'utf8')).toBe('one');

  // Counts every change of the page that leaves a permission card's button in it, from here on.
  await page.evaluate(() => {
    const w = window as unknown as { cardsSeen: number };
    w.cardsSeen = 0;
    new MutationObserver(() => {
      if ([...document.querySelectorAll('button')].some((b) => b.textContent === 'Allow once')) w.cardsSeen++;
    }).observe(document.body, { childList: true, subtree: true });
  });
  await send(page, 'run in yolo');
  await expect(conversation(page).getByText('Two written.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'two.txt'), 'utf8')).toBe('two');
  expect(await page.evaluate(() => (window as unknown as { cardsSeen: number }).cardsSeen)).toBe(0);
  await conversation(page).getByRole('button', { name: '1 command. Show the activity of this turn' }).last().click();
  await expect(conversation(page).getByRole('button', { name: 'Ran printf two > two.txt , ok , auto-approved' })).toBeVisible();
});

test('Assisted is refused with the reason while its reviewer model is not offered; the Thread stays in Safe and still asks', async ({ page, project, model, workdir }) => {
  model.reply('warm assisted', 'Warm.');
  // The CLI ends the turn on a denial without asking the model again.
  model.on({ userMessage: 'still asks me' }, { toolCalls: [{ id: 'call_asks', name: 'bash', arguments: JSON.stringify({ command: 'printf a > assisted.txt', description: 'Write it' }) }] }, {}, 'asks');
  await startThread(page, project, 'warm assisted');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await pickMode(page, 'Assisted');
  await expect(page.getByRole('alert').filter({ hasText: /^assisted permissions need the .+ reviewer model, which this account does not offer$/ })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Permissions and execution: Safe · Interactive' })).toBeVisible();

  await send(page, 'still asks me');
  const card = conversation(page).getByRole('group', { name: 'Run shell command' });
  await expect(card.getByRole('button', { name: 'Allow once' })).toBeVisible();
  await card.getByRole('button', { name: 'Deny' }).click();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.existsSync(path.join(workdir, 'assisted.txt'))).toBe(false);
});
