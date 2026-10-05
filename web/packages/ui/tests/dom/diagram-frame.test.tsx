import { describe, expect, test, vi } from 'vitest';

// Mermaid itself does not lay out here; the test pins the box the frame hands it.
const mermaid = vi.hoisted(() => ({
  initialize: vi.fn(),
  parse: vi.fn(),
  render: vi.fn(),
}));
vi.mock('mermaid', () => ({ default: mermaid }));

await import('../../src/diagram-frame/main');

/** Posts a request to the frame as its parent does and resolves with the reply. */
function draw(id: string, source: string): Promise<unknown> {
  return new Promise((resolve) => {
    const post = vi.spyOn(window.parent, 'postMessage').mockImplementationOnce((reply: unknown) => {
      post.mockRestore();
      resolve(reply);
    });
    window.dispatchEvent(new MessageEvent('message', { data: { id, source, theme: {} }, source: window.parent, origin: 'http://localhost' }));
  });
}

describe('diagram frame', () => {
  test.each(['unrelated sender', 'malformed request'])('ignores a message with %s', async (kind) => {
    mermaid.initialize.mockClear();
    mermaid.parse.mockClear();
    mermaid.render.mockClear();
    const post = vi.spyOn(window.parent, 'postMessage').mockImplementation(() => {});
    try {
      window.dispatchEvent(new MessageEvent('message', {
        data: kind === 'malformed request' ? { id: 'untrusted', source: 'flowchart LR; a --> b' } : { id: 'untrusted', source: 'flowchart LR; a --> b', theme: {} },
        source: kind === 'unrelated sender' ? null : window.parent,
        origin: 'https://unrelated.example',
      }));
      await Promise.resolve();
      expect(mermaid.initialize).not.toHaveBeenCalled();
      expect(mermaid.parse).not.toHaveBeenCalled();
      expect(mermaid.render).not.toHaveBeenCalled();
      expect(post).not.toHaveBeenCalled();
    } finally {
      post.mockRestore();
    }
  });

  test('an xychart is drawn in a box as wide as the chart, so its labels measure at full size', async () => {
    const boxes: { width: string; attached: boolean }[] = [];
    let box: HTMLElement | undefined;
    mermaid.parse.mockResolvedValueOnce({ diagramType: 'xychart', config: { xyChart: { width: 2160, height: 320 } } });
    mermaid.render.mockImplementationOnce(async (_id: string, _source: string, el?: HTMLElement) => {
      box = el;
      boxes.push({ width: el?.style.width ?? '', attached: !!el?.isConnected });
      return { svg: '<svg viewBox="0 0 2160 320"></svg>' };
    });
    const reply = await draw('d1', 'xychart-beta\n  x-axis [a, b]\n  line [1, 2]');
    expect(boxes).toEqual([{ width: '2160px', attached: true }]);
    expect(box?.isConnected).toBe(false);
    expect(reply).toMatchObject({ id: 'd1', width: 2160, height: 320 });
  });

  test('other diagrams keep the frame body, whose width a gantt takes', async () => {
    mermaid.parse.mockResolvedValueOnce({ diagramType: 'flowchart-v2', config: {} });
    mermaid.render.mockResolvedValueOnce({ svg: '<svg viewBox="0 0 100 50"></svg>' });
    await draw('d2', 'flowchart LR\n  a --> b');
    expect(mermaid.render).toHaveBeenLastCalledWith('d2', 'flowchart LR\n  a --> b', undefined);
  });
});
