import { initialWindow, liveWindow, recentProjection, upsertTranscriptItem, windowPage } from './lib/historyState.ts';
import { boundItems, idleSteerEcho, itemCursor, mergeItems, TAIL_ITEMS } from './lib/historyWindow.ts';
import { applyBoardFrame } from './lib/board.ts';
import type { AccountUsage, BoardData, BoardFrame, BoardJob, HistoryPage, Interaction, Item, ItemKind, Project, SessionDetail, SessionSummary, Settings, SnapshotData, Subagent, SubagentDetail, SubagentPage, UpdateData } from './api';

export type Connection = 'connecting' | 'connected' | 'reconnecting' | 'offline';

/** The service's defaults, in force until its snapshot arrives (and on a service too old to send one). */
export const DEFAULT_SETTINGS: Settings = { send_default: 'steer', terminal: false };

/** A frame that arrived for a subagent while its transcript fetch was in flight. */
export type Buffered = Extract<UpdateData, { name: 'item' | 'delta' | 'tool_output' | 'subagent' | 'items_trimmed' }>;

// A stalled HTTP fetch must not retain an unlimited stream. Retry from a fresh
// snapshot on overflow; dropping individual deltas would corrupt the transcript.
const MAX_BUFFERED_FRAMES = 256;
// Reserve the other 2 MiB for detail-stream page hydration.
const MAX_BUFFERED_CHARS = 2 * 1024 * 1024;
const frameBytes = (frame: Buffered) => JSON.stringify(frame).length * 2;
const bufferedBytes = (state: State) => (state.historyRequest?.bufferedChars ?? 0) + Object.values(state.agents).reduce((sum, agent) => sum + agent.bufferedChars, 0);

/** A subagent transcript, present once its block was expanded. Live frames with that agent_id land here. */
export interface AgentTranscript {
  loading: boolean;
  /** Events through this sequence are already included in the fetched transcript and metadata. */
  snapshotSeq: number;
  error?: string;
  items: Item[];
  buffered: Buffered[];
  bufferedChars: number;
}

export interface HistoryRequest {
  before: string;
  direction?: 'older' | 'newer';
  loading: boolean;
  error?: string;
  buffered: Buffered[];
  bufferedChars: number;
}

/**
 * One Project's Board (key: the Project id, or `unassigned`), from `GET /api/board` and the
 * `board` frames after it (ADR 0005 §15). Frames that arrive while a fetch is in flight wait
 * in `buffered` and replay on its reply; a revision gap marks it `stale`, and it is fetched again.
 */
export interface BoardState {
  loading: boolean;
  /** Null until the first reply: the board is unknown, not empty. */
  data: BoardData | null;
  error?: string;
  stale: boolean;
  buffered: BoardFrame[];
}

/** Frames kept while a board fetch is in flight; more means fetching again. */
const MAX_BOARD_BUFFER = 64;
/** The latest frames of Utility jobs, by job id; older ones leave first. */
const MAX_BOARD_JOBS = 32;

export const boardKey = (projectId: string) => projectId || 'unassigned';

export interface State {
  /** Whether a snapshot has arrived at least once: until then the Projects and Tasks are unknown, not absent (loading, never the empty state). */
  loaded: boolean;
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  /** Detail of the selected session, from the latest snapshot; null until it arrives. */
  detail: SessionDetail | null;
  /** Frozen presentation while detail loads: the selected task's cached page, or the previous task. */
  previous: SessionDetail | null;
  /** A cache hit can remain visible through a slow refresh; it never confirms detail or permits actions. */
  previousCached: boolean;
  /** seq of the latest snapshot; updates with seq <= this are ignored. -1 before any snapshot. */
  snapshotSeq: number;
  /** Newest selected-task frame or detail response, used to reject stale reloads. */
  detailSeq: number;
  detailGeneration: number;
  bodyVersions: Record<string, number>;
  historyRequest: HistoryRequest | null;
  /** Older-page items already include frames through their HTTP snapshot. */
  historyItemSeq: Record<string, number>;
  connection: Connection;
  agents: Record<string, AgentTranscript>;
  /** Each subagent's latest step, kept from live frames even while its transcript is not open: kind and call only, never output. */
  agentSteps: Record<string, Item>;
  /** The service's settings, from the snapshot and `settings` frames; the defaults until the first snapshot. */
  settings: Settings;
  /** The account quotas, from the snapshot and `usage` frames; null until a snapshot carries them (#188). */
  usage: AccountUsage | null;
  /** The Boards this page follows, by `boardKey`. */
  boards: Record<string, BoardState>;
  /** The latest `board_job` frame of each Utility job. */
  boardJobs: Record<string, BoardJob>;
}

export const initialState: State = {
  loaded: false,
  projects: [],
  sessions: [],
  selectedId: null,
  detail: null,
  previous: null,
  previousCached: false,
  snapshotSeq: -1,
  detailSeq: -1,
  detailGeneration: 0,
  bodyVersions: {},
  historyRequest: null,
  historyItemSeq: {},
  connection: 'connecting',
  agents: {},
  agentSteps: {},
  settings: DEFAULT_SETTINGS,
  usage: null,
  boards: {},
  boardJobs: {},
};

export type Action =
  | { type: 'reset' }
  | { type: 'select'; id: string | null; cached?: SessionDetail }
  | { type: 'connection'; status: Connection }
  | { type: 'snapshot'; data: SnapshotData }
  | { type: 'update'; data: UpdateData }
  /** Frames batched by the stream handler (deltas, one animation frame's worth), applied in order in one render. */
  | { type: 'updates'; data: UpdateData[] }
  | { type: 'settings'; settings: Settings }
  | { type: 'detail_loaded'; detail: SessionDetail }
  | { type: 'history_loading'; sessionId: string; before: string; direction?: 'older' | 'newer' }
  | { type: 'history_latest'; sessionId: string }
  | { type: 'history_loaded'; sessionId: string; before: string; page: HistoryPage }
  | { type: 'history_failed'; sessionId: string; before: string; error: string }
  /** A session from an HTTP reply; ignored when the live state is already newer (by updated_at). */
  | { type: 'upsert_session'; session: SessionSummary }
  | { type: 'remove_session'; id: string }
  | { type: 'upsert_project'; project: Project }
  | { type: 'remove_project'; id: string }
  | { type: 'upsert_interaction'; sessionId: string; interaction: Interaction }
  | { type: 'agent_loading'; sessionId: string; agentId: string }
  | ({ type: 'agent_loaded'; sessionId: string; agentId: string } & SubagentDetail)
  | { type: 'agent_failed'; sessionId: string; agentId: string; error: string }
  | { type: 'agent_unloaded'; sessionId: string; agentId: string }
  /** A page of older subagents, read with the detail's `subagents_before`. */
  | { type: 'subagents_older'; sessionId: string; before: string; page: SubagentPage }
  | { type: 'board_loading'; key: string }
  | { type: 'board_loaded'; key: string; data: BoardData }
  | { type: 'board_failed'; key: string; error: string }
  /** The planner was turned off, or a Project left: its Board is no longer followed. */
  | { type: 'board_dropped'; key: string };

/** Older subagents go before the held ones; a held record is fresher than its recorded copy. */
export function withOlderSubagents(held: Subagent[], older: Subagent[]): Subagent[] {
  const known = new Set(held.map((s) => s.id));
  return [...older.filter((s) => !known.has(s.id)), ...held];
}

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'reset': return { ...initialState };
    case 'select': {
      if (action.id === state.selectedId) return state;
      const cached = action.cached?.id === action.id && action.cached.representation === 'compact-v1' && action.cached.epoch ? action.cached : undefined;
      return { ...state, selectedId: action.id, detail: null, previous: action.id ? (cached ?? state.detail ?? state.previous) : null, previousCached: !!cached, detailSeq: -1, bodyVersions: {}, detailGeneration: state.detailGeneration + 1, agents: {}, agentSteps: {}, historyRequest: null, historyItemSeq: {} };
    }
    case 'connection': {
      if (state.connection === action.status) return state;
      const detail = action.status !== 'connected' && state.detail
        ? { ...state.detail, ...(state.detail.turn_timings ? { turn_timings: state.detail.turn_timings.map((timing) => timing.state === 'working' ? { ...timing, state: 'unknown' as const } : timing) } : {}), ...(state.detail.background_tasks ? { background_tasks: { ...state.detail.background_tasks, known: false } } : {}), ...(state.detail.execution ? { execution: { ...state.detail.execution, known: false } } : {}) }
        : state.detail;
      return { ...state, connection: action.status, detail };
    }
    case 'snapshot': {
      const { seq, sessions, session, projects, settings, usage } = action.data;
      const detail = session?.id === state.selectedId ? initialWindow(session) : null;
      // A fresh snapshot invalidates subagent transcripts loaded under the old stream;
      // expanded blocks reload them (see SubagentBlock).
      return {
        ...state,
        loaded: true,
        projects: projects ?? [],
        sessions,
        detail,
        previous: null,
        previousCached: false,
        snapshotSeq: seq,
        detailSeq: seq,
        bodyVersions: {},
        detailGeneration: state.detailGeneration + 1,
        historyRequest: null,
        historyItemSeq: {},
        connection: 'connected',
        agents: {},
        agentSteps: {},
        settings: settings ?? DEFAULT_SETTINGS,
        usage: usage ?? null,
        boards: staleSince(state.boards, action.data.boards),
      };
    }
    case 'board_loading': {
      const board = state.boards[action.key];
      return { ...state, boards: { ...state.boards, [action.key]: { loading: true, data: board?.data ?? null, stale: false, buffered: [] } } };
    }
    case 'board_loaded': {
      const board = state.boards[action.key];
      if (!board?.loading) return state;
      // Marked stale while in flight (a new stream, too many frames): the reply is kept, and fetched again.
      return { ...state, boards: { ...state.boards, [action.key]: replayBoard({ loading: false, data: action.data, stale: board.stale, buffered: [] }, board.buffered) } };
    }
    case 'board_failed': {
      const board = state.boards[action.key];
      if (!board?.loading) return state;
      return { ...state, boards: { ...state.boards, [action.key]: { ...board, loading: false, error: action.error, buffered: [] } } };
    }
    case 'board_dropped': {
      if (!state.boards[action.key]) return state;
      const boards = { ...state.boards };
      delete boards[action.key];
      return { ...state, boards };
    }
    case 'settings':
      return { ...state, settings: action.settings };
    case 'detail_loaded': {
      const detail = initialWindow(action.detail);
      if (detail.id !== state.selectedId || detail.seq === undefined || detail.seq <= state.detailSeq) return state;
      // A reply started before navigation cannot activate a cached revisit.
      if (state.previousCached && !state.detail) return state;
      if (state.detail?.epoch && detail.epoch && detail.epoch !== state.detail.epoch) return state;
      return { ...withSession(state, detail), detail, previous: null, previousCached: false, detailSeq: detail.seq, bodyVersions: {}, detailGeneration: state.detailGeneration + 1, agents: {}, agentSteps: {}, historyRequest: null, historyItemSeq: {} };
    }
    case 'history_latest':
      if (state.detail?.id !== action.sessionId || !state.detail.recent_items) return state;
      return { ...state, detail: { ...state.detail, ...recentProjection(state.detail), history_index: state.detail.history_index }, historyRequest: null };
    case 'history_loading':
      if (state.detail?.id !== action.sessionId || (action.direction === 'newer' ? state.detail.history_after : state.detail.history_before) !== action.before || !action.before || state.historyRequest?.loading) return state;
      return { ...state, historyRequest: { before: action.before, direction: action.direction, loading: true, buffered: [], bufferedChars: 0 } };
    case 'history_failed':
      if (state.detail?.id !== action.sessionId || !state.historyRequest?.loading || state.historyRequest.before !== action.before) return state;
      return { ...state, historyRequest: { before: action.before, loading: false, error: action.error, buffered: [], bufferedChars: 0 } };
    case 'history_loaded': {
      const detail = state.detail;
      const request = state.historyRequest;
      if (detail?.id !== action.sessionId || (request?.direction === 'newer' ? detail.history_after : detail.history_before) !== action.before || !request?.loading || request.before !== action.before) return state;
      if (detail.epoch && action.page.epoch && action.page.epoch !== detail.epoch) return state;
      let older = detail.representation === 'compact-v1' ? action.page.items : action.page.items.filter(item => !detail.items.some(held => held.id === item.id));
      for (const frame of request.buffered) {
        if (frame.seq <= action.page.seq) continue;
        if (frame.name === 'items_trimmed') older = trimItems(older, frame.items, '');
        else if (frame.name !== 'subagent' && !frame.agent_id) {
          const id = frame.name === 'item' ? frame.item.id : frame.item_id;
          if (!older.some(i => i.id === id)) continue;
          if (frame.name === 'item') older = upsert(older, frame.item);
          else if (frame.name === 'delta') older = appendDelta(older, id, frame.kind, frame.text);
          else older = appendToolOutput(older, id, frame.text);
        }
      }
      const historyItemSeq = { ...state.historyItemSeq };
      const held = new Map([...(detail.recent_items ?? []), ...detail.items].map(item => [item.id, item]));
      const received = new Map(older.map(item => [item.id, item]));
      const reconciled = detail.items.map(item => {
        const replacement = received.get(item.id);
        return (historyItemSeq[item.id] ?? -1) > action.page.seq || replacement && idleSteerEcho(item, replacement) ? item : replacement ?? item;
      });
      older = older.map(item => (historyItemSeq[item.id] ?? -1) > action.page.seq ? held.get(item.id) ?? item : item);
      for (const item of older) historyItemSeq[item.id] = Math.max(historyItemSeq[item.id] ?? -1, action.page.seq);
      const refreshed = new Map(older.map(item => [item.id, item]));
      const recentBase = detail.recent_items?.map(item => {
        const replacement = refreshed.get(item.id);
        return replacement && idleSteerEcho(item, replacement) ? item : replacement ?? item;
      });
      const recentIDs = new Set(recentBase?.map(item => item.id));
      const recent = recentBase ? boundItems(mergeItems(recentBase, older.filter(item => recentIDs.has(item.id)), 'newer'), 'newer', TAIL_ITEMS) : undefined;
      let next = windowPage({ ...detail, items: reconciled, recent_items: recent?.items, recent_before: recent?.droppedBefore.length ? itemCursor(recent.items[0].id) : detail.recent_before }, { ...action.page, items: older }, request.direction ?? 'older');
      if (request.direction === 'newer' && action.page.after === '') {
        const ids = new Set(next.items.map(item => item.id));
        const newerLive = (recent?.items ?? []).filter(item => !ids.has(item.id) && (historyItemSeq[item.id] ?? -1) > action.page.seq);
        const latest = boundItems([...next.items, ...newerLive], 'newer', TAIL_ITEMS);
        next = { ...next, history_after: newerLive.length && next.items.length ? itemCursor(next.items.at(-1)!.id) : next.history_after, recent_items: latest.items, recent_before: latest.items.length && (latest.droppedBefore.length || next.history_before) ? itemCursor(latest.items[0].id) : '' };
      }
      const retained = new Set([...next.items, ...(next.recent_items ?? [])].map(item => item.id));
      for (const id of Object.keys(historyItemSeq)) if (!retained.has(id)) delete historyItemSeq[id];
      return { ...state, detail: next, historyRequest: null, historyItemSeq };
    }
    case 'upsert_session': {
      // An HTTP reply can land after live frames that already carry a newer state.
      const current = state.sessions.find((s) => s.id === action.session.id);
      if (current && Date.parse(action.session.updated_at) < Date.parse(current.updated_at)) return state;
      return withSession(state, action.session);
    }
    case 'remove_session':
      return withoutSession(state, action.id);
    case 'upsert_project':
      return { ...state, projects: upsert(state.projects, action.project) };
    case 'remove_project':
      return withoutProject(state, action.id);
    case 'upsert_interaction': {
      const detail = state.detail;
      if (detail?.id !== action.sessionId) return state;
      return { ...state, detail: { ...detail, interactions: upsert(detail.interactions, action.interaction) } };
    }
    case 'agent_loading':
      if (state.selectedId !== action.sessionId || state.detail?.id !== action.sessionId) return state;
      // What was loaded before stays on screen while the fresh copy is on its way (stale while loading).
      return { ...state, agents: { ...state.agents, [action.agentId]: { loading: true, snapshotSeq: state.agents[action.agentId]?.snapshotSeq ?? -1, items: state.agents[action.agentId]?.items ?? [], buffered: [], bufferedChars: 0 } } };
    case 'agent_loaded': {
      if (state.selectedId !== action.sessionId || !state.agents[action.agentId]?.loading) return state;
      const detail = state.detail;
      const withAgent = detail ? { ...detail, subagents: upsert(detail.subagents, action.subagent) } : detail;
      const loaded = { ...state, detail: withAgent, agents: { ...state.agents, [action.agentId]: { loading: false, snapshotSeq: action.seq, items: action.items, buffered: [], bufferedChars: 0 } } };
      // The response and SSE may arrive in either order. Replay only events
      // after the server's snapshot, retaining their original order.
      return (state.agents[action.agentId]?.buffered ?? []).reduce((next, frame) => withAgentFrame(next, action.agentId, frame), loaded);
    }
    case 'subagents_older': {
      const detail = state.detail;
      if (detail?.id !== action.sessionId || !action.before || detail.subagents_before !== action.before) return state;
      return { ...state, detail: { ...detail, subagents: withOlderSubagents(detail.subagents, action.page.subagents), subagents_before: action.page.before } };
    }
    case 'agent_failed':
      if (state.selectedId !== action.sessionId || !state.agents[action.agentId]?.loading) return state;
      return {
        ...state,
        agents: { ...state.agents, [action.agentId]: { loading: false, snapshotSeq: -1, error: action.error, items: [], buffered: [], bufferedChars: 0 } },
      };
    case 'agent_unloaded': {
      if (state.selectedId !== action.sessionId || !state.agents[action.agentId]) return state;
      const agents = { ...state.agents };
      delete agents[action.agentId];
      return { ...state, agents };
    }
    case 'updates':
      return action.data.reduce((next, data) => reducer(next, { type: 'update', data }), state);
    case 'update': {
      const d = action.data;
      if (d.seq <= state.snapshotSeq) return state;
      if ((d.name === 'history' || d.name === 'items_trimmed') && state.previousCached && state.previous?.id === d.session_id) {
        state = { ...state, previous: null, previousCached: false };
      }
      switch (d.name) {
        case 'session':
          if (d.session.id === state.selectedId) {
            if (d.seq <= state.detailSeq) return state;
            state = { ...state, detailSeq: d.seq };
          }
          return withSession(state, d.session);
        case 'session_removed':
          return withoutSession(state, d.session_id);
        case 'project':
          return { ...state, projects: upsert(state.projects, d.project) };
        case 'project_removed':
          return withoutProject(state, d.project_id);
        case 'settings':
          return { ...state, settings: d.settings };
        case 'usage':
          return { ...state, usage: d.usage };
        case 'board':
          return withBoardFrame(state, d);
        case 'board_job': {
          const jobs = Object.entries({ ...state.boardJobs, [d.job_id]: d }).slice(-MAX_BOARD_JOBS);
          return { ...state, boardJobs: Object.fromEntries(jobs) };
        }
      }
      const detail = state.detail;
      if (d.session_id !== detail?.id || d.seq <= state.detailSeq) return state;
      state = { ...state, detailSeq: d.seq };
      if (state.historyRequest?.loading && (d.name === 'items_trimmed' || ((d.name === 'item' || d.name === 'delta' || d.name === 'tool_output') && !d.agent_id))) {
        const request = state.historyRequest;
        const size = frameBytes(d);
        state = { ...state, historyRequest: request.buffered.length >= MAX_BUFFERED_FRAMES || bufferedBytes(state) + size > MAX_BUFFERED_CHARS
          ? { ...request, loading: false, error: 'History changed too quickly. Scroll up to retry.', buffered: [], bufferedChars: 0 }
          : { ...request, buffered: [...request.buffered, d], bufferedChars: request.bufferedChars + size } };
      }
      if ((d.name === 'item' || d.name === 'delta' || d.name === 'tool_output') && !d.agent_id) {
        const id = d.name === 'item' ? d.item.id : d.item_id;
        if (d.seq <= (state.historyItemSeq[id] ?? -1)) return state;
        // Updates to items not fetched yet belong to a future history page.
        if (detail.history_before !== undefined && !detail.items.some(i => i.id === id) && !detail.recent_items?.some(i => i.id === id) && !detail.history_index?.some(i => i.id === id) && !(d.name === 'item' && d.append)) return state;
        state = { ...state, historyItemSeq: { ...state.historyItemSeq, [id]: d.seq } };
      }
      switch (d.name) {
        case 'history':
          return { ...state, bodyVersions: {}, detailGeneration: state.detailGeneration + 1, detail: initialWindow({ ...detail, seq: d.seq, history: d.history, history_reason: d.history_reason, history_before: d.history_before, history_truncated: d.history_truncated, items: d.items, subagents: d.subagents, subagents_before: d.subagents_before }), agents: {}, agentSteps: {}, historyRequest: null, historyItemSeq: {} };
        case 'item':
          if (d.item.compact) state = { ...state, bodyVersions: { ...state.bodyVersions, [JSON.stringify([d.agent_id ?? '', d.item.id])]: d.seq } };
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return withWindow(state, liveWindow(detail, detail.representation === 'compact-v1' && !detail.items.some(item => item.id === d.item.id) && (detail.history_after || !d.append) ? detail.items : upsertTranscriptItem(detail.items, d.item), d.append || detail.recent_items?.some(item => item.id === d.item.id) ? upsertTranscriptItem(detail.recent_items ?? detail.items, d.item) : detail.recent_items ?? detail.items, [d.item]));
        case 'delta':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return withWindow(state, liveWindow(detail, detail.representation !== 'compact-v1' || detail.items.some(item => item.id === d.item_id) ? appendDelta(detail.items, d.item_id, d.kind, d.text) : detail.items, detail.recent_items?.some(item => item.id === d.item_id) ? appendDelta(detail.recent_items, d.item_id, d.kind, d.text) : detail.recent_items ?? detail.items));
        case 'tool_output':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return withWindow(state, liveWindow(detail, appendToolOutput(detail.items, d.item_id, d.text), appendToolOutput(detail.recent_items ?? detail.items, d.item_id, d.text)));
        case 'items_trimmed': {
          const historyItemSeq = { ...state.historyItemSeq };
          const bodyVersions = { ...state.bodyVersions };
          for (const item of d.items) delete bodyVersions[JSON.stringify([item.agent_id ?? '', item.id])];
          for (const item of d.items) if (!item.agent_id) delete historyItemSeq[item.id];
          state = { ...state, bodyVersions, historyItemSeq, detail: { ...detail, history_truncated: true, items: trimItems(detail.items, d.items, ''), recent_items: detail.recent_items ? trimItems(detail.recent_items, d.items, '') : undefined, history_index: detail.history_index ? trimItems(detail.history_index, d.items, '') : undefined } };
          for (const agentId of new Set(d.items.flatMap((it) => it.agent_id ? [it.agent_id] : []))) {
            state = withAgentFrame(state, agentId, d);
          }
          return state;
        }
        case 'interaction':
          return { ...state, detail: { ...detail, interactions: upsert(detail.interactions, d.interaction) } };
        case 'queue':
          return { ...state, detail: { ...detail, queue: d.queue, queue_paused: d.paused } };
        case 'submission':
          return { ...state, detail: { ...detail, last_submission: d.submission } };
        case 'subagent':
          return withAgentFrame(state, d.subagent.id, d);
        case 'turn_timing':
          return { ...state, detail: { ...detail, turn_timings: upsert(detail.turn_timings ?? [], d.turn_timing) } };
        case 'background_tasks':
          return { ...state, detail: { ...detail, background_tasks: d.background_tasks } };
      }
    }
  }
}

/**
 * Frames sent while no stream was open are lost: a new snapshot's Board revisions say which
 * followed Boards moved meanwhile. One whose loaded revision differs, that is still loading, or
 * that the snapshot does not list (or a snapshot without revisions at all) is fetched again.
 */
function staleSince(boards: Record<string, BoardState>, revisions: Record<string, number> | undefined): Record<string, BoardState> {
  return Object.fromEntries(
    Object.entries(boards).map(([key, board]) => {
      const revision = revisions?.[key === 'unassigned' ? '' : key];
      const current = revision !== undefined && !board.loading && board.data?.revision === revision;
      return [key, current || board.stale ? board : { ...board, stale: true }];
    }),
  );
}

/** A `board` frame on the Board it names: buffered while that Board loads, applied by revision, a gap marks it stale. Boards not followed ignore it. */
function withBoardFrame(state: State, frame: BoardFrame): State {
  const key = boardKey(frame.project_id);
  const board = state.boards[key];
  if (!board) return state;
  if (board.loading) {
    // Already to be fetched again once this reply lands: nothing to keep.
    if (board.stale) return state;
    const next = board.buffered.length >= MAX_BOARD_BUFFER ? { ...board, stale: true, buffered: [] } : { ...board, buffered: [...board.buffered, frame] };
    return { ...state, boards: { ...state.boards, [key]: next } };
  }
  return { ...state, boards: { ...state.boards, [key]: replayBoard(board, [frame]) } };
}

function replayBoard(board: BoardState, frames: readonly BoardFrame[]): BoardState {
  let next = board;
  for (const frame of frames) {
    if (!next.data || next.stale) break;
    const outcome = applyBoardFrame(next.data, frame);
    if (outcome.kind === 'gap') next = { ...next, stale: true };
    else if (outcome.kind === 'applied') next = { ...next, data: outcome.data, error: undefined };
  }
  return next;
}

function appendDelta(items: Item[], itemId: string, kind: ItemKind, text: string, agentId?: string): Item[] {
  const out = items.slice();
  const i = out.findIndex((x) => x.id === itemId);
  if (i < 0) out.push({ id: itemId, kind, text, time: new Date().toISOString(), ...(agentId ? { agent_id: agentId } : {}) });
  else out[i] = { ...out[i], text: (out[i].text ?? '') + text };
  return out;
}

function appendToolOutput(items: Item[], itemId: string, text: string): Item[] {
  const i = items.findIndex((item) => item.id === itemId);
  const item = items[i];
  if (!item?.tool || !text) return items;
  const out = items.slice();
  out[i] = { ...item, tool: { ...item.tool, output: (item.tool.output ?? '') + text } };
  return out;
}

/**
 * Routes subagent updates and buffers them during the transcript fetch. Metadata
 * stays live even before the transcript is opened. Frames already in the fetched
 * snapshot are ignored, including those delivered after the response.
 */
function withAgentFrame(state: State, agentId: string, frame: Buffered): State {
  const a = state.agents[agentId];
  if (a && frame.seq <= a.snapshotSeq) return state;
  if (frame.name === 'subagent' && state.detail) {
    state = { ...state, detail: { ...state.detail, subagents: upsert(state.detail.subagents, frame.subagent) } };
  }
  state = withAgentStep(state, agentId, frame);
  if (!a || a.error) return state;
  if (a.loading) {
    const bufferedChars = a.bufferedChars + frameBytes(frame);
    if (a.buffered.length >= MAX_BUFFERED_FRAMES || bufferedBytes(state) + frameBytes(frame) > MAX_BUFFERED_CHARS) {
      return { ...state, agents: { ...state.agents, [agentId]: { loading: false, snapshotSeq: -1, items: [], buffered: [], bufferedChars: 0, error: 'Too much output arrived while loading. Retry to load the latest transcript.' } } };
    }
    const items = frame.name === 'items_trimmed' ? applyFrame(a.items, frame, agentId) : a.items;
    return { ...state, agents: { ...state.agents, [agentId]: { ...a, items, buffered: [...a.buffered, frame], bufferedChars } } };
  }
  return { ...state, agents: { ...state.agents, [agentId]: { ...a, items: applyFrame(a.items, frame, agentId) } } };
}

/** Records what a subagent is doing now: a new item, or a streamed one whose kind changed. Deltas of the same item change nothing. */
function withAgentStep(state: State, agentId: string, frame: Buffered): State {
  const prev = state.agentSteps[agentId];
  let step: Item | undefined;
  if (frame.name === 'item') {
    const { id, kind, time, tool } = frame.item;
    step = { id, kind, time, agent_id: agentId, ...(tool ? { tool: { name: tool.name, title: tool.title, status: tool.status, input: tool.input?.slice(0, 500) } } : {}) };
    if (prev?.id === id && prev.kind === kind && prev.tool?.status === step.tool?.status) return state;
  } else if (frame.name === 'delta') {
    if (prev?.id === frame.item_id && prev.kind === frame.kind) return state;
    step = { id: frame.item_id, kind: frame.kind, time: new Date().toISOString(), agent_id: agentId };
  }
  return step ? { ...state, agentSteps: { ...state.agentSteps, [agentId]: step } } : state;
}

function applyFrame(items: Item[], frame: Buffered, agentId: string): Item[] {
  if (frame.name === 'item') return upsert(items, frame.item);
  if (frame.name === 'delta') return appendDelta(items, frame.item_id, frame.kind, frame.text, agentId);
  if (frame.name === 'tool_output') return appendToolOutput(items, frame.item_id, frame.text);
  if (frame.name === 'items_trimmed') return trimItems(items, frame.items, agentId);
  return items;
}

function trimItems(items: Item[], removed: { id: string; agent_id?: string }[], agentId: string): Item[] {
  const ids = new Set(removed.filter((it) => (it.agent_id ?? '') === agentId).map((it) => it.id));
  return ids.size ? items.filter((it) => !ids.has(it.id)) : items;
}

function upsert<T extends { id: string }>(list: T[], v: T): T[] {
  const i = list.findIndex((x) => x.id === v.id);
  if (i < 0) return [...list, v];
  const out = list.slice();
  out[i] = v;
  return out;
}

function withSession(state: State, s: SessionSummary): State {
  const detail = state.detail;
  // state_detail, last_model, context and usage are omitempty on the wire: an absent key must clear the old value.
  const merged =
    detail?.id === s.id ? { ...detail, ...s, state_detail: s.state_detail, last_model: s.last_model, stage: s.stage, context: s.context, usage: s.usage, execution: s.execution } : detail;
  return { ...state, sessions: upsert(state.sessions, s), detail: merged };
}

function withoutSession(state: State, id: string): State {
  const sessions = state.sessions.filter((s) => s.id !== id);
  const previous = state.previous?.id === id ? null : state.previous;
  if (state.selectedId === id) return { ...state, sessions, selectedId: null, detail: null, previous: null, previousCached: false, agents: {}, historyRequest: null, historyItemSeq: {} };
  return { ...state, sessions, previous, previousCached: !!previous && state.previousCached };
}

function withoutProject(state: State, id: string): State {
  // The server also sends session_removed for each of its tasks; dropping them here keeps
  // the rail consistent if those frames were coalesced away.
  const gone = state.sessions.filter((s) => s.project_id === id).map((s) => s.id);
  const next = gone.reduce((st, sid) => withoutSession(st, sid), state);
  return { ...next, projects: next.projects.filter((p) => p.id !== id) };
}

function withWindow(state: State, detail: SessionDetail): State {
  if (detail.representation !== 'compact-v1') return { ...state, detail };
  const retained = new Set([...detail.items, ...(detail.recent_items ?? [])].map(item => item.id));
  const historyItemSeq = Object.fromEntries(Object.entries(state.historyItemSeq).filter(([id]) => retained.has(id)));
  const bodyVersions = Object.fromEntries(Object.entries(state.bodyVersions).slice(-2000));
  return { ...state, detail, historyItemSeq, bodyVersions };
}
