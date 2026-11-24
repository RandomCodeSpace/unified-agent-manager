// Messages sent while a turn runs: Send now (steer), After this turn (queue) and the paused queue.
import path from 'node:path';
import type { Page } from '@playwright/test';
import { conversation, expect, send, startThread, test, threadState, type Model } from './harness';

/**
 * A turn that waits on the permission card of a file write: it stays open for as long as the case
 * needs, with no stream to race (and a write, unlike a shell command, is not moved to the
 * background by a steer). `then` answers the request after the write.
 */
function heldTurn(model: Model, workdir: string, prompt: string, id: string, then: Parameters<Model['on']>[1]) {
  model.on({ toolCallId: id }, then, {}, `after ${id}`);
  model.on({ userMessage: prompt }, { toolCalls: [{ id, name: 'create', arguments: JSON.stringify({ path: path.join(workdir, `${id}.txt`), file_text: 'held\n' }) }] }, {}, `holds ${prompt}`);
}

const allow = (page: Page) => conversation(page).getByRole('group', { name: 'Write file' }).getByRole('button', { name: 'Allow once' });

/** Sends `text` the other way than Send does: After this turn where Send steers. */
async function sendAfterThisTurn(page: Page, text: string) {
  await page.getByRole('textbox', { name: 'Message' }).fill(text);
  await page.getByRole('button', { name: 'More send options' }).click();
  await page.getByRole('menuitem', { name: /^After this turn/ }).click();
}

test('Send now: the steer reads accepted in bold italic, then delivered in normal text, and the agent takes it in; stopped first, it reads Not delivered', async ({ page, project, model, workdir }) => {
  heldTurn(model, workdir, 'start the held turn', 'call_hold1', { content: 'File written.' });
  model.reply('use tabs instead', 'Tabs it is.');
  heldTurn(model, workdir, 'start another turn', 'call_hold2', { content: 'Never reached.' });
  await startThread(page, project, 'start the held turn');
  const conv = conversation(page);
  await expect(allow(page)).toBeVisible();

  await page.getByRole('textbox', { name: 'Message' }).fill('use tabs instead');
  await page.getByRole('button', { name: 'Send now', exact: true }).click();
  const steer = conv.getByText('use tabs instead', { exact: true });
  await expect(conv.getByText('You (accepted, not delivered yet):')).toBeVisible();
  await expect(steer).toHaveCSS('font-style', 'italic');
  await allow(page).click();
  await expect(conv.getByText('Tabs it is.')).toBeVisible();
  await expect(conv.getByText('You (accepted, not delivered yet):')).toHaveCount(0);
  await expect(steer).toHaveCSS('font-style', 'normal');
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'start another turn');
  await expect(allow(page)).toBeVisible();
  await page.getByRole('textbox', { name: 'Message' }).fill('never taken in');
  await page.getByRole('button', { name: 'Send now', exact: true }).click();
  await expect(conv.getByText('never taken in', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(conv.getByText('Not delivered', { exact: true })).toBeVisible();
  await expect(conv.getByText(/^Steer not delivered: /)).toBeVisible();
  expect(model.served().some((s) => JSON.stringify(s.request.messages).includes('never taken in'))).toBe(false);
});

test('After this turn: a queued message is sent when the turn ends; a stop or a failure pauses the queue, Resume sends it and Clear drops it', async ({ page, project, model, workdir }) => {
  heldTurn(model, workdir, 'run the first turn', 'call_first', { content: 'First turn done.' });
  model.reply('queued follow-up', 'Follow-up answered.');
  heldTurn(model, workdir, 'run the second turn', 'call_second', { content: 'Never reached.' });
  model.reply('queued behind a stop', 'Resumed and answered.');
  // After its write the third turn's model call fails: a 429 ends the turn once the CLI gives up retrying (5 to 9 s).
  heldTurn(model, workdir, 'run the third turn', 'call_third', { status: 429, error: { message: 'mock rate limit', type: 'rate_limit_error' } });
  model.reply('queued then cleared', 'Never sent.');
  const sent = (prompt: string) => model.served().some((s) => s.fixture === `reply ${prompt}`);
  await startThread(page, project, 'run the first turn');
  const conv = conversation(page);
  await expect(allow(page)).toBeVisible();

  await sendAfterThisTurn(page, 'queued follow-up');
  await expect(page.getByText('1 waiting')).toBeVisible();
  await expect(page.getByText('Sent after this turn')).toBeVisible();
  // It is held until the turn completes.
  expect(sent('queued follow-up')).toBe(false);
  await allow(page).click();
  await expect(conv.getByText('First turn done.')).toBeVisible();
  await expect(conv.getByText('Follow-up answered.')).toBeVisible();
  await expect(page.getByText('1 waiting')).toHaveCount(0);
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'run the second turn');
  await expect(allow(page)).toBeVisible();
  await sendAfterThisTurn(page, 'queued behind a stop');
  await expect(page.getByText('1 waiting')).toBeVisible();
  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(page.getByText('Paused', { exact: true })).toBeVisible();
  expect(sent('queued behind a stop')).toBe(false);
  await page.getByRole('button', { name: 'Resume', exact: true }).click();
  await expect(conv.getByText('Resumed and answered.')).toBeVisible();
  await expect(page.getByText('1 waiting')).toHaveCount(0);
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'run the third turn');
  await expect(allow(page)).toBeVisible();
  await sendAfterThisTurn(page, 'queued then cleared');
  await expect(page.getByText('1 waiting')).toBeVisible();
  await allow(page).click();
  await expect(threadState(page, 'Error')).toBeVisible({ timeout: 45_000 });
  await expect(page.getByText('Paused', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Clear', exact: true }).click();
  await page.getByRole('alertdialog').getByRole('button').last().click();
  await expect(page.getByText('1 waiting')).toHaveCount(0);
  await expect(threadState(page, 'Error')).toBeVisible();
  expect(sent('queued then cleared')).toBe(false);
});
