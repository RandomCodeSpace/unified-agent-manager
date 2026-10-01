import { GripHorizontal, KanbanSquare, Maximize2, Minimize2, Minus, PictureInPicture2, X } from 'lucide-react';
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type HTMLAttributes, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { Segmented } from '../ui/segmented';
import { Tip } from '../ui/tooltip';
import { BoardView } from './BoardView';
import { usePlanner, type PopKind } from './context';
import { MapView } from './MapView';
import { NoticeBar } from './parts';
import { InboxList } from './Requests';
import { TreeView } from './TreeView';

/** The Document Picture-in-Picture API (Chromium); absent elsewhere, where the floating panel is the only pop-out. */
interface DocumentPictureInPicture {
  requestWindow: (options?: { width?: number; height?: number }) => Promise<Window>;
}
declare global {
  interface Window {
    documentPictureInPicture?: DocumentPictureInPicture;
  }
}

/** A phone: no separate window, and the floating panel spans the width. */
const PHONE = '(max-width: 480px)';

export type PopMode = 'pip' | 'float';

/** Whether this browser can also move the pop-out into a separate Picture-in-Picture window (the API exists, not on a phone). */
export function popMode(win: Window = window): PopMode {
  return win.documentPictureInPicture && !win.matchMedia(PHONE).matches ? 'pip' : 'float';
}

/**
 * The page's stylesheets, linked into another document of the same origin: `<link rel=stylesheet>`
 * elements to the same URLs, never style text, so the window stays under `style-src 'self'` and a
 * sign-in proxy sees the page's own cookies. Under `vite dev` the CSS is injected as <style>
 * elements that name their file; the dev server serves each file as CSS at its `/@fs` path.
 */
export function copyStyles(from: Document, to: Document): void {
  const hrefs = new Set<string>();
  for (const link of from.querySelectorAll<HTMLLinkElement>('link[rel="stylesheet"]')) if (link.href) hrefs.add(link.href);
  if (import.meta.env.DEV) {
    for (const style of from.querySelectorAll<HTMLStyleElement>('style[data-vite-dev-id]')) hrefs.add(new URL(`/@fs${style.dataset.viteDevId}`, from.location.href).href);
  }
  for (const href of hrefs) {
    const link = to.createElement('link');
    link.rel = 'stylesheet';
    link.href = href;
    to.head.append(link);
  }
  const motion = from.documentElement.dataset.motion;
  if (motion) to.documentElement.dataset.motion = motion;
  to.documentElement.lang = from.documentElement.lang || 'en';
  to.title = 'UAM planner';
}

/** Opens the Picture-in-Picture window (call it from the click that asked, for the user gesture), with the page's styles in it. */
export async function openPipWindow(win: Window = window): Promise<Window> {
  const pip = await win.documentPictureInPicture!.requestWindow({ width: 520, height: 680 });
  copyStyles(win.document, pip.document);
  return pip;
}

const KIND_ITEMS = [
  { value: 'tree', label: 'Tree' },
  { value: 'board', label: 'Board' },
  { value: 'map', label: 'Map' },
  { value: 'inbox', label: 'Inbox' },
];

/**
 * What a pop-out shows: its own choice of view over the shared view state, a compact header with
 * the caller's `controls`, and the planner's notice. In a separate window (`inWindow`) the views
 * offer no menus: they would open in the page's document, not the window's.
 */
function PopContent({ kind, onKind, controls, handle, headerProps, inWindow = false }: Readonly<{ kind: PopKind; onKind: (k: PopKind) => void; controls: ReactNode; handle?: ReactNode; headerProps?: HTMLAttributes<HTMLDivElement>; inWindow?: boolean }>) {
  const { ui, projects, boards } = usePlanner();
  const project = projects.find((p) => p.id === ui.project);
  const pending = ui.project ? (boards[ui.project]?.data?.requests.length ?? 0) : 0;
  return (
    <div className="flex h-full min-h-0 flex-col bg-canvas text-body">
      <div {...headerProps} className={cn('flex h-11 shrink-0 items-center gap-1.5 pr-1.5 pl-2', headerProps?.className)}>
        {handle}
        <span className="min-w-0 flex-1 truncate text-ui font-medium text-ink">{project?.name ?? (ui.project === 'unassigned' ? 'Unassigned' : 'Planner')}</span>
        <Segmented size="sm" aria-label="Pop-out view" value={kind} onValueChange={(v) => onKind(v as PopKind)} items={KIND_ITEMS.map((k) => (k.value === 'inbox' && pending ? { ...k, label: `Inbox ${pending}` } : k))} />
        <span className="flex shrink-0 items-center">{controls}</span>
      </div>
      <NoticeBar className="mx-2 mb-1" />
      <div className={cn('flex min-h-0 flex-1 flex-col', kind !== 'map' && 'overflow-y-auto overscroll-contain', kind === 'inbox' && 'px-2 pb-2')}>
        {kind === 'tree' && <TreeView menus={!inWindow} />}
        {kind === 'board' && <BoardView menus={!inWindow} />}
        {kind === 'map' && <MapView />}
        {kind === 'inbox' && <InboxList />}
      </div>
    </div>
  );
}

/**
 * The popped-out view (ADR 0005 §10, as the owner reworked it): a floating panel over the page,
 * which folds into a tab (`folded`); or, when the owner moved it there, a separate
 * Picture-in-Picture window, rendered from the main app through a portal. It reads the planner
 * context it is given: the Planner's view state for an explicit pop-out, so selection and filters
 * carry and closing keeps them, or the Task's panel's own. `onClose` is absent for the panel a
 * Task shows on its own, which only folds; `onWindow` where the browser has no windows.
 * Memoised: the app renders on every streamed delta, and the views under it need none of those.
 */
export const PopOutHost = memo(function PopOutHost({ win, kind, onKind, folded, onFold, onClose, onWindow }: Readonly<{
  win: Window | null;
  kind: PopKind;
  onKind: (k: PopKind) => void;
  folded: boolean;
  onFold: (folded: boolean) => void;
  onClose?: () => void;
  onWindow?: () => void;
}>) {
  // The window's own close (its ×, "back to tab") ends the pop-out too.
  useEffect(() => {
    if (!win || !onClose) return;
    const gone = () => onClose();
    win.addEventListener('pagehide', gone);
    return () => win.removeEventListener('pagehide', gone);
  }, [win, onClose]);
  // Hide and Show remove the control they are pressed on: focus on it moves to what takes its place.
  const refocusRef = useRef(false);
  if (win) {
    const close = (
      <Button size="icon" aria-label="Close the pop-out" className="text-muted" onClick={() => win.close()}>
        <X />
      </Button>
    );
    return createPortal(<PopContent kind={kind} onKind={onKind} controls={close} inWindow />, win.document.body);
  }
  const fold = (to: boolean, e: MouseEvent<HTMLElement>) => {
    refocusRef.current = e.currentTarget === e.currentTarget.ownerDocument.activeElement;
    onFold(to);
  };
  if (folded) return <PopTab onOpen={(e) => fold(false, e)} refocusRef={refocusRef} />;
  return <FloatingPanel kind={kind} onKind={onKind} onHide={(e) => fold(true, e)} onClose={onClose} onWindow={onWindow} refocusRef={refocusRef} />;
});

/** Focuses `el` once on mount when `refocusRef` asks (the control it replaced had focus). */
function useRefocus(refocusRef: RefObject<boolean>, el: RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!refocusRef.current) return;
    refocusRef.current = false;
    el.current?.focus();
  }, [refocusRef, el]);
}

/**
 * The folded pop-out: a tab on the right edge, halfway down, with the Board's pending requests.
 * There it clears a Task's header controls and its composer at every width.
 */
function PopTab({ onOpen, refocusRef }: Readonly<{ onOpen: (e: MouseEvent<HTMLElement>) => void; refocusRef: RefObject<boolean> }>) {
  const { ui, boards } = usePlanner();
  const pending = ui.project ? (boards[ui.project]?.data?.requests.length ?? 0) : 0;
  const button = useRef<HTMLButtonElement>(null);
  useRefocus(refocusRef, button);
  return (
    <Tip label="Show the planner" side="left">
      <button
        ref={button}
        type="button"
        aria-label={pending ? `Show the planner, ${pending} pending` : 'Show the planner'}
        className="fixed top-1/2 right-[env(safe-area-inset-right)] z-30 flex h-10 -translate-y-1/2 items-center gap-1.5 rounded-l-md bg-raised pr-2 pl-2.5 text-muted shadow-float transition-colors duration-100 animate-fade-in hover:text-ink pointer-coarse:h-11"
        onClick={onOpen}
      >
        <KanbanSquare aria-hidden="true" className="size-4" />
        {pending > 0 && <span className="rounded-xs bg-attention-wash px-1 text-caption tabular-nums text-attention">{pending}</span>}
      </button>
    </Tip>
  );
}

const MARGIN = 8;
const BOX_KEY = 'uam.plannerBox';

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** The last box the owner left the panel in (not on a phone, where it spans the width). */
function readBox(): Box | null {
  try {
    const b = JSON.parse(localStorage.getItem(BOX_KEY) ?? 'null') as Box | null;
    return b && [b.x, b.y, b.w, b.h].every(Number.isFinite) ? b : null;
  } catch {
    return null;
  }
}

/**
 * The floating panel: above the page (z-30, under dialogs, menus, selects and tooltips), dragged
 * by its header (touch too) and resized from its corner, both also by arrow keys on their
 * handles. Maximise fills the viewport less the margin and Restore returns to the box; a
 * double-click on the header toggles it. Position and size are custom properties written to the
 * CSSOM (a transform and two lengths), so nothing re-renders per pointer move.
 */
function FloatingPanel({ kind, onKind, onHide, onClose, onWindow, refocusRef }: Readonly<{
  kind: PopKind;
  onKind: (k: PopKind) => void;
  onHide: (e: MouseEvent<HTMLElement>) => void;
  onClose?: () => void;
  onWindow?: () => void;
  refocusRef: RefObject<boolean>;
}>) {
  const panel = useRef<HTMLElement>(null);
  const grip = useRef<HTMLButtonElement>(null);
  useRefocus(refocusRef, grip);
  const box = useRef<Box>({ x: 0, y: 0, w: 520, h: 640 });
  const phone = useRef(false);
  const [max, setMax] = useState(false);
  const maxed = useRef(false);
  const drag = useRef<{ mode: 'move' | 'size'; px: number; py: number; x: number; y: number; w: number; h: number } | null>(null);

  const apply = useCallback(() => {
    const el = panel.current;
    if (!el) return;
    const vw = window.innerWidth, vh = window.innerHeight;
    const b = box.current;
    b.w = Math.min(Math.max(280, b.w), vw - MARGIN * 2);
    b.h = Math.min(Math.max(220, b.h), vh - MARGIN * 2);
    b.x = Math.min(Math.max(MARGIN, b.x), vw - b.w - MARGIN);
    b.y = Math.min(Math.max(MARGIN, b.y), vh - b.h - MARGIN);
    const shown = maxed.current ? { x: MARGIN, y: MARGIN, w: vw - MARGIN * 2, h: vh - MARGIN * 2 } : b;
    el.style.setProperty('--fp-x', `${shown.x}px`);
    el.style.setProperty('--fp-y', `${shown.y}px`);
    el.style.setProperty('--fp-w', `${shown.w}px`);
    el.style.setProperty('--fp-h', `${shown.h}px`);
  }, []);
  const save = () => {
    if (phone.current) return;
    try {
      localStorage.setItem(BOX_KEY, JSON.stringify(box.current));
    } catch {
      // Storage full or off: the box lasts this visit.
    }
  };
  useLayoutEffect(() => {
    phone.current = window.matchMedia(PHONE).matches;
    const vw = window.innerWidth, vh = window.innerHeight;
    box.current = phone.current
      ? { x: MARGIN, y: Math.round(vh * 0.38), w: vw - MARGIN * 2, h: Math.round(vh * 0.6) }
      : // Below a Task's header, and short of the composer's row on a short screen.
        (readBox() ?? { x: vw - 520 - 16, y: 64, w: 520, h: Math.min(640, vh - 64 - 176) });
    apply();
    window.addEventListener('resize', apply);
    return () => window.removeEventListener('resize', apply);
  }, [apply]);
  useLayoutEffect(() => {
    maxed.current = max;
    apply();
  }, [max, apply]);

  const start = (mode: 'move' | 'size', e: PointerEvent<HTMLElement>) => {
    // The header's controls are not handles; the grip is.
    if (e.button !== 0 || maxed.current || (mode === 'move' && (e.target as HTMLElement).closest('button:not([data-grip]), [role="radio"]'))) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { mode, px: e.clientX, py: e.clientY, ...box.current };
  };
  const onMove = (e: PointerEvent<HTMLElement>) => {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.px, dy = e.clientY - d.py;
    if (d.mode === 'move') Object.assign(box.current, { x: d.x + dx, y: d.y + dy });
    else Object.assign(box.current, { w: d.w + dx, h: d.h + dy });
    apply();
  };
  const end = () => {
    if (drag.current) save();
    drag.current = null;
  };
  const keys = (mode: 'move' | 'size', e: KeyboardEvent<HTMLElement>) => {
    const step = e.shiftKey ? 64 : 16;
    const delta = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (!delta || maxed.current) return;
    e.preventDefault();
    const b = box.current;
    if (mode === 'move') Object.assign(b, { x: b.x + delta[0], y: b.y + delta[1] });
    else Object.assign(b, { w: b.w + delta[0], h: b.h + delta[1] });
    apply();
    save();
  };

  const controls = (
    <>
      {onWindow && (
        <Tip label="Open in a separate window">
          <Button size="icon" aria-label="Open in a separate window" className="text-muted" onClick={onWindow}>
            <PictureInPicture2 />
          </Button>
        </Tip>
      )}
      <Tip label={max ? 'Restore' : 'Maximise'}>
        <Button size="icon" aria-label={max ? 'Restore the pop-out' : 'Maximise the pop-out'} className="text-muted" onClick={() => setMax(!max)}>
          {max ? <Minimize2 /> : <Maximize2 />}
        </Button>
      </Tip>
      <Tip label="Hide">
        <Button size="icon" aria-label="Hide the pop-out" className="text-muted" onClick={onHide}>
          <Minus />
        </Button>
      </Tip>
      {onClose && (
        <Tip label="Close">
          <Button size="icon" aria-label="Close the pop-out" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      )}
    </>
  );

  return (
    <section
      ref={panel}
      aria-label="Planner pop-out"
      className="fixed top-0 left-0 z-30 flex h-(--fp-h) w-(--fp-w) translate-x-(--fp-x) translate-y-(--fp-y) flex-col overflow-hidden rounded-lg bg-canvas shadow-float animate-fade-in"
    >
      {/* The header drags the panel (not while maximised) and a double-click on it maximises; the grip is its keyboard handle. */}
      <PopContent
        kind={kind}
        onKind={onKind}
        controls={controls}
        headerProps={{
          className: cn('touch-none select-none', !max && 'cursor-move'),
          onPointerDown: (e) => start('move', e),
          onPointerMove: (e) => onMove(e),
          onPointerUp: () => end(),
          onPointerCancel: () => end(),
          onDoubleClick: (e) => {
            if (!(e.target as HTMLElement).closest('button, [role="radio"]')) setMax(!max);
          },
        }}
        handle={
          !max && (
            <button ref={grip} type="button" data-grip="" aria-label="Move the pop-out (arrow keys)" className="flex size-6 shrink-0 cursor-move items-center justify-center rounded-xs text-faint hover:text-body" onKeyDown={(e) => keys('move', e)}>
              <GripHorizontal aria-hidden="true" className="size-4" />
            </button>
          )
        }
      />
      {!max && (
        <button
          type="button"
          aria-label="Resize the pop-out (arrow keys)"
          className="absolute right-0 bottom-0 z-10 size-4 cursor-nwse-resize touch-none rounded-br-lg bg-[linear-gradient(135deg,transparent_50%,var(--color-hairline-strong)_50%)] opacity-70 hover:opacity-100"
          onPointerDown={(e) => start('size', e)}
          onPointerMove={(e) => onMove(e)}
          onPointerUp={() => end()}
          onPointerCancel={() => end()}
          onKeyDown={(e) => keys('size', e)}
        />
      )}
    </section>
  );
}
