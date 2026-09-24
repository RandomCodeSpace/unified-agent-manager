/**
 * Diagram rendering (ADR 0004, "Diagrams in a sandboxed frame"). Mermaid runs only inside one
 * hidden `<iframe sandbox="allow-scripts">` served from `/diagram-frame.html` with its own CSP.
 * The first part is the message protocol both sides share; the rest is the page side, which
 * never inlines what comes back: the SVG string is shown as a `data:` image.
 *
 * The frame has an opaque origin, so the page must post to `'*'` and check `event.source`; the
 * frame answers to the origin the request came from. Each side validates what it receives.
 */

export interface DiagramRequest {
  id: string;
  source: string;
  /** Mermaid theme variables, resolved from the page's tokens. */
  theme: Record<string, string>;
}

export interface DiagramRendered {
  id: string;
  svg: string;
  /** Intrinsic size from the SVG's viewBox, so the page can size the image before it loads. */
  width: number;
  height: number;
}

export interface DiagramFailed {
  id: string;
  error: string;
}

export type DiagramReply = DiagramRendered | DiagramFailed;

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null;

export function parseRequest(data: unknown): DiagramRequest | null {
  if (!isRecord(data) || typeof data.id !== 'string' || typeof data.source !== 'string' || !isRecord(data.theme)) return null;
  const theme: Record<string, string> = {};
  for (const [k, v] of Object.entries(data.theme)) if (typeof v === 'string') theme[k] = v;
  return { id: data.id, source: data.source, theme };
}

export function parseReply(data: unknown): DiagramReply | null {
  if (!isRecord(data) || typeof data.id !== 'string') return null;
  if (typeof data.error === 'string') return { id: data.id, error: data.error };
  if (typeof data.svg === 'string' && typeof data.width === 'number' && typeof data.height === 'number') {
    return { id: data.id, svg: data.svg, width: data.width, height: data.height };
  }
  return null;
}

/**
 * The SVG with an intrinsic size: Mermaid's root says `width="100%"`, which as an image means
 * the browser's 300×150 default, so the viewBox's size (or, without one, the root's own pixel
 * width and height) is written onto the root. Null when the markup states no size.
 */
export function intrinsicSize(svg: string): { svg: string; width: number; height: number } | null {
  const open = /<svg\b[^>]*>/.exec(svg);
  if (!open) return null;
  const tag = open[0];
  const box = /\bviewBox\s*=\s*["']\s*-?[\d.]+[\s,]+-?[\d.]+[\s,]+([\d.]+)[\s,]+([\d.]+)\s*["']/.exec(tag);
  const px = (name: string) => /^[\d.]+(?:px)?$/.test(attr(tag, name)) ? Number.parseFloat(attr(tag, name)) : 0;
  const width = Math.ceil(box ? Number(box[1]) : px('width'));
  const height = Math.ceil(box ? Number(box[2]) : px('height'));
  if (!(width > 0 && height > 0)) return null;
  const sized = tag.replace(/\s(?:width|height)\s*=\s*(?:"[^"]*"|'[^']*')/g, '').replace(/^<svg/, `<svg width="${width}" height="${height}"`);
  return { svg: sized + svg.slice(open.index + tag.length), width, height };
}

const attr = (tag: string, name: string): string => new RegExp(`\\s${name}\\s*=\\s*["']([^"']*)["']`).exec(tag)?.[1]?.trim() ?? '';

export const FRAME_PATH = '/diagram-frame.html';
const RENDER_TIMEOUT = 10_000;

export interface Rendered {
  svg: string;
  width: number;
  height: number;
}

export class DiagramError extends Error {
  /** True when a retry could succeed (the frame stalled); parse errors are not transient. */
  readonly transient: boolean;
  constructor(message: string, transient: boolean) {
    super(message);
    this.transient = transient;
  }
}

interface Pending {
  id: string;
  source: string;
  theme: Record<string, string>;
  resolve: (r: Rendered) => void;
  reject: (e: DiagramError) => void;
  timer?: ReturnType<typeof setTimeout>;
}

/**
 * One request in flight at a time, each with a deadline. Transport-agnostic: `send` posts a
 * request; `receive` takes whatever arrived on the message channel and says whether it was the
 * awaited reply. A missed deadline rejects the request and calls `onStall`, so the owner can
 * replace the frame.
 */
export class DiagramQueue {
  private waiting: Pending[] = [];
  private current: Pending | null = null;
  private seq = 0;
  private readonly send: (req: DiagramRequest) => void;
  private readonly timeoutMs: number;
  private readonly onStall: () => void;

  constructor(send: (req: DiagramRequest) => void, timeoutMs = RENDER_TIMEOUT, onStall: () => void = () => {}) {
    this.send = send;
    this.timeoutMs = timeoutMs;
    this.onStall = onStall;
  }

  render(source: string, theme: Record<string, string>): Promise<Rendered> {
    return new Promise<Rendered>((resolve, reject) => {
      // Mermaid uses the id as a DOM id: it must start with a letter.
      this.waiting.push({ id: `d${++this.seq}`, source, theme, resolve, reject });
      this.next();
    });
  }

  receive(data: unknown): boolean {
    const reply = parseReply(data);
    const p = this.current;
    if (!reply || !p || reply.id !== p.id) return false;
    clearTimeout(p.timer);
    this.current = null;
    if ('svg' in reply) p.resolve({ svg: reply.svg, width: reply.width, height: reply.height });
    else p.reject(new DiagramError(reply.error, false));
    this.next();
    return true;
  }

  /** A discarded frame cannot serve any requests already assigned to it. */
  fail(error: DiagramError) {
    if (this.current) {
      clearTimeout(this.current.timer);
      this.current.reject(error);
      this.current = null;
    }
    for (const pending of this.waiting.splice(0)) pending.reject(error);
  }

  private next() {
    if (this.current || this.waiting.length === 0) return;
    const p = this.waiting.shift()!;
    this.current = p;
    p.timer = setTimeout(() => {
      if (this.current !== p) return;
      this.current = null;
      p.reject(new DiagramError('The diagram took too long to render', true));
      this.onStall();
      this.next();
    }, this.timeoutMs);
    this.send({ id: p.id, source: p.source, theme: p.theme });
  }
}

/**
 * True when the fenced block spanning `text[start, end)` has its closing fence: same character,
 * at least as long as the opening (CommonMark). While a reply streams, an open fence parses as
 * a code block that runs to the end of the text, and a diagram of half a fence is noise.
 */
export function fenceClosed(text: string, start: number, end: number): boolean {
  const block = text.slice(start, end);
  const open = /^[ \t>]*(`{3,}|~{3,})/.exec(block);
  if (!open) return true; // an indented code block has no fence to close
  const close = /\n[ \t>]*(`{3,}|~{3,})[ \t]*$/.exec(block);
  return !!close && close[1][0] === open[1][0] && close[1].length >= open[1].length;
}

export const svgDataUrl = (svg: string): string => 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);

/** Mermaid `base` theme variables, each a DESIGN.md colour token. */
const THEME_TOKENS: Record<string, string> = {
  background: 'code-bg',
  primaryColor: 'raised',
  primaryTextColor: 'ink',
  primaryBorderColor: 'hairline-strong',
  secondaryColor: 'accent-wash',
  secondaryTextColor: 'ink',
  secondaryBorderColor: 'hairline-strong',
  tertiaryColor: 'surface',
  tertiaryTextColor: 'ink',
  tertiaryBorderColor: 'hairline',
  lineColor: 'muted',
  textColor: 'body',
  titleColor: 'ink',
  edgeLabelBackground: 'code-bg',
  clusterBkg: 'surface',
  clusterBorder: 'hairline',
  noteBkgColor: 'warning-wash',
  noteTextColor: 'ink',
  noteBorderColor: 'hairline-strong',
  actorLineColor: 'hairline-strong',
  signalColor: 'body',
  signalTextColor: 'body',
  activationBkgColor: 'accent-wash',
  activationBorderColor: 'accent',
  errorBkgColor: 'error-wash',
  errorTextColor: 'error',
};

/**
 * The frame cannot load the page's fonts (font fetches need CORS, which an opaque origin never
 * passes) and an SVG shown as an image cannot either, so both measure and draw with the system
 * sans stack.
 */
export const DIAGRAM_FONT = "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif";

let theme: Record<string, string> | null = null;

/** Theme variables from the live tokens; a token that is not emitted is left to Mermaid's default. */
export function diagramTheme(): Record<string, string> {
  if (theme) return theme;
  const style = getComputedStyle(document.documentElement);
  // 14px, matched by the sequence sizes in the frame: Mermaid measures with the theme size and
  // draws sequence text with its own, and a mismatch overflows the boxes.
  const out: Record<string, string> = { fontFamily: DIAGRAM_FONT, fontSize: '14px' };
  for (const [variable, token] of Object.entries(THEME_TOKENS)) {
    const value = style.getPropertyValue(`--color-${token}`).trim();
    if (value) out[variable] = value;
  }
  theme = out;
  return out;
}

let frame: HTMLIFrameElement | null = null;
let queue: DiagramQueue | null = null;
let loaded: Promise<void> | null = null;
let removeFrameListeners: (() => void) | null = null;
const cache = new Map<string, Promise<Rendered>>();

function dropFrame() {
  removeFrameListeners?.();
  removeFrameListeners = null;
  queue?.fail(new DiagramError('The diagram renderer stopped responding', true));
  frame?.remove();
  frame = null;
  queue = null;
  loaded = null;
}

/**
 * The shared frame, created on the first diagram. Invisible but laid out at a real size:
 * Mermaid measures text, and some diagrams (gantt, charts) take their width from the page.
 */
function ensureFrame(): { queue: DiagramQueue; loaded: Promise<void> } {
  if (frame && queue && loaded) return { queue, loaded };
  const el = document.createElement('iframe');
  el.setAttribute('sandbox', 'allow-scripts');
  el.setAttribute('aria-hidden', 'true');
  el.tabIndex = -1;
  el.title = 'Diagram renderer';
  el.className = 'pointer-events-none fixed top-0 left-0 -z-10 h-150 w-200 opacity-0';
  el.src = FRAME_PATH;
  const q = new DiagramQueue((req) => el.contentWindow?.postMessage(req, '*'), RENDER_TIMEOUT, dropFrame);
  const onMessage = (e: MessageEvent) => {
    // The sandboxed frame has an opaque origin, so its messages carry "null".
    if (e.source !== el.contentWindow || e.origin !== 'null') return;
    q.receive(e.data);
  };
  window.addEventListener('message', onMessage);
  removeFrameListeners = () => window.removeEventListener('message', onMessage);
  frame = el;
  queue = q;
  loaded = new Promise<void>((resolve, reject) => {
    const clear = () => {
      clearTimeout(timer);
      el.removeEventListener('load', onLoad);
      el.removeEventListener('error', onError);
    };
    const onLoad = () => {
      clear();
      resolve();
    };
    const onError = () => {
      clear();
      dropFrame();
      reject(new DiagramError('The diagram renderer could not load', true));
    };
    const timer = setTimeout(onError, RENDER_TIMEOUT);
    el.addEventListener('load', onLoad, { once: true });
    el.addEventListener('error', onError, { once: true });
  });
  document.body.append(el);
  return { queue: q, loaded };
}

/** Renders Mermaid source to an SVG string; the same source renders once per page. */
export function renderDiagram(source: string): Promise<Rendered> {
  const hit = cache.get(source);
  if (hit) return hit;
  const { queue: q, loaded: ready } = ensureFrame();
  const p = ready.then(() => q.render(source, diagramTheme()));
  cache.set(source, p);
  p.catch((err: unknown) => {
    if (err instanceof DiagramError && err.transient) cache.delete(source);
  });
  return p;
}
