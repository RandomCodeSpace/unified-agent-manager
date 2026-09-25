import type { AccountUsage, Interaction, Item, ItemKind, Project, SessionDetail, SessionSummary, Settings, SnapshotData, SubagentDetail, UpdateData } from './api';

export type Connection = 'connecting' | 'connected' | 'reconnecting' | 'offline';

/** The service's defaults, in force until its snapshot arrives (and on a service too old to send one). */
export const DEFAULT_SETTINGS: Settings = { send_default: 'steer' };

/** A frame that arrived for a subagent while its transcript fetch was in flight. */
export type Buffered = Extract<UpdateData, { name: 'item' | 'delta' | 'tool_output' | 'subagent' | 'items_trimmed' }>;

// A stalled HTTP fetch must not retain an unlimited stream. Retry from a fresh
// snapshot on overflow; dropping individual deltas would corrupt the transcript.
const MAX_BUFFERED_FRAMES = 256;
const MAX_BUFFERED_CHARS = 4 * 1024 * 1024;

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

export interface State {
  /** Whether a snapshot has arrived at least once: until then the Projects and Tasks are unknown, not absent (loading, never the empty state). */
  loaded: boolean;
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
  /** Each subagent's latest step, kept from live frames even while its transcript is not open: kind and call only, never output. */
  agentSteps: Record<string, Item>;
  /** The service's settings, from the snapshot and `settings` frames; the defaults until the first snapshot. */
  settings: Settings;
  /** The account quotas, from the snapshot and `usage` frames; null until a snapshot carries them (#188). */
  usage: AccountUsage | null;
}

export const initialState: State = {
  loaded: false,
  projects: [],
  sessions: [],
  selectedId: null,
  detail: null,
  previous: null,
  snapshotSeq: -1,
  detailSeq: -1,
  connection: 'connecting',
  agents: {},
  agentSteps: {},
  settings: DEFAULT_SETTINGS,
  usage: null,
};

export type Action =
  | { type: 'select'; id: string | null }
  | { type: 'connection'; status: Connection }
  | { type: 'snapshot'; data: SnapshotData }
  | { type: 'update'; data: UpdateData }
  /** Frames batched by the stream handler (deltas, one animation frame's worth), applied in order in one render. */
  | { type: 'updates'; data: UpdateData[] }
  | { type: 'settings'; settings: Settings }
  | { type: 'detail_loaded'; detail: SessionDetail }
  /** A session from an HTTP reply; ignored when the live state is already newer (by updated_at). */
  | { type: 'upsert_session'; session: SessionSummary }
  | { type: 'remove_session'; id: string }
  | { type: 'upsert_project'; project: Project }
  | { type: 'remove_project'; id: string }
  | { type: 'upsert_interaction'; sessionId: string; interaction: Interaction }
  | { type: 'agent_loading'; sessionId: string; agentId: string }
  | ({ type: 'agent_loaded'; sessionId: string; agentId: string } & SubagentDetail)
  | { type: 'agent_failed'; sessionId: string; agentId: string; error: string }
  | { type: 'agent_unloaded'; sessionId: string; agentId: string };

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'select':
      if (action.id === state.selectedId) return state;
      return { ...state, selectedId: action.id, detail: null, previous: action.id ? (state.detail ?? state.previous) : null, detailSeq: -1, agents: {}, agentSteps: {} };
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
        loaded: true,
        projects: projects ?? [],
        sessions,
        detail,
        previous: null,
        snapshotSeq: seq,
        detailSeq: seq,
        connection: 'connected',
        agents: {},
        agentSteps: {},
        settings: settings ?? DEFAULT_SETTINGS,
        usage: usage ?? null,
      };
    }
    case 'settings':
      return { ...state, settings: action.settings };
    case 'detail_loaded': {
      const detail = action.detail;
      if (detail.id !== state.selectedId || detail.seq === undefined || detail.seq <= state.detailSeq) return state;
      return { ...withSession(state, detail), detail, previous: null, detailSeq: detail.seq, agents: {}, agentSteps: {} };
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
          return { ...state, detail: { ...detail, seq: d.seq, history: d.history, history_reason: d.history_reason, history_truncated: d.history_truncated, items: d.items, subagents: d.subagents }, agents: {}, agentSteps: {} };
        case 'item':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return { ...state, detail: { ...detail, items: upsert(detail.items, d.item) } };
        case 'delta':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return { ...state, detail: { ...detail, items: appendDelta(detail.items, d.item_id, d.kind, d.text) } };
        case 'tool_output':
          if (d.agent_id) return withAgentFrame(state, d.agent_id, d);
          return { ...state, detail: { ...detail, items: appendToolOutput(detail.items, d.item_id, d.text) } };
        case 'items_trimmed': {
          state = { ...state, detail: { ...detail, history_truncated: true, items: trimItems(detail.items, d.items, '') } };
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
    const bufferedChars = a.bufferedChars + JSON.stringify(frame).length;
    if (a.buffered.length >= MAX_BUFFERED_FRAMES || bufferedChars > MAX_BUFFERED_CHARS) {
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
    if (prev && prev.id === id && prev.kind === kind && prev.tool?.status === step.tool?.status) return state;
  } else if (frame.name === 'delta') {
    if (prev && prev.id === frame.item_id && prev.kind === frame.kind) return state;
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
