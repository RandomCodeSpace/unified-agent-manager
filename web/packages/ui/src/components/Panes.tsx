import { Suspense, lazy, useEffect, useEffectEvent, useState, type Ref } from 'react';
import { SquareTerminal } from 'lucide-react';
import type { Project } from '../api';
import { BAR_BOX, Loading, Spinner, TranscriptSkeleton, VIEW_BAR } from './common';
import { cn } from '../lib/cn';
import { useResizable } from '../lib/useResizable';
import { Appear } from './ui/appear';
import { EXIT_MS } from './ui/collapse';
import type { TerminalHandle } from './Terminal';

/** The terminal's content brings xterm.js, so it loads with the first terminal opened. */
const TerminalPanel = lazy(() => import('./Terminal'));

/** The header of the non-Task views while the sidebar is a drawer (narrow): its toggle, the brand, the connection. */
/** A lost connection is the main pane's banner right above this header, so the header does not repeat it. */
export function PaneHeader({ leading }: Readonly<{ leading: React.ReactNode }>) {
  return (
    <header className={VIEW_BAR}>
      <div className={BAR_BOX}>
        {leading}
        <span className="text-title font-semibold text-ink">UAM</span>
      </div>
    </header>
  );
}

export function EmptyPane({ leading, children }: Readonly<{ leading: React.ReactNode; children: React.ReactNode }>) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {leading && <PaneHeader leading={leading} />}
      <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 pb-16 text-center animate-rise">{children}</div>
    </div>
  );
}

/**
 * While the stream (re)opens past the quiet period: a `canvas` veil over the pane below its header
 * that takes the pointer, with a spinner at its centre. It fades both ways (Appear, without its scale); the header stays usable.
 */
/**
 * Settings → Terminal's shell, docked under the Task view as part of the layout (DESIGN.md Terminal):
 * the Task view gives it the height, nothing is covered. Its top edge drags that height. It stays
 * across Task switches, hides (its shell running) while another view fills the pane, and ends with Close, which ends the shell.
 * Its content slides up over the band once mounted and back down when `open` turns false, then `onClosed` lets the owner unmount it; the edge stays put.
 */
export function TerminalDock({ project, thread, open, hidden, panel, onClose, onClosed }: Readonly<{ project: Project; thread?: { id: string; dir: string }; open: boolean; hidden: boolean; panel: Ref<TerminalHandle>; onClose: (to: Project | null) => void; onClosed: () => void }>) {
  const { panelRef, handleProps } = useResizable('terminal-h', 320, 160, 'y');
  // The slide starts one frame after mount, so the first paint is below the band; hiding for another view never replays it.
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const frame = requestAnimationFrame(() => setShown(true));
    return () => cancelAnimationFrame(frame);
  }, []);
  const closed = useEffectEvent(onClosed);
  useEffect(() => {
    if (open) return;
    const timer = window.setTimeout(closed, EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [open]);
  // A dock that first renders closed lost its exit (the sign-in screen replaced it): it only reports closed and starts no shell.
  const [opened] = useState(open);
  const dir = thread?.dir ?? project.dir;
  if (!opened) return null;
  return (
    <section ref={panelRef} aria-label="Terminal" className={cn('relative flex h-[var(--panel-h,320px)] shrink-0 flex-col bg-canvas', hidden && 'hidden')}>
      <div {...handleProps} className="group/handle absolute inset-x-0 -top-1 z-10 flex h-2 cursor-row-resize items-center outline-hidden focus-visible:outline-2 focus-visible:outline-focus" title="Drag to resize · double-click to reset">
        <div className="fade-rule w-full transition-[background-color,scale] duration-100 group-hover/handle:scale-y-200 group-hover/handle:bg-accent group-hover/handle:bg-none group-focus-visible/handle:scale-y-200 group-focus-visible/handle:bg-accent group-focus-visible/handle:bg-none group-active/handle:bg-accent group-active/handle:bg-none" />
      </div>
      {/* Clips the slide to the band, so the page never gains a scrollable overflow below it. */}
      <div className="flex min-h-0 flex-1 flex-col overflow-clip">
        <div className={cn('flex min-h-0 flex-1 flex-col transition-transform duration-240 ease-app', shown && open ? 'translate-y-0' : 'translate-y-full')} inert={!open}>
          {/* Until the chunk lands, the panel's header as it first reads. */}
          <Suspense
            fallback={<>
              <div className="flex h-9 shrink-0 items-center gap-1.5 pr-1.5 pl-3">
                <SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted" />
                <span className="text-title text-ink">Terminal</span>
                <span className="min-w-0 flex-1 truncate text-meta text-muted" title={dir}>{dir}</span>
                <output className="shrink-0 text-meta text-muted">Connecting…</output>
              </div>
              <p aria-hidden="true" className="flex min-h-0 flex-1 items-center justify-center text-caption text-muted">Starting a shell…</p>
            </>}
          >
            <TerminalPanel ref={panel} project={project} thread={thread} onClose={onClose} />
          </Suspense>
        </div>
      </div>
    </section>
  );
}

export function LoadingVeil({ show }: Readonly<{ show: boolean }>) {
  return (
    <Appear show={show} className="absolute inset-x-0 top-header bottom-0 z-20 scale-100 items-center justify-center bg-canvas/70">
      <output className="flex items-center gap-2 rounded-full bg-canvas px-3 py-1.5 text-caption text-muted">
        <Spinner />
        Loading…
      </output>
    </Appear>
  );
}

/** While a view's code loads for its first open (Settings, Routines): the header keeps its height and its leading control over the quiet loading word. */
export function ViewLoading({ leading }: Readonly<{ leading: React.ReactNode }>) {
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-busy="true">
      <header className={VIEW_BAR}><div className={BAR_BOX}>{leading}</div></header>
      <Loading className="px-6" />
    </div>
  );
}

/** While the selected Task's detail (or the first snapshot) is on its way and nothing was on screen before: the header holds its height over a transcript-shaped skeleton. */
export function LoadingPane({ leading }: Readonly<{ leading: React.ReactNode }>) {
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-busy="true" aria-label="Loading conversation">
      <header className={VIEW_BAR}><div className={BAR_BOX}>{leading}</div></header>
      <TranscriptSkeleton />
    </div>
  );
}
