import { screen, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import * as data from '../../src/mock/data';
import { openTask, renderApp, sidebar } from './render';

const row = (id: string) => document.querySelector<HTMLElement>(`nav[aria-label="Tasks"] [data-task-row="${id}"]`)!;

describe('unsaved git work', () => {
  const st = data.seed();
  const inP1 = st.tasks.find((t) => t.project_id === 'p1' && t.stage !== 'settled' && t.stage !== 'archived')!;
  const elsewhere = st.tasks.find((t) => t.project_id !== 'p1' && !t.git_pending && t.stage !== 'settled' && t.stage !== 'archived')!;
  const n = st.changes.p1.length;

  test('a row shows its folder\'s counts after the Project name, with the words for screen readers and in the tip', async () => {
    renderApp();
    await sidebar();
    const line = row(inP1.id).querySelector('button[data-nav]')!.firstElementChild as HTMLElement;
    // Project name, then the counts, then the state.
    expect(line.textContent).toMatch(new RegExp(`unified-agent-manager${n}1${n} not committed · 1 not pushed`));
    expect(within(line).getByText(`${n} not committed · 1 not pushed`).className).toContain('sr-only');
    expect(row(elsewhere.id).textContent).not.toMatch(/not committed|not pushed/);
  });

  test('the header Changes count is the folder\'s uncommitted count', async () => {
    await openTask(inP1.id);
    expect(await screen.findByRole('button', { name: new RegExp(`^Open changes.*, ${n} not committed`) })).toBeTruthy();
  });
});
