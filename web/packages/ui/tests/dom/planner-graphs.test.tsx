import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import type { Card, Project } from '../../src/api';
import { PlanGraph } from '../../src/components/planner/PlanGraph';
import { MAP_GRAPH_GUTTER, mapGraphOption, planGraphOption } from '../../src/components/planner/graph-options';
import type { TaskPlan } from '../../src/components/planner/TaskPlan';
import { GRAPH_NODE, MAP_NODE_H, MAP_NODE_W, childIndex, layoutLevel, layoutMap } from '../../src/lib/board';
import { init } from '../../src/lib/echarts';

const card = (seq: number, over: Partial<Card> = {}): Card => ({
  id: `c${seq}`, seq, project_id: 'p1', kind: 'subtask', parent_id: null, rank: seq, title: `Card ${seq}`, desc: '', win_condition: '', status: 'todo', prio: 0,
  labels: [], checklist: [], blocked: false, blocked_by: [], blocks: [], confirmed: true, pinned_sha: '', accept_cmd: null, paths: [], pending_requests: 0,
  revision: 1, created_at: '', updated_at: '', moved_at: '', ...over,
});
const project = { id: 'p1', name: 'Project' } as Project;
const taskPlan = (cards: Card[]): TaskPlan => ({ status: 'ready', project, cards, byId: new Map(cards.map((c) => [c.id, c])), index: childIndex(cards), path: [], mine: cards[0] });
afterEach(() => vi.restoreAllMocks());

test('ECharts draws deterministic map nodes and literal labels at the existing pixel coordinates', () => {
  const cards = [card(1, { kind: 'epic', title: '<img> {safe|title}', progress: { done: 1, total: 2, proposed: 0 } }), card(2, { kind: 'story', parent_id: 'c1' }), card(3, { kind: 'story', parent_id: 'c1', blocked_by: ['c2'], confirmed: false })];
  const layout = layoutMap(cards, { epic: '', showCancelled: false });
  const option = mapGraphOption(layout, 'c1');
  expect(mapGraphOption(layoutMap(cards, { epic: '', showCancelled: false }), 'c1')).toEqual(option);
  const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: layout.width + MAP_GRAPH_GUTTER, height: layout.height });
  try {
    drawing.setOption(option);
    for (const node of layout.nodes) expect(drawing.convertToPixel({ xAxisIndex: 0, yAxisIndex: 0 }, [node.x + MAP_NODE_W / 2, node.y + MAP_NODE_H / 2])).toEqual([node.x + MAP_NODE_W / 2, node.y + MAP_NODE_H / 2]);
    // zrender's own viewport clips its SVG. Every blocker curve must fit the drawing,
    // while its nodes stay at their original coordinates.
    const curves = drawing.getZr().storage.getDisplayList().filter((element) => element.type === 'bezier-curve');
    expect(curves).toHaveLength(layout.edges.length);
    const rightEdges = curves.map((element) => { const rect = element.getBoundingRect(); return rect.x + rect.width; });
    expect(Math.max(...rightEdges)).toBeGreaterThan(layout.width);
    expect(Math.max(...rightEdges)).toBeLessThan(drawing.getWidth());
    const svg = drawing.renderToSVGString();
    expect(svg).toContain('&lt;img&gt; {safe|title}');
    expect(svg).toContain('#1 · Epic · To do · 1/2');
    expect(svg).toContain('stroke-dasharray=');
    const picture = new DOMParser().parseFromString(svg, 'image/svg+xml');
    expect(picture.querySelectorAll('[ecmeta_series_index="0"]')).toHaveLength(cards.length);
    expect(picture.querySelectorAll('path').length).toBeGreaterThan(cards.length + layout.edges.length);
    expect(svg).not.toMatch(/NaN|Infinity|<img|<foreignObject/);
  } finally { drawing.dispose(); }
});

test('dependency graphs render both orientations without shifting nodes or losing their task state', () => {
  const cards = [card(1, { status: 'doing' }), card(2, { blocked_by: ['c1'], confirmed: false })];
  const plan = taskPlan(cards);
  for (const dir of ['lr', 'tb'] as const) {
    const layout = layoutLevel(cards, dir);
    const option = planGraphOption(layout, plan, 'c2');
    expect(planGraphOption(layoutLevel(cards, dir), plan, 'c2')).toEqual(option);
    const drawing = init(null, undefined, { renderer: 'svg', ssr: true, width: layout.width + 16, height: layout.height + 16 });
    try {
      drawing.setOption(option);
      expect(drawing.convertToPixel({ xAxisIndex: 0, yAxisIndex: 0 }, [8 + GRAPH_NODE.w / 2, 8 + GRAPH_NODE.h / 2])).toEqual([8 + GRAPH_NODE.w / 2, 8 + GRAPH_NODE.h / 2]);
      const svg = drawing.renderToSVGString();
      const picture = new DOMParser().parseFromString(svg, 'image/svg+xml');
      expect(picture.querySelectorAll('[ecmeta_series_index="0"]')).toHaveLength(cards.length);
      expect(svg).toContain('This task');
      expect(svg).toContain('In progress');
      expect(svg).toContain('Proposed');
      expect(svg).not.toMatch(/NaN|Infinity/);
    } finally { drawing.dispose(); }
  }
});

test('dependency zoom, drag and keyboard pan leave node activation accessible and resettable', async () => {
  const user = userEvent.setup();
  const onSelect = vi.fn();
  render(<PlanGraph plan={taskPlan([card(1), card(2, { blocked_by: ['c1'] })])} level="" selected={null} onLevel={vi.fn()} onSelect={onSelect} details={() => null} />);
  const viewport = screen.getByRole('group', { name: 'Dependencies between the subtasks' });
  await waitFor(() => expect(viewport.querySelector('svg')?.textContent).toContain('Card 1'));
  await user.click(screen.getByRole('button', { name: 'Zoom in on dependencies' }));
  expect(screen.getByText('125%')).toBeTruthy();
  Object.defineProperty(viewport, 'setPointerCapture', { value: vi.fn(), configurable: true });
  fireEvent.pointerDown(viewport, { pointerId: 1, isPrimary: true, pointerType: 'mouse', button: 0, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(viewport, { pointerId: 1, isPrimary: true, pointerType: 'mouse', clientX: 40, clientY: 30 });
  fireEvent.pointerUp(viewport, { pointerId: 1 });
  expect(viewport.scrollLeft).toBe(60);
  expect(viewport.scrollTop).toBe(70);
  fireEvent.click(screen.getByRole('button', { name: /^#1 Card 1,/ }), { detail: 1 });
  expect(onSelect).not.toHaveBeenCalled();
  screen.getByRole('button', { name: /^#1 Card 1,/ }).focus();
  await user.keyboard('{Enter}');
  expect(onSelect).toHaveBeenCalledWith('c1');
  viewport.focus();
  await user.keyboard('{ArrowRight}');
  expect(viewport.scrollLeft).toBe(100);
  // happy-dom's WheelEvent omits MouseEvent modifier keys.
  const wheel = (deltaY: number) => {
    const event = new WheelEvent('wheel', { deltaY, cancelable: true });
    Object.defineProperty(event, 'ctrlKey', { value: true });
    fireEvent(viewport, event);
    expect(event.defaultPrevented).toBe(true);
  };
  wheel(-10000);
  expect(screen.getByText('200%')).toBeTruthy();
  expect((screen.getByRole('button', { name: 'Zoom in on dependencies' }) as HTMLButtonElement).disabled).toBe(true);
  wheel(10000);
  expect(screen.getByText('40%')).toBeTruthy();
  expect((screen.getByRole('button', { name: 'Zoom out of dependencies' }) as HTMLButtonElement).disabled).toBe(true);
  await user.click(screen.getByRole('button', { name: 'Reset dependency view' }));
  expect(screen.getByText('100%')).toBeTruthy();
  expect(viewport.scrollLeft).toBe(0);
  expect(viewport.scrollTop).toBe(0);
});
