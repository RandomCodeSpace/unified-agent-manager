// What a uam restart leaves of a turn it cut: the Interrupted card of a call that had started, and of one that waited on permission.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, lastUserText, newThread, send, test, threadState } from './harness';
import { pickMode } from './activity-helpers';
import { ownService } from './shell-helpers';

test('a restart mid-turn: a started call offers Run again, which only drafts; a call waiting on permission reads Not run and Ask again asks once; cards survive', async ({ browser, uam, workdir, model }) => {
  const surface = (...messages: object[]) => ({ messages: messages.map((m) => ({ version: '1.0', ...m })) });
  model.toolRound('draw then wait', [{
    id: 'call_card', name: 'uam_surface', arguments: surface(
      { createSurface: { surfaceId: 'kept', catalogId: 'urn:uam:catalog:v1' } },
      { updateComponents: { surfaceId: 'kept', components: [{ id: 'root', component: 'Column', children: ['title'] }, { id: 'title', component: 'Text', text: 'Card kept over the restart', variant: 'title' }] } },
    ),
  }], 'Card drawn.');
  const asked = { command: 'printf asked > asked.txt', description: 'Write asked' };
  // uam's stop closes the client, the CLI denies the waiting call unasked, and it may ask the model once more before it exits.
  model.on({ toolCallId: 'call_asked' }, { content: 'Could not ask.' }, {}, 'after the unasked denial');
  model.on({ userMessage: 'now wait on permission' }, { toolCalls: [{ id: 'call_asked', name: 'bash', arguments: JSON.stringify(asked) }] }, {}, 'asks');
  // The request quotes the command from the call's record; right after a restart that record can be unreadable (the item route
  // answered 404 in one run of five), and the request then names the call instead, as documented. Both are a request to run it again.
  const rerun = (command: string, call: string) => [`Re-run the interrupted command: \`${command}\``, `Re-run the shell command that was interrupted by the restart (call ${call}).`];
  model.on({ toolCallId: 'call_asked_again' }, { content: 'Asked and ran.' }, {}, 'asked again ran');
  model.on({ predicate: (req) => rerun(asked.command, 'call_asked').some((text) => lastUserText(req).includes(text)) }, { toolCalls: [{ id: 'call_asked_again', name: 'bash', arguments: JSON.stringify(asked) }] }, {}, 'asks again');
  model.on({ userMessage: 'run the sleeper' }, { toolCalls: [{ id: 'call_sleep', name: 'bash', arguments: JSON.stringify({ command: 'sleep 120', description: 'Sleep' }) }] }, {}, 'sleeps');
  const own = await ownService(browser, uam.mock.url, workdir);
  try {
    const { page } = own;
    await page.goto('/');
    await expect(page.getByRole('region', { name: 'Home' })).toBeVisible();
    await newThread(page, own.project);
    await send(page, 'draw then wait');
    const conv = conversation(page);
    await expect(conv.getByText('Card drawn.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    await send(page, 'now wait on permission');
    await expect(conv.getByRole('group', { name: 'Run shell command' })).toBeVisible();
    const waitingUrl = page.url();

    await newThread(page, own.project);
    await pickMode(page, 'Allow all');
    await send(page, 'run the sleeper');
    await expect(conv.getByRole('button', { name: 'bash sleep 120 , running' })).toBeVisible();

    await own.restart();
    await expect(threadState(page, 'Interrupted')).toBeVisible({ timeout: 30_000 });
    const served = model.served().length;
    const cut = conv.getByRole('region', { name: 'Interrupted' });
    await expect(cut).toContainText('This call had started and did not finish:');
    await expect(cut.getByRole('code')).toHaveText('sleep 120');
    await expect(cut.getByRole('button', { name: 'Ask again' })).toHaveCount(0);
    await cut.getByRole('button', { name: 'Run again' }).click();
    await expect.poll(() => page.getByRole('textbox', { name: 'Message' }).inputValue()).not.toBe('');
    expect(rerun('sleep 120', 'call_sleep')).toContain(await page.getByRole('textbox', { name: 'Message' }).inputValue());
    // Drafted only: nothing was sent, and the Thread stays as the restart left it.
    await expect(threadState(page, 'Interrupted')).toBeVisible();
    expect(model.served()).toHaveLength(served);

    await page.goto(waitingUrl);
    await expect(threadState(page, 'Interrupted')).toBeVisible();
    await expect(conv.getByRole('region', { name: 'Card kept over the restart' })).toBeVisible();
    await expect(cut).toContainText('Not run: it was waiting for your permission.');
    await expect(cut.getByRole('code')).toHaveText('printf asked > asked.txt');
    await expect(cut.getByRole('button', { name: 'Run again' })).toHaveCount(0);
    expect(fs.existsSync(path.join(workdir, 'asked.txt'))).toBe(false);
    await cut.getByRole('button', { name: 'Ask again' }).click();
    // The request is sent at once, and the permission prompt comes back as usual.
    const card = conv.getByRole('group', { name: 'Run shell command' });
    await expect(card).toContainText('printf asked > asked.txt');
    expect(fs.existsSync(path.join(workdir, 'asked.txt'))).toBe(false);
    await card.getByRole('button', { name: 'Allow once' }).click();
    await expect(conv.getByText('Asked and ran.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(fs.readFileSync(path.join(workdir, 'asked.txt'), 'utf8')).toBe('asked');
    expect(model.served().filter((s) => s.fixture === 'asks again')).toHaveLength(1);
  } finally {
    await own.stop();
  }
});
