import { GripHorizontal, X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useRef, type HTMLAttributes, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { Segmented } from '../ui/segmented';
import { BoardView } from './BoardView';
import { usePlanner, type PopKind } from './context';
import { MapView } from './MapView';
import { NoticeBar } from './parts';
import { InboxList } from './Requests';
import { TreeView } from './TreeView';

/** The Document Picture-in-Picture API (Chromium); absent elsewhere, where the floating panel stands in. */
interface DocumentPictureInPicture {
  requestWindow: (options?: { width?: number; height?: number }) => Promise<Window>;
}
declare global {
  interface Window {
    documentPictureInPicture?: DocumentPictureInPicture;
  }
}

/** A phone: the in-page floating panel is the only pop-out there. */
const PHONE = '(max-width: 480px)';

export type PopMode = 'pip' | 'float';

/** Which pop-out this browser gets: a Picture-in-Picture window where the API exists (not on a phone), else the floating panel. */
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
  const pip = await win.documentPictureInPicture!.requestWindow({ width: 440, height: 600 });
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
 * What a pop-out shows: its own choice of view over the shared view state, a compact header and a
 * close, and the planner's notice. In a separate window (`inWindow`) the views offer no menus: they
 * would open in the page's document, not the window's.
 */
function PopContent({ kind, onKind, onClose, handle, headerProps, inWindow = false }: Readonly<{ kind: PopKind; onKind: (k: PopKind) => void; onClose: () => void; handle?: ReactNode; headerProps?: HTMLAttributes<HTMLDivElement>; inWindow?: boolean }>) {
  const { ui, projects, boards } = usePlanner();
  const project = projects.find((p) => p.id === ui.project);
  const pending = ui.project ? (boards[ui.project]?.data?.requests.length ?? 0) : 0;
  return (
    <div className="flex h-full min-h-0 flex-col bg-canvas text-body">
      <div {...headerProps} className={cn('flex h-11 shrink-0 items-center gap-1.5 pr-1.5 pl-2', headerProps?.className)}>
        {handle}
        <span className="min-w-0 flex-1 truncate text-ui font-medium text-ink">{project?.name ?? (ui.project === 'unassigned' ? 'Unassigned' : 'Planner')}</span>
        <Segmented size="sm" aria-label="Pop-out view" value={kind} onValueChange={(v) => onKind(v as PopKind)} items={KIND_ITEMS.map((k) => (k.value === 'inbox' && pending ? { ...k, label: `Inbox ${pending}` } : k))} />
        <Button size="icon" aria-label="Close the pop-out" className="text-muted" onClick={onClose}>
          <X />
        </Button>
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
 * The popped-out view (ADR 0005 §10): rendered from the main app through a portal, into the
 * Picture-in-Picture window when there is one, else into a floating panel on the page. Both
 * read the same view state, so selection and filters carry, and closing either keeps them.
 */
export function PopOutHost({ win, kind, onKind, onClose }: Readonly<{ win: Window | null; kind: PopKind; onKind: (k: PopKind) => void; onClose: () => void }>) {
  // The window's own close (its ×, "back to tab") ends the pop-out too.
  useEffect(() => {
    if (!win) return;
    const gone = () => onClose();
    win.addEventListener('pagehide', gone);
    return () => win.removeEventListener('pagehide', gone);
  }, [win, onClose]);
  if (win) return createPortal(<PopContent kind={kind} onKind={onKind} onClose={() => win.close()} inWindow />, win.document.body);
  return <FloatingPanel kind={kind} onKind={onKind} onClose={onClose} />;
}

const MARGIN = 8;

/**
 * The in-page stand-in for Picture-in-Picture: a floating surface dragged by its header and
 * resized from its corner. Position and size are custom properties written to the CSSOM during
 * the drag (a transform and two lengths), so nothing re-renders per pointer move.
 */
function FloatingPanel({ kind, onKind, onClose }: Readonly<{ kind: PopKind; onKind: (k: PopKind) => void; onClose: () => void }>) {
  const panel = useRef<HTMLDivElement>(null);
  const box = useRef({ x: 0, y: 0, w: 420, h: 540 });
  const drag = useRef<{ mode: 'move' | 'size'; px: number; py: number; x: number; y: number; w: number; h: number } | null>(null);

  const apply = useCallback(() => {
    const el = panel.current;
    if (!el) return;
    const b = box.current;
    const vw = window.innerWidth, vh = window.innerHeight;
    b.w = Math.min(Math.max(280, b.w), vw - MARGIN * 2);
    b.h = Math.min(Math.max(220, b.h), vh - MARGIN * 2);
    b.x = Math.min(Math.max(MARGIN, b.x), vw - b.w - MARGIN);
    b.y = Math.min(Math.max(MARGIN, b.y), vh - b.h - MARGIN);
    el.style.setProperty('--fp-x', `${b.x}px`);
    el.style.setProperty('--fp-y', `${b.y}px`);
    el.style.setProperty('--fp-w', `${b.w}px`);
    el.style.setProperty('--fp-h', `${b.h}px`);
  }, []);
  useLayoutEffect(() => {
    const phone = window.matchMedia(PHONE).matches;
    box.current = phone
      ? { x: MARGIN, y: Math.round(window.innerHeight * 0.38), w: window.innerWidth - MARGIN * 2, h: Math.round(window.innerHeight * 0.6) }
      : { x: window.innerWidth - 440 - 16, y: 64, w: 420, h: Math.min(560, window.innerHeight - 96) };
    apply();
    window.addEventListener('resize', apply);
    return () => window.removeEventListener('resize', apply);
  }, [apply]);

  const start = (mode: 'move' | 'size', e: PointerEvent<HTMLElement>) => {
    if (e.button !== 0 || (mode === 'move' && (e.target as HTMLElement).closest('button, [role="radio"]'))) return;
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
    drag.current = null;
  };
  const keys = (mode: 'move' | 'size', e: KeyboardEvent<HTMLElement>) => {
    const step = e.shiftKey ? 64 : 16;
    const delta = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (!delta) return;
    e.preventDefault();
    const b = box.current;
    if (mode === 'move') Object.assign(b, { x: b.x + delta[0], y: b.y + delta[1] });
    else Object.assign(b, { w: b.w + delta[0], h: b.h + delta[1] });
    apply();
  };

  return (
    <div
      ref={panel}
      role="dialog"
      aria-label="Planner pop-out"
      className="fixed top-0 left-0 z-30 flex h-(--fp-h) w-(--fp-w) translate-x-(--fp-x) translate-y-(--fp-y) flex-col overflow-hidden rounded-lg bg-canvas shadow-float animate-fade-in"
    >
      {/* The header drags the panel; the grip is its keyboard handle (arrow keys move it). */}
      <PopContent
        kind={kind}
        onKind={onKind}
        onClose={onClose}
        headerProps={{ className: 'cursor-move touch-none select-none', onPointerDown: (e) => start('move', e), onPointerMove: (e) => onMove(e), onPointerUp: () => end(), onPointerCancel: () => end() }}
        handle={
          <button type="button" aria-label="Move the pop-out (arrow keys)" className="flex size-6 shrink-0 cursor-move items-center justify-center rounded-xs text-faint hover:text-body" onKeyDown={(e) => keys('move', e)}>
            <GripHorizontal aria-hidden="true" className="size-4" />
          </button>
        }
      />
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
    </div>
  );
}
