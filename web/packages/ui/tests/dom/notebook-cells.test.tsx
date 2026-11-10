// The notebook layout's cells (components/notebook/Cell): each turn of the main conversation in a
// numbered cell of two tracks. The current layout's DOM is held by a snapshot taken before the cell existed.
import { cleanup, fireEvent, render, waitFor, within } from '@testing-library/react';
import { createPortal } from 'react-dom';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { Interaction, Item, TurnTiming } from '../../src/api';
import { NotebookSlots, useNotebookSlot } from '../../src/components/notebook/slots';
import { Transcript } from '../../src/components/Transcript';
import { applyLayout, type Layout } from '../../src/lib/layout';
import { log, openTask } from './render';

const at = (s: number) => new Date(Date.UTC(2026, 10, 10, 9, 0, s)).toISOString();

// The clock stands three minutes after the first message, so the running turn's time and the cells' ages are fixed.
beforeEach(() => void vi.useFakeTimers({ toFake: ['Date'], now: new Date(at(180)) }));
afterEach(() => {
  vi.useRealTimers();
  applyLayout('classic');
});

/** Two finished turns (the first with a call, the second stopped) and one running with a call in progress. */
const items: Item[] = [
  { id: 'u1', kind: 'user', time: at(0), text: 'Which templates render a post?' },
  { id: 'c1', kind: 'tool', time: at(1), tool: { name: 'view', status: 'completed', input: JSON.stringify({ path: '/w/templates/post.html' }) } },
  { id: 'a1', kind: 'assistant', time: at(5), text: 'Three templates render a post.' },
  { id: 'u2', kind: 'user', time: at(60), text: 'Does the cover image have alt text?' },
  { id: 'a2', kind: 'assistant', time: at(70), text: 'No, line 13 has none.' },
  { id: 'u3', kind: 'user', time: at(120), text: 'Audit the template and fix what is safe.' },
  { id: 'a3', kind: 'assistant', time: at(125), text: 'Splitting this into two surveys.' },
  { id: 'c3', kind: 'tool', time: at(130), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'npm test' }) } },
];
const timings: TurnTiming[] = [
  { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(10), state: 'completed' },
  { id: 't2', user_item_id: 'u2', started_at: at(60), ended_at: at(75), state: 'cancelled' },
  { id: 't3', user_item_id: 'u3', started_at: at(120), state: 'working' },
];

type Options = { layout?: Layout; items?: Item[]; timings?: TurnTiming[]; identity?: Item[]; live?: boolean; working?: boolean; liveCard?: boolean; agentId?: string; interactions?: Interaction[] };

function draw({ layout = 'notebook', live = true, working = true, liveCard = true, agentId, interactions = [], ...o }: Options = {}) {
  applyLayout(layout);
  return render(
    <Transcript sessionId="s" agentId={agentId} items={o.items ?? items} identityItems={o.identity} turnTimings={o.timings ?? timings} interactions={interactions} subagents={[]} live={live} working={working} liveCard={liveCard} provider="copilot" workdir="/w" />,
  ).container;
}

/** The DOM without what the reader's clock and locale decide: the times' text and the tooltips. */
const structure = (el: Element) => el.innerHTML.replace(/(<time\b[^>]*>)[^<]*/g, '$1').replace(/ (title|datetime)="[^"]*"/g, '');

describe('the current layout', () => {
  test('the transcript is the DOM it was before the notebook layout', () => {
    expect(structure(draw({ layout: 'classic' }))).toMatchSnapshot();
  });
});

describe('the notebook layout', () => {
  const cellOf = (root: Element, reply: string) => root.querySelector<HTMLElement>(`[data-reply="${reply}"]`)!;
  const track = (cell: Element, name: 'said' | 'did') => cell.querySelector<HTMLElement>(`[data-track="${name}"]`)!;

  test('the current layout draws no cell, and a subagent\'s transcript none in either', () => {
    const classic = draw({ layout: 'classic' });
    expect(classic.querySelector('.nb-cell, [data-cell], [data-track], [data-notebook-slot]')).toBeNull();
    // Today's order: the message, then its turn (head over answer).
    expect([...classic.children].slice(0, 4).map((el) => el.getAttribute('data-history-anchor') ?? el.getAttribute('data-reply'))).toEqual(['u1', 'u1', 'u2', 'u2']);
    cleanup();
    const agent = draw({ agentId: 'agent-1' });
    expect(agent.querySelector('.nb-cell, [data-cell], [data-track], [data-notebook-slot]')).toBeNull();
    expect(agent.querySelectorAll('[data-reply]')).toHaveLength(3);
  });

  test('each turn is a cell with its number and its state in a word', () => {
    const root = draw();
    const cells = [...root.querySelectorAll<HTMLElement>('.nb-cell')];
    // Nothing else stands beside the cells: the messages and the foot are inside them.
    expect([...root.children]).toEqual(cells);
    expect(cells.map((cell) => [cell.dataset.reply, cell.dataset.cell])).toEqual([['u1', 'done'], ['u2', 'stopped'], ['u3', 'running']]);
    expect(cells.map((cell) => cell.querySelector('.nb-gutter')!.textContent)).toEqual(['Turn 1Done10s3m ago', 'Turn 2Stopped15s2m ago', 'Turn 3Running']);
    expect(cells.map((cell) => cell.querySelector('[data-cell-state]')!.textContent)).toEqual(['Done', 'Stopped', 'Running']);
  });

  test('the message and the answer are in "said", the turn\'s head in "did"', () => {
    const root = draw();
    const first = cellOf(root, 'u1');
    const said = track(first, 'said'), did = track(first, 'did');
    expect(said.querySelector('[data-history-anchor="u1"]')!.textContent).toContain('Which templates render a post?');
    expect(said.textContent).toContain('Three templates render a post.');
    // The message comes before the answer, as today.
    expect(said.querySelector('[data-history-anchor="u1"]')!.compareDocumentPosition(within(said).getByText('Three templates render a post.')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(said.querySelector('[data-history-anchor^="turn-head-"]')).toBeNull();
    const head = did.querySelector<HTMLElement>('[data-history-anchor^="turn-head-"]')!;
    expect(head.textContent).toContain('Took 10s');
    // The timeline opens inside the "did" track.
    fireEvent.click(within(did).getByRole('button', { name: /Show the activity of this turn/ }));
    expect(within(did).getByText(/Read .*post\.html/)).toBeTruthy();
    expect(said.textContent).not.toMatch(/Read .*post\.html/);
  });

  test('the running cell holds the foot: the live step in "said", and the two slots while it is the live tail', () => {
    const root = draw();
    const last = cellOf(root, 'u3');
    // The call in progress stays in the "said" track, under the answer.
    expect(within(track(last, 'said')).getByText('npm test')).toBeTruthy();
    expect(track(last, 'did').textContent).toContain('Working');
    expect(track(last, 'did').firstElementChild!.getAttribute('data-notebook-slot')).toBe('live');
    expect(track(last, 'said').lastElementChild!.getAttribute('data-notebook-slot')).toBe('asks');
    expect(root.querySelectorAll('[data-notebook-slot]')).toHaveLength(2);
    cleanup();
    // A window that does not reach the live tail, and the current layout, give none.
    expect(draw({ liveCard: false, live: false, working: false }).querySelector('[data-notebook-slot]')).toBeNull();
    cleanup();
    expect(draw({ layout: 'classic' }).querySelector('[data-notebook-slot]')).toBeNull();
  });

  test('the Thread fills the slots through notebook/slots', () => {
    function Filler() {
      const live = useNotebookSlot('live'), asks = useNotebookSlot('asks');
      return <>{live && createPortal(<p>the live card</p>, live)}{asks && createPortal(<p>a question that waits</p>, asks)}</>;
    }
    applyLayout('notebook');
    const { container } = render(
      <NotebookSlots>
        <Transcript sessionId="s" items={items} turnTimings={timings} interactions={[]} subagents={[]} live working liveCard provider="copilot" workdir="/w" />
        <Filler />
      </NotebookSlots>,
    );
    const last = cellOf(container, 'u3');
    expect(track(last, 'did').firstElementChild!.textContent).toBe('the live card');
    expect(track(last, 'said').lastElementChild!.textContent).toBe('a question that waits');
  });

  test('a turn that waits on the owner reads "Asked you"', () => {
    const root = draw({ live: true, working: false });
    expect(cellOf(root, 'u3').dataset.cell).toBe('asked');
    expect(cellOf(root, 'u3').querySelector('[data-cell-state]')!.textContent).toBe('Asked you');
  });

  test('a turn whose message is outside the window keeps its cell and its true number', () => {
    // The window starts inside turn 2: its message and all of turn 1 are not loaded.
    const root = draw({ items: items.slice(4), identity: items });
    const cells = [...root.querySelectorAll<HTMLElement>('.nb-cell')];
    expect(cells.map((cell) => cell.dataset.reply)).toEqual(['u2', 'u3']);
    expect(track(cells[0], 'said').querySelector('[data-history-anchor="u2"]')).toBeNull();
    expect(track(cells[0], 'said').textContent).toContain('No, line 13 has none.');
    expect(cells.map((cell) => cell.querySelector('.nb-gutter')!.textContent)).toEqual(['Turn 2Stopped15s2m ago', 'Turn 3Running']);
  });

  test('a turn that ended with nothing still reads, and what came before any message stays outside a cell', () => {
    const before: Item = { id: 'n0', kind: 'assistant', time: at(-5), text: 'Started by a routine.' };
    const root = draw({ items: [before, items[0], ...items.slice(3)], live: false, working: false, timings: [timings[0], timings[1], { ...timings[2], ended_at: at(140), state: 'failed' }] });
    // Before the first message: today's full-width turn, no cell.
    const start = cellOf(root, 'start');
    expect(start.classList.contains('nb-cell')).toBe(false);
    expect(start.parentElement).toBe(root);
    expect(start.textContent).toContain('Started by a routine.');
    // Turn 1 has its message and its end, nothing else.
    const empty = cellOf(root, 'u1');
    expect(empty.classList.contains('nb-cell')).toBe(true);
    expect(track(empty, 'said').textContent).toContain('Which templates render a post?');
    expect(track(empty, 'did').textContent).toBe('Took 10s · No reply');
    expect(cellOf(root, 'u3').querySelector('[data-cell-state]')!.textContent).toBe('Failed');
  });

  test('a message just sent has its cell at once: the status of a turn with nothing in it yet is its head', () => {
    const root = draw({ items: items.slice(0, 6) });
    const last = cellOf(root, 'u3');
    expect(root.lastElementChild).toBe(last);
    expect(track(last, 'said').textContent).toContain('Audit the template and fix what is safe.');
    expect(track(last, 'did').textContent).toContain('Working');
    expect(last.dataset.cell).toBe('running');
  });

  test('a steer message stays where it is in the answer', () => {
    const steer: Item = { id: 's3', kind: 'user', time: at(127), text: 'Skip the linter.', delivery: 'steer' };
    const root = draw({ items: [...items.slice(0, 7), steer, items[7]] });
    expect(root.querySelectorAll('.nb-cell')).toHaveLength(3);
    const said = track(cellOf(root, 'u3'), 'said');
    const order = ['Audit the template and fix what is safe.', 'Splitting this into two surveys.', 'Skip the linter.'].map((text) => said.textContent!.indexOf(text));
    expect(order).toEqual([...order].sort((a, b) => a - b));
    expect(order.every((i) => i >= 0)).toBe(true);
  });

  test('what the scroller and the jumps look rows up by still resolves', () => {
    const root = draw();
    for (const key of ['u1', 'u2', 'u3']) {
      // Task.tsx jumpToReply: the turn by its reply key, then its head.
      const turn = root.querySelector<HTMLElement>(`[data-reply="${CSS.escape(key)}"]`)!;
      expect(turn.classList.contains('nb-cell')).toBe(true);
      expect(turn.querySelector('[data-history-anchor^="turn-head-"]')).toBeTruthy();
      // HistoryAnchor.row: the message by its id.
      expect(turn.contains(root.querySelector(`[data-history-anchor="${CSS.escape(key)}"]`))).toBe(true);
    }
    expect(root.querySelectorAll('[data-reply]')).toHaveLength(3);
    // HistoryAnchor.group, Task.tsx locate and jumpTo: the fold that holds an item.
    const fold = (id: string) => [...root.querySelectorAll<HTMLElement>('[data-history-items]')].find((node) => (JSON.parse(node.dataset.historyItems!) as string[]).includes(id));
    expect(cellOf(root, 'u1').contains(fold('c1')!)).toBe(true);
    expect(cellOf(root, 'u3').contains(fold('c3')!)).toBe(true);
    // The rows HistoryAnchor anchors by are the ones the current layout has, no more and no fewer.
    const rows = (el: Element) => [...el.querySelectorAll<HTMLElement>('[data-history-anchor], [data-history-items], [id^="item-"]')].map((row) => row.dataset.historyAnchor ?? row.id).sort();
    const notebook = rows(root);
    cleanup();
    expect(notebook).toEqual(rows(draw({ layout: 'classic' })));
  });
});

describe('in the app', () => {
  const live = () => log().queryByRole('region', { name: 'Subagents at work' });

  test('the notebook layout leaves the subagents at work to the live card', async () => {
    vi.useRealTimers();
    applyLayout('classic');
    await openTask('t8');
    await waitFor(() => expect(live()).toBeTruthy());
    cleanup();
    applyLayout('notebook');
    await openTask('t8');
    await waitFor(() => expect(document.querySelector('.nb-cell[data-cell="running"]')).toBeTruthy());
    expect(live()).toBeNull();
    // The running cell gives the Thread its two slots.
    expect(document.querySelectorAll('[role="log"] [data-notebook-slot]')).toHaveLength(2);
  });
});
