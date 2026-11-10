import { render, screen, waitFor, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import type { SessionDetail } from '../../src/api';

test('while the conversation compacts, the live line says so with the breathing dot', () => {
  const session = { id: 't', state: 'working', compacting: true, items: [], interactions: [], subagents: [] } as unknown as SessionDetail;
  const { container } = render(<StatusLine line={statusLine(session, true, true)} session={session} hidden={false} jump={false} onJump={() => {}} />);
  expect(container.textContent).toContain('Compacting the conversation');
  // The breathing dot is the live slot's mark: the line is not the violet compacting words.
  expect(container.querySelector('[data-breathing-dot]')).not.toBeNull();
  // Plain words, not a button: nothing to peek at.
  expect(screen.getByText('Compacting the conversation', { selector: '.sr-only' })).toBeTruthy();
  expect(screen.queryByRole('button')).toBeNull();
});

test('a Task compacting mid-turn says so once, on the strip\'s live line: not at the transcript foot, not in the header', async () => {
  const { openTask } = await import('./render');
  await openTask('t7');
  await waitFor(() => expect(screen.getAllByText('Compacting the conversation')).toHaveLength(1));
  const log = await screen.findByRole('log');
  expect(log.textContent).not.toContain('Compacting the conversation');
  expect(log.textContent).not.toContain('Thinking…');
  // The header keeps Working, in blue.
  const header = screen.getByRole('group', { name: /^State: / });
  expect(header.getAttribute('aria-label')).toBe('State: Working');
  expect(within(header).getByText('Working').getAttribute('data-rung')).toBe('live');
  expect(header.textContent).not.toContain('Compacting');
});

test('automatic compaction beside the turn is a quiet note on a working line, not a Compacting state', () => {
  const session = { id: 't', state: 'working', compacting_background: true, turn_activity: { intent: 'Reading the files', todos: { known: false } }, items: [], interactions: [], subagents: [] } as unknown as SessionDetail;
  const { container } = render(<StatusLine line={statusLine(session, true, false)} session={session} hidden={false} jump={false} onJump={() => {}} />);
  expect(container.textContent).toContain('Reading the files');
  expect(container.textContent).toContain('Compacting in background');
  expect(container.textContent).not.toContain('Compacting the conversation');
  expect(screen.getByText('Reading the files. Compacting in background', { selector: '.sr-only' })).toBeTruthy();
  // Words only, in the line's muted caption (css.test.mjs): no ring.
  expect(screen.getByText('Compacting in background')).toBeTruthy();
  expect(container.querySelector('[data-spinner]')).toBeNull();
});
