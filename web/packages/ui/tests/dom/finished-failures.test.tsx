// A finished turn whose own record failed (mock t-chart, its outcome or reply changed): no check, a warning glyph and words that say so.
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
import { openTask } from './render';

afterEach(() => vi.restoreAllMocks());

/** The mock with t-chart changed by `change`. */
function withChart(change: (task: ReturnType<typeof data.seed>['tasks'][number]) => void) {
  const state = data.seed();
  change(state.tasks.find((t) => t.id === 't-chart')!);
  vi.spyOn(data, 'seed').mockReturnValue(state);
}

const header = () => screen.getByRole('heading', { level: 1 }).parentElement!;
const row = () => document.querySelector<HTMLElement>('nav[aria-label="Tasks"] [data-task-row="t-chart"]')!;

test('failed tests and commands in the outcome turn the header chip and the row from a check to a warning, in words', async () => {
  withChart((t) => { t.outcome = 'Charted the commits; tests fail; 1 command failed'; });
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished · tests fail, 1 command failed');
  expect(chip.className).toContain('text-warning');
  expect(chip.querySelector('.lucide-circle-alert')).not.toBeNull();
  expect(header().querySelector('.lucide-check')).toBeNull();
  await waitFor(() => expect(row().textContent).toContain('Tests fail'));
  expect(row().textContent).not.toContain('Finished');
  const status = within(row()).getByText('Tests fail');
  expect(status.className).toContain('text-warning');
  expect(status.querySelector('.lucide-circle-alert')).not.toBeNull();
  expect(status.querySelector('.lucide-circle-check')).toBeNull();
});

test('a reply claim the turn\'s record contradicts turns the header chip too', async () => {
  withChart((t) => {
    const reply = t.items.filter((i) => i.kind === 'assistant').at(-1)!;
    reply.text = `${reply.text}\n\nAll tests pass.`;
  });
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished · a claim contradicted');
  expect(chip.className).toContain('text-warning');
  expect(header().querySelector('.lucide-check')).toBeNull();
});

test('a clean finished turn keeps its check', async () => {
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished');
  expect(chip.className).toContain('text-success');
  expect(chip.querySelector('.lucide-check')).not.toBeNull();
});
