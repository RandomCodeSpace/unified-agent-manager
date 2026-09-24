import type { Interaction, SessionDetail, SessionSummary, SnapshotData, UpdateData } from './api';

export type Connection = 'connecting' | 'connected' | 'reconnecting' | 'offline';

export interface State {
  sessions: SessionSummary[];
  selectedId: string | null;
  /** Detail of the selected session, from the latest snapshot; null until it arrives. */
  detail: SessionDetail | null;
  /** seq of the latest snapshot; updates with seq <= this are ignored. -1 before any snapshot. */
  snapshotSeq: number;
  connection: Connection;
}

export const initialState: State = {
  sessions: [],
  selectedId: null,
  detail: null,
  snapshotSeq: -1,
  connection: 'connecting',
};

export type Action =
  | { type: 'select'; id: string | null }
  | { type: 'connection'; status: Connection }
  | { type: 'snapshot'; data: SnapshotData }
  | { type: 'update'; data: UpdateData }
  | { type: 'upsert_session'; session: SessionSummary }
  | { type: 'upsert_interaction'; sessionId: string; interaction: Interaction };

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'select':
      if (action.id === state.selectedId) return state;
      return { ...state, selectedId: action.id, detail: null };
    case 'connection':
      return state.connection === action.status ? state : { ...state, connection: action.status };
    case 'snapshot': {
      const { seq, sessions, session } = action.data;
      const detail = session && session.id === state.selectedId ? session : null;
      return { ...state, sessions, detail, snapshotSeq: seq, connection: 'connected' };
    }
    case 'upsert_session':
      return withSession(state, action.session);
    case 'upsert_interaction': {
      const detail = state.detail;
      if (!detail || detail.id !== action.sessionId) return state;
      return { ...state, detail: { ...detail, interactions: upsert(detail.interactions, action.interaction) } };
    }
    case 'update': {
      const d = action.data;
      if (d.seq <= state.snapshotSeq) return state;
      if (d.name === 'session') return withSession(state, d.session);
      const detail = state.detail;
      if (!detail || d.session_id !== detail.id) return state;
      switch (d.name) {
        case 'item':
          return { ...state, detail: { ...detail, items: upsert(detail.items, d.item) } };
        case 'delta': {
          const items = detail.items.slice();
          const i = items.findIndex((x) => x.id === d.item_id);
          if (i < 0) items.push({ id: d.item_id, kind: d.kind, text: d.text, time: new Date().toISOString() });
          else items[i] = { ...items[i], text: (items[i].text ?? '') + d.text };
          return { ...state, detail: { ...detail, items } };
        }
        case 'interaction':
          return { ...state, detail: { ...detail, interactions: upsert(detail.interactions, d.interaction) } };
        case 'submission':
          return { ...state, detail: { ...detail, last_submission: d.submission } };
      }
    }
  }
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
  // state_detail is omitempty on the wire: an absent key must clear the old value.
  const merged = detail && detail.id === s.id ? { ...detail, ...s, state_detail: s.state_detail } : detail;
  return { ...state, sessions: upsert(state.sessions, s), detail: merged };
}
