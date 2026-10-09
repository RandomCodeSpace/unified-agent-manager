import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { Schedules } from '../../src/components/Schedules';
import type { ScheduleSnapshot } from '../../src/api';
import { runTime } from '../../src/lib/routines';

afterEach(() => vi.restoreAllMocks());

const next = '2026-10-09T12:00:00Z';
const known: ScheduleSnapshot = {
  supported: true,
  known: true,
  entries: [
    { id: '7', recurring: true, interval_ms: 300000, next_run_at: next },
    { id: '9', recurring: true, cron: '0 9 * * 1-5', timezone: 'Asia/Singapore', next_run_at: next },
    { id: '11', recurring: false, next_run_at: next },
    { id: '12', recurring: true, self_paced: true },
  ],
};

test('a known list shows its count and opens a read-only list of cadence, timezone and next run', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch');
  const user = userEvent.setup();
  render(<Schedules snapshot={known} open />);
  await user.click(screen.getByRole('button', { name: 'Scheduled in this Task: 4' }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText('Scheduled in this Task')).toBeTruthy();
  expect(within(dialog).getByText('Every 5m 0s')).toBeTruthy();
  expect(within(dialog).getByText('Cron 0 9 * * 1-5')).toBeTruthy();
  expect(within(dialog).getByText('Once')).toBeTruthy();
  expect(within(dialog).getByText('Self-paced')).toBeTruthy();
  expect(within(dialog).getByText('Next run not set')).toBeTruthy();
  expect(within(dialog).getByText(`Next run ${runTime(next)} · Asia/Singapore`)).toBeTruthy();
  // Display only: nothing to create, cancel or re-arm, and no reads of its own.
  expect(within(dialog).queryAllByRole('button')).toHaveLength(0);
  expect(fetch).not.toHaveBeenCalled();
});

test('a partial list says so; unknown, unsupported, empty and closed Tasks show nothing', () => {
  const partial = { supported: true, known: false, truncated: true, entries: known.entries };
  const view = render(<Schedules snapshot={partial} open />);
  expect(screen.getByRole('button', { name: 'Scheduled in this Task: 4+' })).toBeTruthy();
  for (const snapshot of [
    null,
    undefined,
    { supported: true, known: false, reason: 'unavailable', entries: [] },
    { supported: false, known: false, reason: 'unsupported', entries: [] },
    { supported: true, known: true, entries: [] },
  ] as (ScheduleSnapshot | null | undefined)[]) {
    view.rerender(<Schedules snapshot={snapshot} open />);
    expect(screen.queryByRole('button')).toBeNull();
  }
  view.rerender(<Schedules snapshot={known} open={false} />);
  expect(screen.queryByRole('button')).toBeNull();
});

test('the list closes when the schedules go and updates in place by ID', async () => {
  const user = userEvent.setup();
  const view = render(<Schedules snapshot={known} open />);
  await user.click(screen.getByRole('button', { name: 'Scheduled in this Task: 4' }));
  await screen.findByRole('dialog');
  view.rerender(<Schedules snapshot={{ ...known, entries: known.entries.slice(1) }} open />);
  expect(screen.queryByText('Every 5m 0s')).toBeNull();
  expect(screen.getByRole('button', { name: 'Scheduled in this Task: 3' })).toBeTruthy();
  view.rerender(<Schedules snapshot={null} open />);
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  view.rerender(<Schedules snapshot={known} open />);
  expect(screen.queryByRole('dialog')).toBeNull();
});
