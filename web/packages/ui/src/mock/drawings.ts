// Development-only: the drawings routes of the in-browser mock (see install.ts). The server
// owns versions, the diff and the note; this is a port of the proto's deterministic diff
// (~/uam-private/mocks-2026-10-25/diagram/proto/src/diff.js), so the mock behaves like it.
// t-chart's reply d4 has a flowchart fence whose drawing exists with two unsent changes.

import type { Drawing, DrawingChange, DrawingDoc, DrawingPut, DrawingSaved, DrawingStatus, DrawioCheck } from '../api';
import type { DrawnElement } from '../lib/drawings';

interface Entry {
  format: Drawing['format'];
  version: number;
  agent_version: number;
  item_id: string;
  fence_index: number;
  content: string;
  baseline: string;
}

interface Node { id: string; type: string; label: string; x: number; y: number; w: number; h: number; stroke?: string; fill?: string }
interface Edge { id: string; from: string | null; to: string | null; label: string }
interface Model { nodes: Map<string, Node>; edges: Map<string, Edge> }

const SHAPES = new Set(['rectangle', 'ellipse', 'diamond', 'text', 'frame']);
const EDGES = new Set(['arrow', 'line']);
const MOVE_PX = 8;

function elementsOf(content: string): DrawnElement[] {
  try {
    const parsed = JSON.parse(content) as { elements?: DrawnElement[] };
    return (parsed.elements ?? []).filter((e) => !e.isDeleted);
  } catch {
    return [];
  }
}

/** Nodes and edges of a `.excalidraw` text, or of a `.drawio` one (vertex/edge cells). */
export function model(content: string, format: Drawing['format']): Model {
  const nodes = new Map<string, Node>();
  const edges = new Map<string, Edge>();
  if (format === 'drawio') {
    const doc = new DOMParser().parseFromString(content, 'application/xml');
    for (const c of doc.querySelectorAll('mxCell')) {
      const id = c.getAttribute('id') ?? '';
      const g = c.querySelector('mxGeometry');
      if (c.getAttribute('vertex') === '1') nodes.set(id, { id, type: /ellipse/.test(c.getAttribute('style') ?? '') ? 'ellipse' : /rhombus/.test(c.getAttribute('style') ?? '') ? 'diamond' : 'rectangle', label: c.getAttribute('value') ?? '', x: Number(g?.getAttribute('x')), y: Number(g?.getAttribute('y')), w: Number(g?.getAttribute('width')), h: Number(g?.getAttribute('height')), stroke: /strokeColor=([^;]+)/.exec(c.getAttribute('style') ?? '')?.[1], fill: /fillColor=([^;]+)/.exec(c.getAttribute('style') ?? '')?.[1] });
      else if (c.getAttribute('edge') === '1') edges.set(id, { id, from: c.getAttribute('source'), to: c.getAttribute('target'), label: c.getAttribute('value') ?? '' });
    }
    return { nodes, edges };
  }
  const live = elementsOf(content);
  const text = new Map<string, string>();
  for (const e of live) if (e.type === 'text' && e.containerId) text.set(e.containerId, (e.originalText ?? e.text ?? '').trim());
  for (const e of live) {
    if (e.type === 'text' && e.containerId) continue;
    if (SHAPES.has(e.type)) nodes.set(e.id, { id: e.id, type: e.type, label: e.type === 'text' ? (e.originalText ?? e.text ?? '').trim() : text.get(e.id) ?? '', x: e.x, y: e.y, w: e.width, h: e.height, stroke: e.strokeColor, fill: e.backgroundColor });
    else if (EDGES.has(e.type)) edges.set(e.id, { id: e.id, from: e.startBinding?.elementId ?? null, to: e.endBinding?.elementId ?? null, label: text.get(e.id) ?? '' });
  }
  return { nodes, edges };
}

function name(m: Model, id: string | null): string {
  const n = id ? m.nodes.get(id) : undefined;
  if (!n) return '(nothing)';
  return n.label ? `"${n.label}"` : `unlabelled ${n.type === 'rectangle' ? 'box' : n.type} ${near(m, n)}`;
}

// An unlabelled shape has only its place to go by: name it by the closest labelled one.
function near(m: Model, n: Node): string {
  let best: { o: Node; dx: number; dy: number } | undefined;
  let gap = Infinity;
  for (const o of m.nodes.values()) {
    if (!o.label) continue;
    const dx = o.x + o.w / 2 - (n.x + n.w / 2);
    const dy = o.y + o.h / 2 - (n.y + n.h / 2);
    const d = Math.hypot(dx, dy);
    if (d < gap) [best, gap] = [{ o, dx, dy }, d];
  }
  if (!best) return 'on its own';
  const { o, dx, dy } = best;
  const side = Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? 'left of' : 'right of') : dy > 0 ? 'above' : 'below';
  return `${side} "${o.label}"`;
}

const edgeText = (m: Model, e: Edge) => `${name(m, e.from)} → ${name(m, e.to)}${e.label ? ` "${e.label}"` : ''}`;

/** Changes from the agent's version (a) to the user's (b). status: id → added|changed|moved|removed. */
export function diff(a: Model, b: Model): { changes: DrawingChange[]; status: Record<string, DrawingStatus> } {
  const changes: DrawingChange[] = [];
  const status: Record<string, DrawingStatus> = {};
  const moved: string[] = [];
  for (const [id, n] of b.nodes) {
    const o = a.nodes.get(id);
    if (!o) {
      changes.push({ kind: '+', text: `node ${name(b, id)}` });
      status[id] = 'added';
      continue;
    }
    if (o.label !== n.label) {
      changes.push({ kind: '~', text: `node ${name(a, id)} renamed ${name(b, id)}` });
      status[id] = 'changed';
    }
    if (o.stroke !== n.stroke || o.fill !== n.fill) {
      changes.push({ kind: '~', text: `node ${name(b, id)} restyled` });
      status[id] = 'changed';
    }
    if (Math.abs(o.x - n.x) > MOVE_PX || Math.abs(o.y - n.y) > MOVE_PX || Math.abs(o.w - n.w) > MOVE_PX || Math.abs(o.h - n.h) > MOVE_PX) {
      moved.push(name(b, id));
      status[id] ??= 'moved';
    }
  }
  for (const id of a.nodes.keys()) {
    if (b.nodes.has(id)) continue;
    changes.push({ kind: '-', text: `node ${name(a, id)}` });
    status[id] = 'removed';
  }
  for (const [id, e] of b.edges) {
    const o = a.edges.get(id);
    if (!o) {
      changes.push({ kind: '+', text: `edge ${edgeText(b, e)}` });
      status[id] = 'added';
    } else if (o.from !== e.from || o.to !== e.to) {
      changes.push({ kind: '~', text: `edge ${edgeText(a, o)} now ${edgeText(b, e)}` });
      status[id] = 'changed';
    } else if (o.label !== e.label) {
      changes.push({ kind: '~', text: `edge ${edgeText(b, e)} label was "${o.label}"` });
      status[id] = 'changed';
    }
  }
  for (const [id, o] of a.edges) {
    if (b.edges.has(id)) continue;
    changes.push({ kind: '-', text: `edge ${edgeText(a, o)}` });
    status[id] = 'removed';
  }
  if (moved.length) changes.push({ kind: '~', text: `moved ${moved.join(', ')}` });
  return { changes, status };
}

/** The note uam attaches to the user's next message. */
export function note(path: string, from: number, to: number, changes: DrawingChange[], current: Model): string {
  if (!changes.length) return '';
  return [
    `The user edited ${path} since your version (v${from} → v${to}):`,
    ...changes.map((c) => `${c.kind} ${c.text}`),
    `Now ${current.nodes.size} nodes, ${current.edges.size} edges. Build on the user's version; uam_diagram_read for the full diagram.`,
  ].join('\n');
}

/* The fixture: a request-flow drawing as the converter and the page's styling leave it. */

const W = 184;
const H = 64;
const INK = '#111111';
const common = { angle: 0, fillStyle: 'solid', strokeWidth: 1.5, strokeStyle: 'solid', roughness: 0, opacity: 100, groupIds: [], frameId: null, seed: 1, version: 1, versionNonce: 1, isDeleted: false, link: null, locked: false, updated: 1 };
const label = (id: string, container: string, text: string, x: number, y: number, w: number, fontSize = 16): DrawnElement => ({
  ...common, id, type: 'text', x: x + (w - text.length * 8) / 2, y: y - 10, width: text.length * 8, height: 20, strokeColor: INK, backgroundColor: 'transparent', roundness: null,
  text, originalText: text, fontSize, fontFamily: 6, textAlign: 'center', verticalAlign: 'middle', containerId: container, autoResize: true, lineHeight: 1.25, boundElements: null,
});
export const box = (id: string, x: number, y: number, text: string, out: string[] = [], inn: string[] = []): DrawnElement[] => [
  { ...common, id, type: 'rectangle', x, y, width: W, height: H, strokeColor: INK, backgroundColor: '#ffffff', roundness: { type: 3 }, boundElements: [{ id: `${id}-label`, type: 'text' }, ...[...out, ...inn].map((a) => ({ id: a, type: 'arrow' }))] },
  label(`${id}-label`, id, text, x, y + H / 2, W),
];
export function arrow(boxes: DrawnElement[], id: string, from: string, to: string, text?: string): DrawnElement[] {
  const a = boxes.find((b) => b.id === from)!;
  const b = boxes.find((x) => x.id === to)!;
  const across = Math.abs(b.x - a.x) >= Math.abs(b.y - a.y);
  const dir = across ? Math.sign(b.x - a.x) : Math.sign(b.y - a.y);
  const sx = across ? a.x + W / 2 + (dir * W) / 2 + dir * 6 : a.x + W / 2;
  const sy = across ? a.y + H / 2 : a.y + H / 2 + (dir * H) / 2 + dir * 6;
  const ex = across ? b.x + W / 2 - (dir * W) / 2 - dir * 6 : b.x + W / 2;
  const ey = across ? b.y + H / 2 : b.y + H / 2 - (dir * H) / 2 - dir * 6;
  const line: DrawnElement = {
    ...common, id, type: 'arrow', x: sx, y: sy, width: Math.abs(ex - sx), height: Math.abs(ey - sy), strokeColor: INK, backgroundColor: 'transparent', roundness: { type: 2 },
    points: [[0, 0], [ex - sx, ey - sy]], lastCommittedPoint: null, startArrowhead: null, endArrowhead: 'arrow', elbowed: false,
    startBinding: { elementId: from, focus: 0, gap: 6 }, endBinding: { elementId: to, focus: 0, gap: 6 }, boundElements: text ? [{ id: `${id}-label`, type: 'text' }] : null,
  };
  return text ? [line, label(`${id}-label`, id, text, (sx + ex) / 2 - 40, (sy + ey) / 2, 80, 14)] : [line];
}
const file = (elements: DrawnElement[]) => JSON.stringify({ type: 'excalidraw', version: 2, source: 'uam', elements, appState: { gridSize: null }, files: {} });

/** The agent's version: the request flow the fence in t-chart's reply d4 draws. */
export function agentScene(): DrawnElement[] {
  const boxes = [...box('browser', 0, 0, 'Browser', ['e1']), ...box('proxy', 300, 0, 'Sign-in proxy', ['e2'], ['e1']), ...box('web', 600, 0, 'uam web (Go)', ['e3', 'e5'], ['e2']),
    ...box('disk', 300, 200, 'Session state', [], ['e5', 'e6']), ...box('cli', 600, 200, 'Copilot CLI', ['e4', 'e6'], ['e3']), ...box('model', 900, 200, 'Model API', [], ['e4'])];
  return [...boxes, ...arrow(boxes, 'e1', 'browser', 'proxy', 'HTTPS'), ...arrow(boxes, 'e2', 'proxy', 'web'), ...arrow(boxes, 'e3', 'web', 'cli', 'SDK stdio'), ...arrow(boxes, 'e4', 'cli', 'model'), ...arrow(boxes, 'e5', 'web', 'disk', 'reads'), ...arrow(boxes, 'e6', 'cli', 'disk', 'writes')];
}

/** What the user did by hand: renamed the model box, moved the state box and added an event cache. */
function userScene(): DrawnElement[] {
  const boxes = [...box('browser', 0, 0, 'Browser', ['e1']), ...box('proxy', 300, 0, 'Sign-in proxy', ['e2'], ['e1']), ...box('web', 600, 0, 'uam web (Go)', ['e3', 'e5', 'e7'], ['e2']),
    ...box('disk', 300, 290, 'Session state', [], ['e5', 'e6']), ...box('cli', 600, 200, 'Copilot CLI', ['e4', 'e6'], ['e3']), ...box('model', 900, 200, 'Model API (BYOM)', [], ['e4']), ...box('cache', 900, 0, 'Event cache', [], ['e7'])];
  return [...boxes, ...arrow(boxes, 'e1', 'browser', 'proxy', 'HTTPS'), ...arrow(boxes, 'e2', 'proxy', 'web'), ...arrow(boxes, 'e3', 'web', 'cli', 'SDK stdio'), ...arrow(boxes, 'e4', 'cli', 'model'), ...arrow(boxes, 'e5', 'web', 'disk', 'reads'), ...arrow(boxes, 'e6', 'cli', 'disk', 'writes'), ...arrow(boxes, 'e7', 'web', 'cache', 'replays')];
}

export const FIXTURE_PATH = 'docs/diagram-d4-0.excalidraw';

/** The checks the mock server makes of a draw.io origin, in the same words as the service. */
export function check(origin: string): DrawioCheck {
  const o = origin.trim().replace(/\/+$/, '').toLowerCase();
  const errors: string[] = [];
  const warnings: string[] = [];
  let detail = { public: false, cors: false, framing_ok: true };
  if (!/^https?:\/\/[^/]+$/.test(o)) errors.push('The origin must be an absolute http(s) origin, such as https://embed.diagrams.net.');
  else if (o.startsWith('http://') && !/^http:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/.test(o)) errors.push('An http origin cannot be framed by this https page (mixed content).');
  else if (o === 'https://embed.diagrams.net') {
    detail = { public: true, cors: true, framing_ok: true };
    warnings.push('Diagrams are edited in a page from embed.diagrams.net. Content stays in your browser; the editor’s version beacon is off.');
  } else if (o === 'https://blocked.example.test') errors.push('The page at this origin refuses to be framed (X-Frame-Options).');
  else if (/\.example\.test$|localhost|127\.0\.0\.1/.test(o)) warnings.push('This self-hosted page sends no Access-Control-Allow-Origin header; it runs with its own origin only.');
  else errors.push('The origin did not answer within 5 s.');
  return { ok: errors.length === 0, errors, warnings, detail };
}

export function drawingsMock() {
  const entries: Record<string, Record<string, Entry>> = {
    't-chart': { [FIXTURE_PATH]: { format: 'excalidraw', version: 2, agent_version: 1, item_id: 'd4', fence_index: 0, content: file(userScene()), baseline: file(agentScene()) } },
  };
  const reply = (status: number, body?: unknown) => body === undefined ? new Response(null, { status }) : new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  const fail = (status: number, error: string) => reply(status, { error });
  const diffOf = (e: Entry) => diff(model(e.baseline, e.format), model(e.content, e.format));
  const summary = (path: string, e: Entry): Drawing => ({ path, format: e.format, version: e.version, agent_version: e.agent_version, item_id: e.item_id, fence_index: e.fence_index, changes_count: e.version === e.agent_version ? 0 : diffOf(e).changes.length });
  const saved = (path: string, e: Entry): DrawingSaved => {
    const d = e.version === e.agent_version ? { changes: [], status: {} } : diffOf(e);
    return { version: e.version, agent_version: e.agent_version, ...d, note: note(path, e.agent_version, e.version, d.changes, model(e.content, e.format)) };
  };
  const view = (path: string, e: Entry): DrawingDoc => ({ path, format: e.format, item_id: e.item_id, fence_index: e.fence_index, content: e.content, baseline: e.version === e.agent_version ? null : e.baseline, ...saved(path, e) });

  return {
    check,
    list: (session: string): Drawing[] => Object.entries(entries[session] ?? {}).map(([path, e]) => summary(path, e)),
    /** A prompt carried the notes: the agent's version catches up with the user's. Returns the notes, one notice each. */
    consume(session: string): string[] {
      const notes: string[] = [];
      for (const [path, e] of Object.entries(entries[session] ?? {})) {
        if (e.version === e.agent_version) continue;
        notes.push(saved(path, e).note);
        e.agent_version = e.version;
        e.baseline = e.content;
      }
      return notes;
    },
    route(method: string, url: URL, body: Record<string, unknown>): Response | null {
      let r: RegExpMatchArray | null;
      if (url.pathname === '/api/settings/drawio/check' && method === 'POST') {
        if (typeof body.origin !== 'string') return fail(400, 'origin is required');
        return reply(200, check(body.origin));
      }
      if ((r = url.pathname.match(/^\/api\/sessions\/([^/]+)\/drawings$/)) && method === 'GET') return reply(200, { drawings: this.list(decodeURIComponent(r[1])) });
      if ((r = url.pathname.match(/^\/api\/sessions\/([^/]+)\/drawings\/(.+)$/))) {
        const session = decodeURIComponent(r[1]);
        const path = r[2].split('/').map(decodeURIComponent).join('/');
        if (!/^docs\/[^/]+\.(excalidraw|drawio)$/.test(path)) return fail(400, 'the path must be docs/<name>.excalidraw or .drawio');
        const mine = (entries[session] ??= {});
        if (method === 'GET') {
          const e = mine[path];
          if (!e) return fail(404, 'no such drawing');
          const at = url.searchParams.get('at');
          // History: the mock keeps no versions; the current diff stands in while the agent's version is the one asked for.
          if (at !== null && Number(at) !== e.agent_version) return fail(404, 'that version is no longer kept');
          return reply(200, view(path, e));
        }
        if (method === 'PUT') {
          const put = body as unknown as DrawingPut;
          if (typeof put.content !== 'string' || (put.format !== 'excalidraw' && put.format !== 'drawio')) return fail(400, 'content and format are required');
          if (put.content.length > 2 * 1024 * 1024) return fail(413, 'the drawing is larger than 2 MiB');
          if (put.origin === 'convert') {
            if (mine[path]) return fail(409, 'that drawing already exists');
            mine[path] = { format: put.format, version: 1, agent_version: 1, item_id: String(put.item_id ?? ''), fence_index: Number(put.fence_index ?? 0), content: put.content, baseline: put.content };
            return reply(200, saved(path, mine[path]));
          }
          const e = mine[path];
          if (!e) return fail(404, 'no such drawing; convert a diagram first');
          if (put.base_version !== undefined && put.base_version !== e.version) return fail(409, 'the drawing changed since this version was loaded');
          e.content = put.content;
          e.version++;
          // Edited back to the agent's version: nothing to send.
          if (diffOf(e).changes.length === 0) e.agent_version = e.version;
          return reply(200, saved(path, e));
        }
      }
      return null;
    },
  };
}
