import { render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { StatusLine, statusLine } from '../../src/components/StatusLine';
import { StateMark } from '../../src/components/common';
import type { SessionDetail } from '../../src/api';

test('while the conversation compacts, the status line and the state chip say so', () => {
  const session = { id: 't', state: 'working', compacting: true, items: [], interactions: [], subagents: [] } as unknown as SessionDetail;
  const { container } = render(<StatusLine line={statusLine(session, true, true, [])} session={session} hidden={false} onJump={() => {}} />);
  expect(container.textContent).toContain('Compacting the conversation…');
  expect(screen.getByRole('button', { name: 'Compacting the conversation. Jump to bottom' })).toBeTruthy();
  render(<StateMark state="working" label compacting text="Compacting…" />);
  expect(screen.getByText('Compacting…')).toBeTruthy();
  expect(screen.queryByText('Working')).toBeNull();
});

test('a Task compacting mid-turn says so at the transcript foot as well as above the composer', async () => {
  const { openTask } = await import('./render');
  await openTask('t7');
  const log = await screen.findByRole('log');
  await waitFor(() => expect(log.textContent).toContain('Compacting the conversation…'));
  expect(log.textContent).not.toContain('Thinking…');
  expect(screen.getAllByText('Compacting the conversation…').length).toBe(2);
  // The header's state chip says it too, in place of Working.
  const header = screen.getByRole('heading', { level: 1 }).parentElement!;
  expect(header.textContent).toContain('Compacting…');
  expect(header.textContent).not.toContain('Working');
});
