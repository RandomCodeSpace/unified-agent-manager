import { render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import { StateMark } from '../../src/components/common';
import type { SessionDetail } from '../../src/api';

test('while the conversation compacts, the live line says so with the breathing dot; the state chip can still say it', () => {
  const session = { id: 't', state: 'working', compacting: true, items: [], interactions: [], subagents: [] } as unknown as SessionDetail;
  const { container } = render(<StatusLine line={statusLine(session, true, true)} session={session} hidden={false} onJump={() => {}} />);
  expect(container.textContent).toContain('Compacting the conversation');
  expect(container.querySelector('.animate-breathe')).not.toBeNull();
  expect(container.querySelector('.text-badge-violet')).toBeNull();
  expect(screen.getByRole('button', { name: 'Compacting the conversation. Jump to bottom' })).toBeTruthy();
  render(<StateMark state="working" label compacting text="Compacting…" />);
  expect(screen.getByText('Compacting…')).toBeTruthy();
  expect(screen.queryByText('Working')).toBeNull();
});

test('a Task compacting mid-turn says so once, on the strip\'s live line: not at the transcript foot, not in the header', async () => {
  const { openTask } = await import('./render');
  await openTask('t7');
  await waitFor(() => expect(screen.getAllByText('Compacting the conversation')).toHaveLength(1));
  const log = await screen.findByRole('log');
  expect(log.textContent).not.toContain('Compacting the conversation');
  expect(log.textContent).not.toContain('Thinking…');
  // The header keeps the still "Working".
  const header = screen.getByRole('heading', { level: 1 }).parentElement!;
  expect(header.textContent).toContain('Working');
  expect(header.textContent).not.toContain('Compacting');
});

test('automatic compaction beside the turn is a quiet note on a working line, not a Compacting state', () => {
  const session = { id: 't', state: 'working', compacting_background: true, turn_activity: { intent: 'Reading the files', todos: { known: false } }, items: [], interactions: [], subagents: [] } as unknown as SessionDetail;
  const { container } = render(<StatusLine line={statusLine(session, true, false)} session={session} hidden={false} onJump={() => {}} />);
  expect(container.textContent).toContain('Reading the files');
  expect(container.textContent).toContain('Compacting in background');
  expect(container.textContent).not.toContain('Compacting the conversation');
  expect(screen.getByRole('button', { name: 'Reading the files. Compacting in background. Jump to bottom' })).toBeTruthy();
  // Words only, in the line's muted caption: no ring, no violet.
  const note = screen.getByText('Compacting in background');
  expect(note.closest('.text-badge-violet, .text-body')).toBeNull();
  expect(container.querySelector('.animate-spin, .bg-badge-violet')).toBeNull();
});
