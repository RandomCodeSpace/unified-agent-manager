// Drawings against the mock: t-chart holds a flowchart fence (d2), a gantt fence (d3) and a
// fence whose drawing exists with unsent changes (d4, mock/drawings.ts). Excalidraw needs a canvas
// happy-dom has not, so the package is a stand-in here; the real editor runs in scripts/check-drawings.mjs.
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, test, vi } from 'vitest';
import { api, ApiError } from '../../src/api';
import { mermaidFences } from '../../src/lib/diagram';
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
/** The stand-in's `updateScene`: what Reload and Revert put on the canvas. */
const updateScene = vi.fn();
beforeEach(() => updateScene.mockClear());

vi.mock('@excalidraw/excalidraw', () => ({
  Excalidraw: ({ excalidrawAPI, onChange }: { excalidrawAPI?: (api: unknown) => void; onChange?: Emit }) => {
    emit = onChange ?? null;
    excalidrawAPI?.({ scrollToContent: () => {}, updateScene });
    return <div className="excalidraw-container" tabIndex={-1} data-testid="excalidraw" />;
  },
  MainMenu: Object.assign(() => null, { DefaultItems: new Proxy({}, { get: () => () => null }), Separator: () => null }),
  exportToCanvas: async () => ({ toDataURL: () => 'data:image/png;base64,AAAA', width: 400, height: 200 }),
  convertToExcalidrawElements: (skeletons: Record<string, unknown>[]) => skeletons.map((s) => ({ ...s, label: undefined })),
}));

/** A drawing's card once its lazy module has rendered it. */
const cardOf = async (file: string) =>
  within(await waitFor(() => {
    const el = document.querySelector(`[data-drawing$="${file}"]`);
    if (!el) throw new Error(`no card for ${file}`);
    return el as HTMLElement;
  }));
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Opens d4's editor and reports Excalidraw's restored scene, as the real one does before any touch. */
async function openEditor(user: Awaited<ReturnType<typeof openTask>>['user']) {
  await user.click(await (await cardOf('diagram-d4-0.excalidraw')).findByRole('button', { name: 'Edit', exact: true }));
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
    const card = await cardOf('diagram-d4-0.excalidraw');
    await waitFor(() => expect(card.getByText(/^Yours v4 · /)).toBeTruthy());
  } finally {
    put.mockRestore();
  }
});

test('a 409 keeps the scene, says the agent changed the drawing, and Reload puts the server’s file on the canvas', async () => {
  const { user } = await openTask('t-chart');
  const put = vi.spyOn(api, 'putDrawing').mockRejectedValue(new ApiError(409, 'the drawing changed since this version was loaded'));
  try {
    const { canvas, restored } = await openEditor(user);
    fireEvent.keyDown(canvas, { key: 'Shift' });
    act(() => emit!([{ ...restored[0], x: 50 }], {}, {}));
    const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' }));
    expect(await editor.findByText(/The agent changed this drawing/)).toBeTruthy();
    expect(updateScene).not.toHaveBeenCalled();
    await user.click(editor.getByRole('button', { name: 'Reload' }));
    await waitFor(() => expect(updateScene).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(editor.queryByText(/The agent changed this drawing/)).toBeNull());
    expect(editor.getByText('Saved')).toBeTruthy();
    expect(put).toHaveBeenCalledTimes(1);
  } finally {
    put.mockRestore();
  }
});

test('Revert puts the agent’s file on the canvas and saves it, so the versions agree again', async () => {
  const { user } = await openTask('t-chart');
  const put = vi.spyOn(api, 'putDrawing');
  try {
    await openEditor(user);
    const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' }));
    await user.click(editor.getByRole('button', { name: 'Revert to agent’s' }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    const body = put.mock.calls[0][2];
    expect(body.base_version).toBe(2);
    expect(JSON.parse(body.content).elements.some((e: { text?: string }) => e.text === 'Event cache')).toBe(false);
    expect(updateScene).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(editor.getByText('Same as the agent’s')).toBeTruthy());
    expect(editor.queryByRole('button', { name: 'Revert to agent’s' })).toBeNull();
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
    expect(await (await cardOf('diagram-d2-0.excalidraw')).findByText('Agent’s v1')).toBeTruthy();
    // The click that made the drawing also opens its editor; Done closes it and the conversation is reachable again.
    const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d2-0.excalidraw' }));
    // Full screen takes the whole viewport and comes back.
    await user.click(editor.getByRole('button', { name: 'Full screen' }));
    expect(editor.getByRole('button', { name: 'Exit full screen' }).getAttribute('aria-pressed')).toBe('true');
    await user.click(editor.getByRole('button', { name: 'Exit full screen' }));
    expect(editor.getByRole('button', { name: 'Full screen' }).getAttribute('aria-pressed')).toBe('false');
    await user.click(editor.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Edit diagram-d2-0.excalidraw' })).toBeNull());
    expect(screen.queryByRole('button', { name: 'Edit as drawing' })).toBeNull();
    expect(log().getAllByText('mermaid')).toHaveLength(1);
  } finally {
    put.mockRestore();
  }
});

test('a drawing with unsent changes shows the pair, and collapses once the note went with a message', async () => {
  const { user } = await openTask('t-chart');
  const card = await cardOf('diagram-d4-0.excalidraw');
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
  const card = await cardOf('diagram-d4-0.excalidraw');
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
  const card = await cardOf('diagram-d4-0.excalidraw');
  await user.click(await card.findByRole('button', { name: 'Compare' }));
  const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d4-0.excalidraw' }));
  const lines = within(editor.getByRole('list', { name: 'Changes' })).getAllByRole('listitem').map((li) => li.textContent);
  expect(lines).toEqual(['~node "Model API" renamed "Model API (BYOM)"', '+node "Event cache"', '+edge "uam web (Go)" → "Event cache" "replays"', '~moved "Session state"']);
  expect(editor.getByText(/^The user edited docs\/diagram-d4-0\.excalidraw since your version \(v1 → v2\):/)).toBeTruthy();
  expect(editor.getByRole('region', { name: 'Sent with your next message' })).toBeTruthy();
  expect(editor.getByRole('region', { name: 'What the agent reads' })).toBeTruthy();
});

test('with draw.io enabled a converted fence opens the embed, and a frame the page refuses says to reload', async () => {
  const { user } = await openTask('t-chart');
  await act(() => api.updateWebSettings({ drawio: { origin: 'https://embed.diagrams.net', enabled: true } }));
  const put = vi.spyOn(api, 'putDrawing');
  try {
    await user.click((await screen.findAllByRole('button', { name: 'Edit as drawing' }))[0]);
    await waitFor(() => expect(put).toHaveBeenCalledWith('t-chart', 'docs/diagram-d2-0.drawio', expect.objectContaining({ origin: 'convert', format: 'drawio' })));
    const editor = within(await screen.findByRole('dialog', { name: 'Edit diagram-d2-0.drawio' }));
    const frame = editor.getByTitle('draw.io editor') as HTMLIFrameElement;
    expect(frame.src).toContain('https://embed.diagrams.net/?embed=1');
    expect(editor.queryByText(/Reload the page to edit in draw.io/)).toBeNull();
    // The page was served before draw.io was enabled: its policy refuses the frame.
    act(() => {
      document.dispatchEvent(Object.assign(new Event('securitypolicyviolation'), { blockedURI: 'https://embed.diagrams.net/?embed=1' }));
    });
    expect(await editor.findByText(/Reload the page to edit in draw.io/)).toBeTruthy();
    expect(editor.getByRole('button', { name: 'Reload' })).toBeTruthy();
  } finally {
    put.mockRestore();
  }
});
