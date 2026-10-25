// Drawings: a mermaid fence turned into a `.excalidraw` (or `.drawio`) file the user edits in
// place. The server keeps the agent's version and diffs the two; this module draws from what it
// says: the converted elements in the page's tokens, the colour-coded Changes picture from the
// server's `status`, the PNG pictures the cards show, and the mxGraph form for draw.io.

import type { DrawingStatus } from '../api';

/** The Excalidraw element fields this module reads or sets; the rest travels untouched. */
export interface DrawnElement {
  id: string;
  type: string;
  x: number;
  y: number;
  width: number;
  height: number;
  isDeleted?: boolean;
  containerId?: string | null;
  strokeColor?: string;
  backgroundColor?: string;
  strokeStyle?: string;
  strokeWidth?: number;
  opacity?: number;
  boundElements?: unknown;
  startBinding?: { elementId: string; [key: string]: unknown } | null;
  endBinding?: { elementId: string; [key: string]: unknown } | null;
  text?: string;
  originalText?: string;
  fontFamily?: number;
  [key: string]: unknown;
}

/** An element skeleton as `@excalidraw/mermaid-to-excalidraw` emits it (looser than an element: no size yet, labels nested). */
export type Skeleton = Record<string, unknown> & { type?: string; label?: Record<string, unknown> };

export type DrawingFormat = 'excalidraw' | 'drawio';

/** The `.excalidraw` file's JSON shape: what Excalidraw itself saves. */
export interface ExcalidrawFile {
  type: 'excalidraw';
  version: 2;
  source: string;
  elements: DrawnElement[];
  appState: Record<string, unknown>;
  files: Record<string, unknown>;
}

const SHAPES = new Set(['rectangle', 'ellipse', 'diamond', 'frame']);
const EDGES = new Set(['arrow', 'line']);
/** Excalidraw's Nunito, the closest of its fonts to the page's Figtree. */
export const NUNITO = 6;

/** The drawing a fence of a reply becomes: `docs/diagram-<item>-<fence>.<ext>`, the item id reduced to file-safe characters. */
export function drawingPath(itemId: string, fenceIndex: number, format: DrawingFormat): string {
  return `docs/diagram-${itemId.replaceAll(/[^A-Za-z0-9_-]/g, '-')}-${fenceIndex}.${format}`;
}

/** A colour token's live value, as the diagram theme reads them (`lib/diagram.ts`). */
export const token = (name: string): string => getComputedStyle(document.documentElement).getPropertyValue(`--color-${name}`).trim();

/** The converter's skeletons in the page's look: no roughness, a 1.5px `ink` stroke, `raised` fills, Nunito labels. */
export function styleSkeletons(skeletons: Skeleton[]): Skeleton[] {
  const ink = token('ink');
  const raised = token('raised');
  return skeletons.map((e) => {
    const shape = SHAPES.has(e.type ?? '');
    const text = e.type === 'text';
    return {
      ...e,
      roughness: 0,
      strokeWidth: 1.5,
      strokeColor: ink,
      ...(shape ? { backgroundColor: raised, fillStyle: 'solid' } : {}),
      ...(text ? { fontFamily: NUNITO } : {}),
      ...(e.label ? { label: { ...e.label, fontFamily: NUNITO, strokeColor: ink } } : {}),
    };
  });
}

export const live = (elements: readonly DrawnElement[]): DrawnElement[] => elements.filter((e) => !e.isDeleted);

/** The `.excalidraw` file text for elements and files. */
export function excalidrawFile(elements: readonly DrawnElement[], files: Record<string, unknown>): string {
  const file: ExcalidrawFile = { type: 'excalidraw', version: 2, source: 'uam', elements: live(elements), appState: { gridSize: null }, files };
  return JSON.stringify(file);
}

/** The elements and files of a `.excalidraw` file's text; empty for text that is not one. */
export function parseExcalidraw(text: string): { elements: DrawnElement[]; files: Record<string, unknown> } {
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed !== 'object' || parsed === null) return { elements: [], files: {} };
    const { elements, files } = parsed as Partial<ExcalidrawFile>;
    return { elements: Array.isArray(elements) ? live(elements) : [], files: files && typeof files === 'object' ? files : {} };
  } catch {
    return { elements: [], files: {} };
  }
}

/** The Changes picture's tones per status: a stroke and a fill, from the page's tokens. */
function tones(): Record<DrawingStatus, { strokeColor: string; backgroundColor: string }> {
  return {
    added: { strokeColor: token('success'), backgroundColor: token('success-wash') },
    changed: { strokeColor: token('warning'), backgroundColor: token('warning-wash') },
    moved: { strokeColor: token('accent'), backgroundColor: token('accent-wash') },
    removed: { strokeColor: token('error'), backgroundColor: 'transparent' },
  };
}

/**
 * One picture of both versions (the proto's `overlay`): added green, changed amber, moved blue
 * with a dashed ghost at the old place, removed as red dashed ghosts, the rest dimmed.
 */
export function overlay(base: readonly DrawnElement[], current: readonly DrawnElement[], status: Record<string, DrawingStatus>): DrawnElement[] {
  const tone = tones();
  const dim = token('faint');
  // Every colour here is a token of the scheme on screen (the file's are the light scheme's), so the picture draws plain in dark too.
  const ink = token('ink');
  const raised = token('raised');
  const out: DrawnElement[] = [];
  for (const e of live(current)) {
    const st = status[e.containerId ?? e.id];
    if (e.type === 'text') out.push({ ...e, strokeColor: ink, ...(st ? {} : { opacity: 55 }) });
    else if (st && st !== 'removed') out.push({ ...e, ...(e.type === 'arrow' || e.type === 'line' ? { strokeColor: tone[st].strokeColor } : tone[st]), strokeWidth: 2.5 });
    else out.push({ ...e, strokeColor: dim, ...(e.type === 'arrow' || e.type === 'line' ? {} : { backgroundColor: raised }), opacity: 55 });
    if (st === 'moved' && e.type !== 'text') {
      const was = base.find((b) => b.id === e.id);
      if (was) out.unshift({ ...was, id: `${e.id}~was`, boundElements: null, strokeColor: tone.moved.strokeColor, backgroundColor: 'transparent', strokeStyle: 'dashed', opacity: 45 });
    }
  }
  const gone = new Set(base.filter((b) => status[b.id] === 'removed').map((b) => b.id));
  for (const b of base) {
    if (!gone.has(b.id) && !(b.containerId && gone.has(b.containerId))) continue;
    out.push({
      ...b,
      id: `${b.id}~gone`,
      containerId: b.containerId ? `${b.containerId}~gone` : b.containerId,
      boundElements: null,
      startBinding: null,
      endBinding: null,
      strokeColor: tone.removed.strokeColor,
      ...(b.type === 'text' ? {} : { backgroundColor: 'transparent', strokeStyle: 'dashed' }),
      opacity: 70,
    });
  }
  return out;
}

/** Changes as the card's foot reads them: the first two without their "node"/"edge" prefix, then "· n more". */
export function changeSummary(changes: readonly { text: string }[]): string {
  const first = changes.slice(0, 2).map((c) => c.text.replace(/^(node|edge) /, '')).join(' · ');
  return changes.length > 2 ? `${first} · ${changes.length - 2} more` : first;
}

export const changesWord = (n: number): string => `${n} change${n === 1 ? '' : 's'}`;

declare global {
  interface Window {
    EXCALIDRAW_ASSET_PATH?: string;
  }
}

/** Excalidraw's fonts: copied into the build and served from this origin (vite.config.ts `excalidrawAssets`, in dev from the package), never a CDN. */
export const EXCALIDRAW_ASSETS = '/assets/excalidraw/';

/** The Excalidraw module, loaded on first use (its own chunk), with its asset path set before it looks. */
export function loadExcalidraw(): Promise<typeof import('@excalidraw/excalidraw')> {
  window.EXCALIDRAW_ASSET_PATH ??= EXCALIDRAW_ASSETS;
  return import('@excalidraw/excalidraw');
}

export interface Picture {
  url: string;
  width: number;
  height: number;
}

/**
 * A transparent PNG of the elements at 2× (the `<img>` sits on the `raised` token), drawn with
 * `exportToCanvas`: the SVG export subsets fonts with `new Function`, which the page's `script-src`
 * forbids. A file keeps the light scheme's colours; `dark` draws it through Excalidraw's dark
 * filter, as the editor does.
 */
export async function picture(elements: readonly DrawnElement[], files: Record<string, unknown>, dark: boolean): Promise<Picture> {
  const { exportToCanvas } = await loadExcalidraw();
  const canvas = await exportToCanvas({
    elements: live(elements) as never,
    appState: { exportBackground: false, exportWithDarkMode: dark },
    files: files as never,
    exportPadding: 28,
    getDimensions: (width: number, height: number) => ({ width: width * 2, height: height * 2, scale: 2 }),
  });
  return { url: canvas.toDataURL('image/png'), width: canvas.width / 2, height: canvas.height / 2 };
}

/* draw.io: the same converted elements as mxGraph XML, and the Changes picture by restyling it. */

const DRAWIO_STYLE: Record<string, string> = {
  rectangle: 'rounded=1;whiteSpace=wrap;html=1;',
  ellipse: 'ellipse;whiteSpace=wrap;html=1;',
  diamond: 'rhombus;whiteSpace=wrap;html=1;',
};

/** The label bound to each container or arrow, escaped: draw.io reads a cell's value as HTML (`html=1`). */
function labels(elements: readonly DrawnElement[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const e of elements) if (e.type === 'text' && e.containerId) out.set(e.containerId, (e.originalText ?? e.text ?? '').trim().replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;'));
  return out;
}

/** Excalidraw elements as a draw.io file: one vertex per shape (its label as the value), one edge per arrow, ids kept. */
export function toDrawioXml(elements: readonly DrawnElement[]): string {
  const doc = document.implementation.createDocument(null, 'mxfile');
  const diagram = doc.documentElement.appendChild(doc.createElement('diagram'));
  diagram.setAttribute('id', 'uam');
  diagram.setAttribute('name', 'Page-1');
  const model = diagram.appendChild(doc.createElement('mxGraphModel'));
  const root = model.appendChild(doc.createElement('root'));
  const cell = (attrs: Record<string, string>) => {
    const c = root.appendChild(doc.createElement('mxCell'));
    for (const [k, v] of Object.entries(attrs)) c.setAttribute(k, v);
    return c;
  };
  cell({ id: '0' });
  cell({ id: '1', parent: '0' });
  const label = labels(elements);
  for (const e of live(elements)) {
    if (DRAWIO_STYLE[e.type]) {
      const c = cell({ id: e.id, value: label.get(e.id) ?? '', style: DRAWIO_STYLE[e.type], vertex: '1', parent: '1' });
      const g = c.appendChild(doc.createElement('mxGeometry'));
      g.setAttribute('x', String(Math.round(e.x)));
      g.setAttribute('y', String(Math.round(e.y)));
      g.setAttribute('width', String(Math.round(e.width)));
      g.setAttribute('height', String(Math.round(e.height)));
      g.setAttribute('as', 'geometry');
    } else if (EDGES.has(e.type) && e.startBinding && e.endBinding) {
      const c = cell({ id: e.id, value: label.get(e.id) ?? '', style: 'edgeStyle=orthogonalEdgeStyle;rounded=0;html=1;', edge: '1', parent: '1', source: e.startBinding.elementId, target: e.endBinding.elementId });
      const g = c.appendChild(doc.createElement('mxGeometry'));
      g.setAttribute('relative', '1');
      g.setAttribute('as', 'geometry');
    }
  }
  return new XMLSerializer().serializeToString(doc);
}

const styleWith = (style: string, extra: Record<string, string>) => {
  const kept = style.split(';').filter((part) => part && !Object.keys(extra).some((k) => part.startsWith(`${k}=`)));
  return [...kept, ...Object.entries(extra).map(([k, v]) => `${k}=${v}`)].join(';') + ';';
};

/** The Changes picture for draw.io: the current file with each cell's style in its status's tone, removed cells from the base as dashed ghosts. */
export function drawioOverlayXml(base: string, current: string, status: Record<string, DrawingStatus>): string {
  const tone = tones();
  const parser = new DOMParser();
  const doc = parser.parseFromString(current, 'application/xml');
  const root = doc.querySelector('root');
  if (!root) return current;
  const dim = token('faint');
  for (const c of [...root.querySelectorAll('mxCell')]) {
    const id = c.getAttribute('id') ?? '';
    if (id === '0' || id === '1') continue;
    const st = status[id];
    const edge = c.getAttribute('edge') === '1';
    const style = c.getAttribute('style') ?? '';
    if (st && st !== 'removed') c.setAttribute('style', styleWith(style, { strokeColor: tone[st].strokeColor, strokeWidth: '2.5', ...(edge ? {} : { fillColor: tone[st].backgroundColor }) }));
    else c.setAttribute('style', styleWith(style, { strokeColor: dim, opacity: '55' }));
  }
  const ghosts = parser.parseFromString(base, 'application/xml');
  const present = new Set([...root.querySelectorAll('mxCell')].map((c) => c.getAttribute('id')));
  for (const b of [...ghosts.querySelectorAll('mxCell')]) {
    const id = b.getAttribute('id') ?? '';
    const st = status[id];
    if (st !== 'removed' && !(st === 'moved' && b.getAttribute('vertex') === '1')) continue;
    const ghost = doc.importNode(b, true) as Element;
    const suffix = st === 'removed' ? '~gone' : '~was';
    ghost.setAttribute('id', `${id}${suffix}`);
    for (const end of ['source', 'target']) {
      const ref = ghost.getAttribute(end);
      if (ref && !present.has(ref)) ghost.setAttribute(end, `${ref}~gone`);
    }
    ghost.setAttribute('style', styleWith(ghost.getAttribute('style') ?? '', { strokeColor: st === 'removed' ? tone.removed.strokeColor : tone.moved.strokeColor, fillColor: 'none', dashed: '1', opacity: st === 'removed' ? '70' : '45' }));
    root.appendChild(ghost);
  }
  return new XMLSerializer().serializeToString(doc);
}
