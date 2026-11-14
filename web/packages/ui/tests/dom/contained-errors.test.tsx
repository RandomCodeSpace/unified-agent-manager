// A render error in one part of a Thread stays in that part: the rest of the transcript, the composer and the sidebar stay.
import { screen, waitFor } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import type * as Common from '../../src/components/common';
import { composer, log, openTask, sidebar } from './render';

// One reply's markdown throws while it renders; every other reply renders as usual.
vi.mock('../../src/components/common', async (original) => {
  const actual = await original<typeof Common>();
  return { ...actual, Markdown: (props: Parameters<typeof actual.Markdown>[0]) => {
    if (props.text.includes('cannot render')) throw new Error('render failed');
    return actual.Markdown(props);
  } };
});

test('a transcript row that throws is one muted line; the rest of the transcript and the composer stay', async () => {
  const quiet = vi.spyOn(console, 'error').mockImplementation(() => {});
  const { mock } = await openTask('t3');
  // A turn's sign-off speaks for the turn's end, which the new reply moves: everything else stays as it was.
  const kept = log().getAllByText(/./).filter((node) => !node.closest('[data-history-anchor^="turn-head-"]')).map((node) => node.textContent);
  mock.broadcast('item', { session_id: 't3', item: { id: 'broken', kind: 'assistant', time: new Date().toISOString(), text: 'This reply cannot render.' } });
  expect(await log().findByText('This message could not be shown.')).toBeTruthy();
  for (const text of kept) expect(log().getAllByText((_, node) => node?.textContent === text).length).toBeGreaterThan(0);
  expect(log().getAllByRole('button', { name: /activity of this turn/ }).length).toBeGreaterThan(0);
  expect(screen.queryByText('UAM could not show this page')).toBeNull();
  expect(composer().disabled).toBe(false);
  await sidebar();
  // The row draws again once the reply changes.
  mock.broadcast('item', { session_id: 't3', item: { id: 'broken', kind: 'assistant', time: new Date().toISOString(), text: 'This reply renders now.' } });
  expect(await log().findByText('This reply renders now.')).toBeTruthy();
  expect(log().queryByText('This message could not be shown.')).toBeNull();
  // React still reports what it caught.
  await waitFor(() => expect(quiet).toHaveBeenCalled());
  quiet.mockRestore();
});
