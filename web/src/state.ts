import type { Interaction, Item, Project, SessionDetail, SessionSummary, SnapshotData, Subagent, UpdateData } from './api';

export type Connection = 'connecting' | 'connected' | 'reconnecting' | 'offline';

/** A subagent transcript, present once its block was expanded. Live frames with that agent_id land here. */
export interface AgentTranscript {
  loading: boolean;
  error?: string;
  items: Item[];
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
  | { type: 'agent_loaded'; agentId: string; subagent: Subagent; items: Item[] }
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
      return { ...state, agents: { ...state.agents, [action.agentId]: { loading: true, items: [] } } };
    case 'agent_loaded': {
      // Frames that arrived while the fetch was in flight were buffered under the id;
      // the fetched transcript wins for items in both, buffered-only items are kept.
      const buffered = state.agents[action.agentId]?.items ?? [];
      let items = action.items;
      for (const b of buffered) if (!items.some((x) => x.id === b.id)) items = [...items, b];
      const detail = state.detail;
      const withAgent = detail ? { ...detail, subagents: upsert(detail.subagents, action.subagent) } : detail;
      return { ...state, detail: withAgent, agents: { ...state.agents, [action.agentId]: { loading: false, items } } };
    }
    case 'agent_failed':
      return { ...state, agents: { ...state.agents, [action.agentId]: { loading: false, error: action.error, items: [] } } };
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
          if (d.agent_id) return withAgentItems(state, d.agent_id, (items) => upsert(items, d.item));
          return { ...state, detail: { ...detail, items: upsert(detail.items, d.item) } };
        case 'delta': {
          const apply = (items: Item[]) => appendDelta(items, d.item_id, d.kind, d.text, d.agent_id);
          if (d.agent_id) return withAgentItems(state, d.agent_id, apply);
          return { ...state, detail: { ...detail, items: apply(detail.items) } };
        }
        case 'interaction':
          return { ...state, detail: { ...detail, interactions: upsert(detail.interactions, d.interaction) } };
        case 'submission':
          return { ...state, detail: { ...detail, last_submission: d.submission } };
        case 'subagent':
          return { ...state, detail: { ...detail, subagents: upsert(detail.subagents, d.subagent) } };
      }
    }
  }
}

function appendDelta(items: Item[], itemId: string, kind: Item['kind'], text: string, agentId?: string): Item[] {
  const out = items.slice();
  const i = out.findIndex((x) => x.id === itemId);
  if (i < 0) out.push({ id: itemId, kind, text, time: new Date().toISOString(), agent_id: agentId });
  else out[i] = { ...out[i], text: (out[i].text ?? '') + text };
  return out;
}

/** Applies fn to a subagent transcript if that transcript is loaded (or loading); otherwise the frame is dropped
 *  and the transcript is fetched whole when the block is expanded. */
function withAgentItems(state: State, agentId: string, fn: (items: Item[]) => Item[]): State {
  const a = state.agents[agentId];
  if (!a) return state;
  return { ...state, agents: { ...state.agents, [agentId]: { ...a, items: fn(a.items) } } };
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
    detail && detail.id === s.id ? { ...detail, ...s, state_detail: s.state_detail, last_model: s.last_model } : detail;
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
  const next = withSessionsOf(state, id);
  return { ...next, projects: next.projects.filter((p) => p.id !== id) };
}

function withSessionsOf(state: State, projectId: string): State {
  const gone = state.sessions.filter((s) => s.project_id === projectId).map((s) => s.id);
  return gone.reduce((st, id) => withoutSession(st, id), state);
}
