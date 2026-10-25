import { screen, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import * as data from '../../src/mock/data';
import { openTask, renderApp, sidebar } from './render';

const row = (id: string) => document.querySelector<HTMLElement>(`nav[aria-label="Threads"] [data-task-row="${id}"]`)!;

describe("a Thread's own changes", () => {
  const st = data.seed();
  const inP1 = st.tasks.find((t) => t.project_id === 'p1' && t.stage !== 'settled' && t.stage !== 'archived' && st.changes.p1.some((f) => f.by?.includes(t.id)))!;
  const mine = st.changes.p1.filter((f) => f.by?.includes(inP1.id)).length;

  test('a row shows the files its Thread changed after the Project name, never its folder\'s counts', async () => {
    renderApp();
    await sidebar();
    const line = row(inP1.id).querySelector('button[data-nav]')!.firstElementChild as HTMLElement;
    expect(line.textContent).toMatch(/^.*unified-agent-manager(\d+)\1 files? changed by this thread/);
    expect(within(line).getByText(/changed by this thread$/).className).toContain('sr-only');
    expect(document.querySelector('nav[aria-label="Threads"]')!.textContent).not.toMatch(/not committed|not pushed/);
  });

  test('the header Changes count is the Thread\'s own changed files', async () => {
    await openTask(inP1.id);
    expect(await screen.findByRole('button', { name: new RegExp(`^Open changes, ${mine} files?`) })).toBeTruthy();
  });
});
