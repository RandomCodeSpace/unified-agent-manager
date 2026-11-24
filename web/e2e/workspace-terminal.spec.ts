// The web terminal (docs/web.md, "Terminal"). It draws with WebGL, so its text is
// not in the page: each case has the shell write files and reads them from disk.
import fs from 'node:fs';
import path from 'node:path';
import { type Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState, type Project, type Model } from './harness';
import { startWorktreeThread } from './workspace-helpers';

/** Opens a finished Thread in `project` to hang the terminal on. */
async function openThread(page: Page, project: Project, model: Model, prompt: string) {
  model.reply(prompt, `Reply to ${prompt}.`);
  await startThread(page, project, prompt);
  await expect(conversation(page).getByText(`Reply to ${prompt}.`)).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
}

/** Opens the terminal from the header and waits for its shell. */
async function openTerminal(page: Page) {
  await page.getByRole('button', { name: 'Terminal', exact: true }).click();
  const dock = page.getByRole('region', { name: 'Terminal', exact: true });
  await expect(dock.getByText('Connected', { exact: true })).toBeVisible();
  return dock;
}

/** Types `command` and Enter into the terminal. */
async function run(dock: ReturnType<Page['getByRole']>, command: string) {
  await dock.getByRole('textbox').focus();
  await dock.page().keyboard.type(command);
  await dock.page().keyboard.press('Enter');
}

/** The text of `file` once the shell has written a full line to it. */
const written = async (file: string) => {
  await expect.poll(() => fs.existsSync(file) && fs.readFileSync(file, 'utf8').endsWith('\n')).toBe(true);
  return fs.readFileSync(file, 'utf8').trim();
};

const alive = (pid: number) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

test('the Terminal setting gates the header button, and turning it off ends the open shell', async ({ page, uam, project, model, workdir }) => {
  try {
    await openThread(page, project, model, 'gate the terminal');
    const button = page.getByRole('button', { name: 'Terminal', exact: true });
    await expect(button).toHaveCount(0);

    const toggle = async (on: boolean) => {
      await page.goto('/#settings');
      await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'UAM and access' }).click();
      const terminal = page.getByRole('switch', { name: 'Terminal' });
      await terminal.setChecked(on);
      await expect(terminal).toBeChecked({ checked: on });
      await page.goBack();
      await expect(threadState(page, 'Finished')).toBeVisible();
    };
    await toggle(true);
    await expect(button).toBeVisible();
    const dock = await openTerminal(page);
    const pidFile = path.join(workdir, 'gate-pid.txt');
    await run(dock, `sleep 300 & echo $! > ${pidFile}`);
    const pid = Number(await written(pidFile));
    expect(alive(pid)).toBe(true);

    await toggle(false);
    await expect.poll(() => alive(pid)).toBe(false);
    await expect(button).toHaveCount(0);
    await expect(dock).toHaveCount(0);
    expect(await uam.api('GET', '/api/settings')).toMatchObject({ terminal: false });
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('typed commands run in a real shell in the Project folder, and keys stay out of the composer', async ({ page, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    await openThread(page, project, model, 'type in the terminal');
    const dock = await openTerminal(page);
    await expect(dock).toContainText(workdir);
    await run(dock, 'pwd > pwd.txt; echo ok-$((6*7)) > m.txt');
    expect(await written(path.join(workdir, 'm.txt'))).toBe('ok-42');
    expect(await written(path.join(workdir, 'pwd.txt'))).toBe(workdir);
    await expect(page.getByRole('textbox', { name: 'Message' })).toHaveValue('');
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('Close and Restart ask first, a reload ends the shell, and exit shows its code', async ({ page, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    await openThread(page, project, model, 'end the terminal');
    let dock = await openTerminal(page);
    const shellOf = async (name: string) => {
      const file = path.join(workdir, name);
      await run(dock, `echo $$ > ${file}`);
      return Number(await written(file));
    };
    const pidFile = path.join(workdir, 'child-pid.txt');
    await run(dock, `sleep 300 & echo $! > ${pidFile}`);
    const child = Number(await written(pidFile));
    const first = await shellOf('shell-1.txt');

    await dock.getByRole('button', { name: 'Close terminal' }).click();
    const close = page.getByRole('alertdialog', { name: 'Close the terminal?' });
    await close.getByRole('button', { name: 'Cancel' }).click();
    await expect(close).toBeHidden();
    await expect(dock.getByText('Connected', { exact: true })).toBeVisible();
    expect(await shellOf('shell-1b.txt')).toBe(first);

    await dock.getByRole('button', { name: 'Restart' }).click();
    const restart = page.getByRole('alertdialog', { name: 'Restart the terminal?' });
    await restart.getByRole('button', { name: 'Restart' }).click();
    await expect(restart).toBeHidden();
    await expect.poll(() => alive(first)).toBe(false);
    await expect(dock.getByText('Connected', { exact: true })).toBeVisible();
    const second = await shellOf('shell-2.txt');
    expect(second).not.toBe(first);
    await expect.poll(() => alive(child)).toBe(false);

    await page.reload();
    await expect(threadState(page, 'Finished')).toBeVisible();
    await expect.poll(() => alive(second)).toBe(false);
    dock = await openTerminal(page);
    expect(await shellOf('shell-3.txt')).not.toBe(second);

    await run(dock, 'exit 3');
    await expect(dock.getByText('Exited (code 3)', { exact: true })).toBeVisible();
    await dock.getByRole('button', { name: 'Close terminal' }).click();
    await expect(dock).toHaveCount(0);
    await expect(page.getByRole('alertdialog')).toHaveCount(0);
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('a terminal opened from a worktree Thread runs in the worktree', async ({ page, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    model.reply('a worktree to open a shell in', 'Ready in the worktree.');
    const tree = await startWorktreeThread(page, project, 'a worktree to open a shell in');
    await expect(conversation(page).getByText('Ready in the worktree.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const dock = await openTerminal(page);
    await expect(dock).toContainText(tree.path);
    await run(dock, '(pwd; git branch --show-current) > pwd.tmp && mv pwd.tmp shell-pwd.txt');
    expect(await written(path.join(tree.path, 'shell-pwd.txt'))).toBe(`${tree.path}\n${tree.branch}`);
    expect(fs.existsSync(path.join(workdir, 'shell-pwd.txt'))).toBe(false);
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('the dock resizes by its top edge and by keyboard, and the shell gets the new size', async ({ page, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    await openThread(page, project, model, 'resize the terminal');
    const dock = await openTerminal(page);
    // The shell reports the terminal's size when asked, after a resize has finished: nothing is typed while the dock resizes.
    const file = path.join(workdir, 'size.txt');
    const size = async () => {
      fs.rmSync(file, { force: true });
      await run(dock, `stty size > size.tmp && mv size.tmp ${file}`);
      const [rows, cols] = (await written(file)).split(' ').map(Number);
      return { rows, cols };
    };
    await expect.poll(async () => (await size()).rows).toBeGreaterThan(0);
    const first = await size();
    const handle = dock.getByRole('separator', { name: 'Resize panel' });
    const height = Number(await handle.getAttribute('aria-valuenow'));
    await handle.focus();
    for (let i = 0; i < 4; i++) await page.keyboard.press('ArrowUp');
    await expect(handle).toHaveAttribute('aria-valuenow', String(height + 64));
    await expect.poll(async () => (await size()).rows).toBeGreaterThan(first.rows);
    const taller = await size();
    expect(taller.cols).toBe(first.cols);

    const box = (await handle.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2 + 120, { steps: 4 });
    await page.mouse.up();
    await expect(handle).toHaveAttribute('aria-valuenow', String(height + 64 - 120));
    await expect.poll(async () => (await size()).rows).toBeLessThan(first.rows);
    expect((await size()).cols).toBe(first.cols);
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('keys typed in the terminal, Esc and uam shortcuts included, go to the shell', async ({ page, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    await openThread(page, project, model, 'keys in the terminal');
    const url = page.url();
    const dock = await openTerminal(page);
    const file = path.join(workdir, 'keys.txt');
    await run(dock, `cat -v > ${file}`);
    await page.keyboard.press('Escape');
    await page.keyboard.press('Alt+n');
    await page.keyboard.type('end');
    await page.keyboard.press('Enter');
    await page.keyboard.press('Control+d');
    // cat -v writes Esc as ^[; Alt+N reaches the shell as Esc n, not as uam's New thread.
    expect(await written(file)).toBe('^[^[nend');
    await expect(dock.getByText('Connected', { exact: true })).toBeVisible();
    await expect(page).toHaveURL(url);
    await expect(page.getByRole('dialog', { name: 'New thread' })).toHaveCount(0);
    await expect(page.getByRole('textbox', { name: 'Message' })).toHaveValue('');
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('the mouse and Ctrl+Shift+C/V work the clipboard, and a refused clipboard says so', async ({ page, context, uam, project, model, workdir }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    await openThread(page, project, model, 'clipboard in the terminal');
    const dock = await openTerminal(page);
    const box = (await dock.boundingBox())!;
    // The screen starts under the 36 px header, 8 px in; a row is 17.5 px.
    const at = (col: number, row: number) => ({ x: box.x + 8 + col * 8.4 + 4, y: box.y + 36 + row * 17.5 + 8 });

    // Without the browser's leave to read the clipboard, a right-click says so.
    await page.mouse.click(at(2, 2).x, at(2, 2).y, { button: 'right' });
    await expect(dock.getByText('Clipboard blocked. Use Ctrl+Shift+V to paste.', { exact: true })).toBeVisible();

    await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: uam.url });
    const clip = (text: string) => page.evaluate((t) => navigator.clipboard.writeText(t), text);
    // A click pastes once the clipboard read resolves; Enter must come after the text.
    await page.evaluate(() => {
      const w = window as Window & { pastes?: number };
      const read = navigator.clipboard.readText.bind(navigator.clipboard);
      w.pastes = 0;
      navigator.clipboard.readText = async () => {
        const text = await read();
        setTimeout(() => { w.pastes!++; }, 0);
        return text;
      };
    });
    const pastes = () => page.evaluate(() => (window as Window & { pastes?: number }).pastes);
    const pasted = async (how: () => Promise<void>, name: string, clicked = true) => {
      await clip(`echo ${name} > ${name}.txt`);
      const before = await pastes();
      await how();
      if (clicked) await expect.poll(pastes).toBe(before! + 1);
      await page.keyboard.press('Enter');
      expect(await written(path.join(workdir, `${name}.txt`))).toBe(name);
    };
    await pasted(() => page.mouse.click(at(2, 2).x, at(2, 2).y, { button: 'right' }), 'right');
    await pasted(() => page.mouse.click(at(2, 2).x, at(2, 2).y, { button: 'middle' }), 'middle');
    await pasted(async () => {
      await dock.getByRole('textbox').focus();
      await page.keyboard.press('Control+Shift+V');
    }, 'keyed', false);

    // Releasing a selection copies it.
    await run(dock, "printf '\\033[2J\\033[HCOPYME'");
    await clip('');
    await page.mouse.move(at(0, 0).x - 3, at(0, 0).y);
    await page.mouse.down();
    await page.mouse.move(at(5, 0).x + 3, at(5, 0).y, { steps: 4 });
    await page.mouse.up();
    await expect(dock.getByText('Copied', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('COPYME');
    await clip('');
    await dock.getByRole('textbox').focus();
    await page.keyboard.press('Control+Shift+C');
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('COPYME');
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('at most eight terminals run at once; the ninth says so and opens once one closes', async ({ page, context, uam, project, model }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  const pages = [page];
  try {
    await openThread(page, project, model, 'eight terminals');
    const url = page.url();
    for (let i = 0; i < 8; i++) pages.push(await context.newPage());
    for (const p of pages.slice(1)) await p.goto(url);
    for (const p of pages.slice(0, 8)) {
      await expect(threadState(p, 'Finished')).toBeVisible();
      await openTerminal(p);
    }
    const ninth = pages[8];
    await expect(threadState(ninth, 'Finished')).toBeVisible();
    await ninth.getByRole('button', { name: 'Terminal', exact: true }).click();
    const refused = ninth.getByRole('region', { name: 'Terminal', exact: true }).getByRole('alert');
    await expect(refused).toContainText('Could not open a terminal.');

    await pages[0].close();
    await refused.getByRole('button', { name: 'Retry' }).click();
    await expect(ninth.getByRole('region', { name: 'Terminal', exact: true }).getByText('Connected', { exact: true })).toBeVisible();
  } finally {
    for (const p of pages.slice(1)) await p.close();
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});

test('without WebGL the dock says the terminal needs it', async ({ page, uam, project, model }) => {
  await uam.api('PATCH', '/api/settings', { terminal: true });
  try {
    // As a browser with WebGL turned off answers.
    await page.addInitScript(() => {
      const get = HTMLCanvasElement.prototype.getContext;
      HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, id: string, options?: unknown) {
        return id === 'webgl2' || id === 'webgl' ? null : get.call(this, id, options as never);
      } as typeof get;
    });
    await openThread(page, project, model, 'no webgl here');
    await page.getByRole('button', { name: 'Terminal', exact: true }).click();
    const dock = page.getByRole('region', { name: 'Terminal', exact: true });
    await expect(dock.getByRole('alert')).toHaveText('The terminal needs WebGL, which this browser has turned off.');
    await expect(dock.getByText('Connected', { exact: true })).toHaveCount(0);
  } finally {
    await uam.api('PATCH', '/api/settings', { terminal: false });
  }
});
