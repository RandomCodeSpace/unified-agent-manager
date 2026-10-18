// When the LLM is engaged and when work runs locally: the open Thread's phase under the composer, and the counts of
// model calls in flight across the service (the sidebar's Usage and AI button shows them: usage-ai.test.tsx). Mock: t1
// runs a tool locally, t8 waits on the model with one subagent, t6 waits on you, t7 compacts, and t22's audits run on
// after its turn ended.
import { act, render, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Phase, SnapshotData } from '../../src/api';
import { PhaseBlock, phaseClock } from '../../src/components/ModelPhase';
import { initialState, reducer } from '../../src/state';
import { openTask } from './render';

afterEach(() => vi.useRealTimers());

const NOW = Date.parse('2026-10-17T12:00:00Z');
const ago = (s: number) => new Date(NOW - s * 1000).toISOString();

function block(phase: Phase | undefined) {
  return render(<PhaseBlock phase={phase} />).container;
}

/** What assistive technology reads of el: its text without what is hidden from it. */
function spoken(el: Element | null | undefined): string {
  if (!el) return '';
  const copy = el.cloneNode(true) as Element;
  copy.querySelectorAll('[aria-hidden="true"], [aria-hidden=""]').forEach((n) => n.remove());
  return copy.textContent ?? '';
}

/** What the block shows: its glyph's word, whose work it is and the clock. */
const shown = (el: Element | null | undefined) => el?.querySelector(CELL)?.querySelector(':scope > span[aria-hidden]')?.textContent ?? '';
/** The block's cell (the detail carries a title too). */
const CELL = '[title]:not([data-phase-detail])';

describe('the phase under the composer', () => {
  test('a local tool is one block with a clock that ticks each second, its tool and argument beside it', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const box = block({ kind: 'local', since: ago(42), tool: 'bash', detail: 'go test ./internal/vterm/...' });
    const phase = box.querySelector('[data-phase="local"]')!;
    expect(spoken(phase)).toBe('bash go test ./internal/vterm/...Local · no model, 0:42');
    expect(shown(phase)).toBe('Local 0:42');
    await act(async () => { vi.advanceTimersByTime(1000); });
    expect(shown(phase)).toBe('Local 0:43');
    expect(phase.querySelector('svg')!.getAttribute('class')).toContain('lucide-terminal');
    // Nothing spins: the Working glyph and the running step keep the only rings.
    expect(box.querySelector('.animate-spin, .animate-pulse')).toBeNull();
  });

  test('a model call is the Cpu block without the model’s name; waiting on you says what is asked; none shows nothing', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const model = block({ kind: 'model', since: ago(65), model: 'gpt-6-luna', calls: 2, subagents: 1 }).querySelector('[data-phase="model"]')!;
    expect(spoken(model)).toBe('Model, 1:05');
    expect(model.textContent).not.toContain('gpt-6-luna');
    expect(model.querySelector('[data-phase-detail]')).toBeNull();
    expect(model.querySelector('svg')!.getAttribute('class')).toContain('lucide-cpu');
    expect(model.querySelector(CELL)!.className).toContain('bg-accent-wash');
    const waiting = block({ kind: 'waiting', since: ago(3725), detail: 'approve bash' }).querySelector('[data-phase="waiting"]')!;
    expect(spoken(waiting)).toBe('approve bashWaiting on you, 1:02:05');
    expect(shown(waiting)).toBe('You 1:02:05');
    expect(waiting.querySelector(CELL)!.className).toContain('bg-warning-wash');
    expect(block(undefined).childElementCount).toBe(0);
  });

  test('the detail is an item of the row that truncates first; a phone hides it and whose work it is', () => {
    const phase = block({ kind: 'model', by: 'subagent', since: ago(5), detail: 'List the exported symbols of web/src/lib that nothing calls.' }).querySelector('[data-phase]')!;
    // The wrapper takes no box, so the detail and the block shrink against the branch in the footer's own row.
    expect(phase.className.split(' ')).toContain('contents');
    const detail = phase.querySelector('[data-phase-detail]')!;
    expect(detail.className.split(' ')).toEqual(expect.arrayContaining(['min-w-0', 'shrink-1000', 'truncate', 'max-sm:hidden']));
    expect(detail.getAttribute('title')).toBe('List the exported symbols of web/src/lib that nothing calls.');
    // The block never shrinks.
    expect(phase.querySelector(CELL)!.parentElement!.className).toContain('shrink-0');
    const by = [...phase.querySelectorAll('span')].find((s) => s.textContent === ' · subagent')!;
    expect(by.className).toContain('max-sm:hidden');
  });

  test('the hairline after the block shows only while the time split follows it', () => {
    const phase = block({ kind: 'model', since: ago(5) }).querySelector('[data-phase]')!;
    const rule = phase.querySelector('[data-phase-rule]')!;
    expect(rule.className.split(' ')).toContain('hidden');
    expect(phase.className).toContain('[&:has(+*)_[data-phase-rule]]:block');
    // A phone shows the block alone: no hairline, and the open Thread's row hides the split after it.
    expect(rule.className).toContain('max-sm:hidden!');
  });

  test('the row under the composer: Files, the branch, the detail, the block, then the time split, which gives way on a phone', async () => {
    await openTask('t1');
    await waitFor(() => expect(document.querySelector('[data-phase="local"]')).not.toBeNull());
    const group = document.querySelector('[data-phase="local"]')!.parentElement!;
    expect(group.className).toContain('max-sm:[&>[data-phase]~*]:hidden');
    await waitFor(() => expect(group.lastElementChild?.getAttribute('aria-label')).toMatch(/^Time in this Thread/));
    const foot = document.querySelector('[data-composer-foot]')!;
    expect(foot.textContent).not.toContain('Current branch');
    // In order: Files, the place, then the right-hand group; the place never takes the free space itself.
    expect(foot.children[0].textContent).toBe('Files');
    const place = foot.querySelector('[data-place]')!;
    expect(place.className.split(' ')).not.toContain('flex-1');
    expect(place.className.split(' ')).toContain('min-w-0');
  });

  test('the clock reads minutes and seconds, hours past the hour', () => {
    expect([phaseClock(0), phaseClock(59_999), phaseClock(605_000), phaseClock(3_729_000), phaseClock(-5)]).toEqual(['0:00', '0:59', '10:05', '1:02:09', '0:00']);
  });

  test('the open Thread shows its own phase: local on t1, the model on t8', async () => {
    await openTask('t1');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="local"]'))).toContain('bash go test ./internal/vterm/... -run Redraw -count=1Local · no model'));
    expect(document.querySelector('[data-phase="model"]')).toBeNull();
  });

  test('t8 waits on the model, and the block leaves its name to the Model picker', async () => {
    await openTask('t8');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="model"]'))).toMatch(/^Model, \d+:\d\d$/));
  });

  test('between turns a subagent still running says so, with what it does', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const model = block({ kind: 'model', by: 'subagent', since: ago(12), model: 'gpt-6-luna', detail: 'Survey the docs', calls: 1, subagents: 1 });
    expect(spoken(model.querySelector('[data-phase="model"]'))).toBe('Survey the docsModel · subagent, 0:12');
    expect(shown(model.querySelector('[data-phase="model"]'))).toBe('Model · subagent 0:12');
    // Without a description it shows no detail: the model's name is the Model picker's.
    expect(spoken(block({ kind: 'model', by: 'subagent', since: ago(3), model: 'gpt-6-luna', calls: 1, subagents: 1 }).querySelector('[data-phase]'))).toBe('Model · subagent, 0:03');
    expect(spoken(block({ kind: 'local', by: 'subagent', since: ago(4), tool: 'bash', detail: 'go test ./...' }).querySelector('[data-phase="local"]'))).toBe('bash go test ./...Local · subagent, 0:04');
    expect(model.querySelector('.animate-spin, .animate-pulse')).toBeNull();
  });

  test('compaction is the Thread’s own model call: it says compacting', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    expect(spoken(block({ kind: 'model', by: 'compaction', since: ago(20), model: 'gpt-6-luna', calls: 1 }).querySelector('[data-phase="model"]'))).toBe('Model · compacting, 0:20');
  });

  test('t22 shows its audits running after the turn', async () => {
    await openTask('t22');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="model"]'))).toContain('List the exported symbols of web/src/lib that nothing calls.Model · subagent'));
  });

  test('t7 shows its compaction', async () => {
    await openTask('t7');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="model"]'))).toContain('Model · compacting'));
  });
});

describe('the model calls in flight', () => {
  test('follows the snapshot and the model_calls frames', () => {
    const snap = { seq: 1, projects: [], sessions: [], session: null, model_calls: { threads: 1, subagents: 0, background: 0, local: 0 } } as unknown as SnapshotData;
    let state = reducer(initialState, { type: 'snapshot', data: snap });
    expect(state.modelCalls).toEqual({ threads: 1, subagents: 0, background: 0, local: 0 });
    state = reducer(state, { type: 'update', data: { name: 'model_calls', seq: 2, model_calls: { threads: 0, subagents: 0, background: 1, local: 2 } } });
    expect(state.modelCalls).toEqual({ threads: 0, subagents: 0, background: 1, local: 2 });
    // An older service's snapshot carries none.
    expect(reducer(state, { type: 'snapshot', data: { ...snap, seq: 3, model_calls: undefined } }).modelCalls).toBeNull();
  });
});
