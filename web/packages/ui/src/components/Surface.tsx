/**
 * A `uam_surface` call's card: the A2UI v1.0 surface the call created, drawn with uam's own
 * components and kept live by the `surface` event. Inputs write to a local copy of the data
 * model; a newer version from the server replaces it but keeps the owner's unsent edits where
 * the agent left that value alone. Text is always plain text; an unknown component draws
 * nothing. The renderer, not the model, keeps the card designed (DESIGN.md Cards): uam's own
 * "Card from the agent" line, one house card, nested Cards flat, a title line, packed short
 * rows, template rows as one grid, media at the column's width.
 */
import {
  ArrowLeft, ArrowRight, Bell, BellOff, Calendar, CalendarDays, Camera, Check, CircleAlert, CircleCheck, CircleDashed, CircleQuestionMark, CircleUserRound, CreditCard,
  Download, Ellipsis, EllipsisVertical, Eye, EyeOff, FastForward, Folder, Heart, HeartOff, House, Image, Info, Lock, LockOpen, Mail, MapPin, Menu, Paperclip, Pause,
  Pencil, Phone, PhoneCall, Play, Plus, Printer, RefreshCw, Rewind, Search, Send, Settings, Share2, ShoppingCart, SkipBack, SkipForward, Square, Star, StarHalf,
  StarOff, Trash, TriangleAlert, Upload, User, Volume, Volume1, Volume2, VolumeX, X, type LucideIcon,
} from 'lucide-react';
import { createContext, memo, useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { ApiError, describeError, isStatus, newRequestId, type Surface, type SurfaceAction, type SurfaceComponent } from '../api';
import { cn } from '../lib/cn';
import { actionContext, boundPath, cardTitle, children, drawnCount, failedChecks, get, isHeading, leadTitle, MAX_DEPTH, rememberSurface, resolve, rowName, set, SurfaceVersions, text } from '../lib/surface';
import { Note, Skeleton } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Dialog } from './ui/dialog';
import { inputVariants, Input } from './ui/input';
import { Meter } from './ui/meter';
import { Segmented } from './ui/segmented';

type Model = Record<string, unknown>;

/** What a component and everything it draws read from the data model: nothing, only paths under their template item, or anything. */
type Reads = 'none' | 'relative' | 'absolute';

/** The part of the card's context that does not change as the owner types. */
interface Shared {
  byId: Map<string, SurfaceComponent>;
  /** The Text styled as the card's title though the agent left it `body`. */
  title: string | null;
  write: (path: string, value: unknown) => void;
  act: (component: SurfaceComponent, base: string) => void;
  /** The raw file route's URL for a path in the Thread's working folder: media never load from anywhere else. */
  media: (path: string) => string;
  /** The Button instance (`id`, newline, template base) whose action is in flight, and the one last sent. */
  sending: string | null;
  sent: string | null;
  /** Pointers a blocked press marked empty, and the blocked Button instances with what they say. */
  invalid: ReadonlySet<string>;
  blocked: ReadonlyMap<string, string>;
  /** Pointers the agent changed over an unsent edit. */
  updated: ReadonlySet<string>;
  reads: (id: string) => Reads;
}
interface Ctx extends Shared {
  model: Model;
  shared: Shared;
}

/** A card the renderer stops drawing past this many components (a shared child counts per reference, a template per item). */
export const NODE_BUDGET = 2000;
const NONE: ReadonlySet<string> = new Set();
const NO_BLOCKS: ReadonlyMap<string, string> = new Map();
const instance = (id: string, base: string) => `${id}\n${base}`;
const same = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b);

/**
 * The local copy: the model shown, the pointers edited since the last send with the edit count
 * at each one's last edit (`edits` counts every edit), the agent's changes over them, and the
 * server's model and version it came from.
 */
interface Local { model: Model; dirty: ReadonlyMap<string, number>; edits: number; updated: ReadonlySet<string>; server: Model; seen: number }
const CLEAN: ReadonlyMap<string, number> = new Map();

/** A newer server version, keeping each unsent edit whose value the agent left alone; where the agent changed it too, the agent wins and the field says so. */
function merge(local: Local, surface: Surface): Local {
  let model = surface.data_model;
  const dirty = new Map<string, number>();
  const updated = new Set(local.updated);
  for (const [p, at] of local.dirty) {
    const mine = get(local.model, p);
    if (same(get(model, p), mine)) continue;
    if (same(get(local.server, p), get(model, p))) {
      model = set(model, p, mine ?? null);
      dirty.set(p, at);
    } else updated.add(p);
  }
  return { model, dirty, edits: local.edits, updated, server: surface.data_model, seen: surface.version };
}

/** Every binding in a component's own props: none, only relative ones, or an absolute one. */
function ownReads(value: unknown): Reads {
  if (!value || typeof value !== 'object') return 'none';
  const path = (value as { '@path'?: unknown; path?: unknown })['@path'] ?? (value as { componentId?: unknown; path?: unknown }).path;
  if (typeof path === 'string') return path.startsWith('/') ? 'absolute' : 'relative';
  let found: Reads = 'none';
  for (const v of Object.values(value)) {
    const r = ownReads(v);
    if (r === 'absolute') return r;
    if (r === 'relative') found = r;
  }
  return found;
}

/**
 * Where a component sits, when that changes its look: `tight` is a caption under a line of text;
 * `label` a short caption naming the media or row under it; `beside` is beside a Text in a Row
 * (the Text grows, an input's own label is read, not shown); `cell` is a cell of template rows or
 * of a packed row, where the layout sets the width (its weight is dropped) and an input's label,
 * the same on every row, is read with the row's words, not shown.
 */
type Place = 'tight' | 'label' | 'beside' | 'cell' | undefined;
const labelHidden = (place: Place) => place === 'beside' || place === 'cell';
/** An input's name when its label is not enough: no label, the row's words; a hidden repeated label, the label and the row's words. */
const ariaName = (label: string, place: Place, name: string | undefined) => (label ? (place === 'cell' && name && name !== label ? `${label}: ${name}` : undefined) : name || undefined);


export const SurfaceCard = memo(function SurfaceCard({ sessionId, callId }: Readonly<{ sessionId: string; callId: string }>) {
  const api = useApi();
  const version = useContext(SurfaceVersions)[callId];
  const [surface, setSurface] = useState<Surface | null>(null);
  const [error, setError] = useState('');
  const [missing, setMissing] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    api.surface(sessionId, callId, controller.signal).then((s) => { setSurface(s); setError(''); rememberSurface(sessionId, s); }, (err: unknown) => {
      if (controller.signal.aborted) return;
      setMissing(isStatus(err, 404));
      setError(describeError(err));
    });
    return () => controller.abort();
  }, [sessionId, callId, api, version, attempt]);
  const onAction = useCallback((body: SurfaceAction) => api.surfaceAction(sessionId, body).catch((err: unknown) => {
    // The card changed since it was drawn: fetch it, so the owner sees what they would press now.
    if (isStatus(err, 409)) setAttempt((n) => n + 1);
    throw err;
  }), [api, sessionId]);
  const media = useCallback((path: string) => api.rawFileUrl(sessionId, path), [api, sessionId]);
  const retry = <Button size="sm" variant="ghost" onClick={() => { setError(''); setAttempt((n) => n + 1); }}>Retry</Button>;
  // Only a tombstone says the agent removed the card; a surface the server does not know draws nothing.
  if (missing && !surface) return null;
  if (error && !surface) return <Note tone="error" role="alert">Could not load this card: {error} {retry}</Note>;
  if (!surface) return <Skeleton label="Loading the card…" rows={3} className="rounded-lg bg-raised p-4 shadow-raised" />;
  if (surface.deleted) return <Note>Removed by the agent</Note>;
  // One shape with or without the note, so a failed refresh never remounts the card and loses its unsent edits.
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <SurfaceView surface={surface} onAction={onAction} media={media} />
      {/* A failed refresh keeps the card it has and says it may be behind. */}
      {error && <Note role="status" className="flex flex-wrap items-center gap-x-2 px-1">May be out of date · {error} {retry}</Note>}
    </div>
  );
});

/** uam's own line at the top of every card and in every card dialog: the agent draws below it and cannot move or restyle it. */
function FromAgent() {
  return <p className="text-caption text-muted">Card from the agent</p>;
}

/** The surface itself, with its local data model; exported for the DOM test. */
export function SurfaceView({ surface, onAction, media }: Readonly<{ surface: Surface; onAction: (body: SurfaceAction) => Promise<void>; media: (path: string) => string }>) {
  const [local, setLocal] = useState<Local>(() => ({ model: surface.data_model, dirty: CLEAN, edits: 0, updated: NONE, server: surface.data_model, seen: surface.version }));
  const [sending, setSending] = useState<string | null>(null);
  const [sent, setSent] = useState<string | null>(null);
  const [failed, setFailed] = useState('');
  // The server's word on a press of a card that had changed since it was drawn.
  const [changed, setChanged] = useState('');
  // The Button instances whose press a check blocked; their failures mark fields until they pass.
  const [pressed, setPressed] = useState<ReadonlyMap<string, { id: string; base: string }>>(new Map());
  if (local.seen !== surface.version) setLocal(merge(local, surface));
  const { model } = local;
  // Read by the action at press time, after the render that showed it.
  const latest = useRef(local);
  useLayoutEffect(() => {
    latest.current = local;
  });
  // One action in flight per card, taken before the request so a second press in the same tick finds it.
  const busy = useRef(false);
  // The last press and its request id, kept until the server answers it, so pressing again retries it under the same id.
  const retry = useRef<{ key: string; id: string; sent: string } | null>(null);

  const byId = useMemo(() => new Map(surface.components.map((c) => [c.id, c])), [surface.components]);
  const reads = useMemo(() => {
    const known = new Map<string, Reads>();
    const of = (id: string): Reads => {
      const hit = known.get(id);
      if (hit) return hit;
      // A cycle reads as anything until it resolves, which only re-renders more.
      known.set(id, 'absolute');
      const c = byId.get(id);
      let r: Reads = c ? ownReads(c) : 'none';
      const next = c ? [c.children, c.child, c.trigger, c.content, ...(Array.isArray(c.tabs) ? c.tabs.map((t: { child?: unknown }) => t?.child) : [])] : [];
      const ids = next.flatMap((k) => (typeof k === 'string' ? [k] : Array.isArray(k) ? k.map(String) : k && typeof k === 'object' && typeof (k as { componentId?: unknown }).componentId === 'string' ? [String((k as { componentId: string }).componentId)] : []));
      for (const k of ids) {
        if (r === 'absolute') break;
        const child = of(k);
        if (child !== 'none') r = child;
      }
      known.set(id, r);
      return r;
    };
    return of;
  }, [byId]);
  const count = useMemo(() => drawnCount(byId, model, NODE_BUDGET), [byId, model]);
  const title = cardTitle({ components: surface.components, data_model: model });
  const titleId = title && !isHeading(byId.get(title.id)) ? title.id : null;

  const write = useCallback((path: string, value: unknown) => setLocal((s) => {
    const updated = s.updated.has(path) ? new Set([...s.updated].filter((p) => p !== path)) : s.updated;
    return { ...s, model: set(s.model, path, value), dirty: new Map(s.dirty).set(path, s.edits + 1), edits: s.edits + 1, updated };
  }), []);
  const act = useCallback((component: SurfaceComponent, base: string) => {
    const event = (component.action as { event?: { name?: string; context?: unknown } } | undefined)?.event;
    if (!event?.name || busy.current) return;
    const key = instance(component.id, base);
    const now = latest.current;
    if (failedChecks(component, now.model, base).length) {
      setPressed((m) => new Map(m).set(key, { id: component.id, base }));
      return;
    }
    busy.current = true;
    setSending(key);
    setFailed('');
    setChanged('');
    const press = { surface_id: surface.surface_id, name: event.name, source_component_id: component.id, context: actionContext(event.context, now.model, base), base_path: base, version: now.seen };
    // The same press again after a lost answer keeps its id, so the server can answer it once; anything changed makes it a new press.
    const sent = JSON.stringify(press);
    const id = retry.current?.key === key && retry.current.sent === sent ? retry.current.id : newRequestId();
    retry.current = { key, id, sent };
    onAction({ ...press, request_id: id })
      .then(() => {
        retry.current = null;
        setSent(key);
        setPressed(new Map());
        // An edit made while the press was in flight is not in what was sent, so it stays protected.
        setLocal((s) => ({ ...s, dirty: new Map([...s.dirty].filter(([p, at]) => now.dirty.get(p) !== at)) }));
      }, (err: unknown) => {
        // A refusal is final, so the next press is a new one; a lost answer may have queued it, so that id is kept.
        // A status of 0 is a request that got no answer at all.
        if (err instanceof ApiError && err.status > 0 && err.status < 500) retry.current = null;
        if (isStatus(err, 409)) setChanged(describeError(err));
        else setFailed(describeError(err));
      })
      .finally(() => {
        busy.current = false;
        setSending(null);
      });
  }, [onAction, surface.surface_id]);

  // The failures of the blocked presses, as a key, so the sets below change only when the failures do.
  const failures = JSON.stringify([...pressed].map(([key, { id, base }]) => {
    const c = byId.get(id);
    const failed = c ? failedChecks(c, model, base) : [];
    return [key, failed.flatMap((f) => (f.path ? [f.path] : [])), failed.length ? failed.find((f) => f.message)?.message || 'Fill in the required fields' : ''] as const;
  }));
  const { invalid, blocked } = useMemo(() => {
    const list = JSON.parse(failures) as [string, string[], string][];
    if (!list.length) return { invalid: NONE, blocked: NO_BLOCKS };
    return { invalid: new Set(list.flatMap(([, paths]) => paths)), blocked: new Map(list.filter(([, , m]) => m).map(([key, , m]) => [key, m])) };
  }, [failures]);
  const shared = useMemo<Shared>(() => ({ byId, title: titleId, write, act, media, sending, sent, invalid, blocked, updated: local.updated, reads }), [byId, titleId, write, act, media, sending, sent, invalid, blocked, local.updated, reads]);
  const ctx: Ctx = { ...shared, shared, model };
  return (
    // An untitled card is no landmark: two of them in a Thread would share one name.
    <section aria-label={title?.text || undefined} data-surface={surface.surface_id} className="flex min-w-0 flex-col gap-3 rounded-lg bg-raised p-4 text-ui text-body tabular-nums shadow-raised sm:px-5">
      <FromAgent />
      {count > NODE_BUDGET ? <Note>This card is too large to show</Note> : <Node ctx={ctx} id="root" base="" depth={0} />}
      {failed && <Note tone="error" role="alert">Could not send: {failed}</Note>}
      {changed && <Note role="status" className="first-letter:uppercase">{changed}</Note>}
      {/* Mounted with the card, so a screen reader hears when the agent changes a value the owner had edited. */}
      <p role="status" className="sr-only">{local.updated.size ? 'Updated by the agent' : ''}</p>
    </section>
  );
}

const JUSTIFY: Record<string, string> = { start: 'justify-start', center: 'justify-center', end: 'justify-end', spaceBetween: 'justify-between', spaceAround: 'justify-around', spaceEvenly: 'justify-evenly', stretch: '[&>*]:flex-1' };
const SPREAD = new Set(['spaceBetween', 'spaceAround', 'spaceEvenly']);
const ALIGN: Record<string, string> = { start: 'items-start', center: 'items-center', end: 'items-end', stretch: 'items-stretch' };
const INPUTS = new Set(['TextField', 'Slider', 'ChoicePicker', 'DateTimeInput']);
const CONTROLS = new Set([...INPUTS, 'Button', 'Modal', 'CheckBox']);
const MEDIA = new Set(['Image', 'Video', 'AudioPlayer']);
const LABELLED = new Set([...MEDIA, 'Row']);
/** Words that name what follows rather than say something: short, one line, no sentence stop. */
const isLabel = (words: string) => words.length <= 40 && !words.includes('\n') && !/[.:;?!]$/.test(words);
/** Two lines that say the same thing: every word of one is in the other ("Voice note", "Release voice note"). */
function repeats(a: string, b: string) {
  const words = (s: string) => s.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean);
  const [x, y] = [words(a), words(b)];
  return x.length > 0 && y.length > 0 && (x.every((w) => y.includes(w)) || y.every((w) => x.includes(w)));
}
const TONE = {
  neutral: { chip: 'muted', Icon: CircleDashed },
  running: { chip: 'accent', Icon: null },
  ok: { chip: 'success', Icon: CircleCheck },
  warn: { chip: 'warning', Icon: CircleAlert },
  fail: { chip: 'error', Icon: TriangleAlert },
} as const;

// The basic catalog's icon names on lucide; where lucide has no exact match the nearest stands in (noted).
const ICONS: Record<string, LucideIcon> = {
  accountCircle: CircleUserRound, add: Plus, arrowBack: ArrowLeft, arrowForward: ArrowRight, attachFile: Paperclip, calendarToday: Calendar,
  call: PhoneCall, camera: Camera, check: Check, close: X, delete: Trash, download: Download, edit: Pencil, event: CalendarDays, error: CircleAlert,
  fastForward: FastForward, favorite: Heart, favoriteOff: HeartOff, folder: Folder, help: CircleQuestionMark, home: House, info: Info,
  locationOn: MapPin, lock: Lock, lockOpen: LockOpen, mail: Mail, menu: Menu, moreVert: EllipsisVertical, moreHoriz: Ellipsis,
  notificationsOff: BellOff, notifications: Bell, pause: Pause, payment: CreditCard, person: User, phone: Phone, photo: Image, play: Play,
  print: Printer, refresh: RefreshCw, rewind: Rewind, search: Search, send: Send, settings: Settings, share: Share2, shoppingCart: ShoppingCart,
  skipNext: SkipForward, skipPrevious: SkipBack, star: Star, starHalf: StarHalf, starOff: StarOff,
  stop: Square, // nearest: lucide has no plain stop glyph
  upload: Upload, visibility: Eye, visibilityOff: EyeOff, volumeDown: Volume1,
  volumeMute: Volume, // nearest: the speaker without waves
  volumeOff: VolumeX, volumeUp: Volume2, warning: TriangleAlert,
};
const SVG_PATH = /^[MmLlHhVvCcSsQqTtAaZz0-9.,\s-]{1,2048}$/;
const FIT: Record<string, string> = { contain: 'object-contain', cover: 'object-cover', fill: 'object-fill', none: 'object-none', scaleDown: 'object-scale-down' };
// A feature image keeps its own shape up to the column's width and a height, so it is never letterboxed; only `smallFeature`, `icon` and `avatar` are small on purpose, and `header` is a full-width band.
const IMAGE_SIZE: Record<string, string> = {
  icon: 'size-6 shrink-0', avatar: 'size-10 shrink-0 rounded-full', smallFeature: 'max-h-32 w-auto max-w-full self-start', mediumFeature: 'max-h-72 w-auto max-w-full self-start', largeFeature: 'max-h-[28rem] w-auto max-w-full self-start', header: 'h-40 w-full',
};

/** A Button's look: its variant, and its label (a Text child in the button's own colour; any other child draws as itself). */
function buttonLook(ctx: Ctx, c: SurfaceComponent, base: string, depth: number) {
  const variant = c.variant === 'primary' ? 'primary' : c.variant === 'borderless' ? 'ghost' : 'secondary';
  const label = typeof c.child === 'string' ? ctx.byId.get(c.child) : undefined;
  return { variant, label: label?.component === 'Text' ? text(label.text, ctx.model, base) : label ? <Node ctx={ctx} id={label.id} base={base} depth={depth + 1} /> : 'Send' } as const;
}

/** Short enough to sit in a packed row: icons, chips, buttons, a few words, or a group of only those. */
function compact(ctx: Ctx, c: SurfaceComponent | undefined, base: string, depth = 0): boolean {
  if (!c || depth > 2) return false;
  if (c.component === 'Text') return !isHeading(c) && text(c.text, ctx.model, base).length <= 32;
  if (c.component === 'Row' || c.component === 'Column') return Array.isArray(c.children) && c.children.every((id) => compact(ctx, ctx.byId.get(String(id)), base, depth + 1));
  return ['Icon', 'Button', 'Modal', 'Status', 'CheckBox'].includes(c.component);
}

/** A control small enough to end a table row: a CheckBox, a Button, or a single choice of up to five segments. */
const compactControl = (c: SurfaceComponent | undefined) =>
  c?.component === 'CheckBox' || c?.component === 'Button' || (c?.component === 'ChoicePicker' && c.variant !== 'multipleSelection' && Array.isArray(c.options) && c.options.length <= 5);

/**
 * Template rows that read as a table, as each row's cells: a Row per item, or a Column per item
 * holding a Status or a Progress, a cell per child; or a Column per item of lines of text over
 * one compact control, the text one cell and the control the trailing one, so the controls line
 * up down the card instead of stretching under each item.
 */
function tableCells(ctx: Ctx, spec: unknown): string[][] | null {
  const id = spec && typeof spec === 'object' && !Array.isArray(spec) ? (spec as { componentId?: unknown }).componentId : undefined;
  const item = typeof id === 'string' ? ctx.byId.get(id) : undefined;
  if (!item || !Array.isArray(item.children) || item.children.length < 2) return null;
  const ids = item.children.filter((k): k is string => typeof k === 'string');
  const specs = ids.map((k) => ctx.byId.get(k));
  if (item.component === 'Row' || (item.component === 'Column' && specs.some((k) => k?.component === 'Status' || k?.component === 'Progress'))) return ids.map((k) => [k]);
  const words = specs.slice(0, -1);
  return item.component === 'Column' && words.every((k) => k?.component === 'Text' && !isHeading(k)) && compactControl(specs.at(-1)) ? [ids.slice(0, -1), ids.slice(-1)] : null;
}

type NodeProps = Readonly<{ ctx: Ctx; id: string; base: string; depth: number; place?: Place; /** The row's words, naming an input that has no label. */ name?: string }>;

/** The part of the model a component's drawing depends on; a node redraws only when it changes, so a keystroke redraws its own row, not the card. */
const scope = (p: NodeProps) => {
  const r = p.ctx.reads(p.id);
  return r === 'none' ? null : r === 'absolute' ? p.ctx.model : get(p.ctx.model, p.base);
};

const Node = memo(
  function Node({ ctx, id, base, depth, place, name }: NodeProps): ReactNode {
  const c = ctx.byId.get(id);
  // A cycle or a runaway tree draws nothing past this depth.
  if (!c || depth > MAX_DEPTH) return null;
  const { model } = ctx;
  // A weighted child shares the row by its weight, and wraps to its own line once the row is narrower than 8rem a child.
  const weight = place !== 'cell' && typeof c.weight === 'number' ? ({ flexGrow: c.weight, flexBasis: 0, minWidth: '8rem' } as CSSProperties) : undefined;
  switch (c.component) {
    case 'Text': {
      const value = text(c.text, model, base);
      if (!value) return null;
      const title = isHeading(c) || c.id === ctx.title;
      return (
        <p style={weight} className={cn('min-w-0 [overflow-wrap:anywhere] whitespace-pre-wrap', title ? 'text-title text-ink' : c.variant === 'caption' ? 'text-caption text-muted' : 'text-ui text-body', place === 'tight' && '-mt-2', place === 'label' && '-mb-2', place === 'beside' && 'flex-1 basis-48')}>
          {value}
        </p>
      );
    }
    case 'Row': {
      const list = children(c.children, model, base);
      const specs = list.map((k) => ctx.byId.get(k.id));
      const kinds = specs.map((k) => String(k?.component));
      // A row of only buttons starts at the start: uam's own prompts end theirs at the right, and a card must not look like one.
      const buttons = kinds.length > 0 && kinds.every((k) => k === 'Button' || k === 'Modal');
      // Short items pack to the start with the house gap: spreading three icons to the card's edges reads as a layout bug, not a design.
      // Two items, or words with a control, keep the spread the agent asked for.
      // Weights on such items spread them as surely as `justify` does, so they pack too.
      const mixed = kinds.includes('Text') && kinds.some((k) => CONTROLS.has(k));
      const spread = SPREAD.has(String(c.justify)) || specs.some((k) => typeof k?.weight === 'number');
      const packed = buttons || (spread && list.length !== 2 && !mixed && specs.every((k, i) => compact(ctx, k, list[i].base)));
      const beside = kinds.includes('Text') && kinds.some((k) => INPUTS.has(k));
      const row = (Array.isArray(c.children) ? rowName(ctx.byId, model, c.children, base) : '') || name;
      // An icon and its words read as one label.
      const gap = specs.some((k) => k?.component === 'Icon') ? 'gap-x-2' : 'gap-x-4';
      return (
        <div style={weight} className={cn('flex min-w-0 flex-row flex-wrap gap-y-2', gap, packed ? undefined : JUSTIFY[String(c.justify)], ALIGN[String(c.align ?? 'center')])}>
          {list.map((k) => <Node key={`${k.id}:${k.base}`} ctx={ctx} id={k.id} base={k.base} depth={depth + 1} place={beside ? 'beside' : packed ? 'cell' : undefined} name={row} />)}
        </div>
      );
    }
    case 'Column': {
      const cells = tableCells(ctx, c.children);
      if (cells) return <TableRows ctx={ctx} spec={c.children} cells={cells} base={base} depth={depth} weight={weight} />;
      const list = children(c.children, model, base);
      const specs = list.map((k) => ctx.byId.get(k.id));
      const tight = specs.every((k, i) => compact(ctx, k, list[i].base));
      // Each template item is a row: its own words name its unlabelled inputs, however deep they sit.
      const template = !Array.isArray(c.children);
      return (
        <div style={weight} className={cn('flex min-w-0 flex-col', tight ? 'gap-1' : 'gap-3', ALIGN[String(c.align ?? 'stretch')])}>
          {list.map((k, i) => {
            const own = specs[i];
            const before = specs[i - 1]?.component === 'Text' ? text(specs[i - 1]!.text, model, list[i - 1].base) : '';
            // A short caption over media or a row names it and hugs it; any other caption hugs the line of text above it (a title's subtitle, a name's path).
            const label = !tight && own?.variant === 'caption' && LABELLED.has(String(specs[i + 1]?.component)) && isLabel(text(own.text, model, k.base));
            const caption = !tight && !label && i > 0 && own?.variant === 'caption' && !!before;
            // Media take the caption above them as their name; elsewhere the row's words pass through.
            const named = template ? rowName(ctx.byId, model, [k.id], k.base) || name : MEDIA.has(String(own?.component)) && before ? before : name;
            return <Node key={`${k.id}:${k.base}`} ctx={ctx} id={k.id} base={k.base} depth={depth + 1} place={label ? 'label' : caption ? 'tight' : undefined} name={named} />;
          })}
        </div>
      );
    }
    case 'List': {
      const cells = c.direction === 'horizontal' ? null : tableCells(ctx, c.children);
      if (cells) return <TableRows ctx={ctx} spec={c.children} cells={cells} base={base} depth={depth} weight={weight} />;
      return (
        <div style={weight} role="list" className={cn('flex min-w-0', c.direction === 'horizontal' ? 'flex-row gap-4 overflow-x-auto' : 'flex-col gap-3', ALIGN[String(c.align)])}>
          {children(c.children, model, base).map((k) => (
            <div key={`${k.id}:${k.base}`} role="listitem" className="min-w-0"><Node ctx={ctx} id={k.id} base={k.base} depth={depth + 1} name={(Array.isArray(c.children) ? '' : rowName(ctx.byId, model, [k.id], k.base)) || name} /></div>
          ))}
        </div>
      );
    }
    case 'Card':
      return <SurfaceGroup ctx={ctx} c={c} base={base} depth={depth} weight={weight} name={name} />;
    case 'Divider':
      return c.axis === 'vertical' ? <span aria-hidden="true" className="w-px self-stretch fade-rule-y" /> : <div role="separator" className="fade-rule" />;
    case 'Tabs':
      return <SurfaceTabs ctx={ctx} c={c} base={base} depth={depth} />;
    case 'Button': {
      const { variant, label } = buttonLook(ctx, c, base, depth);
      const key = instance(c.id, base);
      const blocked = ctx.blocked.get(key);
      return (
        <span style={weight} className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
          <Button variant={variant} disabled={!!ctx.sending && ctx.sending !== key} loading={ctx.sending === key} onClick={() => ctx.act(c, base)}>
            {label}
          </Button>
          {blocked ? <span role="alert" className="text-caption text-error">{blocked}</span> : ctx.sent === key && !ctx.sending && <span role="status" className="text-caption text-muted">Sent to the agent</span>}
        </span>
      );
    }
    case 'CheckBox':
      return <SurfaceCheck ctx={ctx} c={c} base={base} weight={weight} name={name} />;
    case 'TextField':
      return <SurfaceField ctx={ctx} c={c} base={base} weight={weight} place={place} name={name} />;
    case 'Slider':
      return <SurfaceSlider ctx={ctx} c={c} base={base} weight={weight} place={place} name={name} />;
    case 'ChoicePicker':
      return <SurfaceChoice ctx={ctx} c={c} base={base} weight={weight} place={place} name={name} />;
    case 'Status': {
      const tone = TONE[String(resolve(c.tone, model, base)) as keyof typeof TONE] ?? TONE.neutral;
      const Icon = tone.Icon;
      return (
        <Chip style={weight} tone={tone.chip} className="px-0">
          {Icon ? <Icon aria-hidden="true" className="size-3.5" /> : <span aria-hidden="true" className="mx-1 size-1.5 rounded-full bg-accent" />}
          {text(c.label, model, base)}
        </Chip>
      );
    }
    case 'Progress': {
      const value = Number(resolve(c.value, model, base)) || 0;
      const top = Number(resolve(c.max, model, base));
      const max = top > 0 ? top : 100;
      const own = text(c.label, model, base).trim();
      // The component's own name is no label: the bar shows its share instead of repeating "Progress" on every row.
      const label = own.toLowerCase() === 'progress' ? '' : own;
      const share = Math.min(1, Math.max(0, value / max));
      const shown = label || `${Math.round(share * 100)}%`;
      return (
        <div style={weight} role="progressbar" aria-valuemin={0} aria-valuemax={max} aria-valuenow={share * max} aria-valuetext={shown} aria-label={label || 'Progress'} className="flex min-w-24 flex-1 items-center gap-2.5">
          <Meter share={share} fill={share >= 1 ? 'bg-success' : 'bg-accent'} className="flex-1" />
          <span className={cn('shrink-0 text-right text-caption text-muted tabular-nums', label ? 'max-w-32 truncate' : 'w-9')}>{shown}</span>
        </div>
      );
    }
    case 'Icon': {
      const name = c.name && typeof c.name === 'object' && !('@path' in c.name) ? c.name : resolve(c.name, model, base);
      const svgPath = name && typeof name === 'object' ? (name as { svgPath?: unknown }).svgPath : undefined;
      if (typeof svgPath === 'string' && SVG_PATH.test(svgPath)) {
        return <svg style={weight} aria-hidden="true" viewBox="0 0 24 24" fill="currentColor" className="size-4 shrink-0 text-muted"><path d={svgPath} /></svg>;
      }
      const Icon = typeof name === 'string' && Object.hasOwn(ICONS, name) ? ICONS[name] : undefined;
      return Icon ? <Icon style={weight} aria-hidden="true" className="size-4 shrink-0 text-muted" /> : null;
    }
    case 'Image':
      return <SurfaceImage ctx={ctx} c={c} base={base} weight={weight} />;
    case 'Video': {
      const url = text(c.url, model, base);
      const poster = text(c.posterUrl, model, base);
      // An agent's clip has no captions to offer, as in the file preview (common.tsx).
      // At the clip's own shape (no bars beside it), up to the column's width and 320px high.
      // eslint-disable-next-line jsx-a11y/media-has-caption
      return url ? <video style={weight} controls preload="metadata" src={ctx.media(url)} poster={poster ? ctx.media(poster) : undefined} className="block max-h-80 w-auto max-w-full min-w-0 self-start rounded-md bg-sunken" /> : null;
    }
    case 'AudioPlayer': {
      const url = text(c.url, model, base);
      const description = text(c.description, model, base);
      // A description that repeats the caption just above says it once.
      const shown = description && !(name && repeats(name, description)) ? description : '';
      return url ? (
        <div style={weight} className="flex min-w-0 flex-col gap-1">
          {shown && <span className="text-caption text-muted">{shown}</span>}
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <audio controls preload="metadata" src={ctx.media(url)} aria-label={description || name || 'Audio'} className="h-10 w-full min-w-0" />
        </div>
      ) : null;
    }
    case 'Modal':
      return <SurfaceModal ctx={ctx} c={c} base={base} depth={depth} weight={weight} />;
    case 'DateTimeInput':
      return <SurfaceDateTime ctx={ctx} c={c} base={base} weight={weight} place={place} name={name} />;
    default:
      return null;
  }
  },
  (a, b) => a.id === b.id && a.base === b.base && a.depth === b.depth && a.place === b.place && a.name === b.name && a.ctx.shared === b.ctx.shared && scope(a) === scope(b),
);

/**
 * A Card: the root and the Cards directly inside it draw flat, since the surface is already the
 * one house card and a box in a box reads as a grey slab; a Card inside one of those is a
 * hairline group, so a deliberate grouping still reads.
 */
function SurfaceGroup({ ctx, c, base, depth, weight, name }: Part & Readonly<{ depth: number }>) {
  const inner = useContext(InCard);
  const body = typeof c.child === 'string' && <Node ctx={ctx} id={c.child} base={base} depth={depth + 1} name={name} />;
  if (c.id === 'root') return <div style={weight} className="flex min-w-0 flex-col gap-3">{body}</div>;
  return (
    <div style={weight} className={cn('flex min-w-0 flex-col gap-3', inner && 'rounded-md border border-hairline p-3')}>
      <InCard value>{body}</InCard>
    </div>
  );
}
const InCard = createContext(false);

/**
 * Template rows as one grid, so their names, chips, bars and controls line up down the card:
 * each cell is a shared column (a Progress takes the room left, else the first text-like cell
 * does; the rest keep their own width, weights dropped), and a missing value keeps its cell.
 * On a narrow card (under 32rem) each row wraps instead: the name takes a line of its own, so
 * a severity chip before it sits above it rather than squeezing it.
 */
function TableRows({ ctx, spec, cells, base, depth, weight }: Readonly<{ ctx: Ctx; spec: unknown; cells: string[][]; base: string; depth: number; weight?: CSSProperties }>) {
  const kinds = cells.map((ids) => (ids.length > 1 ? 'Column' : ctx.byId.get(ids[0])?.component));
  const bar = kinds.includes('Progress');
  const first = kinds.findIndex((k) => k === 'Text' || k === 'Column' || k === 'Row');
  const columns = kinds.map((k, i) => {
    if (k === 'Progress') return 'minmax(6rem,1fr)';
    if (i === first) return bar ? 'fit-content(24rem)' : 'minmax(0,1fr)';
    return k === 'Text' || k === 'Column' || k === 'Row' ? 'fit-content(16rem)' : 'auto';
  });
  const narrow = (k: string | undefined, i: number) => (k === 'Progress' ? 'min-w-24 flex-1' : i === first ? 'basis-full' : undefined);
  return (
    <div style={weight} className="@container min-w-0">
      <div style={{ gridTemplateColumns: columns.join(' ') }} role="list" className="flex flex-col gap-2 @lg:grid @lg:gap-x-4 @lg:gap-y-0">
        {children(spec, ctx.model, base).map((row) => {
          const name = rowName(ctx.byId, ctx.model, cells.flat(), row.base);
          return (
            <div key={row.base} role="listitem" className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @lg:col-span-full @lg:grid @lg:min-h-8 @lg:grid-cols-subgrid @lg:py-1">
              {cells.map((ids, i) => (
                <div key={ids[0]} className={cn('flex min-w-0 items-center', narrow(kinds[i], i))}>
                  {ids.length === 1
                    ? <Node ctx={ctx} id={ids[0]} base={row.base} depth={depth + 2} place="cell" name={name} />
                    : <div className="flex min-w-0 flex-1 flex-col gap-1">{ids.map((k) => <Node key={k} ctx={ctx} id={k} base={row.base} depth={depth + 2} place="cell" name={name} />)}</div>}
                </div>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/**
 * What a bound input says about its value under it: "Required" in `error` once a press it
 * blocks marked it empty, or "Updated by the agent" where the agent changed a value the owner
 * had edited; `aria` wires it to the input. Where the input's label is not shown either (a
 * table row), "Required" is only read: the error ring and the Button's message say it.
 */
function fieldNote(ctx: Ctx, path: string | null, id: string, labelShown: boolean) {
  const error = !!path && ctx.invalid.has(path);
  const updated = !error && !!path && ctx.updated.has(path);
  const noteId = `${id}-note`;
  return {
    aria: { 'aria-invalid': error || undefined, 'aria-describedby': error || updated ? noteId : undefined },
    note: error ? <span id={noteId} className={labelShown ? 'text-caption text-error' : 'sr-only'}>Required</span> : updated ? <span id={noteId} className="text-caption text-muted">Updated by the agent</span> : null,
  };
}

function Labelled({ id, label, weight, place, note, children: body }: Readonly<{ id: string; label: string; weight?: CSSProperties; place?: Place; note?: ReactNode; children: ReactNode }>) {
  return (
    <div style={weight} className="flex min-w-0 flex-col gap-1">
      {label && <label htmlFor={id} className={labelHidden(place) ? 'sr-only' : 'text-caption text-muted'}>{label}</label>}
      {body}
      {note}
    </div>
  );
}

type Part = Readonly<{ ctx: Ctx; c: SurfaceComponent; base: string; weight?: CSSProperties; place?: Place; name?: string }>;

function SurfaceField({ ctx, c, base, weight, place, name }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const label = text(c.label, ctx.model, base);
  const { aria, note } = fieldNote(ctx, path, id, !!label && !labelHidden(place));
  const common = { id, value: text(c.value, ctx.model, base), placeholder: text(c.placeholder, ctx.model, base) || undefined, readOnly: !path, 'aria-label': ariaName(label, place, name), ...aria };
  return (
    <Labelled id={id} label={label} weight={weight} place={place} note={note}>
      {c.variant === 'longText'
        ? <textarea {...common} rows={3} className={cn(inputVariants(), 'h-auto min-h-20 resize-y py-2')} onChange={(e) => path && ctx.write(path, e.target.value)} />
        : <Input {...common} type={c.variant === 'number' ? 'number' : c.variant === 'obscured' ? 'password' : 'text'} onChange={(e) => path && ctx.write(path, c.variant === 'number' ? e.target.valueAsNumber : e.target.value)} />}
    </Labelled>
  );
}

function SurfaceSlider({ ctx, c, base, weight, place, name }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const label = text(c.label, ctx.model, base);
  const { aria, note } = fieldNote(ctx, path, id, !!label && !labelHidden(place));
  const min = typeof c.min === 'number' ? c.min : 0;
  const max = typeof c.max === 'number' ? c.max : 100;
  const value = Number(resolve(c.value, ctx.model, base)) || min;
  const step = typeof c.steps === 'number' && c.steps > 0 ? (max - min) / c.steps : 'any';
  return (
    <Labelled id={id} label={label} weight={weight} place={place} note={note}>
      <div className="flex items-center gap-3">
        <input id={id} type="range" min={min} max={max} step={step} value={value} disabled={!path} aria-label={ariaName(label, place, name)} {...aria} className="h-8 min-w-0 flex-1 accent-accent pointer-coarse:h-11" onChange={(e) => path && ctx.write(path, e.target.valueAsNumber)} />
        <output htmlFor={id} className="w-10 shrink-0 text-right text-ui text-ink tabular-nums">{Number.isInteger(value) ? value : value.toFixed(2)}</output>
      </div>
    </Labelled>
  );
}

function SurfaceCheck({ ctx, c, base, weight, name }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const label = text(c.label, ctx.model, base);
  const { aria, note } = fieldNote(ctx, path, id, !!label);
  return (
    <div style={weight} className="flex min-w-0 flex-col gap-0.5">
      <label className="flex min-h-8 cursor-pointer items-center gap-2.5 text-ui text-ink pointer-coarse:min-h-11">
        <input type="checkbox" className="size-3.5 accent-accent" checked={!!resolve(c.value, ctx.model, base)} disabled={!path} aria-label={label ? undefined : name || undefined} {...aria} onChange={(e) => path && ctx.write(path, e.target.checked)} />
        <span className="min-w-0">{label}</span>
      </label>
      {note}
    </div>
  );
}

function SurfaceChoice({ ctx, c, base, weight, place, name }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const raw = resolve(c.value, ctx.model, base);
  const chosen = Array.isArray(raw) ? raw.map(String) : typeof raw === 'string' ? [raw] : [];
  const options = (Array.isArray(c.options) ? c.options : []).map((o: { label?: unknown; value?: unknown }) => ({ value: String(o?.value ?? ''), label: text(o?.label, ctx.model, base) }));
  const label = text(c.label, ctx.model, base);
  // An unlabelled picker is named by its row's words, so six of them in a list are not six "Choice"s.
  const named = ariaName(label, place, name) ?? (label ? undefined : 'Choice');
  const { aria, note } = fieldNote(ctx, path, id, !!label && !labelHidden(place));
  const multiple = c.variant === 'multipleSelection';
  const choose = (v: string[]) => path && ctx.write(path, v);
  if (!multiple && options.length <= 5) {
    return (
      <div style={weight} className="flex min-w-0 flex-col gap-1">
        {label && <span id={id} className={labelHidden(place) ? 'sr-only' : 'text-caption text-muted'}>{label}</span>}
        {/* Sized to its segments' words, never stretched across the card. With nothing chosen yet (or a value no segment has) the thumb hides, so no segment reads as picked. */}
        <Segmented aria-labelledby={label && !named ? id : undefined} aria-label={named} aria-invalid={aria['aria-invalid']} aria-describedby={aria['aria-describedby']} value={chosen[0] ?? ''} disabled={!path} items={options} onValueChange={(v) => choose([v])} className={cn('max-w-full min-w-48 self-start', !options.some((o) => o.value === chosen[0]) && '[&>span:first-child]:opacity-0', aria['aria-invalid'] && 'shadow-[inset_0_0_0_1px_var(--color-error)]')} />
        {note}
      </div>
    );
  }
  return (
    <fieldset style={weight} className="flex min-w-0 flex-col gap-0.5" disabled={!path} aria-label={named} aria-describedby={aria['aria-describedby']}>
      {label && <legend className={labelHidden(place) ? 'sr-only' : 'mb-1 text-caption text-muted'}>{label}</legend>}
      {options.map((o) => {
        const on = chosen.includes(o.value);
        return (
          <label key={o.value} className={cn('flex min-h-8 cursor-pointer items-center gap-2.5 rounded-sm px-2 text-ui transition-colors hover:bg-tint-hover pointer-coarse:min-h-11', on && 'bg-tint-hover text-ink')}>
            <input type={multiple ? 'checkbox' : 'radio'} name={id} checked={on} aria-invalid={aria['aria-invalid']} className="size-3.5 accent-accent" onChange={() => choose(multiple ? (on ? chosen.filter((x) => x !== o.value) : [...chosen, o.value]) : [o.value])} />
            <span className="min-w-0 flex-1">{o.label}</span>
          </label>
        );
      })}
      {note}
    </fieldset>
  );
}

function SurfaceTabs({ ctx, c, base, depth }: Readonly<{ ctx: Ctx; c: SurfaceComponent; base: string; depth: number }>) {
  const tabs = (Array.isArray(c.tabs) ? c.tabs : []).map((t: { title?: unknown; child?: unknown }, i: number) => ({ value: String(i), label: text(t?.title, ctx.model, base), child: typeof t?.child === 'string' ? t.child : '' }));
  const [at, setAt] = useState('0');
  const shown = tabs.find((t) => t.value === at) ?? tabs[0];
  if (!shown) return null;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <Segmented aria-label="Tabs" value={shown.value} items={tabs} onValueChange={setAt} className="self-start" />
      <Node ctx={ctx} id={shown.child} base={base} depth={depth + 1} />
    </div>
  );
}

function SurfaceImage({ ctx, c, base, weight }: Part) {
  const url = text(c.url, ctx.model, base);
  const [failed, setFailed] = useState('');
  if (!url) return null;
  if (failed === url) return <Note>Image not available: {url}</Note>;
  const variant = IMAGE_SIZE[String(c.variant)] ? String(c.variant) : 'largeFeature';
  // `contain` unless the agent chose: a screenshot capped in height must not be cropped or stretched.
  const fit = FIT[String(c.fit)] ?? (variant === 'header' || variant === 'avatar' || variant === 'icon' ? 'object-cover' : 'object-contain');
  return <img style={weight} src={ctx.media(url)} alt={text(c.description, ctx.model, base)} loading="lazy" onError={() => setFailed(url)} className={cn('block h-auto min-w-0 rounded-md bg-sunken', IMAGE_SIZE[variant], fit)} />;
}

/**
 * The trigger opens uam's dialog with the content inside and sends nothing; a Button in the
 * content acts and leaves the dialog open. The content's lead line, when it reads as a title,
 * titles the dialog; else the trigger's label does.
 */
function SurfaceModal({ ctx, c, base, depth, weight }: Part & Readonly<{ depth: number }>) {
  const [open, setOpen] = useState(false);
  const trigger = typeof c.trigger === 'string' ? ctx.byId.get(c.trigger) : undefined;
  if (!trigger || typeof c.content !== 'string') return null;
  // A Button trigger keeps its look but only opens the dialog; any other component is drawn inside a borderless button.
  const look = trigger.component === 'Button' ? buttonLook(ctx, trigger, base, depth) : { variant: 'ghost', label: <Node ctx={ctx} id={trigger.id} base={base} depth={depth + 1} /> } as const;
  const lead = leadTitle(ctx.byId, ctx.model, c.content, base);
  const content = ctx.byId.get(c.content);
  const body = lead && content?.component === 'Column' && Array.isArray(content.children) ? content.children.slice(1).map(String) : [c.content];
  return (
    <span style={weight} className="inline-flex">
      <Button variant={look.variant} aria-haspopup="dialog" onClick={() => setOpen(true)}>{look.label}</Button>
      <Dialog open={open} onOpenChange={setOpen} title={lead?.text || (typeof look.label === 'string' ? look.label : 'Details')} description="Card from the agent">
        <div className="flex min-w-0 flex-col gap-3 text-ui text-body tabular-nums">
          {body.map((id) => <Node key={id} ctx={ctx} id={id} base={base} depth={depth + 1} />)}
        </div>
      </Dialog>
    </span>
  );
}

function SurfaceDateTime({ ctx, c, base, weight, place, name }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const label = text(c.label, ctx.model, base);
  const { aria, note } = fieldNote(ctx, path, id, !!label && !labelHidden(place));
  const date = c.enableDate === true;
  const clock = c.enableTime === true;
  const type = date && clock ? 'datetime-local' : clock ? 'time' : 'date';
  // shortcut: datetime-local shows no zone or seconds, so a stored "…T14:30:00Z" shows its first 16 characters; convert zones if agents send them.
  const shown = (s: string) => (type === 'datetime-local' ? s.slice(0, 16) : s);
  return (
    <Labelled id={id} label={label} weight={weight} place={place} note={note}>
      <Input
        id={id}
        aria-label={ariaName(label, place, name)}
        {...aria}
        type={type}
        value={shown(text(c.value, ctx.model, base))}
        min={shown(text(c.min, ctx.model, base)) || undefined}
        max={shown(text(c.max, ctx.model, base)) || undefined}
        readOnly={!path}
        className="w-auto min-w-44 self-start"
        onChange={(e) => path && ctx.write(path, e.target.value)}
      />
    </Labelled>
  );
}
