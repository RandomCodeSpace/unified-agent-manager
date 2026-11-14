import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState, toolResult } from './harness';

test('Allow once runs the shell command and shows its output', async ({ page, project, model, workdir }) => {
  model.toolRound('run the marker', [{ id: 'call_allow', name: 'bash', arguments: { command: 'printf ran > ran.txt && echo wrote-marker', description: 'Write the marker file' } }], 'Marker written.');
  await startThread(page, project, 'run the marker');
  const card = conversation(page).getByRole('group', { name: 'Run shell command' });
  await expect(card).toContainText('printf ran > ran.txt && echo wrote-marker');
  await expect(card).toContainText('Write the marker file');
  await expect(threadState(page, 'Needs permission')).toBeVisible();

  await card.getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Marker written.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(card).toBeHidden();
  expect(fs.readFileSync(path.join(workdir, 'ran.txt'), 'utf8')).toBe('ran');
  expect(toolResult(model, 'call_allow')).toContain('wrote-marker');

  await conversation(page).getByRole('button', { name: '1 command. Show the activity of this turn' }).click();
  await expect(conversation(page).getByText('wrote-marker')).toBeVisible();
});

test('Deny refuses the shell command and the turn ends cleanly', async ({ page, project, model, workdir }) => {
  // The CLI ends the turn on a denial without asking the model again.
  model.on({ userMessage: 'run the denied' }, { toolCalls: [{ id: 'call_deny', name: 'bash', arguments: JSON.stringify({ command: 'printf no > denied.txt', description: 'Write the denied file' }) }] });
  await startThread(page, project, 'run the denied');
  const card = conversation(page).getByRole('group', { name: 'Run shell command' });
  await card.getByRole('button', { name: 'Deny' }).click();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(card).toBeHidden();
  expect(fs.existsSync(path.join(workdir, 'denied.txt'))).toBe(false);
  expect(model.served()).toHaveLength(1);
});

test('a question is answered from the composer and the turn continues', async ({ page, project, model }) => {
  model.toolRound('ask me a colour', [{ id: 'call_ask', name: 'ask_user', arguments: { question: 'Which colour?', choices: ['Blue (Recommended)', 'Red'] } }], 'Green it is.');
  await startThread(page, project, 'ask me a colour');
  const choices = page.getByRole('group', { name: 'Which colour?' });
  await expect(choices.getByRole('radio', { name: 'Blue (Recommended)' })).toBeChecked();
  await expect(threadState(page, 'Needs answer')).toBeVisible();

  await page.getByRole('textbox', { name: 'Your answer' }).fill('Green');
  await page.getByRole('button', { name: 'Answer', exact: true }).click();
  await expect(conversation(page).getByText('Green it is.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(toolResult(model, 'call_ask')).toContain('Green');
  await expect(page.getByRole('textbox', { name: 'Message' })).toBeVisible();
});

test('Stop ends a slow turn and the next prompt completes', async ({ page, project, model }) => {
  // Streamed in full, the reply would take about a minute.
  model.reply('slow answer', Array.from({ length: 60 }, (_, i) => `slow line ${i + 1}`).join('\n\n'), { latency: 250, chunkSize: 4 });
  model.reply('quick answer', 'Quick reply after the stop.');
  await startThread(page, project, 'slow answer');
  await expect(threadState(page, 'Working')).toBeVisible();
  await expect(conversation(page).getByText('slow line 1', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();

  await send(page, 'quick answer');
  await expect(conversation(page).getByText('Quick reply after the stop.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
});

test('an upstream failure ends the turn with its error and the next prompt succeeds', async ({ page, project, model }) => {
  // A 429 is the fastest failure: the CLI gives up after five quick retries.
  model.fail('break please', 429, 'mock rate limit');
  model.reply('recover please', 'Recovered after the failure.');
  await startThread(page, project, 'break please');
  await expect(threadState(page, 'Error')).toBeVisible({ timeout: 45_000 });
  await expect(conversation(page).getByText(/429 mock rate limit/)).toBeVisible();
  await expect(conversation(page).getByRole('alert')).toHaveText('Rate limit reached. Wait, then send your message again.');

  await send(page, 'recover please');
  await expect(conversation(page).getByText('Recovered after the failure.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
});
