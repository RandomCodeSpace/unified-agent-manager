// The composer is typed in while a turn streams: a streamed text delta must not re-render it.
import { act } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { composer, log, openTask } from './render';

const renders = vi.hoisted(() => ({ count: 0 }));
vi.mock('../../src/components/Assist', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../src/components/Assist')>();
  // The composer calls this hook once each time it renders, and nothing else calls it.
  return { ...actual, useSuggestion: (...args: Parameters<typeof actual.useSuggestion>) => {
    renders.count++;
    return actual.useSuggestion(...args);
  } };
});

/** Long enough for the next animation frame, where queued deltas land, and the render after it. */
const frame = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 100)); });

// t15 is a compact Thread, as every Thread the service sends is: its latest rows are kept beside its window.
test('a streamed text delta does not re-render the composer; a new item does', async () => {
  const { mock } = await openTask('t15');
  const time = new Date().toISOString();
  // `append` marks a new row, as the service sends it: the row joins the Thread's latest rows too.
  mock.broadcast('item', { session_id: 't15', append: true, item: { id: 'streamed', kind: 'assistant', time, text: 'One' } });
  await log().findByText('One');
  await frame();
  renders.count = 0;
  for (const text of [' two', ' three', ' four']) {
    mock.broadcast('delta', { session_id: 't15', item_id: 'streamed', kind: 'assistant', text });
    await frame();
  }
  await log().findByText('One two three four');
  expect(renders.count).toBe(0);
  // A prompt sent from another page is one the composer's Up-arrow history must see.
  mock.broadcast('item', { session_id: 't15', append: true, item: { id: 'steered', kind: 'user', time, text: 'Cover TERM=vt100 too' } });
  await log().findByText('Cover TERM=vt100 too');
  expect(renders.count).toBeGreaterThan(0);
  expect(composer().disabled).toBe(false);
});
