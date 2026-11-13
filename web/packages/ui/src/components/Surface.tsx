/**
 * A `uam_surface` call's card: the A2UI v1.0 surface the call created, drawn with uam's own
 * components and kept live by the `surface` event. Inputs write to a local copy of the data
 * model; a newer version from the server replaces it. Text is always plain text; an unknown
 * component draws nothing. The renderer, not the model, keeps the card designed (DESIGN.md
 * Cards): one house card, nested Cards flat, a title line, packed short rows, template rows
 * as one grid, media at the column's width.
 */
import {
  ArrowLeft, ArrowRight, Bell, BellOff, Calendar, CalendarDays, Camera, Check, CircleAlert, CircleCheck, CircleDashed, CircleQuestionMark, CircleUserRound, CreditCard,
  Download, Ellipsis, EllipsisVertical, Eye, EyeOff, FastForward, Folder, Heart, HeartOff, House, Image, Info, Lock, LockOpen, Mail, MapPin, Menu, Paperclip, Pause,
  Pencil, Phone, PhoneCall, Play, Plus, Printer, RefreshCw, Rewind, Search, Send, Settings, Share2, ShoppingCart, SkipBack, SkipForward, Square, Star, StarHalf,
  StarOff, Trash, TriangleAlert, Upload, User, Volume, Volume1, Volume2, VolumeX, X, type LucideIcon,
} from 'lucide-react';
import { memo, useContext, useEffect, useId, useState, type CSSProperties, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type Surface, type SurfaceAction, type SurfaceComponent } from '../api';
import { cn } from '../lib/cn';
import { actionContext, boundPath, cardTitle, checksPass, children, isHeading, leadTitle, rememberSurface, resolve, set, SurfaceVersions, text } from '../lib/surface';
import { Note, Skeleton } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Dialog } from './ui/dialog';
import { inputVariants, Input } from './ui/input';
import { Meter } from './ui/meter';
import { Segmented } from './ui/segmented';

type Model = Record<string, unknown>;

interface Ctx {
  byId: Map<string, SurfaceComponent>;
  model: Model;
  /** The Text styled as the card's title though the agent left it `body`. */
  title: string | null;
  write: (path: string, value: unknown) => void;
  act: (component: SurfaceComponent, base: string) => void;
  /** The raw file route's URL for a path in the Thread's working folder: media never load from anywhere else. */
  media: (path: string) => string;
  sending: string | null;
  sent: string | null;
}

/** Where a component sits, when that changes its look: `tight` is a caption under a line of text; `beside` is beside a Text in a Row (the Text grows, an input's own label is read, not shown). */
type Place = 'tight' | 'beside' | undefined;

export const SurfaceCard = memo(function SurfaceCard({ sessionId, callId }: Readonly<{ sessionId: string; callId: string }>) {
  const api = useApi();
  const version = useContext(SurfaceVersions)[callId];
  const [surface, setSurface] = useState<Surface | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    api.surface(sessionId, callId, controller.signal).then((s) => { setSurface(s); setError(''); rememberSurface(sessionId, s); }, (err: unknown) => {
      if (!controller.signal.aborted) setError(describeError(err));
    });
    return () => controller.abort();
  }, [sessionId, callId, api, version, attempt]);
  if (error && !surface) return <Note tone="error" role="alert">Could not load this card: {error} <Button size="sm" onClick={() => { setError(''); setAttempt((n) => n + 1); }}>Retry</Button></Note>;
  if (!surface) return <Skeleton label="Loading the card…" rows={3} className="rounded-lg bg-raised p-4 shadow-raised" />;
  if (surface.deleted) return <Note>Removed by the agent</Note>;
  return <SurfaceView surface={surface} onAction={(body) => api.surfaceAction(sessionId, body)} media={(path) => api.rawFileUrl(sessionId, path)} />;
});

/** The surface itself, with its local data model; exported for the DOM test. */
export function SurfaceView({ surface, onAction, media }: Readonly<{ surface: Surface; onAction: (body: SurfaceAction) => Promise<void>; media: (path: string) => string }>) {
  const [model, setModel] = useState(surface.data_model);
  const [seen, setSeen] = useState(surface.version);
  const [sending, setSending] = useState<string | null>(null);
  const [sent, setSent] = useState<string | null>(null);
  const [failed, setFailed] = useState('');
  // A newer version from the server replaces the local copy, edits included.
  if (seen !== surface.version) {
    setSeen(surface.version);
    setModel(surface.data_model);
  }
  const title = cardTitle({ components: surface.components, data_model: model });
  const ctx: Ctx = {
    byId: new Map(surface.components.map((c) => [c.id, c])),
    model,
    title: title && !isHeading(surface.components.find((c) => c.id === title.id)) ? title.id : null,
    write: (path, value) => setModel((m) => set(m, path, value)),
    act: (component, base) => {
      const event = (component.action as { event?: { name?: string; context?: unknown } } | undefined)?.event;
      if (!event?.name) return;
      setSending(component.id);
      setFailed('');
      onAction({ surface_id: surface.surface_id, name: event.name, source_component_id: component.id, context: actionContext(event.context, model, base) })
        .then(() => setSent(component.id), (err: unknown) => setFailed(describeError(err)))
        .finally(() => setSending(null));
    },
    sending,
    sent,
    media,
  };
  return (
    <section aria-label={title?.text || 'Card from the agent'} data-surface={surface.surface_id} className="flex min-w-0 flex-col gap-3 rounded-lg bg-raised p-4 text-ui text-body tabular-nums shadow-raised sm:px-5">
      <Node ctx={ctx} id="root" base="" depth={0} />
      {failed && <Note tone="error" role="alert">Could not send: {failed}</Note>}
    </section>
  );
}

const JUSTIFY: Record<string, string> = { start: 'justify-start', center: 'justify-center', end: 'justify-end', spaceBetween: 'justify-between', spaceAround: 'justify-around', spaceEvenly: 'justify-evenly', stretch: '[&>*]:flex-1' };
const SPREAD = new Set(['spaceBetween', 'spaceAround', 'spaceEvenly']);
const ALIGN: Record<string, string> = { start: 'items-start', center: 'items-center', end: 'items-end', stretch: 'items-stretch' };
const INPUTS = new Set(['TextField', 'Slider', 'ChoicePicker', 'DateTimeInput']);
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
// Every feature image fills its column up to a height; only `smallFeature`, `icon` and `avatar` are small on purpose.
const IMAGE_SIZE: Record<string, string> = {
  icon: 'size-6', avatar: 'size-10 rounded-full', smallFeature: 'max-h-32 w-auto max-w-full', mediumFeature: 'max-h-72 w-full', largeFeature: 'max-h-[28rem] w-full', header: 'h-40 w-full',
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

/** Template rows that read as a table: a Row per item, or a Column per item holding a Status or a Progress. */
function tableItem(ctx: Ctx, spec: unknown): SurfaceComponent | null {
  const id = spec && typeof spec === 'object' && !Array.isArray(spec) ? (spec as { componentId?: unknown }).componentId : undefined;
  const item = typeof id === 'string' ? ctx.byId.get(id) : undefined;
  if (!item || !Array.isArray(item.children) || item.children.length < 2) return null;
  const kinds = item.children.map((k) => ctx.byId.get(String(k))?.component);
  return item.component === 'Row' || (item.component === 'Column' && kinds.some((k) => k === 'Status' || k === 'Progress')) ? item : null;
}

function Node({ ctx, id, base, depth, place }: Readonly<{ ctx: Ctx; id: string; base: string; depth: number; place?: Place }>): ReactNode {
  const c = ctx.byId.get(id);
  // A cycle or a runaway tree draws nothing past this depth.
  if (!c || depth > 24) return null;
  const { model } = ctx;
  // A weighted child shares the row by its weight, and wraps to its own line once the row is narrower than 8rem a child.
  const weight = typeof c.weight === 'number' ? ({ flexGrow: c.weight, flexBasis: 0, minWidth: '8rem' } as CSSProperties) : undefined;
  switch (c.component) {
    case 'Text': {
      const value = text(c.text, model, base);
      if (!value) return null;
      const title = isHeading(c) || c.id === ctx.title;
      return (
        <p style={weight} className={cn('min-w-0 [overflow-wrap:anywhere] whitespace-pre-wrap', title ? 'text-title text-ink' : c.variant === 'caption' ? 'text-caption text-muted' : 'text-ui text-body', place === 'tight' && '-mt-2', place === 'beside' && 'flex-1 basis-48')}>
          {value}
        </p>
      );
    }
    case 'Row': {
      const list = children(c.children, model, base);
      const specs = list.map((k) => ctx.byId.get(k.id));
      // Short items pack to the start with the house gap: spreading three icons to the card's edges reads as a layout bug, not a design.
      const packed = SPREAD.has(String(c.justify)) && specs.every((k, i) => compact(ctx, k, list[i].base));
      const beside = specs.some((k) => k?.component === 'Text') && specs.some((k) => INPUTS.has(String(k?.component)));
      // An icon and its words read as one label.
      const gap = specs.some((k) => k?.component === 'Icon') ? 'gap-x-2' : 'gap-x-4';
      return (
        <div style={weight} className={cn('flex min-w-0 flex-row flex-wrap gap-y-2', gap, packed ? undefined : JUSTIFY[String(c.justify)], ALIGN[String(c.align ?? 'center')])}>
          {list.map((k) => <Node key={`${k.id}:${k.base}`} ctx={ctx} id={k.id} base={k.base} depth={depth + 1} place={beside ? 'beside' : undefined} />)}
        </div>
      );
    }
    case 'Column': {
      const item = tableItem(ctx, c.children);
      if (item) return <TableRows ctx={ctx} spec={c.children} item={item} base={base} depth={depth} weight={weight} />;
      const list = children(c.children, model, base);
      const tight = list.every((k) => compact(ctx, ctx.byId.get(k.id), k.base));
      return (
        <div style={weight} className={cn('flex min-w-0 flex-col', tight ? 'gap-1' : 'gap-3', ALIGN[String(c.align ?? 'stretch')])}>
          {list.map((k, i) => {
            // A caption hugs the line above it (a title's subtitle, a name's path).
            const caption = !tight && i > 0 && ctx.byId.get(k.id)?.variant === 'caption' && ctx.byId.get(list[i - 1].id)?.component === 'Text';
            return <Node key={`${k.id}:${k.base}`} ctx={ctx} id={k.id} base={k.base} depth={depth + 1} place={caption ? 'tight' : undefined} />;
          })}
        </div>
      );
    }
    case 'List': {
      const item = c.direction === 'horizontal' ? null : tableItem(ctx, c.children);
      if (item) return <TableRows ctx={ctx} spec={c.children} item={item} base={base} depth={depth} weight={weight} />;
      return (
        <div style={weight} role="list" className={cn('flex min-w-0', c.direction === 'horizontal' ? 'flex-row gap-4 overflow-x-auto' : 'flex-col gap-3', ALIGN[String(c.align)])}>
          {children(c.children, model, base).map((k) => (
            <div key={`${k.id}:${k.base}`} role="listitem" className="min-w-0"><Node ctx={ctx} id={k.id} base={k.base} depth={depth + 1} /></div>
          ))}
        </div>
      );
    }
    case 'Card':
      // Every Card draws flat: the surface is already the one house card, and a box in a box reads as a grey slab.
      return <div style={weight} className="flex min-w-0 flex-col gap-3">{typeof c.child === 'string' && <Node ctx={ctx} id={c.child} base={base} depth={depth + 1} />}</div>;
    case 'Divider':
      return c.axis === 'vertical' ? <span aria-hidden="true" className="w-px self-stretch fade-rule-y" /> : <div role="separator" className="fade-rule" />;
    case 'Tabs':
      return <SurfaceTabs ctx={ctx} c={c} base={base} depth={depth} />;
    case 'Button': {
      const { variant, label } = buttonLook(ctx, c, base, depth);
      const ok = checksPass(c, model, base);
      return (
        <span style={weight} className="inline-flex items-center gap-2">
          <Button variant={variant} disabled={!ok} loading={ctx.sending === c.id} onClick={() => ctx.act(c, base)}>
            {label}
          </Button>
          {ctx.sent === c.id && ctx.sending !== c.id && <span role="status" className="text-caption text-muted">Sent to the agent</span>}
        </span>
      );
    }
    case 'CheckBox': {
      const path = boundPath(c.value, base);
      return (
        <label style={weight} className="flex min-h-8 cursor-pointer items-center gap-2.5 text-ui text-ink pointer-coarse:min-h-11">
          <input type="checkbox" className="size-3.5 accent-accent" checked={!!resolve(c.value, model, base)} disabled={!path} onChange={(e) => path && ctx.write(path, e.target.checked)} />
          <span className="min-w-0">{text(c.label, model, base)}</span>
        </label>
      );
    }
    case 'TextField':
      return <SurfaceField ctx={ctx} c={c} base={base} weight={weight} place={place} />;
    case 'Slider':
      return <SurfaceSlider ctx={ctx} c={c} base={base} weight={weight} place={place} />;
    case 'ChoicePicker':
      return <SurfaceChoice ctx={ctx} c={c} base={base} weight={weight} place={place} />;
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
      const max = Number(resolve(c.max, model, base)) || 100;
      const own = text(c.label, model, base).trim();
      // The component's own name is no label: the bar shows its share instead of repeating "Progress" on every row.
      const label = own.toLowerCase() === 'progress' ? '' : own;
      const share = Math.min(1, Math.max(0, value / max));
      const shown = label || `${Math.round(share * 100)}%`;
      return (
        <div style={weight} role="progressbar" aria-valuemin={0} aria-valuemax={max} aria-valuenow={value} aria-valuetext={shown} aria-label={label || 'Progress'} className="flex min-w-24 flex-1 items-center gap-2.5">
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
      const Icon = typeof name === 'string' ? ICONS[name] : undefined;
      return Icon ? <Icon style={weight} aria-hidden="true" className="size-4 shrink-0 text-muted" /> : null;
    }
    case 'Image':
      return <SurfaceImage ctx={ctx} c={c} base={base} weight={weight} />;
    case 'Video': {
      const url = text(c.url, model, base);
      const poster = text(c.posterUrl, model, base);
      // An agent's clip has no captions to offer, as in the file preview (common.tsx).
      // eslint-disable-next-line jsx-a11y/media-has-caption
      return url ? <video style={weight} controls preload="metadata" src={ctx.media(url)} poster={poster ? ctx.media(poster) : undefined} className="block max-h-80 w-full min-w-0 rounded-md bg-sunken" /> : null;
    }
    case 'AudioPlayer': {
      const url = text(c.url, model, base);
      const description = text(c.description, model, base);
      return url ? (
        <div style={weight} className="flex min-w-0 flex-col gap-1">
          {description && <span className="text-caption text-muted">{description}</span>}
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <audio controls preload="metadata" src={ctx.media(url)} aria-label={description || 'Audio'} className="h-10 w-full min-w-0" />
        </div>
      ) : null;
    }
    case 'Modal':
      return <SurfaceModal ctx={ctx} c={c} base={base} depth={depth} weight={weight} />;
    case 'DateTimeInput':
      return <SurfaceDateTime ctx={ctx} c={c} base={base} weight={weight} place={place} />;
    default:
      return null;
  }
}

/**
 * Template rows as one grid, so their names, chips and bars line up down the card: each item's
 * children are cells of a shared column (a Progress takes the room left, else the first
 * text-like cell does; the rest keep their own width), and a missing value keeps its cell.
 * On a narrow card (under 32rem) each row wraps instead: a leading name takes its own line.
 */
function TableRows({ ctx, spec, item, base, depth, weight }: Readonly<{ ctx: Ctx; spec: unknown; item: SurfaceComponent; base: string; depth: number; weight?: CSSProperties }>) {
  const cells = (item.children as unknown[]).filter((k): k is string => typeof k === 'string');
  const kinds = cells.map((k) => ctx.byId.get(k)?.component);
  const bar = kinds.includes('Progress');
  const first = kinds.findIndex((k) => k === 'Text' || k === 'Column' || k === 'Row');
  const columns = kinds.map((k, i) => {
    if (k === 'Progress') return 'minmax(6rem,1fr)';
    if (i === first) return bar ? 'fit-content(24rem)' : 'minmax(0,1fr)';
    return k === 'Text' || k === 'Column' || k === 'Row' ? 'fit-content(16rem)' : 'auto';
  });
  const narrow = (k: string | undefined, i: number) => (k === 'Progress' ? 'min-w-24 flex-1' : i === first ? (i === 0 ? 'basis-full' : 'flex-1 basis-48') : undefined);
  return (
    <div style={weight} className="@container min-w-0">
      <div style={{ gridTemplateColumns: columns.join(' ') }} role="list" className="flex flex-col gap-2 @lg:grid @lg:gap-x-4 @lg:gap-y-0">
        {children(spec, ctx.model, base).map((row) => (
          <div key={row.base} role="listitem" className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @lg:col-span-full @lg:grid @lg:min-h-8 @lg:grid-cols-subgrid @lg:py-1">
            {cells.map((k, i) => <div key={k} className={cn('flex min-w-0 items-center', narrow(kinds[i], i))}><Node ctx={ctx} id={k} base={row.base} depth={depth + 2} /></div>)}
          </div>
        ))}
      </div>
    </div>
  );
}

function Labelled({ id, label, weight, place, children: body }: Readonly<{ id: string; label: string; weight?: CSSProperties; place?: Place; children: ReactNode }>) {
  return (
    <div style={weight} className="flex min-w-0 flex-col gap-1">
      {label && <label htmlFor={id} className={place === 'beside' ? 'sr-only' : 'text-caption text-muted'}>{label}</label>}
      {body}
    </div>
  );
}

type Part = Readonly<{ ctx: Ctx; c: SurfaceComponent; base: string; weight?: CSSProperties; place?: Place }>;

function SurfaceField({ ctx, c, base, weight, place }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const common = { id, value: text(c.value, ctx.model, base), placeholder: text(c.placeholder, ctx.model, base) || undefined, readOnly: !path };
  return (
    <Labelled id={id} label={text(c.label, ctx.model, base)} weight={weight} place={place}>
      {c.variant === 'longText'
        ? <textarea {...common} rows={3} className={cn(inputVariants(), 'h-auto min-h-20 resize-y py-2')} onChange={(e) => path && ctx.write(path, e.target.value)} />
        : <Input {...common} type={c.variant === 'number' ? 'number' : c.variant === 'obscured' ? 'password' : 'text'} onChange={(e) => path && ctx.write(path, c.variant === 'number' ? e.target.valueAsNumber : e.target.value)} />}
    </Labelled>
  );
}

function SurfaceSlider({ ctx, c, base, weight, place }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const min = typeof c.min === 'number' ? c.min : 0;
  const max = typeof c.max === 'number' ? c.max : 100;
  const value = Number(resolve(c.value, ctx.model, base)) || min;
  const step = typeof c.steps === 'number' && c.steps > 0 ? (max - min) / c.steps : 'any';
  return (
    <Labelled id={id} label={text(c.label, ctx.model, base)} weight={weight} place={place}>
      <div className="flex items-center gap-3">
        <input id={id} type="range" min={min} max={max} step={step} value={value} disabled={!path} className="h-8 min-w-0 flex-1 accent-accent pointer-coarse:h-11" onChange={(e) => path && ctx.write(path, e.target.valueAsNumber)} />
        <output htmlFor={id} className="w-10 shrink-0 text-right text-ui text-ink tabular-nums">{Number.isInteger(value) ? value : value.toFixed(2)}</output>
      </div>
    </Labelled>
  );
}

function SurfaceChoice({ ctx, c, base, weight, place }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const raw = resolve(c.value, ctx.model, base);
  const chosen = Array.isArray(raw) ? raw.map(String) : typeof raw === 'string' ? [raw] : [];
  const options = (Array.isArray(c.options) ? c.options : []).map((o: { label?: unknown; value?: unknown }) => ({ value: String(o?.value ?? ''), label: text(o?.label, ctx.model, base) }));
  const label = text(c.label, ctx.model, base);
  const multiple = c.variant === 'multipleSelection';
  const choose = (v: string[]) => path && ctx.write(path, v);
  if (!multiple && options.length <= 5) {
    return (
      <div style={weight} className="flex min-w-0 flex-col gap-1">
        {label && <span id={id} className={place === 'beside' ? 'sr-only' : 'text-caption text-muted'}>{label}</span>}
        {/* With nothing chosen yet the thumb hides, so no segment reads as picked. */}
        <Segmented aria-labelledby={label ? id : undefined} aria-label={label ? undefined : 'Choice'} value={chosen[0] ?? ''} disabled={!path} items={options} onValueChange={(v) => choose([v])} className={cn('min-w-48', !chosen.length && '[&>span:first-child]:opacity-0')} />
      </div>
    );
  }
  return (
    <fieldset style={weight} className="flex min-w-0 flex-col gap-0.5" disabled={!path}>
      {label && <legend className={place === 'beside' ? 'sr-only' : 'mb-1 text-caption text-muted'}>{label}</legend>}
      {options.map((o) => {
        const on = chosen.includes(o.value);
        return (
          <label key={o.value} className={cn('flex min-h-8 cursor-pointer items-center gap-2.5 rounded-sm px-2 text-ui transition-colors hover:bg-tint-hover pointer-coarse:min-h-11', on && 'bg-tint-hover text-ink')}>
            <input type={multiple ? 'checkbox' : 'radio'} name={id} checked={on} className="size-3.5 accent-accent" onChange={() => choose(multiple ? (on ? chosen.filter((x) => x !== o.value) : [...chosen, o.value]) : [o.value])} />
            <span className="min-w-0 flex-1">{o.label}</span>
          </label>
        );
      })}
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
  const lead = leadTitle(ctx.byId, ctx.model, c.content);
  const content = ctx.byId.get(c.content);
  const body = lead && content?.component === 'Column' && Array.isArray(content.children) ? content.children.slice(1).map(String) : [c.content];
  return (
    <span style={weight} className="inline-flex">
      <Button variant={look.variant} aria-haspopup="dialog" onClick={() => setOpen(true)}>{look.label}</Button>
      <Dialog open={open} onOpenChange={setOpen} title={lead?.text || (typeof look.label === 'string' ? look.label : 'Details')}>
        <div className="flex min-w-0 flex-col gap-3 text-ui text-body tabular-nums">
          {body.map((id) => <Node key={id} ctx={ctx} id={id} base={base} depth={depth + 1} />)}
        </div>
      </Dialog>
    </span>
  );
}

function SurfaceDateTime({ ctx, c, base, weight, place }: Part) {
  const id = useId();
  const path = boundPath(c.value, base);
  const date = c.enableDate === true;
  const clock = c.enableTime === true;
  const type = date && clock ? 'datetime-local' : clock ? 'time' : 'date';
  // shortcut: datetime-local shows no zone or seconds, so a stored "…T14:30:00Z" shows its first 16 characters; convert zones if agents send them.
  const shown = (s: string) => (type === 'datetime-local' ? s.slice(0, 16) : s);
  return (
    <Labelled id={id} label={text(c.label, ctx.model, base)} weight={weight} place={place}>
      <Input
        id={id}
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
