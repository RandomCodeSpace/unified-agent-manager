import { render } from '@testing-library/react';
import { expect, test } from 'vitest';
import { TaskTitle } from '../../src/components/common';
import type { SessionSummary } from '../../src/api';

const draw = (fields: Partial<SessionSummary>) =>
  render(<TaskTitle session={{ name: '', title: '', state: 'working', ...fields } as SessionSummary} />).container.textContent;

test('a thread waits for a title only while the server makes one', () => {
  expect(draw({ title_pending: true })).toBe('Waiting for a title…');
  // Working without a title job (an older server, or none planned) is a new thread, not a wait.
  expect(draw({})).toBe('New thread');
  expect(draw({ state: 'idle', title_pending: false })).toBe('New thread');
  // A fallback title shows while the model's title is on its way; a name wins over both.
  expect(draw({ title: 'Why do teams write changelogs?', title_pending: true })).toBe('Why do teams write changelogs?');
  expect(draw({ name: 'Mine', title: 'Theirs', title_pending: true })).toBe('Mine');
});
