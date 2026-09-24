import type { AccountUsage, Interaction, Item, ItemKind, Project, SessionDetail, SessionSummary, Settings, SnapshotData, SubagentDetail, UpdateData } from './api';

export type Connection = 'connecting' | 'connected' | 'reconnecting' | 'offline';

/** The service's defaults, in force until its snapshot arrives (and on a service too old to send one). */
export const DEFAULT_SETTINGS: Settings = { send_default: 'steer' };

/** A frame that arrived for a subagent while its transcript fetch was in flight. */
export type Buffered = Extract<UpdateData, { name: 'item' | 'delta' | 'subagent' }>;

/** A subagent transcript, present once its block was expanded. Live frames with that agent_id land here. */
export interface AgentTranscript {
  loading: boolean;
  /** Events through this sequence are already included in the fetched transcript and metadata. */
  snapshotSeq: number;
  error?: string;
  items: Item[];
  buffered: Buffered[];
}

export interface State {
  projects: Project[];
  sessions: SessionSummary[];
  selectedId: string | null;
  /** Detail of the selected session, from the latest snapshot; null until it arrives. */
  detail: SessionDetail | null;
  /** The Task that was on screen before the selection changed, kept until the new detail arrives. Frozen: no frames apply to it. */
  previous: SessionDetail | null;
  /** seq of the latest snapshot; updates with seq <= this are ignored. -1 before any snapshot. */
  snapshotSeq: number;
  /** Newest selected-task frame or detail response, used to reject stale reloads. */
  detailSeq: number;
  connection: Connection;
  agents: Record<string, AgentTranscript>;
  /** The service's settings, from the snapshot and `settings` frames; the defaults until the first snapshot. */
  settings: Settings;
  /** The account quotas, from the snapshot and `usage` frames; null until a snapshot carries them (#188). */
  usage: AccountUsage | null;
}

export const initialState: State = {
  projects: [],
  sessions: [],
  selectedId: null,
  detail: null,
  previous: null,
  snapshotSeq: -1,
  detailSeq: -1,
  connection: 'connecting',
  agents: {},
  settings: DEFAULT_SETTINGS,
  usage: null,
};

export type Action =
  | { type: 'select'; id: string | null }
  | { type: 'connection'; status: Connection }
  | { type: 'snapshot'; data: SnapshotData }
  | { type: 'update'; data: UpdateData }
  | { type: 'settings'; settings: Settings }
  | { type: 'detail_loaded'; detail: SessionDetail }
  /** A session from an HTTP reply; ignored when the live state is already newer (by updated_at). */
  | { type: 'upsert_session'; session: SessionSummary }
  | { type: 'remove_session'; id: string }
  | { type: 'upsert_project'; project: Project }
  | { type: 'remove_project'; id: string }
  | { type: 'upsert_interaction'; sessionId: string; interaction: Interaction }
  | { type: 'agent_loading'; agentId: string }
  | ({ type: 'agent_loaded'; agentId: string } & SubagentDetail)
  | { type: 'agent_failed'; agentId: string; error: string };

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'select':
      if (action.id === state.selectedId) return state;
      return { ...state, selectedId: action.id, detail: null, previous: action.id ? (state.detail ?? state.previous) : null, detailSeq: -1, agents: {} };
    case 'connection': {
      if (state.connection === action.status) return state;
      const detail = action.status !== 'connected' && state.detail
        ? { ...state.detail, ...(state.detail.turn_timings ? { turn_timings: state.detail.turn_timings.map((timing) => timing.state === 'working' ? { ...timing, state: 'unknown' as const } : timing) } : {}), ...(state.detail.background_tasks ? { background_tasks: { ...state.detail.background_tasks, known: false } } : {}), ...(state.detail.execution ? { execution: { ...state.detail.execution, known: false } } : {}) }
        : state.detail;
      return { ...state, connection: action.status, detail };
    }
    case 'snapshot': {
      const { seq, sessions, session, projects, settings, usage } = action.data;
      const detail = session && session.id === state.selectedId ? session : null;
      // A fresh snapshot invalidates subagent transcripts loaded under the old stream;
      // expanded blocks reload them (see SubagentBlock).
      return {
        ...state,
        projects: projects ?? [],
        sessions,
        detail,
        previous: null,
        snapshotSeq: seq,
        detailSeq: seq,
        connection: 'connected',
        agents: {},
        settings: settings ?? DEFAULT_SETTINGS,
        usage: usage ?? null,
      };
    }
    case 'settings':
      return { ...state, settings: action.settings };
    case 'detail_loaded': {
      const detail = action.detail;
      if (detail.id !== state.selectedId || detail.seq === undefined || detail.seq <= state.detailSeq) return state;
      return { ...withSession(state, detail), detail, previous: null, detailSeq: detail.seq, agents: {} };
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
      if (!detail || detail.id !== action.sessionId) return state;
      return { ...state, detail: { ...detail, interactions: upsert(detail.interactions, action.interaction) } };
    }
    case 'agent_loading':
      return { ...state, agents: { ...state.agents, [action.agentId]: { loading: true, snapshotSeq: state.agents[action.agentId]?.snapshotSeq ?? -1, items: [], buffered: [] } } };
    case 'agent_loaded': {
      const detail = state.detail;
      const withAgent = detail ? { ...detail, subagents: upsert(detail.subagents, action.subagent) } : detail;
      const loaded = { ...state, detail: withAgent, agents: { ...state.agents, [action.agentId]: { loading: false, snapshotSeq: action.seq, items: action.items, buffered: [] } } };
      // The response and SSE may arrive in either order. Replay only events
      // after the server's snapshot, retaining their original order.
      return (state.agents[action.agentId]?.buffered ?? []).reduce((next, frame) => withAgentFrame(next, action.agentId, frame), loaded);
    }
    case 'agent_failed':
      return {
        ...state,
        agents: { ...state.agents, [action.agentId]: { loading: false, snapshotSeq: -1, error: action.error, items: [], buffered: [] } },
      };
    case 'update': {
      const d = action.data;
      if (d.seq <= state.snapshotSeq) return state;
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
      }
      const detail = state.detail;
      if (!detail || d.session_id !== detail.id || d.seq <= state.detailSeq) return state;
      state = { ...state, detailSeq: d.seq };
      switch (d.name) {
        case 'history':
          return { ...state, detail: { ...detail, seq: d.seq, history: d.history, history_reason: d.history_reason, history_truncated: d.history_truncated, items: d.items, subagents: d.subagents }, agents: {} };
        case 'item':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return { ...state, detail: { ...detail, items: upsert(detail.items, d.item) } };
        case 'delta':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return { ...state, detail: { ...detail, items: appendDelta(detail.items, d.item_id, d.kind, d.text) } };
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

function appendDelta(items: Item[], itemId: string, kind: ItemKind, text: string, agentId?: string): Item[] {
  const out = items.slice();
  const i = out.findIndex((x) => x.id === itemId);
  if (i < 0) out.push({ id: itemId, kind, text, time: new Date().toISOString(), ...(agentId ? { agent_id: agentId } : {}) });
  else out[i] = { ...out[i], text: (out[i].text ?? '') + text };
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
  if (!a) return state;
  if (a.loading) return { ...state, agents: { ...state.agents, [agentId]: { ...a, buffered: [...a.buffered, frame] } } };
  return { ...state, agents: { ...state.agents, [agentId]: { ...a, items: applyFrame(a.items, frame, agentId) } } };
}

function applyFrame(items: Item[], frame: Buffered, agentId: string): Item[] {
  if (frame.name === 'item') return upsert(items, frame.item);
  if (frame.name === 'delta') return appendDelta(items, frame.item_id, frame.kind, frame.text, agentId);
  return items;
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
    detail && detail.id === s.id ? { ...detail, ...s, state_detail: s.state_detail, last_model: s.last_model, stage: s.stage, context: s.context, usage: s.usage, execution: s.execution } : detail;
  return { ...state, sessions: upsert(state.sessions, s), detail: merged };
}

function withoutSession(state: State, id: string): State {
  const sessions = state.sessions.filter((s) => s.id !== id);
  const previous = state.previous?.id === id ? null : state.previous;
  if (state.selectedId === id) return { ...state, sessions, selectedId: null, detail: null, previous: null, agents: {} };
  return { ...state, sessions, previous };
}

function withoutProject(state: State, id: string): State {
  // The server also sends session_removed for each of its tasks; dropping them here keeps
  // the rail consistent if those frames were coalesced away.
  const gone = state.sessions.filter((s) => s.project_id === id).map((s) => s.id);
  const next = gone.reduce((st, sid) => withoutSession(st, sid), state);
  return { ...next, projects: next.projects.filter((p) => p.id !== id) };
}
