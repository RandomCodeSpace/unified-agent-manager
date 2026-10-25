// Drawings against the mock: t-chart holds a flowchart fence (d2), a gantt fence (d3) and a
// fence whose drawing exists with unsent changes (d4, mock/drawings.ts). Excalidraw needs a canvas
// happy-dom has not, so the package is a stand-in here; the real editor runs in scripts/check-drawings.mjs.
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { mermaidFences } from '../../src/lib/drawings';
import { composer, log, openTask } from './render';

vi.mock('../../src/lib/diagram', async (original) => {
  const actual = await original<typeof import('../../src/lib/diagram')>();
  return {
    ...actual,
    renderDiagram: async (source: string) => ({ svg: '<svg width="10" height="10"></svg>', width: 10, height: 10, diagramType: source.startsWith('gantt') ? 'gantt' : 'flowchart-v2' }),
    convertDiagram: async () => ({ elements: [{ type: 'rectangle', id: 'a', x: 0, y: 0, width: 100, height: 40, label: { text: 'Browser' } }, { type: 'rectangle', id: 'b', x: 200, y: 0, width: 100, height: 40, label: { text: 'Proxy' } }], files: {} }),
  };
});

type Emit = (elements: Record<string, unknown>[], state: unknown, files: Record<string, unknown>) => void;
/** The stand-in's `onChange`, so a test can report scenes the way Excalidraw does. */
let emit: Emit | null = null;

vi.mock('@excalidraw/excalidraw', () => ({
  Excalidraw: ({ excalidrawAPI, onChange }: { excalidrawAPI?: (api: unknown) => void; onChange?: Emit }) => {
    emit = onChange ?? null;
    excalidrawAPI?.({ scrollToContent: () => {}, updateScene: () => {} });
    return <div className="excalidraw-container" tabIndex={-1} data-testid="excalidraw" />;
  },
  MainMenu: Object.assign(() => null, { DefaultItems: new Proxy({}, { get: () => () => null }), Separator: () => null }),
  exportToCanvas: async () => ({ toDataURL: () => 'data:image/png;base64,AAAA', width: 400, height: 200 }),
  convertToExcalidrawElements: (skeletons: Record<string, unknown>[]) => skeletons.map((s) => ({ ...s, label: undefined })),
}));

const cardOf = (file: string) => within(document.querySelector(`[data-drawing$="${file}"]`) as HTMLElement);
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Opens d4's editor and reports Excalidraw's restored scene, as the real one does before any touch. */
async function openEditor(user: Awaited<ReturnType<typeof openTask>>['user']) {
  await user.click(await cardOf('diagram-d4-0.excalidraw').findByRole('button', { name: 'Edit', exact: true }));
  const canvas = await screen.findByTestId('excalidraw');
  const restored = [{ type: 'rectangle', id: 'a', x: 0, y: 0, width: 100, height: 40, roughness: 0, locked: false }];
  act(() => emit!(restored, {}, {}));
  return { canvas, restored };
}

test('the scene Excalidraw restores is the baseline: nothing saves until a real edit', async () => {
  const { user } = await openTask('t-chart');
  const put = vi.spyOn(api, 'putDrawing');
  try {
    const { canvas, restored } = await openEditor(user);
    await sleep(250);
    expect(put).not.toHaveBeenCalled();
    // The same scene after a key press is no change either.
    fireEvent.keyDown(canvas, { key: 'Shift' });
    act(() => emit!(restored, {}, {}));
    await sleep(250);
    expect(put).not.toHaveBeenCalled();
    // A moved box is.
    act(() => emit!([{ ...restored[0], x: 50 }], {}, {}));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put).toHaveBeenCalledWith('t-chart', 'docs/diagram-d4-0.excalidraw', expect.objectContaining({ base_version: 2 }));
  } finally {
    put.mockRestore();
  }
});

test('saves go one at a time: a second edit waits for the first PUT and carries the version it returned', async () => {
  const { user } = await openTask('t-chart');
  const real = api.putDrawing;
  const put = vi.spyOn(api, 'putDrawing').mockImplementation((id, path, body) => sleep(400).then(() => real(id, path, body)));
  try {
    const { canvas, restored } = await openEditor(user);
    fireEvent.keyDown(canvas, { key: 'Shift' });
    act(() => emit!([{ ...restored[0], x: 50 }], {}, {}));
    await sleep(300);
    act(() => emit!([{ ...restored[0], x: 90 }], {}, {}));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2), { timeout: 3000 });
    expect(put.mock.calls.map((c) => c[2].base_version)).toEqual([2, 3]);
    await waitFor(() => expect(screen.getByText('Saved')).toBeTruthy());
    expect(screen.queryByText('The agent changed this drawing.')).toBeNull();
    await waitFor(() => expect(cardOf('diagram-d4-0.excalidraw').getByText(/^Yours v4 · /)).toBeTruthy());
  } finally {
    put.mockRestore();
  }
});

test('a reply’s mermaid fences are found by index, with a closing fence of equal or greater length', () => {
  const text = 'Intro\n\n```mermaid\nflowchart LR\n  A --> B\n````\n\n~~~~mermaid\nsequenceDiagram\n  A->>B: hi\n~~~~\n\n```mermaid\ngantt\n```\n';
  expect(mermaidFences(text)).toEqual(['flowchart LR\n  A --> B', 'sequenceDiagram\n  A->>B: hi', 'gantt']);
});

test('a flowchart offers Edit as drawing, a gantt does not, and converting swaps the card for the drawing', async () => {
  const { user } = await openTask('t-chart');
  const buttons = await screen.findAllByRole('button', { name: 'Edit as drawing' });
  // d2's flowchart only: d3's gantt cannot convert and d4 already is a drawing.
  expect(buttons).toHaveLength(1);
  expect(log().getAllByText('mermaid')).toHaveLength(2);
  const put = vi.spyOn(api, 'putDrawing');
  try {
    await user.click(buttons[0]);
    await waitFor(() => expect(put).toHaveBeenCalledWith('t-chart', 'docs/diagram-d2-0.excalidraw', expect.objectContaining({ origin: 'convert', item_id: 'd2', fence_index: 0, format: 'excalidraw' })));
    await waitFor(() => expect(document.querySelector('[data-drawing$="diagram-d2-0.excalidraw"]')).toBeTruthy());
    expect(await cardOf('diagram-d2-0.excalidraw').findByText('Agent’s v1')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Edit as drawing' })).toBeNull();
    expect(log().getAllByText('mermaid')).toHaveLength(1);
  } finally {
    put.mockRestore();
  }
});

test('a drawing with unsent changes shows the pair, and collapses once the note went with a message', async () => {
  const { user } = await openTask('t-chart');
  const card = cardOf('diagram-d4-0.excalidraw');
  expect(await card.findByText('Yours v2 · 4 changes')).toBeTruthy();
  expect(await card.findByRole('button', { name: 'Agent’s version' })).toBeTruthy();
  expect(card.getByRole('button', { name: 'Edit your version' })).toBeTruthy();
  expect(card.getByText('Goes to the agent with your next message')).toBeTruthy();
  expect(screen.getByText('4 changes by you ·', { exact: false })).toBeTruthy();
  await user.type(composer(), 'Looks right, continue');
  await user.keyboard('{Enter}');
  await log().findByText(/^The user edited docs\/diagram-d4-0\.excalidraw since your version \(v1 → v2\)/);
  await waitFor(() => expect(card.getByText('Agent’s v2')).toBeTruthy());
  expect(card.queryByText(/^Yours v/)).toBeNull();
  expect(card.queryByRole('button', { name: 'Agent’s version' })).toBeNull();
  expect(card.getByRole('button', { name: 'Edit drawing' })).toBeTruthy();
});

test('Edit opens the anchored editor on Yours with the version segment, and Done closes it', async () => {
  const { user } = await openTask('t-chart');
  const card = cardOf('diagram-d4-0.excalidraw');
  await user.click(await card.findByRole('button', { name: 'Edit' }));
  const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' }));
  const segment = editor.getByRole('radiogroup', { name: 'Version' });
  const checked = (name: string | RegExp) => within(segment).getByRole('radio', { name }).getAttribute('aria-checked');
  expect(checked('Agent’s v1')).toBe('false');
  expect(checked('Yours v2')).toBe('true');
  expect(checked(/^Changes/)).toBe('false');
  expect(editor.getByText('Yours · 4 changes')).toBeTruthy();
  expect(await editor.findByTestId('excalidraw')).toBeTruthy();
  expect(editor.getByRole('button', { name: 'Revert to agent’s' })).toBeTruthy();
  await user.click(editor.getByRole('button', { name: 'Done' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' })).toBeNull());
});

test('Compare opens Changes with the server’s change lines and the note', async () => {
  const { user } = await openTask('t-chart');
  const card = cardOf('diagram-d4-0.excalidraw');
  await user.click(await card.findByRole('button', { name: 'Compare' }));
  const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' }));
  const lines = within(editor.getByRole('list', { name: 'Changes' })).getAllByRole('listitem').map((li) => li.textContent);
  expect(lines).toEqual(['~node "Model API" renamed "Model API (BYOM)"', '+node "Event cache"', '+edge "uam web (Go)" → "Event cache" "replays"', '~moved "Session state"']);
  expect(editor.getByText(/^The user edited docs\/diagram-d4-0\.excalidraw since your version \(v1 → v2\):/)).toBeTruthy();
  expect(editor.getByRole('region', { name: 'Sent with your next message' })).toBeTruthy();
  expect(editor.getByRole('region', { name: 'What the agent reads' })).toBeTruthy();
});
