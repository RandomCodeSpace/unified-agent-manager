import type { Interaction, Item, ItemKind, Project, SessionDetail, SessionSummary, SnapshotData, SubagentDetail, UpdateData } from './api';

export type Connection = 'connecting' | 'connected' | 'reconnecting' | 'offline';

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
  /** seq of the latest snapshot; updates with seq <= this are ignored. -1 before any snapshot. */
  snapshotSeq: number;
  connection: Connection;
  agents: Record<string, AgentTranscript>;
}

export const initialState: State = {
  projects: [],
  sessions: [],
  selectedId: null,
  detail: null,
  snapshotSeq: -1,
  connection: 'connecting',
  agents: {},
};

export type Action =
  | { type: 'select'; id: string | null }
  | { type: 'connection'; status: Connection }
  | { type: 'snapshot'; data: SnapshotData }
  | { type: 'update'; data: UpdateData }
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
      return { ...state, selectedId: action.id, detail: null, agents: {} };
    case 'connection':
      return state.connection === action.status ? state : { ...state, connection: action.status };
    case 'snapshot': {
      const { seq, sessions, session, projects } = action.data;
      const detail = session && session.id === state.selectedId ? session : null;
      // A fresh snapshot invalidates subagent transcripts loaded under the old stream;
      // expanded blocks reload them (see SubagentBlock).
      return {
        ...state,
        projects: projects ?? [],
        sessions,
        detail,
        snapshotSeq: seq,
        connection: 'connected',
        agents: {},
      };
    }
    case 'upsert_session':
      return withSession(state, action.session);
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
          return withSession(state, d.session);
        case 'session_removed':
          return withoutSession(state, d.session_id);
        case 'project':
          return { ...state, projects: upsert(state.projects, d.project) };
        case 'project_removed':
          return withoutProject(state, d.project_id);
      }
      const detail = state.detail;
      if (!detail || d.session_id !== detail.id) return state;
      switch (d.name) {
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
  // state_detail and last_model are omitempty on the wire: an absent key must clear the old value.
  const merged =
    detail && detail.id === s.id ? { ...detail, ...s, state_detail: s.state_detail, last_model: s.last_model, stage: s.stage, context: s.context } : detail;
  return { ...state, sessions: upsert(state.sessions, s), detail: merged };
}

function withoutSession(state: State, id: string): State {
  const sessions = state.sessions.filter((s) => s.id !== id);
  if (state.selectedId === id) return { ...state, sessions, selectedId: null, detail: null, agents: {} };
  return { ...state, sessions };
}

function withoutProject(state: State, id: string): State {
  // The server also sends session_removed for each of its tasks; dropping them here keeps
  // the rail consistent if those frames were coalesced away.
  const gone = state.sessions.filter((s) => s.project_id === id).map((s) => s.id);
  const next = gone.reduce((st, sid) => withoutSession(st, sid), state);
  return { ...next, projects: next.projects.filter((p) => p.id !== id) };
}
