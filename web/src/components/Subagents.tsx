import { flushSync } from 'react-dom';
import { HistoryAnchor } from './HistoryAnchor';
import { windowInteractions } from '../lib/transcript';
import { BodyNotice, DetailVisibility, useDetailAgent, useItemBody } from './Details';
import { ArrowLeft, Bot, Copy, Crosshair, Ellipsis, Square, X } from 'lucide-react';
import { useEffect, useEffectEvent, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, api, describeError, isStatus, modelName, newRequestId, readOnly, type Interaction, type Item, type Meta, type SessionDetail, type Subagent, type SubagentStatus, type Submission } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { useDensity } from '../lib/density';
import { useResizable } from '../lib/useResizable';
import type { AgentTranscript } from '../state';
import { useFileHintItems } from './FileReferences';
import { Markdown, Note, Skeleton, useApp } from './common';
import { AgentChip, AgentItems, duration } from './Transcript';
import { Button } from './ui/button';
import { EXIT_MS } from './ui/collapse';
import { AlertDialog, Sheet, useConfirm } from './ui/dialog';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** "<model> · <effort>" from whatever the provider reported; empty when it reported neither. */
function setupOf(meta: Meta | null, provider: string, s: Subagent): string {
  return [s.model ? modelName(meta, provider, s.model) : '', s.effort ?? ''].filter(Boolean).join(' · ');
}

export type PanelView = { view: 'list' } | { view: 'agent'; id: string };

const GROUPS: { status: SubagentStatus; label: string }[] = [
  { status: 'running', label: 'Running' },
  { status: 'idle', label: 'Idle' },
  { status: 'failed', label: 'Failed' },
  { status: 'completed', label: 'Completed' },
  { status: 'cancelled', label: 'Cancelled' },
];

/** A stop request for one subagent; the provider's SSE still owns its terminal status. */
interface StopState {
  busy: boolean;
  requested: boolean;
  error: string | null;
}
const NOT_STOPPING: StopState = { busy: false, requested: false, error: null };

/** One record of stop requests per panel, so the list row and the transcript header agree. */
function useStops(sessionId: string): [Record<string, StopState>, (agentId: string) => void] {
  const [stops, setStops] = useState<Record<string, StopState>>({});
  const set = (agentId: string, v: StopState) => setStops((all) => ({ ...all, [agentId]: v }));
  async function stop(agentId: string) {
    const current = stops[agentId];
    if (current?.busy || current?.requested) return;
    set(agentId, { busy: true, requested: false, error: null });
    try {
      await api.cancelSubagent(sessionId, agentId);
      set(agentId, { busy: false, requested: true, error: null });
    } catch (e) {
      set(agentId, { busy: false, requested: false, error: describeError(e) });
    }
  }
  return [stops, (agentId) => void stop(agentId)];
}

const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

/** Distance from the bottom, in px, under which the view counts as "at the bottom". */
const BOTTOM_SLACK = 32;
const NO_ITEMS: Item[] = [];

/**
 * A side panel: inline beside the column at ≥1280px with a draggable inner edge (width
 * remembered per panel), an overlay sheet from the right at 960–1279, a full-screen sheet
 * below. The overlay is a Base UI dialog (focus trap, Esc, backdrop) that slides in and
 * out. Inline, the column commits its width at once and the panel slides over the space it
 * left; closing slides it out, then `onClosed` lets the owner unmount it.
 */
export function SidePanel({ id, inline, open, onClose, onClosed, label, children, className, defaultWidth = 440 }: { id: string; inline: boolean; open: boolean; onClose: () => void; onClosed: () => void; label: string; children: ReactNode; className?: string; defaultWidth?: number }) {
  const { narrow } = useApp();
  const { panelRef, handleProps } = useResizable(id, defaultWidth);
  // The slide starts one frame after mount, so the first paint is off-screen.
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!inline) return;
    const frame = requestAnimationFrame(() => setShown(open));
    return () => cancelAnimationFrame(frame);
  }, [inline, open]);
  const closed = useEffectEvent(onClosed);
  useEffect(() => {
    if (!inline || open) return;
    const timer = window.setTimeout(closed, EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [inline, open]);
  if (!inline) {
    return (
      <Sheet open={open} onOpenChange={(o) => !o && onClose()} onClosed={onClosed} side="right" label={label} className={cn('w-full max-w-none bg-canvas', !narrow && 'w-[min(var(--spacing-panel),100vw)]', className)}>
        {children}
      </Sheet>
    );
  }
  return (
    <aside ref={panelRef} aria-label={label} className={cn('relative flex w-(--panel-w) shrink-0 flex-col bg-canvas', className)}>
      <div className={cn('flex min-h-0 flex-1 flex-col transition-transform duration-240 ease-app', shown && open ? 'translate-x-0' : 'translate-x-full')} inert={!open}>
        <div
          {...handleProps}
          className="group/handle absolute inset-y-0 -left-1 z-10 flex w-2 cursor-col-resize items-center justify-center outline-hidden focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-focus"
          title="Drag to resize · double-click to reset"
        >
          <span aria-hidden="true" className="fade-rule-y h-full transition-[background-color,width] duration-100 group-hover/handle:w-0.5 group-hover/handle:bg-accent group-focus-visible/handle:w-0.5 group-focus-visible/handle:bg-accent group-active/handle:bg-accent" />
        </div>
        {children}
      </div>
    </aside>
  );
}

export function PanelHeader({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3', className)}>{children}</div>;
}

/**
 * The subagent panel beside (or over) the conversation: a grouped list of the task's
 * subagents, or one subagent's transcript. Subagent output never appears in the main
 * column, so the two streams cannot be confused.
 */
export function SubagentPanel({
  session,
  agents,
  snapshotSeq,
  view,
  inline,
  open,
  onView,
  onClose,
  onClosed,
  onLocate,
}: {
  session: SessionDetail;
  agents: Record<string, AgentTranscript>;
  snapshotSeq: number;
  view: PanelView;
  inline: boolean;
  /** False while the panel leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  onView: (v: PanelView) => void;
  onClose: () => void;
  onClosed: () => void;
  /** Scroll the main transcript to the `task` tool row that spawned a subagent. */
  onLocate: (toolCallId: string) => void;
}) {
  const { meta, narrow } = useApp();
  const lead = useRef<HTMLButtonElement>(null);
  const [stops, stopNow] = useStops(session.id);
  const current = view.view === 'agent' ? session.subagents.find((s) => s.id === view.id) : undefined;
  // Stopping a subagent ends its work for good, so it is confirmed first (DESIGN.md Confirmations); one dialog serves the list and the transcript header.
  const stopConfirm = useConfirm<Subagent>();
  const stop = (id: string) => {
    const s = session.subagents.find((x) => x.id === id);
    if (s) stopConfirm.ask(s);
  };
  const stopDialog = (
    <AlertDialog
      {...stopConfirm.props}
      title={`Stop subagent ${stopConfirm.target ? `“${stopConfirm.target.name}”` : ''}?`}
      description="It stops where it is. What it has done so far stays in its transcript; the main agent gets no result from it."
      confirmLabel="Stop subagent"
      onConfirm={() => {
        const id = stopConfirm.target?.id;
        stopConfirm.close();
        if (id) stopNow(id);
      }}
    />
  );

  // Focus the leading control whenever the view changes (open, back, open transcript).
  useEffect(() => {
    lead.current?.focus({ preventScroll: true });
  }, [view]);

  if (view.view === 'agent' && current) {
    const setup = setupOf(meta, session.provider, current);
    return (
      <SidePanel id="subagents" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label={`Subagent ${current.name}`}>
        <PanelHeader>
          <Button ref={lead} size="icon-md" aria-label="Back to the subagent list" className="-ml-1 text-muted" onClick={() => onView({ view: 'list' })}>
            <ArrowLeft />
          </Button>
          <div className="flex min-w-0 flex-1 flex-col">
            <span className="truncate text-title text-ink" title={current.name}>
              {current.name}
            </span>
            {setup && <span className="truncate text-meta text-muted" title={setup}>{setup}</span>}
          </div>
          <AgentChip status={current.status} />
          <StopSubagent session={session} subagent={current} stopping={stops[current.id] ?? NOT_STOPPING} onStop={() => stop(current.id)} />
          <Button size="icon-md" aria-label="Close subagents" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </PanelHeader>
        <DetailVisibility open={open}><AgentTranscriptView
          provider={session.provider}
          key={current.id}
          sessionId={session.id}
          workdir={session.workdir}
          subagent={current}
          interactions={session.interactions}
          transcript={agents[current.id]}
          snapshotSeq={snapshotSeq}
          open={open}
          parent={session.items.find((i) => i.id === current.parent_tool_call_id)}
          result={current.status === 'completed' || current.status === 'idle' ? session.items.find((i) => i.id === current.parent_tool_call_id)?.tool?.output : undefined}
        /></DetailVisibility>
        <SubagentComposer key={`composer-${current.id}`} session={session} subagent={current} />
        {stopDialog}
      </SidePanel>
    );
  }

  const running = session.subagents.filter((s) => s.status === 'running').length;
  return (
    <SidePanel id="subagents" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Subagents">
      <PanelHeader>
        {narrow && (
          <Button ref={lead} size="icon-md" aria-label="Back to the task" className="-ml-1 text-muted" onClick={onClose}>
            <ArrowLeft />
          </Button>
        )}
        <Bot aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Subagents</span>
        <span className="text-caption tabular-nums text-muted">
          {session.subagents.length}
          {running > 0 && ` · ${running} running`}
        </span>
        <span className="flex-1" />
        {!narrow && (
          <Button ref={lead} size="icon-md" aria-label="Close subagents" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        )}
      </PanelHeader>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2">
        {GROUPS.map(({ status, label }) => {
          const rows = session.subagents.filter((s) => s.status === status);
          if (!rows.length) return null;
          return (
            <section key={status} aria-label={label} className="mb-3">
              <div className="flex h-7 items-center gap-2 px-2 text-caption text-muted">
                <span>{label}</span>
                <span className="tabular-nums text-muted">{rows.length}</span>
                <span className="h-px flex-1 bg-hairline" aria-hidden="true" />
              </div>
              <ul className="flex flex-col gap-px">
                {rows.map((s) => (
                  <SubagentRow key={s.id} session={session} subagent={s} setup={setupOf(meta, session.provider, s)} onOpen={() => onView({ view: 'agent', id: s.id })} onLocate={onLocate} stopping={stops[s.id] ?? NOT_STOPPING} onStop={() => stop(s.id)} />
                ))}
              </ul>
            </section>
          );
        })}
      </div>
      {stopDialog}
    </SidePanel>
  );
}

function SubagentRow({
  session,
  subagent: s,
  setup,
  onOpen,
  onLocate,
  stopping,
  onStop,
}: {
  session: SessionDetail;
  subagent: Subagent;
  /** "<model> · <effort>", or empty. */
  setup: string;
  onOpen: () => void;
  onLocate: (toolCallId: string) => void;
  stopping: StopState;
  onStop: () => void;
}) {
  const [, copy] = useCopied();
  const meta: string[] = [];
  if (s.started_at) meta.push(`Started ${clock(s.started_at)}`);
  const took = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  if (took) meta.push(took);
  const canStop = s.status === 'running' && !readOnly(session);

  const items: ActionItem[] = [
    { key: 'open', label: 'Open', icon: <Bot />, onSelect: onOpen },
    ...(s.status === 'running' ? [{ key: 'stop', label: stopping.requested ? 'Stop requested' : 'Stop', icon: <Square />, disabled: !canStop || stopping.busy || stopping.requested, onSelect: onStop }] : []),
    ...(s.parent_tool_call_id ? [{ key: 'locate', label: 'Show where it was spawned', icon: <Crosshair />, onSelect: () => onLocate(s.parent_tool_call_id!) }] : []),
    { key: 'copy', label: 'Copy agent ID', icon: <Copy />, onSelect: () => copy(s.id), separator: true },
  ];

  return (
    <li>
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div className="group/agent relative rounded-sm transition-colors hover:bg-tint-hover" />}>
          <button type="button" className="flex w-full flex-col items-start gap-0.5 rounded-sm py-2 pr-9 pl-3 text-left focus-visible:-outline-offset-2" onClick={onOpen} title={s.description || undefined}>
            <span className="flex w-full items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-ui font-medium text-ink" title={s.name || undefined}>{s.name || 'Subagent'}</span>
              <AgentChip status={s.status} />
            </span>
            {s.description && <span className="line-clamp-2 text-caption text-muted">{s.description}</span>}
            <span className="flex flex-wrap items-center gap-x-2 text-caption text-muted">
              {setup && <span className="text-meta">{setup}</span>}
              {meta.length > 0 && <span className="tabular-nums">{meta.join(' · ')}</span>}
            </span>
            {stopping.error && <span className="text-caption text-error">{stopping.error}</span>}
          </button>
          <span className="absolute top-2 right-1.5 flex items-center">
            <Menu.Root modal={false}>
              <Menu.Trigger render={<Button size="icon" aria-label={`Actions for subagent ${s.name}`} className="text-muted opacity-0 transition-opacity group-hover/agent:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" />}>
                <Ellipsis />
              </Menu.Trigger>
              <Menu.Content align="end">
                <Menu.Actions items={items} />
              </Menu.Content>
            </Menu.Root>
          </span>
        </ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>
    </li>
  );
}

/**
 * One subagent's transcript. Mounting loads it from the subagent route, after which live
 * frames tagged with this agent_id keep it current (frames during the fetch are buffered
 * and replayed, see state.ts). Reloads on every fresh snapshot to close any gap.
 */
function AgentTranscriptView({
  sessionId,
  provider,
  workdir,
  subagent,
  interactions,
  transcript: legacyTranscript,
  snapshotSeq,
  result: legacyResult,
  open,
  parent,
}: {
  sessionId: string;
  provider: string;
  workdir: string;
  subagent: Subagent;
  /** The Task's requests; this subagent's are those with its agent_id. */
  interactions: Interaction[];
  transcript: AgentTranscript | undefined;
  snapshotSeq: number;
  /** The `task` tool's output on the parent item, which is the subagent's result. */
  result?: string;
  open: boolean;
  parent?: Item;
}) {
  const { dispatch } = useApp();
  const density = useDensity();
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const live = subagent.status === 'running';
  const [attempt, setAttempt] = useState(0);
  const detail = useDetailAgent(subagent.id, open);
  const transcript = detail.compact ? detail.agent : legacyTranscript;
  const parentItem: Item = parent ?? { id: subagent.parent_tool_call_id ?? '', kind: 'tool', time: '', compact: { has_text: false, has_reasoning: false } };
  const { item: parentFullItem, body: parentBody, attach: parentAttach, retry: parentRetry } = useItemBody(parentItem, open && !!subagent.parent_tool_call_id && (subagent.status === 'completed' || subagent.status === 'idle'));
  const result = detail.compact ? parentFullItem?.tool?.output : legacyResult;
  const pageRead = useRef<{ token: number; controller: AbortController } | null>(null);
  const pageToken = useRef(0);
  const retryAfter = useRef(0);
  const [windowReset, setWindowReset] = useState(0);
  useEffect(() => () => { pageRead.current?.controller.abort(); pageRead.current = null; }, [sessionId, subagent.id, open, detail.agent?.seq]);

  useEffect(() => {
    if (detail.compact || !open) return;
    let cancelled = false;
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 10000);
    dispatch({ type: 'agent_loading', sessionId, agentId: subagent.id });
    api
      .subagent(sessionId, subagent.id, controller.signal)
      .then((d) => !cancelled && dispatch({ type: 'agent_loaded', sessionId, agentId: subagent.id, ...d }))
      .catch((e: unknown) => !cancelled && dispatch({ type: 'agent_failed', sessionId, agentId: subagent.id, error: describeError(e) }));
    return () => {
      cancelled = true;
      controller.abort();
      window.clearTimeout(timer);
      dispatch({ type: 'agent_unloaded', sessionId, agentId: subagent.id });
    };
  }, [sessionId, subagent.id, snapshotSeq, attempt, dispatch, detail.compact, open]);

  // Follow new output only while the reader is at the bottom.
  const items: Item[] = transcript?.items ?? NO_ITEMS;
  useFileHintItems(subagent.id, items, open);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (atBottom.current && !detail.agent?.after) el.scrollTop = el.scrollHeight;
  }, [items, detail.agent?.after]);

  function nearEdge(el: HTMLElement, direction: 'older' | 'newer') {
    const rect = el.querySelector('[data-history-window]')?.getBoundingClientRect();
    const edge = el.getBoundingClientRect();
    const distance = direction === 'older' ? rect ? edge.top - rect.top : el.scrollTop : rect ? rect.bottom - edge.bottom : el.scrollHeight - el.scrollTop - el.clientHeight;
    return distance < Math.min(1000, Math.max(200, el.clientHeight * 2));
  }

  function loadOlder(direction: 'older' | 'newer' = 'older') {
    const agent = detail.agent, store = detail.store;
    const before = direction === 'older' ? agent?.before : agent?.after;
    if (Date.now() < retryAfter.current || !detail.compact || !open || !agent || !before || agent.loading || agent.page || pageRead.current || !store) return;
    const controller = new AbortController(), token = ++pageToken.current;
    pageRead.current = { token, controller };
    store.action({ type: 'page_start', agentId: subagent.id, before, token, direction });
    const timer = window.setTimeout(() => {
      if (pageRead.current?.token !== token) return;
      retryAfter.current = Date.now() + 1000;
      store.action({ type: 'page_failed', agentId: subagent.id, token, error: 'Loading earlier messages timed out. Scroll up to retry.' });
      controller.abort();
    }, 10000);
    void api.subagentHistory(sessionId, subagent.id, before, controller.signal, direction).then(page => {
      if (controller.signal.aborted || pageRead.current?.token !== token || store.value.agent?.page?.token !== token) return;
      atBottom.current = false;
      store.action({ type: 'page_done', agentId: subagent.id, before, token, page });
    }).catch(error => {
      if (pageRead.current?.token === token && !controller.signal.aborted) { retryAfter.current = Date.now() + 1000; store.action({ type: 'page_failed', agentId: subagent.id, token, error: describeError(error) }); }
    }).finally(() => { window.clearTimeout(timer); if (pageRead.current?.token === token) pageRead.current = null; });
  }
  const loadOnDemand = useEffectEvent(loadOlder);
  useEffect(() => {
    const el = scroller.current;
    if (!el || !open) return;
    let touchY = 0;
    const key = (event: globalThis.KeyboardEvent) => { if (['Home', 'PageUp', 'ArrowUp'].includes(event.key) && nearEdge(el, 'older')) loadOnDemand(); if (['End', 'PageDown', 'ArrowDown'].includes(event.key) && nearEdge(el, 'newer')) loadOnDemand('newer'); };
    const start = (event: TouchEvent) => { touchY = event.touches[0]?.clientY ?? 0; };
    const move = (event: TouchEvent) => { const y = event.touches[0]?.clientY ?? touchY; if (y > touchY && nearEdge(el, 'older')) loadOnDemand(); if (y < touchY && nearEdge(el, 'newer')) loadOnDemand('newer'); touchY = y; };
    el.addEventListener('keydown', key); el.addEventListener('touchstart', start, { passive: true }); el.addEventListener('touchmove', move, { passive: true });
    return () => { el.removeEventListener('keydown', key); el.removeEventListener('touchstart', start); el.removeEventListener('touchmove', move); };
  }, [open]);
  const scrollTop = useRef(0);
  function onScroll() {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = !detail.agent?.after && el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
    if (el.scrollTop < scrollTop.current && nearEdge(el, 'older')) loadOlder();
    else if (el.scrollTop > scrollTop.current && nearEdge(el, 'newer')) loadOlder('newer');
    scrollTop.current = el.scrollTop;
  }

  const visibleInteractions = detail.compact ? windowInteractions(items, interactions, 0, !!detail.agent?.before, !!detail.agent?.after) : interactions;
  function latest() {
    pageRead.current?.controller.abort(); pageRead.current = null;
    flushSync(() => { detail.store?.action({ type: 'latest', agentId: subagent.id }); setWindowReset(value => value + 1); });
    atBottom.current = true;
    if (scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }

  return (
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The transcript scroll region accepts keyboard paging at both boundaries.
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain px-4 py-4 [overflow-wrap:anywhere]" ref={scroller} onScroll={onScroll} onWheel={event => { if (event.deltaY < 0 && nearEdge(event.currentTarget, 'older')) loadOlder(); if (event.deltaY > 0 && nearEdge(event.currentTarget, 'newer')) loadOlder('newer'); }} role="log" tabIndex={0} aria-busy={(!transcript || transcript.loading) && items.length === 0 ? true : undefined}>
      {detail.agent?.page && <Note>Loading {detail.agent.page.direction === 'newer' ? 'newer' : 'earlier'} messages…</Note>}
      {detail.agent?.after && <Button className="sticky top-0 z-10 self-center" size="sm" variant="secondary" onClick={latest}>Jump to latest</Button>}
      {detail.agent?.pageError && <Note tone="error">{detail.agent.pageError}</Note>}
      {subagent.description && <Note>{subagent.description}</Note>}
      {(!transcript || transcript.loading) && items.length === 0 ? (
        <Skeleton label="Loading the transcript…" rows={5} />
      ) : transcript?.error ? (
        <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
          <span className="min-w-0 flex-1">Could not load the transcript: {transcript.error}</span>
          <Button size="sm" variant="secondary" onClick={() => { if (detail.compact) detail.retry?.(); else setAttempt((n) => n + 1); }}>
            Retry
          </Button>
        </Note>
      ) : (
        <>
          <HistoryAnchor scroller={scroller} firstItem={items[0]?.id ?? ''} lastItem={items.at(-1)?.id} itemIds={detail.compact ? items.map(item => item.id) : undefined} knownIds={detail.agent?.index?.map(item => item.id)} resetKey={`${detail.store?.value.epoch}:${windowReset}`} className="flex flex-col gap-3">
            <AgentItems sessionId={sessionId} provider={provider} workdir={workdir} agentId={subagent.id} items={items} identityItems={detail.agent?.index} historyItemSeq={detail.agent?.itemSeq} interactions={visibleInteractions} live={live && !detail.agent?.after} density={density} />
          </HistoryAnchor>
          {items.length === 0 && <Note>Nothing recorded yet.</Note>}
        </>
      )}
      {subagent.status === 'failed' && <Note tone="error">Failed{subagent.error ? `: ${subagent.error}` : '.'}</Note>}
      {subagent.status === 'cancelled' && <Note>Stopped before it finished.</Note>}
      {detail.compact && parentBody && <div ref={parentAttach}><BodyNotice body={parentBody} retry={parentRetry} /></div>}
      {result && (
        <div className="rounded-md bg-raised px-3.5 py-2.5 text-ui shadow-raised">
          <div className="mb-1 text-caption text-muted">Result sent to the main agent</div>
          <Markdown text={result} />
        </div>
      )}
    </div>
  );
}

const UNCERTAIN_FOLLOW_UP = 'The subagent may or may not have received your message. Check its transcript before sending again; it will not be resent automatically.';

/**
 * A follow-up to one idle subagent, which the main agent never sees. Shown only while the
 * subagent is idle; enabled only while the task itself is active and between turns. The
 * status change and the user item both arrive over SSE, so nothing is added optimistically.
 */
function SubagentComposer({ session, subagent }: { session: SessionDetail; subagent: Subagent }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Submission | null>(null);
  const pending = useRef<{ text: string; id: string } | null>(null);
  // An uncertain follow-up keeps the subagent running until the provider settles it; say so meanwhile.
  if (subagent.status !== 'idle') {
    return outcome?.status === 'uncertain' ? (
      <div className="px-4 py-2">
        <Note tone="warn" role="alert">
          {UNCERTAIN_FOLLOW_UP}
        </Note>
      </div>
    ) : null;
  }
  const blocked = readOnly(session)
    ? session.stage === 'settled'
      ? 'Settled. Reopen this task to continue the same conversation.'
      : 'Archived. This task is read-only.'
    : LIVE.includes(session.state)
      ? 'Unavailable while the task is running a turn.'
      : null;
  const cannotSubmit = busy || !!blocked || !text.trim();

  async function send() {
    const t = text.trim();
    if (cannotSubmit) return;
    if (!pending.current || pending.current.text !== t) pending.current = { text: t, id: newRequestId() };
    const id = pending.current.id;
    setBusy(true);
    setError(null);
    try {
      const sub = await api.promptSubagent(session.id, subagent.id, t, id);
      setOutcome(sub);
      pending.current = null;
      if (sub.status === 'accepted') setText('');
    } catch (e) {
      // Only a transport failure leaves the outcome unknown; a server answer stands on its own.
      setError(isStatus(e, 0) ? `${describeError(e)}. Nothing will be retried automatically. Repeating this action with unchanged text uses the same request (${id.slice(0, 8)}).` : describeError(e));
    } finally {
      setBusy(false);
    }
  }
  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  }

  const textId = `subagent-text-${subagent.id}`;
  return (
    <div className="transcript-dock -mt-6 shrink-0 p-3 pt-6">
      <form
        className="relative isolate flex flex-col rounded-lg bg-raised shadow-float before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:rounded-[inherit] before:opacity-0 before:shadow-focus-float before:transition-opacity before:duration-160 before:content-[''] focus-within:before:opacity-100"
        aria-label={`Follow up with subagent ${subagent.name}`}
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <div className="flex flex-col gap-1 px-3 pt-2">
          <Note>Follow up with this subagent only. The main agent does not see this conversation.</Note>
          {blocked && <Note role="status">{blocked}</Note>}
          {outcome?.status === 'uncertain' && (
            <Note tone="warn" role="alert">
              {UNCERTAIN_FOLLOW_UP}
            </Note>
          )}
          {outcome?.status === 'rejected' && (
            <Note tone="error" role="alert">
              Rejected{outcome.error ? `: ${outcome.error}` : '.'}
            </Note>
          )}
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
        </div>
        <label className="sr-only" htmlFor={textId}>
          Follow-up for subagent {subagent.name}
        </label>
        <textarea
          id={textId}
          rows={2}
          value={text}
          placeholder="Follow up with this subagent…"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={busy || !!blocked}
          className="max-h-40 min-h-12 w-full resize-none bg-transparent px-3 py-2 text-ui text-ink outline-hidden [field-sizing:content] max-sm:text-chat-lg"
        />
        <div className="flex justify-end px-2 pb-2">
          <Button type="submit" size="sm" variant="primary" loading={busy} disabled={cannotSubmit}>
            Send
          </Button>
        </div>
      </form>
    </div>
  );
}

/** Cancellation requests never invent a terminal status; the provider's SSE owns it. */
function StopSubagent({ session, subagent, stopping, onStop }: { session: SessionDetail; subagent: Subagent; stopping: StopState; onStop: () => void }) {
  const { busy, requested, error } = stopping;
  if (subagent.status !== 'running') return null;
  return (
    <Tip label={error ?? (requested ? 'Stop requested' : 'Stop this subagent')}>
      <Button size="sm" variant="secondary" aria-label={`Stop subagent ${subagent.name}`} loading={busy} disabled={requested || readOnly(session)} onClick={onStop}>
        <Square className="!size-3" fill="currentColor" />
        {requested ? 'Requested' : 'Stop'}
      </Button>
    </Tip>
  );
}
