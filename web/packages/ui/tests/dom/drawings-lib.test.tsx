// lib/drawings.ts and lib/drawio.ts: the file shapes, the Changes overlay, the draw.io mapping and
// the hidden exporter, driven with a fake embed window that posts `message` events.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { DrawingStatus } from '../../src/api';
import { changeSummary, changesWord, drawingPath, drawioOverlayXml, excalidrawFile, EXCALIDRAW_ASSETS, live, NUNITO, overlay, parseExcalidraw, picture, styleSkeletons, toDrawioXml, token, type DrawnElement } from '../../src/lib/drawings';

const exportToCanvas = vi.fn(async () => ({ toDataURL: () => 'data:image/png;base64,AAAA', width: 400, height: 200 }));
vi.mock('@excalidraw/excalidraw', () => ({ exportToCanvas: (...args: unknown[]) => exportToCanvas(...(args as [])) }));

const box = (id: string, x: number, label?: string, type = 'rectangle'): DrawnElement[] => [
  { id, type, x, y: 0, width: 100, height: 40, ...(label ? { boundElements: [{ id: `${id}-t`, type: 'text' }] } : {}) },
  ...(label ? [{ id: `${id}-t`, type: 'text', x, y: 0, width: 80, height: 20, containerId: id, text: label, originalText: label }] : []),
];
const edge = (id: string, from: string, to: string): DrawnElement => ({ id, type: 'arrow', x: 0, y: 0, width: 10, height: 0, startBinding: { elementId: from }, endBinding: { elementId: to } });

describe('excalidraw files', () => {
  test('the path, the file text and its parse, dropping deleted elements and refusing other text', () => {
    expect(drawingPath('item/1 b', 2, 'excalidraw')).toBe('docs/diagram-item-1-b-2.excalidraw');
    const els = [...box('a', 0, 'A'), { ...box('z', 9)[0], isDeleted: true }];
    const text = excalidrawFile(els, { f: 1 });
    expect(JSON.parse(text)).toMatchObject({ type: 'excalidraw', version: 2, source: 'uam', files: { f: 1 } });
    expect(live(els)).toHaveLength(2);
    expect(parseExcalidraw(text).elements.map((e) => e.id)).toEqual(['a', 'a-t']);
    expect(parseExcalidraw('not json')).toEqual({ elements: [], files: {} });
    expect(parseExcalidraw('42')).toEqual({ elements: [], files: {} });
    expect(parseExcalidraw('{"elements":"no","files":null}')).toEqual({ elements: [], files: {} });
  });

  test('skeletons take the page’s look: flat, inked, raised fills on shapes, Nunito on text and labels', () => {
    const [shape, text, arrow] = styleSkeletons([{ type: 'rectangle', label: { text: 'A' } }, { type: 'text', text: 'T' }, { type: 'arrow' }]);
    expect(shape).toMatchObject({ roughness: 0, strokeWidth: 1.5, strokeColor: token('ink'), backgroundColor: token('raised'), fillStyle: 'solid', label: { text: 'A', fontFamily: NUNITO } });
    expect(text).toMatchObject({ fontFamily: NUNITO });
    expect(arrow).not.toHaveProperty('backgroundColor');
    expect(arrow).not.toHaveProperty('fontFamily');
  });

  test('the overlay tones each element by status, ghosts moved and removed ones, and dims the rest', () => {
    const base = [...box('a', 0, 'A'), ...box('b', 200, 'B'), ...box('c', 400, 'C'), edge('e1', 'a', 'b')];
    const current = [...box('a', 0, 'A'), ...box('b', 300, 'B'), ...box('d', 600, 'D'), edge('e1', 'a', 'b'), edge('e2', 'b', 'd')];
    const status: Record<string, DrawingStatus> = { b: 'moved', d: 'added', e2: 'added', c: 'removed', 'e1': 'changed' };
    const out = overlay(base, current, status);
    const by = (id: string) => out.find((e) => e.id === id)!;
    expect(by('a')).toMatchObject({ opacity: 55, strokeColor: token('faint'), backgroundColor: token('raised') });
    expect(by('a-t')).toMatchObject({ opacity: 55, strokeColor: token('ink') });
    expect(by('b')).toMatchObject({ strokeWidth: 2.5, strokeColor: token('accent') });
    expect(by('b-t')).not.toHaveProperty('opacity');
    expect(by('b~was')).toMatchObject({ x: 200, strokeStyle: 'dashed', opacity: 45, backgroundColor: 'transparent' });
    expect(by('d')).toMatchObject({ strokeColor: token('success'), backgroundColor: token('success-wash') });
    expect(by('e1')).toMatchObject({ strokeColor: token('warning'), strokeWidth: 2.5 });
    expect(by('e1')).not.toHaveProperty('backgroundColor');
    expect(by('c~gone')).toMatchObject({ strokeStyle: 'dashed', opacity: 70, strokeColor: token('error'), startBinding: null });
    expect(by('c-t~gone')).toMatchObject({ containerId: 'c~gone', opacity: 70 });
    expect(by('c-t~gone')).not.toHaveProperty('strokeStyle');
    expect(out.map((e) => e.id)).not.toContain('c');
  });

  test('a card’s foot and the change word', () => {
    expect(changeSummary([{ text: 'node "A" renamed "B"' }])).toBe('"A" renamed "B"');
    expect(changeSummary([{ text: 'node "A"' }, { text: 'edge "A" → "B"' }, { text: 'moved "C"' }, { text: 'node "D"' }])).toBe('"A" · "A" → "B" · 2 more');
    expect(changesWord(1)).toBe('1 change');
    expect(changesWord(3)).toBe('3 changes');
  });

  test('a picture is a transparent canvas export at 2×, through the dark filter when asked, with the asset path set', async () => {
    const p = await picture(box('a', 0, 'A'), {}, true);
    expect(p).toEqual({ url: 'data:image/png;base64,AAAA', width: 200, height: 100 });
    expect(window.EXCALIDRAW_ASSET_PATH).toBe(EXCALIDRAW_ASSETS);
    const opts = exportToCanvas.mock.calls[0][0] as unknown as { appState: Record<string, unknown>; exportPadding: number; elements: DrawnElement[]; getDimensions: (w: number, h: number) => { width: number; height: number; scale: number } };
    expect(opts.appState).toEqual({ exportBackground: false, exportWithDarkMode: true });
    expect(opts.exportPadding).toBe(28);
    expect(opts.elements).toHaveLength(2);
    expect(opts.getDimensions(10, 20)).toEqual({ width: 20, height: 40, scale: 2 });
  });
});

describe('draw.io mapping', () => {
  test('shapes become vertices with escaped HTML labels and geometry, bound arrows become edges, the rest is dropped', () => {
    const xml = toDrawioXml([...box('a', 10.4, 'A <b> & co'), ...box('b', 200, 'B', 'diamond'), ...box('c', 300, undefined, 'ellipse'), edge('e1', 'a', 'b'), { id: 'loose', type: 'arrow', x: 0, y: 0, width: 1, height: 1 }, { id: 'img', type: 'image', x: 0, y: 0, width: 1, height: 1 }]);
    const doc = new DOMParser().parseFromString(xml, 'application/xml');
    const cell = (id: string) => doc.querySelector(`mxCell[id="${id}"]`)!;
    expect(cell('a').getAttribute('value')).toBe('A &lt;b&gt; &amp; co');
    expect(cell('a').getAttribute('style')).toContain('rounded=1');
    expect(cell('a').querySelector('mxGeometry')!.getAttribute('x')).toBe('10');
    expect(cell('b').getAttribute('style')).toContain('rhombus');
    expect(cell('c').getAttribute('value')).toBe('');
    expect(cell('e1').getAttribute('source')).toBe('a');
    expect(cell('e1').getAttribute('target')).toBe('b');
    expect(cell('e1').getAttribute('edge')).toBe('1');
    expect(doc.querySelector('mxCell[id="loose"]')).toBeNull();
    expect(doc.querySelector('mxCell[id="img"]')).toBeNull();
  });

  test('the overlay restyles cells by status and adds dashed ghosts for removed and moved cells from the base', () => {
    const base = toDrawioXml([...box('a', 0, 'A'), ...box('b', 200, 'B'), ...box('c', 400, 'C'), edge('e1', 'a', 'c')]);
    const current = toDrawioXml([...box('a', 0, 'A'), ...box('b', 300, 'B'), ...box('d', 600, 'D'), edge('e2', 'b', 'd')]);
    const xml = drawioOverlayXml(base, current, { b: 'moved', d: 'added', e2: 'added', c: 'removed', e1: 'removed' });
    const doc = new DOMParser().parseFromString(xml, 'application/xml');
    const style = (id: string) => doc.querySelector(`mxCell[id="${id}"]`)!.getAttribute('style')!;
    expect(style('a')).toContain(`strokeColor=${token('faint')};opacity=55;`);
    expect(style('b')).toContain(`strokeColor=${token('accent')};strokeWidth=2.5;fillColor=${token('accent-wash')};`);
    expect(style('e2')).toContain('strokeWidth=2.5;');
    expect(style('e2')).not.toContain('fillColor');
    expect(style('b~was')).toContain('dashed=1;opacity=45;');
    expect(style('c~gone')).toContain('fillColor=none;dashed=1;opacity=70;');
    expect(doc.querySelector('mxCell[id="e1~gone"]')!.getAttribute('target')).toBe('c~gone');
    expect(doc.querySelector('mxCell[id="e1~gone"]')!.getAttribute('source')).toBe('a');
    expect(drawioOverlayXml(base, '<mxfile/>', {})).toBe('<mxfile/>');
  });
});

describe('draw.io exporter', () => {
  type Drawio = typeof import('../../src/lib/drawio');
  const ORIGIN = 'https://drawio.example.test';
  let posted: { message: unknown; target: string }[];
  let win: { postMessage: (message: unknown, target: string) => void };
  let frames: HTMLIFrameElement[];
  let lib: Drawio;

  beforeEach(async () => {
    vi.resetModules();
    lib = await import('../../src/lib/drawio');
    posted = [];
    win = { postMessage: (message, target) => posted.push({ message: JSON.parse(message as string), target }) };
    frames = [];
    const create = document.createElement.bind(document);
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
      const el = create(tag);
      if (tag === 'iframe') {
        Object.defineProperty(el, 'contentWindow', { value: win });
        frames.push(el as HTMLIFrameElement);
      }
      return el;
    });
    vi.stubGlobal('Image', class {
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      naturalWidth = 300;
      naturalHeight = 150;
      set src(url: string) {
        queueMicrotask(() => (url.startsWith('data:image/png') ? this.onload : this.onerror)?.());
      }
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    for (const f of frames) f.remove();
  });

  const says = (data: unknown, origin = ORIGIN, source: unknown = win) => window.dispatchEvent(new MessageEvent('message', { data: typeof data === 'string' ? data : JSON.stringify(data), origin, source: source as Window }));
  const flush = () => new Promise((r) => setTimeout(r, 0));

  test('the embed URL, the origin a message must carry, and the message parser', () => {
    expect(lib.drawioEmbedUrl('https://embed.diagrams.net//')).toBe('https://embed.diagrams.net/?embed=1&proto=json&spin=1&stealth=1&noExitBtn=1&saveAndExit=0');
    expect(lib.drawioOrigin('https://Embed.Example.test:8443/app/')).toBe('https://embed.example.test:8443');
    expect(lib.drawioMessage('{"event":"init"}')).toEqual({ event: 'init' });
    expect(lib.drawioMessage({ event: 'export', data: 'd', xml: 5 })).toEqual({ event: 'export', data: 'd' });
    expect(lib.drawioMessage({ event: 'autosave', xml: '<x/>' })).toEqual({ event: 'autosave', xml: '<x/>' });
    expect(lib.drawioMessage('not json')).toBeNull();
    expect(lib.drawioMessage({ xml: '<x/>' })).toBeNull();
    expect(lib.drawioMessage(null)).toBeNull();
  });

  test('a picture is measured from its data URL, and an unreadable one is an error', async () => {
    await expect(lib.measure('data:image/png;base64,AAAA')).resolves.toEqual({ url: 'data:image/png;base64,AAAA', width: 300, height: 150 });
    await expect(lib.measure('data:text/plain,x')).rejects.toThrow('could not be read');
  });

  test('exports run one at a time through one hidden frame, from the embed’s origin only, and a reply that is no PNG is refused', async () => {
    const first = lib.exportDrawioPng(ORIGIN, '<a/>');
    const second = lib.exportDrawioPng(ORIGIN, '<b/>');
    expect(frames).toHaveLength(1);
    expect(frames[0].src).toBe(lib.drawioEmbedUrl(ORIGIN));
    expect(frames[0].getAttribute('sandbox')).toBe(lib.DRAWIO_SANDBOX);
    says({ event: 'init' }, 'https://evil.example.test');
    says({ event: 'init' }, ORIGIN, {});
    await flush();
    expect(posted).toHaveLength(0);
    says({ event: 'init' });
    await flush();
    expect(posted).toEqual([{ message: { action: 'load', xml: '<a/>' }, target: ORIGIN }]);
    says({ event: 'load' });
    expect(posted[1]).toEqual({ message: { action: 'export', format: 'png' }, target: ORIGIN });
    says({ event: 'export', data: 'data:image/png;base64,AAAA' });
    await expect(first).resolves.toEqual({ url: 'data:image/png;base64,AAAA', width: 300, height: 150 });
    await flush();
    expect(posted[2]).toEqual({ message: { action: 'load', xml: '<b/>' }, target: ORIGIN });
    says({ event: 'export', data: 'javascript:alert(1)' });
    await expect(second).rejects.toThrow('did not return a PNG');
    // A third picture reuses the started frame.
    const third = lib.exportDrawioPng(ORIGIN, '<c/>');
    await flush();
    expect(frames).toHaveLength(1);
    says({ event: 'export', data: 'data:image/png;base64,BBBB' });
    await expect(third).resolves.toMatchObject({ url: 'data:image/png;base64,BBBB' });
  });

  test('an export that is not answered in time fails that picture; a page that never starts is dropped so the next picture starts another', async () => {
    vi.useFakeTimers();
    const slow = lib.exportDrawioPng(ORIGIN, '<a/>');
    // The expectations attach before the timers fire, or the rejection counts as unhandled.
    const slowFails = expect(slow).rejects.toThrow('did not answer the export in time');
    says({ event: 'init' });
    await vi.advanceTimersByTimeAsync(0);
    expect(posted).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(lib.EXPORT_TIMEOUT);
    await slowFails;
    // Another origin: a new frame, which never says init.
    const dead = lib.exportDrawioPng('https://other.example.test', '<b/>');
    const deadFails = expect(dead).rejects.toThrow('did not start');
    expect(frames).toHaveLength(2);
    expect(frames[0].isConnected).toBe(false);
    await vi.advanceTimersByTimeAsync(lib.EXPORT_TIMEOUT * 4);
    await deadFails;
    expect(frames[1].isConnected).toBe(false);
    const again = lib.exportDrawioPng('https://other.example.test', '<c/>');
    await vi.advanceTimersByTimeAsync(0);
    expect(frames).toHaveLength(3);
    says({ event: 'init' }, 'https://other.example.test');
    await vi.advanceTimersByTimeAsync(0);
    says({ event: 'export', data: 'data:image/png;base64,CCCC' }, 'https://other.example.test');
    await vi.advanceTimersByTimeAsync(0);
    await expect(again).resolves.toMatchObject({ url: 'data:image/png;base64,CCCC' });
  });
});
