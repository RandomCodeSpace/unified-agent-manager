// A main-agent model request that never answers: uam cancels it and continues the turn once, with a note.
import { conversation, expect, startThread, send, test, threadState } from './harness';

test('a model request with no answer is cancelled and retried once, and the transcript says so', async ({ page, project, model }) => {
  // The bound is a minute once a request has answered quickly (four times its silence, never under 60 s).
  test.setTimeout(300_000);
  model.reply('warm the stall watch', 'Warm.');
  // Silent for 30 s before the request is handled, then 25 s before each frame (the role, then the text): past the bound.
  model.reply('hang the model', 'Too late.', { chaos: { latencyMs: 30_000 }, latency: 25_000, chunkSize: 100 });
  model.reply('Your previous model request got no answer', 'Recovered after the stall.');
  await startThread(page, project, 'warm the stall watch');
  await expect(threadState(page, 'Finished')).toBeVisible();

  await send(page, 'hang the model');
  const conv = conversation(page);
  await expect(conv.getByText(/^No answer from the model for \d+ s; uam retried the request\.$/)).toBeVisible({ timeout: 200_000 });
  await expect(conv.getByText('Recovered after the stall.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText('Too late.')).toHaveCount(0);
  // The note stands for uam's continuation: the prompt it sent is not shown as the owner's message.
  await expect(conv.getByText(/^Your previous model request got no answer/)).toHaveCount(0);
});
