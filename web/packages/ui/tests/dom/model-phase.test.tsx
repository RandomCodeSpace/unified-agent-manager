// When the LLM is engaged and when work runs locally: the open Thread's phase under the composer, and the counts of
// model calls in flight across the service (the sidebar's Usage and AI button shows them: usage-ai.test.tsx). Mock: t1
// runs a tool locally, t8 waits on the model with one subagent, t6 waits on you, t7 compacts, and t22's audits run on
// after its turn ended.
import { act, render, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Phase, SnapshotData } from '../../src/api';
import { PhasePill, phaseClock } from '../../src/components/ModelPhase';
import { initialState, reducer } from '../../src/state';
import { openTask } from './render';

afterEach(() => vi.useRealTimers());

const NOW = Date.parse('2026-10-17T12:00:00Z');
const ago = (s: number) => new Date(NOW - s * 1000).toISOString();

function pill(phase: Phase | undefined) {
  return render(<PhasePill phase={phase} provider="copilot" />).container;
}

/** What assistive technology reads of el: its text without what is hidden from it. */
function spoken(el: Element | null | undefined): string {
  if (!el) return '';
  const copy = el.cloneNode(true) as Element;
  copy.querySelectorAll('[aria-hidden="true"]').forEach((n) => n.remove());
  return copy.textContent ?? '';
}

describe('the phase under the composer', () => {
  test('a local tool says so in words, with the tool, its argument and a clock that ticks each second', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const box = pill({ kind: 'local', since: ago(42), tool: 'bash', detail: 'go test ./internal/vterm/...' });
    const shown = box.querySelector('[data-phase="local"]')!;
    expect(spoken(shown)).toBe('Local · no modelbash go test ./internal/vterm/...0:42');
    await act(async () => { vi.advanceTimersByTime(1000); });
    expect(shown.textContent).toContain('0:43');
    // Nothing spins: the Working glyph and the running step keep the only rings.
    expect(box.querySelector('.animate-spin, .animate-pulse')).toBeNull();
  });

  test('a model call names the model; waiting on you says what is asked; none shows nothing', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    expect(spoken(pill({ kind: 'model', since: ago(65), model: 'gpt-6-luna', calls: 2, subagents: 1 }).querySelector('[data-phase="model"]'))).toBe('Modelgpt-6-luna1:05');
    expect(spoken(pill({ kind: 'waiting', since: ago(3725), detail: 'approve bash' }).querySelector('[data-phase="waiting"]'))).toBe('Waiting on youapprove bash1:02:05');
    expect(pill(undefined).childElementCount).toBe(0);
  });

  test('a phone shows the glyph, a short word and the clock: the words and the detail give way', () => {
    for (const [kind, short, label] of [['model', 'Model', 'Model'], ['local', 'Local', 'Local · no model'], ['waiting', 'You', 'Waiting on you']] as const) {
      const shown = pill({ kind, since: ago(5), detail: 'approve bash', tool: 'bash', model: 'gpt-6-luna' }).querySelector(`[data-phase="${kind}"]`)!;
      const word = [...shown.querySelectorAll('span')].find((s) => s.textContent === short && s.getAttribute('aria-hidden') === 'true')!;
      expect(word.className).toContain('sm:hidden');
      expect([...shown.querySelectorAll('span')].find((s) => s.textContent === label && !s.getAttribute('aria-hidden'))!.className).toContain('max-sm:sr-only');
      const detail = [...shown.children].find((c) => c.className.includes('truncate'));
      expect(detail?.className).toContain('max-sm:hidden');
    }
  });

  test('the hairline after the pill shows only while the time split follows it', () => {
    const shown = pill({ kind: 'model', since: ago(5) }).querySelector('[data-phase]')!;
    const rule = shown.querySelector('[data-phase-rule]')!;
    expect(rule.className.split(' ')).toContain('hidden');
    expect(shown.className).toContain('[&:has(+*)_[data-phase-rule]]:block');
    // A phone shows the pill alone: no hairline, and the open Thread's strip hides the split after it.
    expect(rule.className).toContain('max-sm:hidden!');
  });

  test('the time split follows the pill in the strip under the composer, and gives way to it on a phone', async () => {
    await openTask('t1');
    await waitFor(() => expect(document.querySelector('[data-phase="local"]')).not.toBeNull());
    const group = document.querySelector('[data-phase="local"]')!.parentElement!;
    expect(group.className).toContain('max-sm:[&>[data-phase]+*]:hidden');
    await waitFor(() => expect(group.lastElementChild?.getAttribute('aria-label')).toMatch(/^Time in this Thread/));
  });

  test('the clock reads minutes and seconds, hours past the hour', () => {
    expect([phaseClock(0), phaseClock(59_999), phaseClock(605_000), phaseClock(3_729_000), phaseClock(-5)]).toEqual(['0:00', '0:59', '10:05', '1:02:09', '0:00']);
  });

  test('the open Thread shows its own phase: local on t1, the model on t8', async () => {
    await openTask('t1');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="local"]'))).toContain('Local · no modelbash go test ./internal/vterm/... -run Redraw -count=1'));
    expect(document.querySelector('[data-phase="model"]')).toBeNull();
  });

  test('t8 waits on the model', async () => {
    await openTask('t8');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="model"]'))).toContain('ModelClaude Haiku 4.5'));
  });

  test('between turns a subagent still running says so, with what it does', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    const model = pill({ kind: 'model', by: 'subagent', since: ago(12), model: 'gpt-6-luna', detail: 'Survey the docs', calls: 1, subagents: 1 });
    expect(spoken(model.querySelector('[data-phase="model"]'))).toBe('Model · subagentSurvey the docs0:12');
    // Without a description it names the model.
    expect(spoken(pill({ kind: 'model', by: 'subagent', since: ago(3), model: 'gpt-6-luna', calls: 1, subagents: 1 }).querySelector('[data-phase]'))).toBe('Model · subagentgpt-6-luna0:03');
    expect(spoken(pill({ kind: 'local', by: 'subagent', since: ago(4), tool: 'bash', detail: 'go test ./...' }).querySelector('[data-phase="local"]'))).toBe('Local · subagentbash go test ./...0:04');
    // The phone keeps its short word; nothing spins.
    expect([...model.querySelectorAll('[aria-hidden="true"]')].map((n) => n.textContent)).toContain('Model');
    expect(model.querySelector('.animate-spin, .animate-pulse')).toBeNull();
  });

  test('compaction is the Thread’s own model call: it says compacting, with the model', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    vi.setSystemTime(NOW);
    expect(spoken(pill({ kind: 'model', by: 'compaction', since: ago(20), model: 'gpt-6-luna', calls: 1 }).querySelector('[data-phase="model"]'))).toBe('Model · compactinggpt-6-luna0:20');
  });

  test('t22 shows its audits running after the turn', async () => {
    await openTask('t22');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="model"]'))).toContain('Model · subagentList the exported symbols of web/src/lib that nothing calls.'));
  });

  test('t7 shows its compaction', async () => {
    await openTask('t7');
    await waitFor(() => expect(spoken(document.querySelector('[data-phase="model"]'))).toContain('Model · compactingClaude Haiku 4.5'));
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
