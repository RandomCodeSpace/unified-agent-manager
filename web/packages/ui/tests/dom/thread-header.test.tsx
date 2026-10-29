// The Thread header is a card bar whose still fill is the Thread's state (DESIGN.md Thread header): no state word or dot on it.
import { screen, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { PHONE } from '../../src/components/ui/dialog';
import { openTask } from './render';

afterEach(() => vi.restoreAllMocks());

const bar = () => screen.getByRole('group', { name: /^State: / });

test.each([
  ['t8', 'Working', 'bg-accent-wash'],
  ['t2', 'Input', 'bg-warning-wash'],
  ['t5', 'Interrupted', 'bg-warning-wash'],
  ['t4', 'Error', 'bg-error-wash'],
  ['t-chart', 'Finished', 'bg-success-wash'],
])('%s fills the header card for %s', async (id, word, fill) => {
  await openTask(id);
  expect(bar().getAttribute('aria-label')).toBe(`State: ${word}`);
  expect(bar().className).toContain(fill);
  expect(bar().className).not.toContain('bg-raised');
  expect(within(bar()).queryByText(word)).toBeNull();
});

test('an idle Thread\'s header card stays neutral', async () => {
  const state = data.seed();
  state.tasks.find((t) => t.id === 't-chart')!.state = 'idle';
  vi.spyOn(data, 'seed').mockReturnValue(state);
  await openTask('t-chart');
  expect(bar().getAttribute('aria-label')).toBe('State: Idle');
  expect(bar().className).toContain('bg-raised');
  expect(bar().className).not.toMatch(/-wash/);
});

test('the sidebar\'s top row is the same card', async () => {
  await openTask('t8');
  const search = within(screen.getByRole('navigation', { name: 'Threads' })).getByRole('searchbox', { name: 'Search threads' });
  expect(search.closest('header > div')!.className).toContain('shadow-raised');
});

test('on a phone the state is also a glyph after the title', async () => {
  const original = window.matchMedia.bind(window);
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === PHONE ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
  await openTask('t4');
  expect(within(bar()).getByText('Error', { selector: '.sr-only' })).toBeTruthy();
  expect(bar().querySelector('.lucide-x')).not.toBeNull();
});
