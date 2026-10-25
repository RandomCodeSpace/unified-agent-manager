// Drawings against the mock: t-chart holds a flowchart fence (d2), a gantt fence (d3) and a
// fence whose drawing exists with unsent changes (d4, mock/drawings.ts). Excalidraw needs a canvas
// happy-dom has not, so the package is a stand-in here; the real editor runs in scripts/check-drawings.mjs.
import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { composer, log, openTask } from './render';

vi.mock('../../src/lib/diagram', async (original) => {
  const actual = await original<typeof import('../../src/lib/diagram')>();
  return {
    ...actual,
    renderDiagram: async (source: string) => ({ svg: '<svg width="10" height="10"></svg>', width: 10, height: 10, diagramType: source.startsWith('gantt') ? 'gantt' : 'flowchart-v2' }),
    convertDiagram: async () => ({ elements: [{ type: 'rectangle', id: 'a', x: 0, y: 0, width: 100, height: 40, label: { text: 'Browser' } }, { type: 'rectangle', id: 'b', x: 200, y: 0, width: 100, height: 40, label: { text: 'Proxy' } }], files: {} }),
  };
});

vi.mock('@excalidraw/excalidraw', () => ({
  Excalidraw: ({ excalidrawAPI }: { excalidrawAPI?: (api: unknown) => void }) => {
    excalidrawAPI?.({ scrollToContent: () => {}, updateScene: () => {}, getSceneElements: () => [], getFiles: () => ({}) });
    return <div className="excalidraw-container" tabIndex={-1} data-testid="excalidraw" />;
  },
  MainMenu: Object.assign(() => null, { DefaultItems: new Proxy({}, { get: () => () => null }), Separator: () => null }),
  exportToCanvas: async () => ({ toDataURL: () => 'data:image/png;base64,AAAA', width: 400, height: 200 }),
  convertToExcalidrawElements: (skeletons: Record<string, unknown>[]) => skeletons.map((s) => ({ ...s, label: undefined })),
}));

const cardOf = (file: string) => within(document.querySelector(`[data-drawing$="${file}"]`) as HTMLElement);

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
  const notice = await log().findByText(/^The user edited docs\/diagram-d4-0\.excalidraw since your version \(v1 → v2\)/);
  expect(within(notice.closest('.flex') as HTMLElement).getByRole('button', { name: 'Compare' })).toBeTruthy();
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
