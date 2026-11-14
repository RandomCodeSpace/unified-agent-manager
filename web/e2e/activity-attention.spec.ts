// Where the agent needs the user: questions, Needs you across Threads, one Thread in two pages, a dropped stream.
import fs from 'node:fs';
import path from 'node:path';
import type { WebSocketRoute } from '@playwright/test';
import { conversation, expect, newThread, send, startThread, test, threadState, toolResult } from './harness';
import { script } from './activity-helpers';

test('questions: Enter sends the recommended choice, several choices go together, and Decline declines', async ({ page, project, model }) => {
  const ask = (id: string, question: string, choices: string[]) => [{ id, name: 'ask_user', arguments: { question, choices } }];
  script(model, { userMessage: 'ask the colour' }, [ask('call_colour', 'Which colour?', ['Blue (Recommended)', 'Red']), 'Colour noted.']);
  script(model, { userMessage: 'ask the files' }, [ask('call_files', 'Which files? (Choose any that apply)', ['a.txt', 'b.txt', 'c.txt']), 'Files noted.']);
  script(model, { userMessage: 'ask the size' }, [ask('call_size', 'Which size?', ['Small', 'Large']), 'No size then.']);
  await startThread(page, project, 'ask the colour');
  const answer = page.getByRole('textbox', { name: 'Your answer' });
  await expect(page.getByRole('group', { name: 'Which colour?' }).getByRole('radio', { name: 'Blue (Recommended)' })).toBeChecked();
  await expect(answer).toBeFocused();
  await answer.press('Enter');
  await expect(conversation(page).getByText('Colour noted.')).toBeVisible();
  expect(toolResult(model, 'call_colour')).toContain('Blue');

  await send(page, 'ask the files');
  const files = page.getByRole('group', { name: 'Which files?' });
  await files.getByRole('checkbox', { name: 'a.txt' }).check();
  await files.getByRole('checkbox', { name: 'c.txt' }).check();
  await page.getByRole('button', { name: 'Answer', exact: true }).click();
  await expect(conversation(page).getByText('Files noted.')).toBeVisible();
  const picked = toolResult(model, 'call_files') ?? '';
  expect(picked).toContain('a.txt');
  expect(picked).toContain('c.txt');
  expect(picked).not.toContain('b.txt');

  await send(page, 'ask the size');
  await expect(threadState(page, 'Needs answer')).toBeVisible();
  await page.getByRole('button', { name: 'Decline' }).click();
  await expect(conversation(page).getByText('No size then.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(toolResult(model, 'call_size')).not.toMatch(/Small|Large/);
});

test('Needs you: a Thread waiting on permission is flagged in the sidebar while another Thread is open, and clears once answered', async ({ page, project, model, workdir }) => {
  model.toolRound('needs a permission', [{ id: 'call_perm', name: 'bash', arguments: { command: 'printf hi > hi.txt', description: 'Say hi' } }], 'Said hi.')
    .title('needs a permission', 'Waiting thread');
  model.reply('meanwhile hello', 'Hello meanwhile.').title('meanwhile hello', 'Other thread');
  await startThread(page, project, 'needs a permission');
  await expect(threadState(page, 'Needs permission')).toBeVisible();

  await newThread(page, project);
  await send(page, 'meanwhile hello');
  await expect(conversation(page).getByText('Hello meanwhile.')).toBeVisible();
  const rows = page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${project.name} `) });
  const waiting = rows.filter({ hasText: 'Waiting thread' });
  await expect(waiting).toHaveAccessibleName(/ Needs permission Waiting thread /);
  await expect(rows.filter({ hasText: 'Other thread' })).toHaveAccessibleName(/ Finished Other thread /);
  await expect(page.getByRole('button', { name: 'Hide sidebar, 1 need you' })).toBeVisible();

  await waiting.click();
  await conversation(page).getByRole('group', { name: 'Run shell command' }).getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Said hi.')).toBeVisible();
  await expect(waiting).toHaveAccessibleName(/ Finished Waiting thread /);
  await expect(page.getByRole('button', { name: 'Hide sidebar', exact: true })).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'hi.txt'), 'utf8')).toBe('hi');
});

// Product bug: once allowed, the older Thread's row jumps above the newer one until a reload (docs/web.md: creation order, whatever their state).
test.fixme('a sidebar row keeps its creation-order place when its Thread starts working again', async ({ page, project, model }) => {
  model.toolRound('older needs a permission', [{ id: 'call_older', name: 'bash', arguments: { command: 'printf hi > older.txt', description: 'Say hi' } }], 'Older done.')
    .title('older needs a permission', 'Older thread');
  model.reply('newer hello', 'Newer done.').title('newer hello', 'Newer thread');
  await startThread(page, project, 'older needs a permission');
  await expect(threadState(page, 'Needs permission')).toBeVisible();
  await newThread(page, project);
  await send(page, 'newer hello');
  await expect(conversation(page).getByText('Newer done.')).toBeVisible();
  const rows = page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${project.name} `) });
  await expect(rows.nth(0)).toContainText('Newer thread');
  await rows.filter({ hasText: 'Older thread' }).click();
  await page.getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Older done.')).toBeVisible();
  await expect(rows.nth(0)).toContainText('Newer thread');
  await expect(rows.nth(1)).toContainText('Older thread');
});

test('the same Thread in two pages: both stream, a permission answered in one leaves the other, and a third page still loads', async ({ page, context, project, model }) => {
  const lines = Array.from({ length: 12 }, (_, i) => `shared line ${i + 1}`);
  model.reply('warm both', 'Both open.');
  model.on({ predicate: (req) => req.messages.findLast((m) => m.role === 'assistant')?.tool_calls?.some((c) => c.id === 'call_both') ?? false }, { content: 'Both done.' }, {}, 'after both');
  model.on({ userMessage: 'stream to both' }, {
    content: lines.join('\n\n'),
    toolCalls: [{ id: 'call_both', name: 'bash', arguments: JSON.stringify({ command: 'printf both > both.txt', description: 'Write both' }) }],
  }, { latency: 300, chunkSize: lines[0].length + 2 }, 'stream to both');
  const url = await startThread(page, project, 'warm both');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const other = await context.newPage();
  await other.goto(url);
  await expect(conversation(other).getByText('Both open.')).toBeVisible();

  await send(page, 'stream to both');
  // The second page shows the reply while it still streams.
  await expect(conversation(other).getByText('shared line 3', { exact: true })).toBeVisible();
  await expect(conversation(other).getByText('shared line 12', { exact: true })).toHaveCount(0);
  const card = (p: typeof page) => conversation(p).getByRole('group', { name: 'Run shell command' });
  await expect(card(page)).toBeVisible();
  await card(other).getByRole('button', { name: 'Allow once' }).click();
  await expect(card(page)).toHaveCount(0);
  for (const p of [page, other]) {
    await expect(conversation(p).getByText('Both done.')).toBeVisible();
    await expect(conversation(p).getByText('shared line 12', { exact: true })).toHaveCount(1);
  }
  const answers = model.served().flatMap((s) => s.request.messages.filter((m) => m.role === 'tool' && m.tool_call_id === 'call_both'));
  expect(answers).toHaveLength(1);

  const third = await context.newPage();
  await third.goto('/#settings');
  await expect(third.getByRole('navigation', { name: 'Settings sections' })).toBeVisible();
  await third.close();
  await other.close();
});

test('a dropped live connection shows the reconnect notice, and the reply completes once', async ({ page, project, model }) => {
  const parts = Array.from({ length: 16 }, (_, i) => `part ${String(i + 1).padStart(2, '0')}`);
  model.reply('long reply', parts.join('\n\n'), { latency: 400, chunkSize: parts[0].length + 2 });
  const sockets: WebSocketRoute[] = [];
  let down = false;
  await page.routeWebSocket(/\/api\/events\/socket/, (ws) => {
    // While down, the page's reconnects are refused, so the notice stays up long enough to read.
    if (down) return ws.close();
    ws.connectToServer();
    sockets.push(ws);
  });
  await startThread(page, project, 'long reply');
  const conv = conversation(page);
  await expect(conv.getByText('part 03', { exact: true })).toBeVisible();

  down = true;
  for (const ws of sockets.splice(0)) await ws.close();
  const notice = page.getByRole('main').getByText('Connection lost, reconnecting. Work continues on the server.');
  await expect(notice).toBeVisible();
  down = false;
  await expect(notice).toBeHidden();
  await expect(threadState(page, 'Finished')).toBeVisible();
  for (const p of parts) await expect(conv.getByText(p, { exact: true })).toHaveCount(1);
  expect(model.served().filter((s) => s.fixture === 'reply long reply')).toHaveLength(1);
});
