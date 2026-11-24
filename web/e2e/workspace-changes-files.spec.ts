// How Changes shows each kind of file, and the review aids on its cards (docs/web.md, "Changes").
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, test, threadState } from './harness';
import { bash, chain, changesPanel, git, startYoloThread } from './workspace-helpers';

const commitFixtures = (dir: string, files: Record<string, string>) => {
  for (const [name, text] of Object.entries(files)) fs.writeFileSync(path.join(dir, name), text);
  git(dir, 'add', '--', ...Object.keys(files));
  git(dir, 'commit', '-q', '-m', 'fixtures');
};

test('renamed, binary, oversized, deleted, glob-named and risky files each read as what they are', async ({ page, project, model, workdir }) => {
  commitFixtures(workdir, {
    'old.txt': Array.from({ length: 20 }, (_, i) => `line ${i}`).join('\n') + '\n',
    'gone.txt': 'bye 1\nbye 2\n',
    'f[AB].txt': 'glob\n',
    'fA.txt': 'a\n',
  });
  const shell = [
    'git mv old.txt new.txt && echo extra >> new.txt',
    'rm gone.txt',
    "printf '\\000\\001\\002' > bin.dat",
    'echo more-a >> fA.txt',
    // Over 1 MiB of diff.
    "head -c 1200000 /dev/zero | tr '\\0' a | fold -w 100 > big.txt",
    'mkdir -p .github/workflows db/migrations auth',
    "echo 'on: push' > .github/workflows/ci.yml",
    'echo FROM scratch > Dockerfile',
    'echo {} > package-lock.json',
    'echo create > db/migrations/001_init.sql',
    'echo package auth > auth/token.go',
  ].join(' && ');
  model.toolRound('change every kind of file', [
    bash('call_mix', shell),
    // f[AB].txt is also a pathspec for fA.txt, which sorts first; its diff must name it alone.
    { id: 'call_glob', name: 'edit', arguments: { path: path.join(workdir, 'f[AB].txt'), old_str: 'glob', new_str: 'glob\nmore' } },
  ], 'Every kind changed.');
  await startYoloThread(page, project, 'change every kind of file');
  await expect(conversation(page).getByText('Every kind changed.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await page.getByRole('button', { name: /^Open changes, 11 files/ }).click();
  const panel = await changesPanel(page);
  const files = panel.getByRole('navigation', { name: 'Changed files' }).getByRole('button');
  const card = (name: string) => panel.getByRole('region', { name, exact: true });

  // Files that deserve a closer look come first, each with its reason.
  const risky = { '.github/workflows/ci.yml': 'CI workflow', 'Dockerfile': 'Docker', 'package-lock.json': 'Lockfile', 'db/migrations/001_init.sql': 'Migration', 'auth/token.go': 'Auth/security' };
  await expect(files).toHaveCount(11);
  const order = await files.evaluateAll((bs) => bs.map((b) => /^Go to (.*?), /.exec(b.getAttribute('aria-label') ?? '')?.[1]));
  expect(new Set(order.slice(0, 5))).toEqual(new Set(Object.keys(risky)));
  for (const [name, label] of Object.entries(risky)) await expect(card(name).getByText(label, { exact: true })).toBeVisible();
  for (const name of ['new.txt', 'fA.txt', 'f[AB].txt']) await expect(card(name).getByText(/^(CI workflow|Docker|Lockfile|Migration|Deploy|Auth\/security)$/)).toHaveCount(0);

  // A rename in the index names where it came from and counts only the changed line.
  await expect(panel.getByRole('button', { name: 'Go to new.txt, +1 −0', exact: true })).toBeVisible();
  await expect(card('new.txt')).toContainText('from old.txt');
  await expect(card('new.txt').getByRole('row', { name: /\+ extra$/ })).toBeVisible();
  await expect(card('new.txt')).toContainText('17 unchanged lines');
  await expect(card('new.txt').getByRole('row', { name: /\+ line \d+$/ })).toHaveCount(0);

  // An untracked binary file.
  await expect(panel.getByRole('button', { name: 'Go to bin.dat, binary', exact: true })).toBeVisible();
  await expect(card('bin.dat')).toContainText('binary');
  await expect(card('bin.dat').getByText('Binary file: no textual diff is available.')).toBeVisible();

  // A diff over the size limit.
  await expect(card('big.txt').getByText('The diff exceeded its size limit and is unavailable.')).toBeVisible();
  await expect(card('big.txt').getByText(/Could not parse diff/)).toHaveCount(0);

  // A deleted file: its removed lines, and nothing to open.
  await expect(panel.getByRole('button', { name: 'Go to gone.txt, +0 −2', exact: true })).toBeVisible();
  await expect(card('gone.txt').getByRole('button', { name: 'Open gone.txt' })).toBeDisabled();
  await expect(card('gone.txt').getByRole('row', { name: /- bye 1$/ })).toBeVisible();
  await expect(card('gone.txt').getByRole('row', { name: /- bye 2$/ })).toBeVisible();
  await expect(card('new.txt').getByRole('button', { name: 'Open new.txt' })).toBeEnabled();

  // A tracked file named like a glob diffs itself alone.
  await expect(card('f[AB].txt').getByRole('row', { name: /\+ more$/ })).toBeVisible();
  await expect(card('f[AB].txt')).not.toContainText('more-a');
  await expect(card('fA.txt').getByRole('row', { name: /\+ more-a$/ })).toBeVisible();
});

test('a card reverts one hunk, keeps Viewed and comments over a reload, loads its diff when unfolded, and the panel rereads and resizes', async ({ page, project, model, workdir }) => {
  const file = path.join(workdir, 'file.txt');
  const notes = path.join(workdir, 'notes.txt');
  commitFixtures(workdir, { 'file.txt': Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join('\n') + '\n' });
  chain(model, 'edit two places', [
    [{ id: 'call_top', name: 'edit', arguments: { path: file, old_str: 'line 2\n', new_str: 'LINE 2\n' } }, { id: 'call_notes', name: 'create', arguments: { path: notes, file_text: 'note one\n' } }],
    [{ id: 'call_bottom', name: 'edit', arguments: { path: file, old_str: 'line 28\n', new_str: 'LINE 28\n' } }],
  ], 'Edited two places.');
  await startYoloThread(page, project, 'edit two places');
  await expect(conversation(page).getByText('Edited two places.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const open = page.getByRole('button', { name: /^Open changes, 2 files/ });
  await open.click();
  let panel = await changesPanel(page);
  const card = (name: string) => panel.getByRole('region', { name, exact: true });

  // Revert on one hunk puts back that hunk alone.
  const reverts = card('file.txt').getByRole('button', { name: 'Revert', exact: true });
  await expect(reverts).toHaveCount(2);
  await reverts.nth(1).click();
  await expect.poll(() => fs.readFileSync(file, 'utf8').split('\n')[27]).toBe('line 28');
  expect(fs.readFileSync(file, 'utf8').split('\n')[1]).toBe('LINE 2');
  await expect(reverts).toHaveCount(1);
  await expect(card('file.txt').getByRole('row', { name: /\+ LINE 28$/ })).toHaveCount(0);

  // Viewed folds the card; a line comment collects at the bottom.
  await card('file.txt').getByRole('checkbox', { name: 'Viewed file.txt' }).click();
  await expect(card('file.txt').getByRole('button', { name: 'Show the diff of file.txt' })).toHaveAttribute('aria-expanded', 'false');
  await expect(card('file.txt').getByRole('row')).toHaveCount(0);
  await card('notes.txt').getByRole('button', { name: 'Comment on line 1' }).click();
  await card('notes.txt').getByRole('textbox', { name: 'Comment on line 1' }).fill('Reword this note.');
  await card('notes.txt').getByRole('button', { name: 'Add comment' }).click();
  await expect(panel.getByRole('region', { name: 'Review comments' }).getByRole('listitem')).toHaveText('notes.txt:1 Reword this note.');

  await page.reload();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await open.click();
  panel = await changesPanel(page);
  await expect(card('file.txt').getByRole('checkbox', { name: 'Viewed file.txt' })).toBeChecked();
  await expect(card('notes.txt').getByRole('checkbox', { name: 'Viewed notes.txt' })).not.toBeChecked();
  await expect(panel.getByRole('region', { name: 'Review comments' }).getByRole('listitem')).toHaveText('notes.txt:1 Reword this note.');
  await expect(card('notes.txt').getByText('Reword this note.')).toBeVisible();
  await expect(panel.getByText('1 of 2 viewed')).toBeVisible();

  // A folded card has no diff until it is unfolded.
  await expect(card('file.txt').getByRole('row')).toHaveCount(0);
  const diffRead = page.waitForRequest((r) => r.url().includes('/changes/file?') && new URL(r.url()).searchParams.get('path') === 'file.txt');
  await card('file.txt').getByRole('button', { name: 'Show the diff of file.txt' }).click();
  await diffRead;
  await expect(card('file.txt').getByRole('row', { name: /\+ LINE 2$/ })).toBeVisible();

  // Open on the visible Thread, the list is read again without Refresh.
  fs.appendFileSync(notes, 'note two\n');
  await expect(card('notes.txt').getByRole('row', { name: /\+ note two$/ })).toBeVisible();
  await expect(panel.getByRole('button', { name: 'Go to notes.txt, +2 −0', exact: true })).toBeVisible();
  // And at once when the tab becomes visible again.
  const reads = await page.evaluate(() => {
    const doc = document as Document & { visibilityState: DocumentVisibilityState };
    const calls: string[] = [];
    Object.defineProperty(doc, 'visibilityState', { configurable: true, get: () => 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    const fetch = window.fetch;
    window.fetch = (input, init) => {
      calls.push(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url);
      return fetch(input, init);
    };
    Object.defineProperty(doc, 'visibilityState', { configurable: true, get: () => 'visible' });
    document.dispatchEvent(new Event('visibilitychange'));
    window.fetch = fetch;
    delete (doc as Partial<typeof doc>).visibilityState;
    return calls;
  });
  expect(reads.filter((u) => u.includes('/changes?scope=task'))).toHaveLength(1);

  // The inner edge resizes the panel by keyboard and by drag, double-click resets, and the width is kept.
  const handle = () => panel.getByRole('separator', { name: 'Resize panel' });
  const start = Number(await handle().getAttribute('aria-valuenow'));
  const width = () => panel.evaluate((el) => Math.round(el.getBoundingClientRect().width));
  expect(await width()).toBe(start);
  await handle().focus();
  await page.keyboard.press('ArrowRight');
  await expect(handle()).toHaveAttribute('aria-valuenow', String(start - 16));
  await expect.poll(width).toBe(start - 16);
  const box = (await handle().boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 60, box.y + box.height / 2, { steps: 4 });
  await page.mouse.up();
  await expect(handle()).toHaveAttribute('aria-valuenow', String(start - 76));
  await expect.poll(width).toBe(start - 76);
  await page.reload();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await open.click();
  panel = await changesPanel(page);
  await expect(handle()).toHaveAttribute('aria-valuenow', String(start - 76));
  await expect.poll(width).toBe(start - 76);
  await handle().dblclick();
  await expect(handle()).toHaveAttribute('aria-valuenow', String(start));
  await expect.poll(width).toBe(start);
});

test("an edit call's recorded diff in the transcript shows 400 rows until Show all", async ({ page, project, model, workdir }) => {
  const file = path.join(workdir, 'long.txt');
  model.toolRound('create a long file', [{ id: 'call_long', name: 'create', arguments: { path: file, file_text: Array.from({ length: 500 }, (_, i) => `row ${i + 1}`).join('\n') + '\n' } }], 'Long file created.');
  await startYoloThread(page, project, 'create a long file');
  const conv = conversation(page);
  await expect(conv.getByText('Long file created.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await conv.getByRole('button', { name: '1 file changed. Show the activity of this turn' }).click();
  await conv.getByRole('button', { name: /^Created long\.txt , \+500 −0/ }).click();
  await conv.getByRole('button', { name: `${file} +500 −0 create diff` }).click();
  const diff = conv.getByRole('region', { name: `Diff for ${file} +500 −0` });
  // The hunk's head and 399 of its lines.
  await expect(diff.getByRole('row')).toHaveCount(400);
  await expect(diff.getByRole('row', { name: /\+row 399$/ })).toHaveCount(1);
  await expect(diff.getByRole('row', { name: /\+row 400$/ })).toHaveCount(0);
  await diff.getByRole('button', { name: 'Show all 501 lines' }).click();
  await expect(diff.getByRole('row')).toHaveCount(501);
  await expect(diff.getByRole('row', { name: /\+row 500$/ })).toHaveCount(1);
  await expect(diff.getByRole('button', { name: /^Show all/ })).toHaveCount(0);
});

test('Changes lists at most 1000 changed files and says so', async ({ page, project, model }) => {
  // Copilot's session tracking records none of a shell write this big, so the files read as the folder's other changes.
  model.toolRound('write many files', [bash('call_many', 'mkdir many && for i in $(seq 1001 2005); do echo $i > many/f$i.txt; done')], 'Wrote many files.');
  await startYoloThread(page, project, 'write many files');
  await expect(conversation(page).getByText('Wrote many files.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await page.getByRole('button', { name: /^Open changes/ }).click();
  const panel = await changesPanel(page);
  await expect(panel.getByText('Showing the first 1000 changed files.')).toBeVisible();
  const other = panel.getByRole('region', { name: 'Also changed in this folder' }).getByRole('listitem');
  await expect(other).toHaveCount(1000);
  await expect(other.filter({ hasText: 'many/f2000.txt' })).toHaveCount(1);
  await expect(other.filter({ hasText: 'many/f2001.txt' })).toHaveCount(0);
});
