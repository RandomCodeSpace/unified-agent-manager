import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import type { Item } from '../../src/api';
import { receipts } from '../../src/lib/receipts';

// The whole body the item route would answer with, by item ID.
const bodies = new Map<string, Item>();
const reads: string[] = [];
vi.mock('../../src/components/Details', async (original) => ({
  ...(await original<typeof import('../../src/components/Details')>()),
  useBodyRead: () => async (item: Item) => {
    reads.push(item.id);
    return bodies.get(item.id) ?? item;
  },
}));
const { Receipts } = await import('../../src/components/Receipts');

const bash = (id: string, command: string, extra: Partial<NonNullable<Item['tool']>> = {}, item: Partial<Item> = {}): Item => ({ id, kind: 'tool', time: '2026-10-12T00:00:01Z', tool: { name: 'bash', status: 'completed', input: JSON.stringify({ command }), exit_code: 0, ...extra }, ...item });
const stampTexts = () => within(screen.getByRole('list', { name: 'Receipts' })).getAllByRole('listitem').map((li) => li.textContent);

describe('receipts', () => {
  test('a test claim on a run whose output is held back waits, reads the whole output, then settles', async () => {
    // As the compact view sends it: output held back, no counts.
    const held = bash('b1', 'go test ./... 2>&1 | tail -3', { input: undefined, display_arg: 'go test ./... 2>&1 | tail -3', has_output: true }, { compact: { has_text: false, has_reasoning: false } });
    bodies.set('b1', bash('b1', 'go test ./... 2>&1 | tail -3', { output: '--- FAIL: TestX\nFAIL' }));
    const stamps = receipts('Fixed it; all 4 tests pass.', [held]);
    render(<Receipts stamps={stamps} />);
    // Never a final verdict before the read: the pending stamp says it is being checked, in words.
    expect(stampTexts()).toEqual(['all 4 tests passreading the test output']);
    await waitFor(() => expect(stampTexts()).toEqual(['all 4 tests passlast test run reported failures']));
    expect(reads).toContain('b1');
  });

  test('every claim counts; past the first twelve the rest wait behind Show N more', async () => {
    const files = Array.from({ length: 15 }, (_, i) => `f${i}.go`);
    const text = `Updated ${files.map((f) => `\`${f}\``).join(', ')}.`;
    const { rerender } = render(<Receipts stamps={receipts(text, [])} />);
    expect(stampTexts()).toHaveLength(13);
    await userEvent.click(screen.getByRole('button', { name: 'Show 3 more' }));
    expect(stampTexts()).toHaveLength(15);
    expect(screen.queryByRole('button', { name: /more$/ })).toBeNull();
    // All verified: the summary counts all fifteen, not the twelve shown first.
    const edits: Item[] = files.map((f, i) => ({ id: `e${i}`, kind: 'tool', time: '2026-10-12T00:00:01Z', tool: { name: 'edit', status: 'completed', input: JSON.stringify({ path: f }) } }));
    rerender(<Receipts stamps={receipts(text, edits)} />);
    expect(screen.getByText('Its 15 claims match the record')).toBeTruthy();
  });
});
