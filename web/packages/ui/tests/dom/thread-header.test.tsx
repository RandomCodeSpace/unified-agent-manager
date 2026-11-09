// The Thread header is a card bar whose still left edge is the Thread's state (DESIGN.md Thread header): no state word or dot on it.
import { screen, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { PHONE } from '../../src/components/ui/dialog';
import { openTask } from './render';
import { STATE_LABELS } from '../../src/components/common';

afterEach(() => vi.restoreAllMocks());

const bar = () => screen.getByRole('group', { name: /^State: / });

test.each([
  ['t8', 'Working', 'text-accent'],
  ['t2', 'Needs answer', 'bg-attention-wash'],
  ['t5', 'Interrupted', 'text-warning'],
  ['t4', 'Error', 'text-error'],
  ['t-chart', 'Finished', 'text-muted'],
])('%s says %s beside the title, on a flat bar', async (id, word, tone) => {
  await openTask(id);
  expect(bar().getAttribute('aria-label')).toBe(`State: ${word}`);
  expect(bar().className).not.toMatch(/before:bg-|shadow-|bg-raised/);
  expect(within(bar()).getByText(word).className).toContain(tone);
});

test('only a state that waits on the owner is a filled tag; a finished one colours its glyph alone', async () => {
  await openTask('t-chart');
  const finished = within(bar()).getByText('Finished');
  expect(finished.className).not.toMatch(/bg-/);
  expect(finished.querySelector('svg')!.getAttribute('class')).toContain('text-success');
});

test('an idle Thread\'s header label has no colour', async () => {
  const state = data.seed();
  state.tasks.find((t) => t.id === 't-chart')!.state = 'idle';
  vi.spyOn(data, 'seed').mockReturnValue(state);
  await openTask('t-chart');
  expect(bar().getAttribute('aria-label')).toBe('State: Idle');
  expect(within(bar()).getByText('Idle').className).toContain('text-muted');
  expect(within(bar()).getByText('Idle').querySelector('svg')!.getAttribute('class')).toContain('text-faint');
});

test('the sidebar\'s top row is flat on the rail, in the same 36px box', async () => {
  await openTask('t8');
  const search = within(screen.getByRole('navigation', { name: 'Threads' })).getByRole('searchbox', { name: 'Search threads' });
  const box = search.closest('header > div')!;
  expect(box.className).toContain('h-9');
  expect(box.className).not.toMatch(/shadow-|bg-raised/);
});

test('on a phone the state is also a glyph after the title', async () => {
  const original = window.matchMedia.bind(window);
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === PHONE ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
  await openTask('t4');
  expect(within(bar()).getByText('Error', { selector: '.sr-only' })).toBeTruthy();
  expect(bar().querySelector('.lucide-x')).not.toBeNull();
});

test('one state vocabulary: a waiting Thread reads Needs answer or Needs permission, never Input or Needs you', () => {
  expect(STATE_LABELS).toEqual({
    idle: 'Idle',
    starting: 'Starting',
    working: 'Working',
    awaiting_permission: 'Needs permission',
    awaiting_answer: 'Needs answer',
    completed: 'Finished',
    cancelled: 'Stopped',
    failed: 'Error',
    interrupted: 'Interrupted',
    closed: 'Idle',
  });
});
