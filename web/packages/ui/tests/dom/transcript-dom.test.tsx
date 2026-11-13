// The transcript's DOM for a fixed conversation, held by a snapshot so a change to its structure is deliberate.
import { render } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Item, TurnTiming } from '../../src/api';
import { Transcript } from '../../src/components/Transcript';

const at = (s: number) => new Date(Date.UTC(2026, 10, 10, 9, 0, s)).toISOString();

// The clock stands three minutes after the first message, so the running turn's time is fixed.
beforeEach(() => void vi.useFakeTimers({ toFake: ['Date'], now: new Date(at(180)) }));
afterEach(() => vi.useRealTimers());

/** Two finished turns (the first with a call, the second stopped) and one running with a call in progress. */
const items: Item[] = [
  { id: 'u1', kind: 'user', time: at(0), text: 'Which templates render a post?' },
  { id: 'c1', kind: 'tool', time: at(1), tool: { name: 'view', status: 'completed', input: JSON.stringify({ path: '/w/templates/post.html' }) } },
  { id: 'a1', kind: 'assistant', time: at(5), text: 'Three templates render a post.' },
  { id: 'u2', kind: 'user', time: at(60), text: 'Does the cover image have alt text?' },
  { id: 'a2', kind: 'assistant', time: at(70), text: 'No, line 13 has none.' },
  { id: 'u3', kind: 'user', time: at(120), text: 'Audit the template and fix what is safe.' },
  { id: 'a3', kind: 'assistant', time: at(125), text: 'Splitting this into two surveys.' },
  { id: 'c3', kind: 'tool', time: at(130), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'npm test' }) } },
];
const timings: TurnTiming[] = [
  { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(10), state: 'completed' },
  { id: 't2', user_item_id: 'u2', started_at: at(60), ended_at: at(75), state: 'cancelled' },
  { id: 't3', user_item_id: 'u3', started_at: at(120), state: 'working' },
];

/** The DOM without what the reader's clock and locale decide: the times' text and the tooltips. */
const structure = (el: Element) => el.innerHTML.replace(/(<time\b[^>]*>)[^<]*/g, '$1').replace(/ (title|datetime)="[^"]*"/g, '');

test('the transcript keeps its DOM', () => {
  const { container } = render(
    <Transcript sessionId="s" items={items} turnTimings={timings} interactions={[]} subagents={[]} live working liveCard provider="copilot" workdir="/w" />,
  );
  expect(structure(container)).toMatchSnapshot();
});
