// The planner's pure logic (ADR 0005): derived container status and progress (§2), applying
// `board` frames by revision (§15), the outline the Tree draws, and the Map's tree layout.
// No runtime imports, so the node test suite loads it as it is.

import type { BoardData, BoardFrame, BoardRequest, Card, CardKind, CardProgress, CardStatus } from '../api';

/** The Board's columns, in order; `cancelled` joins them only when shown. */
export const BOARD_COLUMNS: readonly CardStatus[] = ['planned', 'todo', 'doing', 'done'];

export const STATUS_LABEL: Record<CardStatus, string> = { planned: 'Planned', todo: 'To do', doing: 'Doing', done: 'Done', cancelled: 'Cancelled' };

export const KIND_LABEL: Record<CardKind, string> = { epic: 'Epic', story: 'Story', subtask: 'Subtask' };

type Leaf = Pick<Card, 'status' | 'confirmed' | 'held_by' | 'pending_requests'>;

/**
 * A container's status and progress from the leaves in its subtree (§2). Only confirmed
 * leaves count, except that a hold anywhere makes it doing; a held leaf or a pending request
 * keeps it from done, and with no confirmed leaves it is never done.
 */
export function deriveContainer(leaves: readonly Leaf[]): { status: CardStatus; progress: CardProgress } {
  const confirmed = leaves.filter((l) => l.confirmed);
  const live = confirmed.filter((l) => l.status !== 'cancelled');
  const done = live.filter((l) => l.status === 'done').length;
  const proposed = leaves.filter((l) => !l.confirmed && l.status !== 'cancelled').length;
  const progress = { done, total: live.length, proposed };
  const held = leaves.some((l) => !!l.held_by);
  const pending = leaves.some((l) => l.pending_requests > 0);
  let status: CardStatus;
  if (confirmed.length === 0) status = held ? 'doing' : 'planned';
  else if (live.length === 0) status = held ? 'doing' : 'cancelled';
  else if (done === live.length) status = held || pending ? 'doing' : 'done';
  else status = held || done > 0 ? 'doing' : 'planned';
  return { status, progress };
}

/** Children by parent id (`''` for the root), in rank order, then by `#seq`. */
export function childIndex(cards: readonly Card[]): Map<string, Card[]> {
  const index = new Map<string, Card[]>();
  for (const c of cards) {
    const key = c.parent_id ?? '';
    const list = index.get(key);
    if (list) list.push(c);
    else index.set(key, [c]);
  }
  for (const list of index.values()) list.sort((a, b) => a.rank - b.rank || a.seq - b.seq);
  return index;
}

/** Every subtask under `id`, at any depth. */
export function leavesUnder(id: string, index: ReadonlyMap<string, Card[]>): Card[] {
  const out: Card[] = [];
  const walk = (parent: string) => {
    for (const c of index.get(parent) ?? []) {
      if (c.kind === 'subtask') out.push(c);
      else walk(c.id);
    }
  };
  walk(id);
  return out;
}

/** The cards with every container's status and progress derived again (the service's rule, and the mock's). */
export function deriveBoard(cards: readonly Card[]): Card[] {
  const index = childIndex(cards);
  return cards.map((c) => {
    if (c.kind === 'subtask') return c;
    const { status, progress } = deriveContainer(leavesUnder(c.id, index));
    return c.status === status && sameProgress(c.progress, progress) ? c : { ...c, status, progress };
  });
}

const sameProgress = (a: CardProgress | undefined, b: CardProgress) => !!a && a.done === b.done && a.total === b.total && a.proposed === b.proposed;

/** The epic › story › subtask path of a card, root first, the card last. */
export function cardPath(card: Card, byId: ReadonlyMap<string, Card>): Card[] {
  const path: Card[] = [card];
  let at = card.parent_id ? byId.get(card.parent_id) : undefined;
  while (at && path.length < 8) {
    path.unshift(at);
    at = at.parent_id ? byId.get(at.parent_id) : undefined;
  }
  return path;
}

/** The epic a card sits under (itself for an epic); undefined at the root. */
export function epicOf(card: Card, byId: ReadonlyMap<string, Card>): Card | undefined {
  return cardPath(card, byId).find((c) => c.kind === 'epic');
}

/* ---------- Live updates (§15) ---------- */

export type FrameOutcome = { kind: 'applied'; data: BoardData } | { kind: 'ignored' } | { kind: 'gap' };

/**
 * One `board` frame on a loaded board: a frame at or below the loaded revision is already
 * in it; the next revision applies (cards upserted, removed ones dropped, requests kept only
 * while pending); anything further ahead means frames were missed, and the board is fetched again.
 */
export function applyBoardFrame(data: BoardData, frame: Pick<BoardFrame, 'revision' | 'cards' | 'removed' | 'requests'>): FrameOutcome {
  if (frame.revision <= data.revision) return { kind: 'ignored' };
  if (frame.revision !== data.revision + 1) return { kind: 'gap' };
  const removed = new Set(frame.removed ?? []);
  const changed = new Map((frame.cards ?? []).map((c) => [c.id, c]));
  const cards = data.cards.filter((c) => !removed.has(c.id) && !changed.has(c.id));
  for (const c of changed.values()) if (!removed.has(c.id)) cards.push(c);
  const decided = new Map((frame.requests ?? []).map((r) => [r.id, r]));
  const requests: BoardRequest[] = data.requests.filter((r) => !decided.has(r.id) && !removed.has(r.card_id));
  for (const r of decided.values()) if (r.status === 'pending') requests.push(r);
  return { kind: 'applied', data: { cards, requests, revision: frame.revision } };
}

/* ---------- The outline (Tree, Board lanes) ---------- */

export interface Filters {
  /** Show only this epic's subtree; null for every card. */
  epic: string | null;
  showCancelled: boolean;
}

export interface OutlineNode {
  card: Card;
  /** Confirmed children (and every child of an unconfirmed node, which is a suggestion as a whole). */
  children: OutlineNode[];
  /** Unconfirmed children, folded behind "+N suggested". */
  suggested: OutlineNode[];
}

/**
 * The plan as an outline: each parent's confirmed children in order, and its unconfirmed ones
 * apart as suggestions. The epic filter keeps one epic's subtree; cancelled cards leave unless shown.
 */
export function buildOutline(cards: readonly Card[], filters: Filters): { roots: OutlineNode[]; suggested: OutlineNode[] } {
  const index = childIndex(cards);
  const visible = (c: Card) => filters.showCancelled || c.status !== 'cancelled';
  const node = (card: Card, withinSuggestion: boolean): OutlineNode => {
    const kids = (index.get(card.id) ?? []).filter(visible);
    const suggestion = withinSuggestion || !card.confirmed;
    return {
      card,
      children: kids.filter((k) => suggestion || k.confirmed).map((k) => node(k, suggestion)),
      suggested: suggestion ? [] : kids.filter((k) => !k.confirmed).map((k) => node(k, true)),
    };
  };
  let top = (index.get('') ?? []).filter(visible);
  if (filters.epic) top = top.filter((c) => c.id === filters.epic);
  return { roots: top.filter((c) => c.confirmed).map((c) => node(c, false)), suggested: filters.epic ? [] : top.filter((c) => !c.confirmed).map((c) => node(c, true)) };
}

/* ---------- The Map's layout ---------- */

export const MAP_NODE_W = 216;
export const MAP_NODE_H = 40;
export const MAP_COL_GAP = 72;
export const MAP_ROW = 52;
const COLUMN: Record<CardKind, number> = { epic: 0, story: 1, subtask: 2 };

export interface MapNode {
  card: Card;
  x: number;
  y: number;
}

export interface MapEdge {
  from: string;
  to: string;
  kind: 'parent' | 'blocker';
}

/**
 * A tidy tree laid out left to right: one column per kind (epic, story, subtask), so a card
 * the root holds directly still sits in its kind's column; each leaf takes the next row and
 * a parent is centred on its first and last child. Sibling trees are a half row apart.
 * Blocker links are secondary edges between cards that are both drawn.
 */
export function layoutMap(cards: readonly Card[], filters: Filters): { nodes: MapNode[]; edges: MapEdge[]; width: number; height: number } {
  const index = childIndex(cards);
  const visible = (c: Card) => filters.showCancelled || c.status !== 'cancelled';
  const nodes: MapNode[] = [];
  const edges: MapEdge[] = [];
  let row = 0;
  const place = (card: Card): number => {
    const kids = (index.get(card.id) ?? []).filter(visible);
    let y: number;
    if (kids.length === 0) {
      y = row * MAP_ROW;
      row += 1;
    } else {
      const ys = kids.map((k) => {
        edges.push({ from: card.id, to: k.id, kind: 'parent' });
        return place(k);
      });
      y = (ys[0] + ys[ys.length - 1]) / 2;
    }
    nodes.push({ card, x: COLUMN[card.kind] * (MAP_NODE_W + MAP_COL_GAP), y });
    return y;
  };
  let roots = (index.get('') ?? []).filter(visible);
  if (filters.epic) roots = roots.filter((c) => c.id === filters.epic);
  roots.forEach((r, i) => {
    if (i > 0) row += 0.5;
    place(r);
  });
  const drawn = new Set(nodes.map((n) => n.card.id));
  for (const n of nodes) for (const blocker of n.card.blocked_by ?? []) if (drawn.has(blocker)) edges.push({ from: blocker, to: n.card.id, kind: 'blocker' });
  const width = nodes.length ? Math.max(...nodes.map((n) => n.x)) + MAP_NODE_W : 0;
  return { nodes, edges, width, height: Math.max(0, row * MAP_ROW - (MAP_ROW - MAP_NODE_H)) };
}

/** Pending requests per Project key, for the Needs-you count. */
export function pendingRequests(boards: Readonly<Record<string, { data: BoardData | null }>>): number {
  let n = 0;
  for (const b of Object.values(boards)) n += b.data?.requests.length ?? 0;
  return n;
}
