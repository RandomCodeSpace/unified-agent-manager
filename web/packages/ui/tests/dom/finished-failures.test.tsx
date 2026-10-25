// A finished turn whose own record failed (mock t-chart, its outcome or reply changed) stays Finished: what it found is hover text, no state.
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
const row = () => document.querySelector<HTMLElement>('nav[aria-label="Threads"] [data-task-row="t-chart"]')!;

test('failed tests in the outcome stay Finished, saying so on the header chip\'s hover', async () => {
  withChart((t) => { t.outcome = 'Charted the commits; tests fail; 1 command failed'; });
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished');
  expect(chip.closest('[title]')?.getAttribute('title')).toBe('Finished · tests fail');
  expect(header().querySelector('.lucide-check')).not.toBeNull();
  await waitFor(() => expect(row().textContent).toMatch(/Review|Finished/));
  expect(row().textContent).not.toContain('Tests fail');
});

test('a reply claim the turn\'s record contradicts says so on the header chip\'s hover', async () => {
  withChart((t) => {
    const reply = t.items.filter((i) => i.kind === 'assistant').at(-1)!;
    reply.text = `${reply.text}\n\nAll tests pass.`;
    // Only the record proves a claim wrong: the turn's own test run failed.
    t.items.splice(t.items.indexOf(reply), 0, { id: 'red-tests', kind: 'tool', time: reply.time, tool: { name: 'bash', status: 'completed', input: JSON.stringify({ command: 'go test ./...' }), exit_code: 1, output: 'FAIL' } });
  });
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished');
  expect(chip.closest('[title]')?.getAttribute('title')).toBe('Finished · a claim contradicted');
});

test('failed commands alone are no state: the turn keeps its check', async () => {
  withChart((t) => { t.outcome = 'Charted the commits; 2 commands failed'; });
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished');
  expect(chip.querySelector('.lucide-check')).not.toBeNull();
  expect(row().textContent).not.toContain('failed');
});

test('a clean finished turn keeps its check', async () => {
  await openTask('t-chart');
  const chip = await within(header()).findByText('Finished');
  expect(chip.className).toContain('text-success');
  expect(chip.querySelector('.lucide-check')).not.toBeNull();
});
