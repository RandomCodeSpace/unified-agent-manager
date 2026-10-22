import { fireEvent, render, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { Item } from '../../src/api';
import { ToolRow, Transcript } from '../../src/components/Transcript';

const at = (s: number) => `2026-10-08T10:00:${String(s).padStart(2, '0')}Z`;
const running: Item = { id: 'c1', kind: 'tool', time: at(1), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'make' }), output: 'out-1\nerr-1\n', tail: [{ text: 'out-1' }, { text: 'err-1', err: true }] } };
const outputs = (view: ReturnType<typeof render>) => view.queryAllByRole('group', { name: 'Live output, last lines' });

test('the live step is the running call\'s row alone, expanded or not: its output is the strip\'s peek', () => {
  const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Build it' }, running];
  const view = render(<Transcript sessionId="s" items={items} turnTimings={[{ id: 't1', user_item_id: 'u1', started_at: at(0), state: 'working' }]} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" liveCard />);
  const row = view.getByRole('button', { name: /^bash make/ });
  expect(outputs(view)).toHaveLength(0);
  expect(row.closest('.animate-rise')?.textContent).not.toContain('out-1');
  fireEvent.click(row);
  expect(outputs(view)).toHaveLength(0);
});

test('an expanded running tool row in the timeline shows the tail until the call ends, stderr marked in words, not as a live region', () => {
  const view = render(<ToolRow item={running} live />);
  expect(outputs(view)).toHaveLength(0);
  fireEvent.click(view.getByRole('button', { name: /^bash make/ }));
  const [tail] = outputs(view);
  expect(tail.tagName).toBe('PRE');
  expect(tail.getAttribute('translate')).toBe('no');
  expect(tail.closest('[aria-live], [role="log"], [role="status"]')).toBeNull();
  const [out, err] = [...tail.children] as HTMLElement[];
  expect(out.textContent).toBe('out-1');
  expect(err.textContent).toBe('errstderr: err-1');
  expect(within(err).getByText('err').getAttribute('aria-hidden')).toBe('true');
  expect(within(err).getByText('stderr:', { exact: false }).className).toContain('sr-only');
  view.rerender(<ToolRow item={{ ...running, tool: { ...running.tool!, status: 'completed', output: 'out-1\nerr-1\n', tail: undefined } }} live />);
  expect(outputs(view)).toHaveLength(0);
});
