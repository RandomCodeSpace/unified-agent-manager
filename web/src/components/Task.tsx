import { ArrowDown, Bot, ChartLine, Ellipsis, FileDiff, FolderTree, GitBranch, Pencil, Plug, SquareTerminal, TriangleAlert } from 'lucide-react';
import { Suspense, lazy, startTransition, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent, type PointerEvent, type ReactNode } from 'react';
import { flushSync } from 'react-dom';
import { LIVE, api, describeError, isStatus, provider, readOnly, stageLabel, taskName, type Changes as ChangesData, type Interaction, type Item, type Project, type SessionDetail, type SessionSummary, type TaskDefaults } from '../api';
import type { AgentTranscript, HistoryRequest } from '../state';
import { popupOpen } from '../App';
import { useDensity } from '../lib/density';
import { historyPage } from '../lib/historyArchive';
import { PreviewContext, TempRootContext } from '../lib/previewContext';
import { awaitsUser, completedChanges, foregroundItems, transcriptWindowStart, windowInteractions } from '../lib/transcript';
import { shownState } from '../lib/tasks';
import { ChangesSheet, defaultScope } from './Changes';
import { CommitPanel, SetUpGitButton } from './CommitPanel';
import { PinnedChartsPanel } from './Chart';
import { INTERRUPTED_TEXT, InlineName, Note, ProjectBadge, ScrollSentinel, Spinner, StateMark, TaskTitle, TranscriptSkeleton, WorkingMark, useApp, useMedia, useScrolled } from './common';
import { byCodeUnit } from '../lib/order';
import { Chip } from './ui/chip';
import { Popover } from './ui/popover';
import { Appear } from './ui/appear';
import { Collapse, usePresence } from './ui/collapse';
import { Composer, type Answering, type FirstMessage } from './Composer';
import { HistoryStatus } from './PreviousSessions';
import { InteractionCard } from './Interactions';
import { SubagentPanel, type PanelView } from './Subagents';
import { CommandOutputPanel, type CommandOutput } from './CommandOutput';
import { FinishCard, SinceYouLeft } from './Finish';
import { away, sinceYouLeft } from '../lib/evidence';
import { canRename, taskMenuItems, useTaskActions } from './taskActions';
import { McpTaskDialog } from './McpTask';
import { Transcript, WorkingLabel } from './Transcript';
import { FileReferencesProvider } from './FileReferences';
import { FilePreview, useFilePreview } from './FilePreview';
import { HistoryAnchor } from './HistoryAnchor';
import { Button } from './ui/button';
import { Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** The Files sheet loads with its first opening, never with the Task. */
const FilesSheet = lazy(() => import('./Files'));

interface Props {
  session: SessionDetail;
  project: Project | undefined;
  agents: Record<string, AgentTranscript>;
  /** Each subagent's latest step from live frames, for its row's summary before its transcript is open. */
  agentSteps: Record<string, Item>;
  snapshotSeq: number;
  historyGeneration: number;
  active: boolean;
  historyRequest: HistoryRequest | null;
  historyItemSeq: Record<string, number>;
  onHistoryReset: () => void;
  sheetOpen: boolean;
  /** Side panels (Changes, Subagents) sit beside the column (wide) rather than over it. */
  sidePanelInline: boolean;
  onSheet: (open: boolean, restoreFocus?: boolean) => void;
  /** The terminal docked under the app (App.tsx): whether it is open, and the toggle that opens it in a Project's folder or closes it. */
  terminalOpen: boolean;
  onTerminal: (projectId: string) => void;
  onSessionUpdate: (s: SessionSummary) => void;
  onInteractionUpdate: (sessionId: string, i: Interaction) => void;
  /** Leading header control (the drawer button on narrow screens). */
  leading?: ReactNode;
  /** The name of the Task that started this one (`spawned_by`); empty when that Task is gone or unnamed. */
  spawnedBy?: string;
  /** The Task's `updated_at` when the owner last had it open, before this visit; "Since you left" counts from it. */
  since?: string;
  /** The name of the Task whose last message this one runs again (`rerun_of`); empty when that Task is gone or unnamed. */
  rerunOf?: string;
}

/** Distance from the bottom, in px, under which the view counts as "at the bottom". */
const BOTTOM_SLACK = 32;
/** iOS WebKit: a scroll-position write under a finger or during momentum fights the scroller and jumps. */
const TOUCH_WEBKIT = typeof CSS !== 'undefined' && CSS.supports('-webkit-touch-callout', 'none');
/** Tailwind's `max-sm`: a phone-width column. */
const PHONE = '(width < 40rem)';

/** The conversation pane: a 44px header, the transcript scrolling across the pane, the composer pinned below. */
export function Task({ session, project, agents, agentSteps, snapshotSeq, historyGeneration, active, historyRequest, historyItemSeq, onHistoryReset, sheetOpen, sidePanelInline, onSheet, terminalOpen, onTerminal, onSessionUpdate, onInteractionUpdate, leading, spawnedBy, since, rerunOf }: Readonly<Props>) {
  const { dispatch, meta, settings } = useApp();
  const tempRoot = meta?.temp_root;
  const tempAlias = meta?.temp_root_aliases?.[0];
  const tempRoots = useMemo(() => ({ temp_root: tempRoot, temp_root_aliases: tempAlias ? [tempAlias] : undefined }), [tempRoot, tempAlias]);
  const actions = useTaskActions();
  const [changes, setChanges] = useState<ChangesData | null>(null);
  const [changesError, setChangesError] = useState<string | null>(null);
  const [changesTick, setChangesTick] = useState(0);
  /** The jump-to-bottom control shows while the reader is away from the bottom. */
  const [jump, setJump] = useState(false);
  const [filesOpen, setFilesOpen] = useState(false);
  /** The Project's pinned charts beside the conversation, a right panel like Files. */
  const [chartsOpen, setChartsOpen] = useState(false);
  const [mcpOpen, setMcpOpen] = useState(false);
  const [panel, setPanel] = useState<PanelView | null>(null);
  /** A command's output in its side panel; like the other right panels, it replaces them. */
  const [output, setOutput] = useState<CommandOutput | null>(null);
  const alive = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);
  const panelOpener = useRef<HTMLElement | null>(null);
  const live = LIVE.includes(session.state);
  const working = session.state === 'working' || session.state === 'starting';
  const compactWindow = session.representation === 'compact-v1';
  const liveItems = session.recent_items ?? session.items;
  // "Since you left": from the owner's last look to this opening; what happens while they watch is seen. Dismissed for this visit.
  const [opened] = useState(() => new Date().toISOString());
  const [sinceMark, setSinceMark] = useState(since);
  const sinceSummary = useMemo(() => (sinceMark ? sinceYouLeft(liveItems, session.turn_timings ?? [], session.interactions, sinceMark, opened, session.workdir, working) : null), [sinceMark, liveItems, session.turn_timings, session.interactions, opened, session.workdir, working]);
  const latestSession = useRef(session);
  useLayoutEffect(() => { latestSession.current = session; }, [session]);
  const [windowReset, setWindowReset] = useState(0);
  const [locateError, setLocateError] = useState('');
  const density = useDensity();
  const scroller = useRef<HTMLElement>(null);
  const previewOpened = useCallback(() => { setPanel(null); setFilesOpen(false); setChartsOpen(false); setOutput(null); onSheet(false); }, [onSheet]);
  const preview = useFilePreview(session.id, `${session.workdir}:${session.epoch}:${historyGeneration}`, active, previewOpened, scroller);
  const closePreview = preview.close;
  useLayoutEffect(() => { if (sheetOpen || panel || filesOpen || chartsOpen || output) closePreview(false); }, [sheetOpen, panel, filesOpen, chartsOpen, output, closePreview]);
  // The Changes sheet can open from outside the header (the composer's count); it replaces Files.
  if (sheetOpen && filesOpen) setFilesOpen(false);
  if (sheetOpen && chartsOpen) setChartsOpen(false);
  if (sheetOpen && output) setOutput(null);
  const atBottom = useRef(true);
  const lastScrollTop = useRef(0);
  const touching = useRef(false);
  const lastScrollAt = useRef(0);
  // A landed page moves the view the other way (its rows are anchored), which is not the reader
  // turning back: scrolling loads nothing in the opposite direction until the reader moves that way.
  const landed = useRef<'older' | 'newer' | null>(null);
  const toward = (direction: 'older' | 'newer' | null) => { if (landed.current !== direction) landed.current = null; };
  // Anchor by ID so incoming output cannot move the beginning while someone reads.
  const [firstVisible, setFirstVisible] = useState<string | undefined>(() => session.items[transcriptWindowStart(session.items)]?.id);
  let visibleStart = firstVisible === undefined ? -1 : session.items.findIndex((it) => it.id === firstVisible);
  if (visibleStart < 0) {
    visibleStart = transcriptWindowStart(session.items);
    const next = session.items[visibleStart]?.id;
    if (next !== firstVisible) setFirstVisible(next);
  }
  if (compactWindow) visibleStart = 0;
  const visibleItems = useMemo(() => session.items.slice(visibleStart), [session.items, visibleStart]);
  const visibleInteractions = useMemo(() => windowInteractions(session.items, session.interactions, visibleStart, !!session.history_before, !!session.history_after), [session.items, session.interactions, session.history_before, session.history_after, visibleStart]);

  const historyAbort = useRef<AbortController | null>(null);
  const historyLive = useRef(active);
  const historyRetryAt = useRef(0);
  useLayoutEffect(() => {
    historyLive.current = active;
    return () => {
      historyLive.current = false;
      historyAbort.current?.abort();
      historyAbort.current = null;
    };
  }, [active, session.id, snapshotSeq, historyGeneration]);
  useEffect(() => {
    if (!historyRequest?.loading) return;
    // A page commit may start the next read before passive cleanup runs.
    // Cancel only the controller owned by this request, never that next read.
    const controller = historyAbort.current;
    return () => {
      controller?.abort();
      if (historyAbort.current === controller) historyAbort.current = null;
    };
  }, [historyRequest?.loading, historyRequest?.before, historyRequest?.direction]);

  /** Resolves once no finger is down and the view has stopped moving, so a page can land without a write mid-scroll. */
  const scrollSettled = (signal: AbortSignal) => new Promise<void>((resolve) => {
    const check = () => {
      if (signal.aborted || (!touching.current && performance.now() - lastScrollAt.current > 150)) resolve();
      else window.setTimeout(check, 50);
    };
    check();
  });

  async function loadEarlier(before = session.history_before, synchronous = false, direction: 'older' | 'newer' = 'older') {
    if (!historyLive.current || historyAbort.current || historyRequest?.loading || Date.now() < historyRetryAt.current) return null;
    atBottom.current = false;
    // Rendering older history may yield between rows (a transition). HistoryAnchor reads the
    // actual visible position immediately before commit and restores it afterward. A parent
    // locate needs its target in the DOM at once, so it commits synchronously.
    const commit: (update: () => void) => void = synchronous ? flushSync : startTransition;
    if (direction === 'older' && visibleStart > 0 && before === session.history_before) {
      const start = transcriptWindowStart(session.items, visibleStart);
      commit(() => setFirstVisible(session.items[start]?.id));
      return null;
    }
    if (!before) return null;
    const controller = new AbortController();
    historyAbort.current = controller;
    dispatch({ type: 'history_loading', sessionId: session.id, before, direction });
    const timeout = window.setTimeout(() => {
      controller.abort();
      dispatch({ type: 'history_failed', sessionId: session.id, before, error: 'History took too long to load. Scroll up to retry.' });
    }, 10000);
    try {
      const page = await historyPage(session.id, '', before, direction, session.epoch, () => api.history(session.id, before, controller.signal, direction));
      if (controller.signal.aborted) return null;
      if (session.epoch && page.epoch && session.epoch !== page.epoch) {
        onHistoryReset();
        return null;
      }
      if (TOUCH_WEBKIT && !synchronous) {
        window.clearTimeout(timeout);
        await scrollSettled(controller.signal);
        if (controller.signal.aborted) return null;
      }
      commit(() => {
        dispatch({ type: 'history_loaded', sessionId: session.id, before, page });
        if (!compactWindow && page.items.length) setFirstVisible(page.items[0].id);
      });
      landed.current = direction;
      return page;
    } catch (error) {
      if (controller.signal.aborted) return null;
      historyRetryAt.current = Date.now() + 1000;
      dispatch({ type: 'history_failed', sessionId: session.id, before, error: `${describeError(error)}. Scroll up to retry.` });
      if (isStatus(error, 409)) onHistoryReset();
      return null;
    } finally {
      window.clearTimeout(timeout);
      if (historyAbort.current === controller) historyAbort.current = null;
    }
  }
  const [scrolled, sentinel] = useScrolled();
  const renaming = actions.renaming?.id === session.id && actions.renaming.place === 'header';
  const busy = !!actions.busy[session.id];

  // The badge count follows turn boundaries and completed file-changing tools. The open sheet
  // also reports its refreshed default list; a tool completion during a badge read queues another.
  const fetching = useRef(false);
  const again = useRef(false);
  const listScope = defaultScope(session);
  useEffect(() => {
    let alive = true;
    fetching.current = true;
    api
      .changes(session.id, listScope)
      .then((c) => {
        if (!alive) return;
        setChanges(c);
        setChangesError(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setChanges(null);
        setChangesError(describeError(e));
      })
      .finally(() => {
        if (!alive) return;
        fetching.current = false;
        if (again.current) {
          again.current = false;
          setChangesTick((t) => t + 1);
        }
      });
    return () => {
      alive = false;
    };
  }, [session.id, listScope, live, changesTick]);
  const edits = liveItems.filter(item => completedChanges([item]) > 0).map(item => item.id).join('\n');
  const seenEdits = useRef(new Set(edits.split('\n')));
  useEffect(() => {
    const current = new Set(edits.split('\n'));
    const changed = [...current].some(id => id && !seenEdits.current.has(id));
    seenEdits.current = current;
    if (!changed) return;
    const timer = window.setTimeout(() => {
      if (fetching.current) again.current = true;
      else setChangesTick((t) => t + 1);
    }, 1000);
    return () => window.clearTimeout(timer);
  }, [edits]);

  const scrollToBottom = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    historyAbort.current?.abort();
    historyAbort.current = null;
    if (session.history_after) flushSync(() => { dispatch({ type: 'history_latest', sessionId: session.id }); setWindowReset(value => value + 1); });
    el.scrollTop = el.scrollHeight;
    lastScrollTop.current = el.scrollTop;
    atBottom.current = true;
    setJump(false);
  }, [dispatch, session.id, session.history_after]);

  // Follow new content only while the reader is at the bottom; otherwise offer a way back.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (atBottom.current && !session.history_after) el.scrollTop = el.scrollHeight;
    else if (session.history_after || el.scrollHeight - el.scrollTop - el.clientHeight > BOTTOM_SLACK) setJump(true);
  }, [session.id, session.items, session.recent_items, session.history_after, session.interactions]);
  // Rows also grow after their own commits (a body arriving, a row expanding); a pinned view follows them.
  const log = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = scroller.current, content = log.current;
    if (!el || !content) return;
    const observer = new ResizeObserver(() => { if (atBottom.current) el.scrollTop = el.scrollHeight; });
    observer.observe(content);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // One side panel at a time: the Changes sheet wins while it is open; opening the other closes it.
  const shownPanel = sheetOpen ? null : panel;
  // A closing panel stays mounted, showing its last view, until its exit has run.
  const panelPresence = usePresence(!!shownPanel);
  const sheetPresence = usePresence(sheetOpen);
  const filesPresence = usePresence(filesOpen);
  const chartsPresence = usePresence(chartsOpen);
  const outputPresence = usePresence(!!output);
  const [lastOutput, setLastOutput] = useState<CommandOutput | null>(null);
  if (output && output !== lastOutput) setLastOutput(output);
  const outputView = output ?? lastOutput;
  const [lastPanel, setLastPanel] = useState<PanelView | null>(null);
  if (shownPanel && shownPanel !== lastPanel) setLastPanel(shownPanel);
  const panelView = shownPanel ?? lastPanel;

  const closePanel = useCallback(() => {
    setPanel(null);
    const opener = panelOpener.current;
    if (opener?.isConnected) opener.focus();
    else scroller.current?.focus({ preventScroll: true });
    panelOpener.current = null;
  }, []);

  const openChanges = useCallback(() => {
    closePreview(false);
    setPanel(null);
    setFilesOpen(false);
    setChartsOpen(false);
    setOutput(null);
    onSheet(true);
  }, [onSheet, closePreview]);
  const toggleFiles = useCallback(() => {
    closePreview(false);
    setPanel(null);
    setChartsOpen(false);
    setOutput(null);
    onSheet(false);
    setFilesOpen((open) => !open);
  }, [onSheet, closePreview]);
  const closeFiles = useCallback(() => setFilesOpen(false), []);
  const toggleCharts = useCallback(() => {
    closePreview(false);
    setPanel(null);
    setFilesOpen(false);
    setOutput(null);
    onSheet(false);
    setChartsOpen((open) => !open);
  }, [onSheet, closePreview]);
  const closeCharts = useCallback(() => setChartsOpen(false), []);
  const showOutput = useCallback((next: CommandOutput) => {
    // A reply that lands after this Task was left must not close the next Task's Changes (App state).
    if (!alive.current) return;
    closePreview(false);
    setPanel(null);
    setFilesOpen(false);
    setChartsOpen(false);
    onSheet(false, false);
    setOutput(next);
  }, [closePreview, onSheet]);
  const closeOutput = useCallback(() => {
    // Esc from the composer leaves focus there; a close from inside the panel lands on the conversation.
    const inside = !!document.activeElement?.closest('[data-command-output]');
    setOutput(null);
    if (inside) scroller.current?.focus({ preventScroll: true });
  }, []);
  const { startRename } = actions;
  const renameInHeader = useCallback(() => startRename(session.id, 'header'), [startRename, session.id]);

  function openPanel(view: PanelView, opener: HTMLElement) {
    closePreview(false);
    panelOpener.current = opener;
    if (sheetOpen) onSheet(false);
    setFilesOpen(false);
    setChartsOpen(false);
    setOutput(null);
    setPanel(view);
  }

  // Closes every right side panel without returning focus to its opener.
  const closeSidePanels = useCallback((keepOutput = false) => {
    closePreview(false);
    setPanel(null);
    setFilesOpen(false);
    setChartsOpen(false);
    if (!keepOutput) setOutput(null);
    panelOpener.current = null;
    onSheet(false, false);
  }, [closePreview, onSheet]);
  // A primary press in the conversation, outside any popup (a hover tooltip does not count), closes the
  // inline side panels. It is judged on pointerdown, before the press can open a popup, and applied on
  // click (capture): closing mid-press would widen the column under the pointer and lose the click, and
  // a click that opens a panel batches after it.
  const conversationPress = useRef(false);
  const onConversationPointerDown = (e: PointerEvent) => {
    conversationPress.current = sidePanelInline && e.button === 0 && e.currentTarget.contains(e.target as Node) && !document.querySelector('[data-popup]:not([data-popup="tooltip"])');
  };
  const onConversationClick = (e: MouseEvent, keepOutput = false) => {
    if (!conversationPress.current || e.detail === 0) return;
    conversationPress.current = false;
    closeSidePanels(keepOutput);
  };

  // Esc closes the panel when no popup owns the key.
  useEffect(() => {
    if (!shownPanel) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !popupOpen()) closePanel();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [shownPanel, closePanel]);

  /** Scroll the transcript to the `task` row that spawned a subagent and flash it. */
  async function locate(toolCallId: string) {
    setLocateError('');
    let current = latestSession.current;
    let index = current.items.findIndex(item => item.id === toolCallId);
    const known = current.history_index ?? current.items;
    const target = known.findIndex(item => item.id === toolCallId);
    const end = known.findIndex(item => item.id === current.items.at(-1)?.id);
    const direction = target > end && end >= 0 ? 'newer' : 'older';
    const visited = new Set<string>();
    while (index < 0 && visited.size < 200) {
      const cursor = direction === 'older' ? current.history_before : current.history_after;
      if (!cursor || visited.has(cursor)) break;
      visited.add(cursor);
      const page = await loadEarlier(cursor, true, direction);
      if (!page) break;
      current = latestSession.current;
      index = current.items.findIndex(item => item.id === toolCallId);
    }
    if (index < 0) { setLocateError('The parent call is not in the retained history.'); return; }
    if (!compactWindow && index < visibleStart) {
      atBottom.current = false;
      flushSync(() => setFirstVisible(current.items[transcriptWindowStart(current.items, index + 1)]?.id));
    }
    if (!document.getElementById(`item-${toolCallId}`)) {
      // A settled subagent's row folds into its turn's activity: open the fold that holds it.
      const fold = [...(log.current?.querySelectorAll<HTMLElement>('[data-history-items]') ?? [])].find(node => (JSON.parse(node.dataset.historyItems ?? '[]') as string[]).includes(toolCallId));
      const toggle = fold?.matches('button') ? fold : fold?.querySelector('button');
      if (toggle?.getAttribute('aria-expanded') === 'false') flushSync(() => toggle.click());
    }
    const el = document.getElementById(`item-${toolCallId}`);
    if (!el) return;
    el.scrollIntoView({ block: 'center' });
    el.classList.add('animate-flash');
    window.setTimeout(() => el.classList.remove('animate-flash'), 1400);
  }

  /**
   * "Jump to where you stopped": scroll to the first of `ids` on screen, its own row or the folded
   * run that holds it, once no finger is down and the view has stopped moving (iOS WebKit jumps
   * on a scroll write mid-scroll). The strip has done its job then.
   */
  async function jumpTo(ids: readonly string[]) {
    setSinceMark(undefined);
    const root = log.current;
    if (!root) return;
    const folds = [...root.querySelectorAll<HTMLElement>('[data-history-items]')];
    const find = (id: string) => root.querySelector<HTMLElement>(`[id="${CSS.escape(`item-${id}`)}"], [data-history-anchor="${CSS.escape(id)}"]`)
      ?? folds.find((node) => (JSON.parse(node.dataset.historyItems ?? '[]') as string[]).includes(id));
    const target = ids.map(find).find(Boolean);
    if (!target) return;
    if (TOUCH_WEBKIT) await new Promise<void>((resolve) => {
      const check = () => (!touching.current && performance.now() - lastScrollAt.current > 150 ? resolve() : window.setTimeout(check, 50));
      check();
    });
    atBottom.current = false;
    // Scroll the conversation only: scrollIntoView also scrolls clipped ancestors, sliding the app.
    const view = scroller.current;
    if (view) view.scrollTo({ top: view.scrollTop + target.getBoundingClientRect().top - view.getBoundingClientRect().top });
    target.classList.add('animate-flash');
    window.setTimeout(() => target.classList.remove('animate-flash'), 1400);
  }

  const touchY = useRef(0);
  // A single upward-demand read can start three viewports ahead. This gives
  // slow responses time to arrive without fetching anything on initial open.
  const nearEdge = (el: HTMLElement, direction: 'older' | 'newer') => {
    const content = el.querySelector('[data-history-window]');
    const edge = el.getBoundingClientRect();
    const rect = content?.getBoundingClientRect();
    const above = rect ? edge.top - rect.top : el.scrollTop;
    const below = rect ? rect.bottom - edge.bottom : el.scrollHeight - el.scrollTop - el.clientHeight;
    const [distance, other] = direction === 'older' ? [above, below] : [below, above];
    // A page read at one end drops rows at the other. In a window shorter than both reaches (folded
    // Compact turns), that is the end in view: read only at the end the reader is closer to. Both
    // ends in view (nothing to scroll yet) count as equally near, so the wheel still reads earlier.
    return distance < Math.min(1600, Math.max(200, el.clientHeight * 3)) && Math.max(0, distance) <= Math.max(0, other);
  };
  const nearEarlier = (el: HTMLElement) => nearEdge(el, 'older');
  const loadNewer = () => session.history_after ? loadEarlier(session.history_after, false, 'newer') : Promise.resolve(null);
  function onScroll() {
    const el = scroller.current;
    if (!el) return;
    const upwards = el.scrollTop < lastScrollTop.current;
    lastScrollTop.current = el.scrollTop;
    lastScrollAt.current = performance.now();
    // Only the reader scrolling up unpins the view; content growing under a pinned view does not.
    atBottom.current = !session.history_after && (el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK || (atBottom.current && !upwards));
    setJump(!atBottom.current);
    if (upwards && landed.current !== 'newer' && nearEarlier(el)) void loadEarlier();
    else if (!upwards && landed.current !== 'older' && nearEdge(el, 'newer')) void loadNewer();
  }

  // A pending question with one question is the composer's extension (DESIGN.md Composer, answer mode),
  // never a card: the composer shows it, holds the text and files, and answers or declines it.
  const question = session.capabilities.questions ? session.interactions.find((i) => awaitsUser(i) && i.kind === 'question' && i.questions?.length === 1) : undefined;
  const questionId = question?.id;
  const onAnswered = useCallback((i: Interaction) => onInteractionUpdate(session.id, i), [onInteractionUpdate, session.id]);
  const answering = useMemo<Answering | null>(() => (question ? { interaction: question, question: question.questions![0], onAnswered } : null), [question, onAnswered]);

  // A decided card collapses in place (its last pending look, inert) instead of vanishing; it leaves once the collapse has run.
  // A request yolo mode is answering is never a card, nor is the question the composer answers.
  const carded = (i: Interaction) => awaitsUser(i) && i.id !== questionId;
  const pendingIds = session.interactions.filter(carded).map((i) => i.id).join(',');
  const [seenPending, setSeenPending] = useState(pendingIds);
  const [lingering, setLingering] = useState<Interaction[]>([]);
  if (pendingIds !== seenPending) {
    setSeenPending(pendingIds);
    const gone = seenPending.split(',').filter((id) => id && !pendingIds.split(',').includes(id));
    const decided = gone.flatMap((id) => session.interactions.filter((i) => i.id === id)).map((i) => ({ ...i, state: 'pending' as const }));
    if (decided.length) setLingering((l) => [...l, ...decided.filter((i) => !l.some((x) => x.id === i.id))]);
  }
  const cards = [...session.interactions.filter(carded), ...lingering.filter((i) => !session.interactions.some((x) => x.id === i.id && carded(x)))];

  // The provider's own notice about a failure already stands in the transcript: the failed line is not repeated under it.
  const failure = session.state === 'failed' ? session.state_detail?.toLowerCase() ?? '' : '';
  const noticed = !!failure && foregroundItems(liveItems).some((i) => i.kind === 'notice' && (i.text ?? '').toLowerCase().includes(failure));

  const name = taskName(session);
  // Recorded history still on its way with nothing to show yet: a skeleton, not the "New task" intro.
  const historyLoading = session.history === 'loading' && session.items.length === 0;
  const fileCount = changes?.supported ? changes.files.length : null;
  // Without git there is nothing for Changes or Files to show: a warning stands in their place (DESIGN.md D3).
  const noGit = project?.no_git;
  const detail = session.state_detail && session.state !== 'failed' ? session.state_detail : undefined;
  const agentsRunning = session.subagents.filter((s) => s.status === 'running').length;
  // A subagent still running after the turn, or compacting, keeps the Task Working (lib/tasks shownState).
  const state = shownState(session);
  const compacting = !!session.compacting && state === 'working';
  // The finish card follows a turn that completed, at the live end of the history.
  const lastTiming = session.turn_timings?.at(-1);
  const finished = !live && state !== 'working' && !session.history_after && !historyLoading && liveItems.some((item) => item.kind === 'assistant' && !item.agent_id)
    && (lastTiming ? lastTiming.state === 'completed' : session.state === 'completed');
  // Then the floating label stays up too, timed from the first of them to start.
  const labelled = working || state === 'working';
  const agentsSince = working ? undefined : session.subagents.filter((s) => s.status === 'running').map((s) => s.started_at ?? '').filter(Boolean).sort(byCodeUnit)[0];
  // Below `sm` the title keeps the row: the state shows its glyph alone, and Files and Terminal move into the actions menu.
  const phone = useMedia(PHONE);
  const folded: ActionItem[] = [];
  if (phone && !noGit) folded.push({ key: 'files', label: filesOpen ? 'Close files' : 'Browse files', icon: <FolderTree />, onSelect: toggleFiles });
  const pinned = project?.charts ?? 0;
  if (phone && pinned > 0) folded.push({ key: 'charts', label: chartsOpen ? 'Close pinned charts' : `Pinned charts, ${pinned}`, icon: <ChartLine />, onSelect: toggleCharts });
  if (phone && settings.terminal && project) folded.push({ key: 'terminal', label: terminalOpen ? 'Close terminal' : 'Open terminal', icon: <SquareTerminal />, takesFocus: !terminalOpen, onSelect: () => onTerminal(project.id) });
  const items = [...taskMenuItems(session, actions, 'header'), ...folded.map((item, i) => ({ ...item, separator: i === 0 }))];
  if (session.capabilities.mcp && (session.stage ?? 'active') === 'active') items.push({ key: 'mcp', label: 'MCP servers…', icon: <Plug />, takesFocus: true, separator: !folded.length, onSelect: () => setMcpOpen(true) });
  const renamable = canRename(session, actions);
  const runningTitle = `${session.subagents_running} ${session.subagents_running === 1 ? 'subagent' : 'subagents'} running`;
  const changesLabel = fileCount === null ? 'Open changes' : `Open changes, ${fileCount} files`;
  const runningSuffix = agentsRunning ? `, ${agentsRunning} running` : '';
  const subagentsLabel = `Subagents, ${session.subagents.length}${session.subagents_before ? ' or more' : ''}${runningSuffix}`;

  return (
    <FileReferencesProvider sessionId={session.id} workdir={session.workdir} generation={`${session.epoch}:${historyGeneration}`} active={active} items={session.items}>
    <PreviewContext.Provider value={preview.open}>
    <TempRootContext.Provider value={tempRoots}>
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3" data-scrolled={scrolled || undefined}>
          {leading}
          <div className="group/title flex min-w-0 flex-1 items-center gap-1.5">
            {project && <ProjectBadge badge={project.badge} className="mr-0.5" />}
            {renaming ? (
              <InlineName initial={session.name} label="Task name" className="h-8 max-w-md text-display-sm font-semibold" onSave={(v) => void actions.rename(session.id, v)} onCancel={actions.cancelRename} />
            ) : (
              <h1
                className="min-w-0 truncate text-display-sm text-ink"
                title={name || undefined}
                onDoubleClick={() => renamable && actions.startRename(session.id, 'header')}
              >
                <TaskTitle session={session} />
              </h1>
            )}
            {readOnly(session) ? (
              <Chip fill="outline">{stageLabel(session)}</Chip>
            ) : (
              <StateMark state={state} label={!phone} text={compacting ? 'Compacting…' : undefined} title={compacting ? 'Compacting the conversation' : state !== session.state ? runningTitle : detail} className="shrink-0" />
            )}
            {/* The last completed turn in one line, beside its state (phones show it in the list). */}
            {state === 'completed' && session.outcome && !readOnly(session) && <span className="min-w-0 max-w-[45%] truncate text-meta text-muted max-sm:hidden" title={session.outcome}>{session.outcome}</span>}
            {busy && <Spinner className="shrink-0" />}
            {/* The pencil takes no room until the title is hovered or it is focused, so the state chip sits by the title. */}
            {renamable && !renaming && (
              <Tip label="Rename">
                <Button size="icon" aria-label="Rename task" className="-ml-1.5 w-0 min-w-0 overflow-hidden px-0 text-muted opacity-0 transition-[width,opacity,margin] duration-100 group-hover/title:ml-0 group-hover/title:w-7 group-hover/title:opacity-100 focus-visible:ml-0 focus-visible:w-7 focus-visible:opacity-100 max-sm:hidden" onClick={() => actions.startRename(session.id, 'header')}>
                  <Pencil />
                </Button>
              </Tip>
            )}
          </div>
          {/* The project strip (DESIGN.md D3): the branch, then Changes, beside the title. */}
          {project?.branch && (
            <span className="flex min-w-0 max-w-40 items-center gap-1 text-meta text-muted max-sm:hidden" title={`Project branch: ${project.branch}\nProject folder: ${session.workdir}`}>
              <GitBranch aria-hidden="true" className="size-3 shrink-0" />
              <span className="truncate">{project.branch}</span>
            </span>
          )}
          {noGit ? (
            <Popover.Root>
              <Popover.Trigger render={<Button id="no-git" size="md" className="px-2 text-warning" />}>
                <TriangleAlert />
                <span className="max-sm:sr-only">{noGit === 'not_installed' ? 'Git not installed' : 'Not a Git repository'}</span>
              </Popover.Trigger>
              <Popover.Content className="w-80 max-w-[calc(100vw-16px)] gap-2">
                <Popover.Title>{noGit === 'not_installed' ? 'Git is not installed' : 'Not a Git repository'}</Popover.Title>
                <Popover.Description>
                  {noGit === 'not_installed' ? 'The server has no git in a standard location' : <><code className="font-mono text-code-sm break-all">{session.workdir}</code> is not in a Git repository</>}, so this Task has no Changes or Files view.
                </Popover.Description>
                {noGit === 'not_repository' && <SetUpGitButton session={session} />}
              </Popover.Content>
            </Popover.Root>
          ) : (
            <>
              <Tip label={`Changes in ${project?.name ?? 'the project'}`}>
                <Button id="changes-link" size="md" aria-pressed={sheetOpen} aria-label={changesLabel} className="px-2 text-muted" onClick={openChanges}>
                  <FileDiff />
                  <span className="max-sm:hidden">Changes</span>
                  {fileCount !== null && <span className="tabular-nums text-ink">{fileCount}</span>}
                </Button>
              </Tip>
              {!phone && (
                <Tip label={`Files in ${project?.name ?? 'the project'}`}>
                  <Button id="files-link" size="md" aria-pressed={filesOpen} aria-label="Browse files" className="px-2 text-muted" onClick={toggleFiles}>
                    <FolderTree />
                    <span className="max-sm:hidden">Files</span>
                  </Button>
                </Tip>
              )}
            </>
          )}
          {pinned > 0 && !phone && (
            <Tip label={`Charts pinned to ${project?.name ?? 'the project'}`}>
              <Button id="charts-link" size="md" aria-pressed={chartsOpen} aria-label={`Pinned charts, ${pinned}`} className="px-2 text-muted" onClick={toggleCharts}>
                <ChartLine />
                <span className="max-sm:hidden">Charts</span>
                <span className="tabular-nums text-ink">{pinned}</span>
              </Button>
            </Tip>
          )}
          {settings.terminal && project && !phone && (
            <Tip label={`Terminal in ${project.name}`}>
              <Button id="terminal-link" size="md" aria-pressed={terminalOpen} aria-label="Terminal" className="px-2 text-muted" onClick={() => onTerminal(project.id)}>
                <SquareTerminal />
                <span className="max-sm:hidden">Terminal</span>
              </Button>
            </Tip>
          )}
          {session.subagents.length > 0 && (
            <Tip label="Subagents">
              <Button
                id="subagents-link"
                size="md"
                aria-pressed={!!shownPanel}
                aria-label={subagentsLabel}
                className="px-2 text-muted"
                onClick={(e) => (shownPanel ? closePanel() : openPanel({ view: 'list' }, e.currentTarget))}
              >
                <Bot />
                <span className="max-sm:hidden">Subagents</span>
                <span className="tabular-nums text-ink">{session.subagents.length}{session.subagents_before && '+'}</span>
                {agentsRunning > 0 && <WorkingMark />}
              </Button>
            </Tip>
          )}
          <Menu.Root modal={false}>
            <Menu.Trigger render={<Button size="icon-md" aria-label="Task actions" className="text-muted" />}>
              <Ellipsis />
            </Menu.Trigger>
            <Menu.Content align="end">
              <Menu.Actions items={items} />
            </Menu.Content>
          </Menu.Root>
        </header>
        {mcpOpen && <McpTaskDialog sessionId={session.id} onClose={() => setMcpOpen(false)} />}

        {sinceMark && sinceSummary && !historyLoading && (
          <div className="shrink-0 px-3 pt-2 sm:px-4 md:px-6">
            <SinceYouLeft away={away(sinceMark, Date.parse(opened))} text={sinceSummary.text} onJump={() => void jumpTo(sinceSummary.ids)} onDismiss={() => setSinceMark(undefined)} />
          </div>
        )}
        {/* eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions, jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling, including paging at its upper edge. */}
        <section aria-label="Conversation" className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain" ref={scroller} onScroll={onScroll} tabIndex={0} onPointerDownCapture={e => { toward(null); onConversationPointerDown(e); }} onClickCapture={onConversationClick}
          onKeyDown={e => {
            if (e.defaultPrevented) return;
            if (['ArrowUp', 'PageUp', 'Home'].includes(e.key)) { toward('older'); if (nearEarlier(e.currentTarget)) void loadEarlier(); }
            if (['ArrowDown', 'PageDown', 'End'].includes(e.key)) { toward('newer'); if (nearEdge(e.currentTarget, 'newer')) void loadNewer(); }
          }}
          onWheel={e => { if (e.deltaY < 0) { toward('older'); if (nearEarlier(e.currentTarget)) void loadEarlier(); } if (e.deltaY > 0) { toward('newer'); if (nearEdge(e.currentTarget, 'newer')) void loadNewer(); } }}
          onTouchStart={e => { touching.current = true; touchY.current = e.touches[0]?.clientY ?? 0; }}
          onTouchEnd={e => { touching.current = e.touches.length > 0; }}
          onTouchCancel={e => { touching.current = e.touches.length > 0; }}
          onTouchMove={e => { const y = e.touches[0]?.clientY ?? 0; if (y > touchY.current) { toward('older'); if (nearEarlier(e.currentTarget)) void loadEarlier(); } if (y < touchY.current) { toward('newer'); if (nearEdge(e.currentTarget, 'newer')) void loadNewer(); } touchY.current = y; }}>
          <ScrollSentinel sentinelRef={sentinel} />
          {/* The foot's extra padding is the dock's overlap plus a gap, so the last row can still scroll clear of the composer. */}
          {historyLoading && <TranscriptSkeleton label="Loading recorded history…" />}
          <div ref={log} className="flex w-full flex-col gap-6 px-3 pt-6 pb-16 sm:px-4 md:px-6" role="log" aria-busy={historyLoading || undefined}>
            {!historyLoading && <HistoryStatus key={`${session.history}:${session.history_reason}`} session={session} />}
            {/* Only at the true start of what the service holds; above it, scrolling still loads more. */}
            {session.spawned_by && visibleStart === 0 && !session.history_before && <Note>{spawnedBy ? `Started by another task, ${spawnedBy}.` : 'Started by another task.'}</Note>}
            {session.routine_id && visibleStart === 0 && !session.history_before && <Note>Started by a routine.</Note>}
            {session.rerun_of && visibleStart === 0 && !session.history_before && <Note>Runs again the last message of {rerunOf ? <button type="button" className="underline decoration-hairline-strong underline-offset-2 hover:text-ink" onClick={() => actions.select(session.rerun_of!)}>{rerunOf}</button> : 'another task'}, to compare.</Note>}
            {session.history_truncated && visibleStart === 0 && !session.history_before && <Note>Earlier history was truncated; only the most recent part is shown.</Note>}
            {(visibleStart > 0 || session.history_before) && <output className="flex items-center gap-2 text-caption text-muted">{historyRequest?.error ?? (historyRequest?.loading && historyRequest.direction !== 'newer' ? <><Spinner />Loading earlier messages…</> : 'Scroll up for earlier messages')}</output>}
            {session.items.length === 0 && session.state === 'idle' && !readOnly(session) && !historyLoading && <NewTaskIntro project={project} />}
            <HistoryAnchor scroller={scroller} firstItem={visibleItems[0]?.id ?? ''} lastItem={visibleItems.at(-1)?.id} itemIds={compactWindow ? visibleItems.map(item => item.id) : undefined} knownIds={session.history_index?.map(item => item.id)} resetKey={`${session.epoch}:${historyGeneration}:${windowReset}`} className="flex flex-col gap-6">
            <Transcript
              sessionId={session.id}
              items={visibleItems}
              identityItems={session.history_index}
              liveItems={liveItems}
              historyItemSeq={historyItemSeq}
              turnTimings={session.turn_timings}
              interactions={visibleInteractions}
              subagents={session.subagents}
              agents={agents}
              agentSteps={agentSteps}
              live={live && !session.history_after}
              working={working && !session.history_after}
              provider={session.provider}
              workdir={session.workdir}
              onOpenAgent={(id, opener) => openPanel({ view: 'agent', id }, opener)}
              footVerb={false}
              compacting={compacting}
              density={density}
              onOpenChanges={openChanges}
              changedLine={!noGit}
            />
            </HistoryAnchor>
            {finished && <FinishCard session={session} items={liveItems} changes={changes} onShowOutput={showOutput} onReview={noGit ? undefined : openChanges} commit={!noGit && <CommitPanel session={session} defaultOpen quiet onChanged={() => setChangesTick((t) => t + 1)} />} />}
            {session.history_after && <output className="flex items-center gap-2 text-caption text-muted">{historyRequest?.direction === 'newer' && historyRequest.loading ? <><Spinner />Loading newer messages…</> : 'Scroll down for newer messages'}</output>}
            {locateError && <Note>{locateError}</Note>}
            {cards.map((i) => (
              <Collapse key={i.id} open={session.interactions.some((x) => x.id === i.id && carded(x))} className="-mt-6" inner="pt-6" onClosed={() => setLingering((l) => l.filter((x) => x.id !== i.id))}>
                <InteractionCard session={session} interaction={i} onUpdate={(next) => onInteractionUpdate(session.id, next)} />
              </Collapse>
            ))}
            {session.state === 'interrupted' && <Note tone="warn">{INTERRUPTED_TEXT}</Note>}
            {session.state === 'failed' && !noticed && (
              <Note tone="error" role="alert">
                Turn failed{session.state_detail ? `: ${session.state_detail}` : '.'}
              </Note>
            )}
          </div>
        </section>

        {/* The floating control plane: the dock overlaps the transcript's foot by 40px and fades it out beneath the composer. */}
        {/* A press here closes the inline panels too, but not the command output: the next command is typed here. */}
        <div className="transcript-dock -mt-10 w-full shrink-0 px-3 pt-10 pb-4 sm:px-4 md:px-6" onPointerDownCapture={onConversationPointerDown} onClickCapture={(e) => onConversationClick(e, true)}>
          {/* The working label stays centred just above the composer; while it shows, "Jump to bottom" is an arrow beside it, so it never moves. */}
          <div className="pointer-events-none absolute inset-x-0 top-0 flex justify-center px-3 *:pointer-events-auto">
            <div className="relative flex">
              <WorkingLabel working={labelled} compacting={compacting} since={agentsSince} items={liveItems} identityItems={session.history_index} turnTimings={session.turn_timings} />
              <Appear show={jump && labelled} className="absolute top-0 left-full ml-2">
                <Tip label="Jump to bottom">
                  <Button variant="secondary" size="icon" aria-label="Jump to bottom" className="shadow-float" onClick={scrollToBottom}>
                    <ArrowDown />
                  </Button>
                </Tip>
              </Appear>
            </div>
            <Appear show={jump && !labelled} className="shrink-0">
              <Button variant="secondary" size="sm" className="shadow-float" onClick={scrollToBottom}>
                <ArrowDown />
                Jump to bottom
              </Button>
            </Appear>
          </div>
          <Composer key={session.id} session={session} onRename={renameInHeader} onSessionUpdate={onSessionUpdate} answering={answering} onCommandOutput={showOutput} />
        </div>
      </div>

      {sheetPresence.mounted && <ChangesSheet session={session} projectName={project?.name ?? 'Project'} changes={changes} changesError={changesError} isDefaultPending={() => fetching.current} inline={sidePanelInline} open={sheetOpen} active={active} onChanges={(next) => { setChanges(next); setChangesError(null); }} onClose={() => onSheet(false)} onClosed={sheetPresence.onClosed} />}
      {filesPresence.mounted && <Suspense fallback={null}><FilesSheet session={session} inline={sidePanelInline} open={filesOpen} onClose={closeFiles} onClosed={filesPresence.onClosed} /></Suspense>}
      {chartsPresence.mounted && project && <PinnedChartsPanel project={project} inline={sidePanelInline} open={chartsOpen} onClose={closeCharts} onClosed={chartsPresence.onClosed} />}
      {outputPresence.mounted && outputView && <CommandOutputPanel output={outputView} inline={sidePanelInline} open={!!output} onClose={closeOutput} onClosed={outputPresence.onClosed} />}
      {panelPresence.mounted && panelView && <SubagentPanel session={session} agents={agents} snapshotSeq={Math.max(snapshotSeq, session.seq ?? -1)} view={panelView} inline={sidePanelInline} open={!!shownPanel} onView={setPanel} onClose={closePanel} onClosed={panelPresence.onClosed} onLocate={locate} />}
      {preview.selection && !sheetOpen && !shownPanel && <FilePreview selection={preview.selection} sessionId={session.id} workdir={session.workdir} inline={sidePanelInline} onClose={() => closePreview()} />}
    </div>
    </TempRootContext.Provider>
    </PreviewContext.Provider>
    </FileReferencesProvider>
  );
}

function NewTaskIntro({ project }: Readonly<{ project: Project | undefined }>) {
  return (
    <div className="flex flex-col items-center gap-1 py-10 text-center animate-rise">
      <p className="text-title text-ink">New task in {project?.name ?? 'this project'}</p>
      <p className="text-ui text-muted">Your first message gives this task its title.</p>
    </div>
  );
}

const noRename = () => {};

/**
 * A new Task before its first message: nothing exists on the service yet, so there is no
 * state, Changes or menu, only the Project's badge and a composer on the Project's
 * defaults. `onSend` creates the Task and delivers the message (App `createTask`).
 */
export function NewTaskPane({ project, defaults, onSend, leading }: Readonly<{ project: Project; defaults: TaskDefaults; onSend: (projectId: string, first: FirstMessage) => Promise<void>; leading?: ReactNode }>) {
  const { meta } = useApp();
  const [session, setSession] = useState<SessionDetail>(() => ({
    id: '', project_id: project.id, provider: defaults.provider, name: '', title: '', workdir: project.dir, conversation_id: '',
    model: defaults.model, last_model: '', effort: defaults.effort, context_size: defaults.context_size, mode: defaults.mode,
    stage: 'active', state: 'idle', open: false, pending: 0, subagents_running: 0, created_at: '', updated_at: '',
    capabilities: provider(meta, defaults.provider)?.capabilities ?? { cancel: false, permissions: false, questions: false, session_diff: false, history: false },
    items: [], interactions: [], subagents: [], history_truncated: false, last_submission: null,
  }));
  const onSettings = useCallback((s: SessionSummary) => setSession((d) => ({ ...d, ...s })), []);
  const newTask = useMemo(() => ({ projectId: project.id, send: (first: FirstMessage) => onSend(project.id, first) }), [project.id, onSend]);
  return (
    <div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3">
        {leading}
        <ProjectBadge badge={project.badge} className="mr-0.5" />
        <h1 className="min-w-0 truncate text-display-sm text-ink">New task</h1>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className="flex w-full flex-col gap-6 px-3 pt-6 pb-16 sm:px-4 md:px-6">
          <NewTaskIntro project={project} />
        </div>
      </div>
      <div className="transcript-dock -mt-10 w-full shrink-0 px-3 pt-10 pb-4 sm:px-4 md:px-6">
        <Composer session={session} onRename={noRename} onSessionUpdate={onSettings} newTask={newTask} />
      </div>
    </div>
  );
}
