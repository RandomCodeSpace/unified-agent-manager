import { act, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { WorkingLabel } from '../../src/components/Transcript';
import type { TurnTiming } from '../../src/api';

afterEach(() => vi.useRealTimers());

test('the working timer excludes approval and question waits, including after remount', () => {
  vi.useFakeTimers();
  const start = Date.parse('2026-09-28T12:00:00Z');
  vi.setSystemTime(start + 5000);
  const timing = { id: 'turn', state: 'working', started_at: new Date(start).toISOString() } as TurnTiming;
  const draw = (value: TurnTiming) => <WorkingLabel working items={[]} turnTimings={[value]} />;
  const view = render(draw(timing));
  expect(screen.getByText('5s')).toBeTruthy();
  const paused = { ...timing, paused_at: new Date(start + 5000).toISOString() };
  view.rerender(draw(paused));
  act(() => vi.advanceTimersByTime(60_000));
  expect(screen.getByText('5s')).toBeTruthy();
  view.rerender(draw({ ...timing, paused_ms: 60_000 }));
  expect(screen.getByText('5s')).toBeTruthy();
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.getByText('6s')).toBeTruthy();
  view.unmount();
  const resumed = render(draw({ ...timing, paused_ms: 60_000 } as TurnTiming));
  expect(screen.getByText('6s')).toBeTruthy();
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.getByText('7s')).toBeTruthy();
  resumed.rerender(draw({ ...timing, paused_ms: 60_000, paused_at: new Date(start + 67_000).toISOString() } as TurnTiming));
  act(() => vi.advanceTimersByTime(30_000));
  expect(screen.getByText('7s')).toBeTruthy();
});
