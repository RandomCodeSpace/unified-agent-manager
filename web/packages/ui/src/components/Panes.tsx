import { Suspense, lazy, startTransition, useCallback, useEffect, useEffectEvent, useRef, useState, type Ref } from 'react';
import { SquareTerminal } from 'lucide-react';
import type { ApiClient, Project, SessionDetail } from '../api';
import type { State } from '../state';
import { BAR_BOX, Loading, Spinner, TranscriptSkeleton, VIEW_BAR, useLate } from './common';
import { cn } from '../lib/cn';
import { useResizable } from '../lib/useResizable';
import { Appear } from './ui/appear';
import { EXIT_MS, usePresence } from './ui/collapse';
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

/** A wait shorter than this shows nothing new: no loading veil or placeholder, no connection banner. */
const QUIET_MS = 600;

/**
 * The main pane while a Thread is on its way: the veil for a load that lasts (`loading`) and the banner for a lost
 * connection that lasts (`connection`); the Thread whose first render, a transition, is done (`mountedId`, and
 * `detailId` once its detail is the one on screen); the Thread the pane holds until then (`held`), and whether
 * the wait has lasted (`lateLoad`).
 */
export function usePaneLoad({ state, settingsOpen, routinesFor }: { state: State; settingsOpen: boolean; routinesFor: string | null }) {
  // Opening a Task reopens the stream: a load that lasts veils the pane with a spinner; only a disconnect that lasts is shown as one.
  const late = useLate(state.connection !== 'connected', QUIET_MS);
  const loading = late && state.connection === 'connecting';
  const connection = late && !loading ? state.connection : 'connected';
  // A Thread's view first renders in a transition, which yields between rows: a long transcript is not one long task.
  // Until then the pane shows what it showed while the detail was on its way; later frames of the Thread render at once.
  const [mountedId, setMountedId] = useState<string | null>(null);
  const arrivedId = state.detail?.id ?? null;
  // Going back to the same Thread (from Home, or after its detail was dropped) renders it the same way. A cached
  // revisit is on screen before its detail arrives: the detail then replaces it in place, not in a second mount.
  const cachedId = state.previousCached && state.previous?.id === state.selectedId ? state.selectedId : null;
  if (!arrivedId && mountedId !== cachedId) setMountedId(cachedId);
  useEffect(() => {
    if (arrivedId && arrivedId !== mountedId) startTransition(() => setMountedId(arrivedId));
  }, [arrivedId, mountedId]);
  // The snapshot and the detail drop the Thread select() froze as they land; the pane keeps it until the new Thread has rendered.
  // Keyed to the selection, so one made from Home or a draft holds nothing; let go once the Thread is on screen.
  const [held, setHeld] = useState<{ id: string | null; detail: SessionDetail | null }>({ id: null, detail: null });
  if (held.id !== state.selectedId || (state.previous && state.previous !== held.detail)) setHeld({ id: state.selectedId, detail: state.previous });
  else if (held.detail && arrivedId && arrivedId === mountedId) setHeld({ id: held.id, detail: null });

  const lateLoad = useLate(!!state.selectedId && (!state.detail || state.detail.id !== mountedId) && !settingsOpen && !routinesFor, QUIET_MS);

  const detailId = arrivedId === mountedId ? arrivedId : undefined;
  return { loading, connection, mountedId, detailId, held, lateLoad };
}

/**
 * The terminal docked under the Thread view: which Project's (or worktree Thread's) shell it shows, the header
 * button that opens, moves or closes it, and the dock kept mounted while it slides out.
 */
export function useTerminalDock({ api, state, onTerminal }: { api: ApiClient; state: State; /** Tells the federation whether a terminal is open here. */ onTerminal: ((open: boolean) => void) | undefined }) {
  // The terminal docked under the Task view (TerminalDock): the Project its shell started in, or null.
  const [terminalId, setTerminalId] = useState<string | null>(null);
  // The worktree Thread whose folder the shell started in instead of the Project's, or null.
  const [terminalTask, setTerminalTask] = useState<string | null>(null);
  const nextTerminalTask = useRef<string | null>(null);
  // Each opening is a dock of its own: one reopened while the last slides out starts a new shell.
  const [terminalOpens, setTerminalOpens] = useState(0);
  useEffect(() => { onTerminal?.(!!terminalId); }, [terminalId, onTerminal]);
  // The header's Terminal opens a closed terminal; an open one closes, or moves to the pressed Task's Project, through the panel, which confirms while its shell is connected.
  const terminalPanel = useRef<TerminalHandle>(null);
  const toggleTerminal = useCallback((projectId: string) => {
    // A Thread in a worktree opens the shell in its worktree.
    const pressed = state.sessions.find((s) => s.id === state.selectedId);
    const task = pressed?.project_id === projectId && pressed.worktree ? pressed.id : null;
    const to = terminalId === projectId && terminalTask === task ? null : state.projects.find((p) => p.id === projectId);
    nextTerminalTask.current = task;
    // A panel still loading has no shell to confirm for.
    if (terminalId && terminalPanel.current && to !== undefined) terminalPanel.current.end(to);
    else {
      if (!terminalId) setTerminalOpens((n) => n + 1);
      setTerminalId(to === null ? null : projectId);
      setTerminalTask(to === null ? null : task);
    }
  }, [terminalId, terminalTask, state.projects, state.sessions, state.selectedId]);
  const closeTerminal = useCallback((to: Project | null) => {
    setTerminalId(to?.id ?? null);
    setTerminalTask(to ? nextTerminalTask.current : null);
    if (!to) document.getElementById('terminal-link')?.focus();
  }, []);
  // Turning Settings → Terminal off ends every shell on the service, and a removed Project takes its shell: the dock leaves.
  const terminalProject = terminalId && state.settings.terminal && api.supports('terminal-v1') ? state.projects.find((p) => p.id === terminalId) : undefined;
  // The dock slides out showing the shell it ended, then unmounts (DESIGN.md Terminal).
  const terminalDock = usePresence(!!terminalProject);
  const [docked, setDocked] = useState<{ project: Project; task: string | null }>();
  if (terminalProject && (terminalProject !== docked?.project || terminalTask !== docked.task)) setDocked({ project: terminalProject, task: terminalTask });
  return { terminalId, setTerminalId, terminalProject, terminalOpens, docked, terminalDock, terminalPanel, toggleTerminal, closeTerminal };
}
