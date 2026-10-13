import { render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { FileDiff } from '../../src/api';
import { FileView } from '../../src/components/Changes';

function patchOf(lines: number) {
  const body = Array.from({ length: lines }, (_, i) => `+line ${i + 1}`).join('\n');
  return `diff --git a/f.txt b/f.txt\n--- /dev/null\n+++ b/f.txt\n@@ -0,0 +1,${lines} @@\n${body}\n`;
}

test('a long diff mounts its rows a slice at a time and ends complete', async () => {
  const lines = 1000;
  const file: FileDiff = { path: 'f.txt', additions: lines, deletions: 0, patch: patchOf(lines) };
  render(<FileView path="f.txt" file={file} error={null} />);
  const diff = () => document.querySelector('.diff')!;
  const rows = () => diff().querySelectorAll('tbody tr').length;
  // The first paint holds the top of the diff only; the rest follows frame by frame, each slice a table the browser can skip off screen.
  expect(rows()).toBeGreaterThan(0);
  expect(rows()).toBeLessThan(lines + 1);
  await waitFor(() => expect(rows()).toBe(lines + 1));
  expect(screen.getAllByRole('table').length).toBeGreaterThan(1);
  expect(diff().textContent).toContain('line 1000');
  // Line numbers run on across slices.
  const numbers = [...diff().querySelectorAll('td.num + td.num')].map((td) => td.textContent?.trim()).filter(Boolean);
  expect(numbers.at(-1)).toBe(String(lines));
  expect(numbers.length).toBe(lines);
});

test('a renamed file names the path it came from', () => {
  const edited: FileDiff = { path: 'docs/new.md', old_path: 'docs/old.md', additions: 1, deletions: 0,
    patch: 'diff --git a/docs/old.md b/docs/new.md\nsimilarity index 80%\nrename from docs/old.md\nrename to docs/new.md\n--- a/docs/old.md\n+++ b/docs/new.md\n@@ -1 +1,2 @@\n a\n+b\n' };
  const { unmount } = render(<FileView path="docs/new.md" file={edited} error={null} />);
  expect(document.querySelector('.diff .head')?.textContent).toContain('docs/old.md → docs/new.md');
  unmount();
  const pure: FileDiff = { path: 'docs/new.md', old_path: 'docs/old.md', additions: 0, deletions: 0,
    patch: 'diff --git a/docs/old.md b/docs/new.md\nsimilarity index 100%\nrename from docs/old.md\nrename to docs/new.md\n' };
  render(<FileView path="docs/new.md" file={pure} error={null} />);
  expect(screen.getByText(/Renamed from docs\/old\.md/)).toBeTruthy();
});
