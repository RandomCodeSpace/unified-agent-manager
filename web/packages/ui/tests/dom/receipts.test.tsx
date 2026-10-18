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
    // Nothing shows before the read: a stamp appears only once the record proves the claim wrong.
    expect(screen.queryByRole('list', { name: 'Receipts' })).toBeNull();
    await waitFor(() => expect(stampTexts()).toEqual(['all 4 tests passlast test run reported failures']));
    expect(reads).toContain('b1');
  });

  test('only contradicted claims show; past the first twelve the rest wait behind Show N more; claims that hold or were not seen say nothing', async () => {
    const commands = Array.from({ length: 15 }, (_, i) => `make t${i}`);
    const text = `Ran ${commands.map((c) => `\`${c}\``).join(', ')}.`;
    const runs = (exit: number) => commands.map((c, i) => bash(`b${i}`, c, { exit_code: exit, status: exit ? 'failed' : 'completed' }));
    const { rerender, container } = render(<Receipts stamps={receipts(text, runs(2))} />);
    expect(stampTexts()).toHaveLength(13);
    await userEvent.click(screen.getByRole('button', { name: 'Show 3 more' }));
    expect(stampTexts()).toHaveLength(15);
    expect(screen.queryByRole('button', { name: /more$/ })).toBeNull();
    // Every run passed, or none ran this turn (an earlier turn's work, restated): nothing at all.
    rerender(<Receipts stamps={receipts(text, runs(0))} />);
    expect(container.innerHTML).toBe('');
    rerender(<Receipts stamps={receipts(text, [])} />);
    expect(container.innerHTML).toBe('');
  });
});
