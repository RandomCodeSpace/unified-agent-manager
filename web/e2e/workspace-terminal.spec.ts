// The web terminal (docs/web.md, "Terminal"). It draws with WebGL, so its text is
// not in the page: each case has the shell write files and reads them from disk.
import fs from 'node:fs';
import path from 'node:path';
import { type Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState, type Project, type Model } from './harness';

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
