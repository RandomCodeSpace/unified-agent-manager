// The Thread header is a card bar whose still left edge is the Thread's state (DESIGN.md Thread header): no state word or dot on it.
import { screen, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { PHONE } from '../../src/components/ui/dialog';
import { openTask } from './render';
import { STATE_LABELS } from '../../src/components/common';
import { chipVariants } from '../../src/components/ui/chip';

afterEach(() => vi.restoreAllMocks());

const bar = () => screen.getByRole('group', { name: /^State: / });

// The label's rung says how much the state asks of the owner (StateLabel): `now` is the one filled tag, `live` and `bad`
// colour the glyph and the word, `done` the glyph alone, `off` nothing.
test.each([
  ['t8', 'Working', 'live'],
  ['t2', 'Needs answer', 'now'],
  ['t5', 'Interrupted', 'bad'],
  ['t4', 'Error', 'bad'],
  ['t-chart', 'Finished', 'done'],
])('%s says %s beside the title, on a flat bar', async (id, word, rung) => {
  await openTask(id);
  expect(bar().getAttribute('aria-label')).toBe(`State: ${word}`);
  expect(bar().className).not.toMatch(/before:bg-|shadow-|bg-raised/);
  expect(within(bar()).getByText(word).dataset.rung).toBe(rung);
});

test('the one filled tag is the attention chip itself, not a copy of its look', async () => {
  await openTask('t2');
  const waiting = within(bar()).getByText('Needs answer');
  // Every class of the chip but its gap, which the label sets for its glyph.
  const chip = chipVariants({ tone: 'attention' }).split(' ').filter((name) => !name.startsWith('gap-'));
  expect(chip.filter((name) => !waiting.classList.contains(name))).toEqual([]);
});

test('an idle Thread\'s header label has no colour', async () => {
  const state = data.seed();
  state.tasks.find((t) => t.id === 't-chart')!.state = 'idle';
  vi.spyOn(data, 'seed').mockReturnValue(state);
  await openTask('t-chart');
  expect(bar().getAttribute('aria-label')).toBe('State: Idle');
  expect(within(bar()).getByText('Idle').dataset.rung).toBe('off');
});

test('the sidebar\'s top row is flat on the rail, in the same 36px box', async () => {
  await openTask('t8');
  const search = within(screen.getByRole('navigation', { name: 'Threads' })).getByRole('searchbox', { name: 'Search threads' });
  const box = search.closest('header > div')!;
  expect(box.className).toContain('h-9');
  expect(box.className).not.toMatch(/shadow-|bg-raised/);
});

const onPhone = () => {
  const original = window.matchMedia.bind(window);
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === PHONE ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
};

test('on a phone the state is also a glyph after the title, the Thread list\'s own', async () => {
  onPhone();
  await openTask('t4');
  const word = within(bar()).getByText('Error', { selector: '.sr-only' });
  expect(word.parentElement!.dataset.rung).toBe('bad');
  expect(word.parentElement!.querySelector('.lucide-triangle-alert')).not.toBeNull();
});

test('on a phone a Thread waiting on a permission shows the shield of the Thread list, in the same rung, with no fill', async () => {
  onPhone();
  await openTask('t6');
  const word = within(bar()).getByText('Needs permission', { selector: '.sr-only' });
  const label = word.parentElement!;
  expect(label.dataset.rung).toBe('now');
  expect(label.querySelector('.lucide-shield-question-mark')).not.toBeNull();
  expect(label.querySelector('.lucide-message-circle-question')).toBeNull();
  expect(label.className).not.toMatch(/bg-/);
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
