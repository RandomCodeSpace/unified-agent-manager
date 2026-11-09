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
    // Every run passed: nothing at all. None ran this turn (an earlier turn's work, restated): no red stamp, only the quiet line.
    rerender(<Receipts stamps={receipts(text, runs(0))} />);
    expect(container.innerHTML).toBe('');
    rerender(<Receipts stamps={receipts(text, [])} />);
    expect(screen.queryByRole('list', { name: 'Receipts' })).toBeNull();
    expect(screen.getByRole('button', { name: '15 claims have no record in this turn' })).toBeTruthy();
  });

  test('claims the record does not show sit behind one closed line under the red stamps, which stay as they were', async () => {
    const text = 'I ran `make build` and `npm test` and updated `inventory.py`; `ls` lists 5 entries.';
    render(<Receipts stamps={receipts(text, [bash('b1', 'make build', { exit_code: 2, status: 'failed' })])} />);
    expect(stampTexts()).toEqual(['make buildexit 2']);
    const line = screen.getByRole('button', { name: '2 claims have no record in this turn' });
    expect(line.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByRole('list', { name: 'Claims with no record in this turn' })).toBeNull();
    await userEvent.click(line);
    expect(line.getAttribute('aria-expanded')).toBe('true');
    const rows = within(screen.getByRole('list', { name: 'Claims with no record in this turn' })).getAllByRole('listitem').map((li) => li.textContent);
    // `ls` is only named, not said to have run: not listed.
    expect(rows).toEqual(['npm test· not run this turn', 'inventory.py· not changed this turn']);
  });

  test('the line waits until no stamp is pending', async () => {
    const held = bash('b2', 'go test ./... 2>&1 | tail -3', { input: undefined, display_arg: 'go test ./... 2>&1 | tail -3', has_output: true }, { compact: { has_text: false, has_reasoning: false } });
    bodies.set('b2', bash('b2', 'go test ./... 2>&1 | tail -3', { output: 'ok  \tpkg\t0.2s' }));
    render(<Receipts stamps={receipts('Updated `inventory.py`; all tests pass.', [held])} />);
    expect(screen.queryByRole('button', { name: /no record/ })).toBeNull();
    expect(await screen.findByRole('button', { name: '1 claim has no record in this turn' })).toBeTruthy();
  });
});
