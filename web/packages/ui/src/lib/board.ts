// The planner's pure logic (ADR 0005): derived container status and progress (§2), applying
// `board` frames by revision (§15), the outline the Tree draws, the Map's tree layout, and a
// Task's place in the plan with the per-level graph its Plan panel draws.
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

/**
 * A container's progress as every view shows it: proposals are plan items, so done counts over
 * all live subtasks, proposals included, and the proposals are still named. `short` is for rows
 * and nodes, `text` where words fit. The derived status keeps to confirmed subtasks (§2).
 */
export function shownProgress(p: CardProgress): { done: number; total: number; proposed: number; fraction: number; short: string; text: string } {
  const total = p.total + p.proposed;
  const proposed = p.proposed ? ` · ${p.proposed} proposed` : '';
  return { done: p.done, total, proposed: p.proposed, fraction: total ? p.done / total : 0, short: `${p.done}/${total}${proposed}`, text: `${p.done}/${total} done${proposed}` };
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

/* ---------- Approved epics (ADR 0006) ---------- */

/** The approved epic a card sits under, the card itself for one; undefined when its epic is not approved. */
export function approvedEpicOf(card: Card, byId: ReadonlyMap<string, Card>): Card | undefined {
  const epic = epicOf(card, byId);
  return epic?.run ? epic : undefined;
}

/** The nearest paused card at or above `card` (itself first); undefined when nothing on its path is paused. */
export function pausedOn(card: Card, byId: ReadonlyMap<string, Card>): Card | undefined {
  return cardPath(card, byId).reverse().find((c) => !!c.paused);
}

/**
 * A card's pause in words, for its chip: "Paused" (by the owner) or "Paused by uam" (an attempt ended
 * without landing), "via #3" when a card above it holds it back; '' when nothing does. A plain string,
 * so a row that takes it stays equal while it does.
 */
export function pauseLabel(card: Card, byId: ReadonlyMap<string, Card>): string {
  const at = pausedOn(card, byId);
  if (!at) return '';
  const who = at.paused === 'uam' ? 'Paused by uam' : 'Paused';
  return at === card ? who : `${who} via #${at.seq}`;
}

/**
 * Whether Launch may start the subtask `card` in a lane under its approved epic (ADR 0006 §3.2
 * Ready): confirmed with its parents, planned or to do, unheld, no pending request, not under a
 * cancelled card, not paused at or above, not flagged blocked and waiting on nothing open. The
 * service also counts the epic's free slots, which it says when it refuses.
 */
export function laneReady(card: Card, byId: ReadonlyMap<string, Card>): boolean {
  if (card.kind !== 'subtask' || card.held_by || card.blocked || card.pending_requests > 0) return false;
  if (card.status !== 'planned' && card.status !== 'todo') return false;
  const path = cardPath(card, byId);
  if (path.some((c) => !c.confirmed || c.status === 'cancelled')) return false;
  return !pausedOn(card, byId) && waitsOf(card, byId).length === 0;
}

/** The subtasks under `card` (itself for one) held in a lane: what Stop stops. */
export function runningLanes(card: Card, byId: ReadonlyMap<string, Card>): Card[] {
  return [...byId.values()].filter((x) => x.kind === 'subtask' && !!x.held_by && !!x.lane && cardPath(x, byId).includes(card));
}

/** The live proposals under `parent`, at any depth: what approving its epic again would confirm. */
export function proposalsUnder(parent: string, index: ReadonlyMap<string, Card[]>): Card[] {
  const out: Card[] = [];
  const walk = (id: string) => {
    for (const c of index.get(id) ?? []) {
      if (c.status === 'cancelled') continue;
      if (!c.confirmed) out.push(c);
      if (c.kind !== 'subtask') walk(c.id);
    }
  };
  walk(parent);
  return out;
}

/** A plan waiting for the owner's approval: a proposed epic, or an approved one with `proposals` added since. */
export interface PlanToApprove {
  epic: Card;
  proposals: number;
}

/** The plans waiting for the owner's approval on one Board (§6.1): proposed epics, then approved epics with live proposals under them; none done or cancelled, which approval refuses. */
export function plansToApprove(cards: readonly Card[]): PlanToApprove[] {
  const index = childIndex(cards);
  const epics = (index.get('') ?? []).filter((c) => c.kind === 'epic' && c.status !== 'cancelled' && c.status !== 'done');
  const proposed = epics.filter((e) => !e.confirmed).map((epic) => ({ epic, proposals: proposalsUnder(epic.id, index).length + 1 }));
  const approved = epics.filter((e) => e.confirmed && e.run).map((epic) => ({ epic, proposals: proposalsUnder(epic.id, index).length })).filter((p) => p.proposals > 0);
  return [...proposed, ...approved];
}

/** Plans to approve over every loaded Board, for the Needs-you count. */
export function plansWaiting(boards: Readonly<Record<string, { data: BoardData | null }>>): number {
  let n = 0;
  for (const b of Object.values(boards)) n += b.data ? plansToApprove(b.data.cards).length : 0;
  return n;
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
 * apart as suggestions. The epic filter keeps one epic's subtree, shown as it is even when the
 * epic is an agent's proposal; cancelled cards leave unless shown.
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
  const top = (index.get('') ?? []).filter(visible);
  if (filters.epic) return { roots: top.filter((c) => c.id === filters.epic).map((c) => node(c, false)), suggested: [] };
  return { roots: top.filter((c) => c.confirmed).map((c) => node(c, false)), suggested: top.filter((c) => !c.confirmed).map((c) => node(c, true)) };
}

/* ---------- The Map's layout ---------- */

export const MAP_NODE_W = 248;
export const MAP_NODE_H = 48;
export const MAP_COL_GAP = 72;
export const MAP_ROW = 60;
/** The Map's zoom limits. */
export const MAP_MIN_K = 0.4;
export const MAP_MAX_K = 2;
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

/** The last node drawn for each card object: an unchanged card at the same place keeps its node, so the Map's memo holds. */
const lastNode = new WeakMap<Card, MapNode>();

/**
 * A tidy tree laid out left to right: one column per kind (epic, story, subtask), so a card
 * the root holds directly still sits in its kind's column; each leaf takes the next row and
 * a parent is centred on its first and last child. Sibling trees are a half row apart.
 * Nodes come parents first (epic, then its stories, then their subtasks), which is the Tab
 * order. Blocker links are secondary edges between drawn cards of one level.
 */
export function layoutMap(cards: readonly Card[], filters: Filters): { nodes: MapNode[]; edges: MapEdge[]; width: number; height: number } {
  const index = childIndex(cards);
  const visible = (c: Card) => filters.showCancelled || c.status !== 'cancelled';
  const nodes: MapNode[] = [];
  const edges: MapEdge[] = [];
  let row = 0;
  const place = (card: Card): number => {
    const kids = (index.get(card.id) ?? []).filter(visible);
    // The parent's slot comes before its children's; its row is known once they are placed.
    const slot = nodes.push(undefined as unknown as MapNode) - 1;
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
    const x = COLUMN[card.kind] * (MAP_NODE_W + MAP_COL_GAP);
    const last = lastNode.get(card);
    const node = last && last.x === x && last.y === y ? last : { card, x, y };
    lastNode.set(card, node);
    nodes[slot] = node;
    return y;
  };
  let roots = (index.get('') ?? []).filter(visible);
  if (filters.epic) roots = roots.filter((c) => c.id === filters.epic);
  roots.forEach((r, i) => {
    if (i > 0) row += 0.5;
    place(r);
  });
  // Each level's links are its own DAG: an edge joins cards of one kind under one parent (§3), so a
  // link made before that rule, across levels, shows in the card panel and not here.
  const drawn = new Map(nodes.map((n) => [n.card.id, n.card]));
  for (const n of nodes) {
    for (const id of n.card.blocked_by ?? []) {
      const b = drawn.get(id);
      if (b && b.kind === n.card.kind && (b.parent_id ?? null) === (n.card.parent_id ?? null)) edges.push({ from: id, to: n.card.id, kind: 'blocker' });
    }
  }
  const width = nodes.length ? Math.max(...nodes.map((n) => n.x)) + MAP_NODE_W : 0;
  return { nodes, edges, width, height: Math.max(0, row * MAP_ROW - (MAP_ROW - MAP_NODE_H)) };
}

/** Where the Map's layer sits (`x`, `y`) and its scale (`k`). */
export interface MapView {
  x: number;
  y: number;
  k: number;
}

const leftEdge = (nodes: readonly MapNode[]) => (nodes.length ? Math.min(...nodes.map((n) => n.x)) : 0);

/** The Map as it opens: scale 1, the plan's top-left corner (the roots) `pad` in from the viewport's. */
export function openingView(layout: { nodes: readonly MapNode[] }, pad: number): MapView {
  return { k: 1, x: pad - leftEdge(layout.nodes), y: pad };
}

/**
 * The whole plan in a `w` × `h` viewport (Fit): never above scale 1 nor below the zoom limit,
 * centred across when it fits, and from the top. A plan too large even at the limit shows its top-left.
 */
export function fitView(layout: { nodes: readonly MapNode[]; width: number; height: number }, w: number, h: number, pad: number): MapView {
  const left = leftEdge(layout.nodes);
  const bw = Math.max(1, layout.width - left);
  const k = Math.min(1, Math.max(MAP_MIN_K, Math.min((w - pad * 2) / bw, (h - pad * 2) / Math.max(layout.height, 1))));
  const x = bw * k <= w - pad * 2 ? (w - bw * k) / 2 - left * k : pad - left * k;
  return { k, x, y: pad };
}

const isOpen = (b: Card | undefined): b is Card => !!b && b.status !== 'done' && b.status !== 'cancelled';

/**
 * The open blockers a card waits on as `#8, #20, #3 (via its story #2)` ('' for none): its own,
 * then each parent's, nearest first, since a blocked epic or story holds back everything under it
 * (§3). A plain string, so a row that takes it stays equal while they do.
 */
export function openBlockerSeqs(card: Card, byId: ReadonlyMap<string, Card>): string {
  const out: string[] = [];
  for (let at: Card | undefined = card, depth = 0; at && depth < 8; at = at.parent_id ? byId.get(at.parent_id) : undefined, depth++) {
    for (const b of at.blocked_by.map((id) => byId.get(id)).filter(isOpen)) out.push(at === card ? `#${b.seq}` : `#${b.seq} (via its ${at.kind} #${at.seq})`);
  }
  return out.join(', ');
}

/**
 * Whether a subtask has started: a Task holds it, or it is done. A started subtask keeps its plan
 * (ADR 0005 decision 8): its fields, place, links and checklist items wait until it is released.
 */
export function isStarted(c: Card): boolean {
  return c.kind === 'subtask' && (!!c.held_by || c.status === 'doing' || c.status === 'done');
}

/** Why a started subtask's plan can't change, or null when it can. */
export function lockedReason(c: Card): string | null {
  if (!isStarted(c)) return null;
  return c.status === 'done' ? 'Done: move it back to To do to change the plan.' : 'In progress: release it to change the plan.';
}

/**
 * Why the container `c` can't move, or null when it can: a move takes every card under it along,
 * so the service refuses it while a subtask under it has started (held, doing or done).
 */
export function startedUnderReason(c: Card, byId: ReadonlyMap<string, Card>): string | null {
  if (c.kind === 'subtask') return null;
  for (const x of byId.values()) {
    if (x === c || !isStarted(x) || !cardPath(x, byId).includes(c)) continue;
    return x.status === 'done' ? `Can't move while #${x.seq} under it is done: move it back to To do first.` : `Can't move while #${x.seq} under it is in progress: release it first.`;
  }
  return null;
}

/**
 * Why `c` can't leave its level (a move to another parent, a split into a story), or null when it
 * can: a link joins cards of one kind under one parent, so the service wants its links removed first.
 */
export function linkedReason(c: Card, byId: ReadonlyMap<string, Card>): string | null {
  const ids = [...new Set([...c.blocked_by, ...c.blocks])];
  if (!ids.length) return null;
  const refs = ids.map((id) => byId.get(id)).filter((x): x is Card => !!x).map((x) => `#${x.seq}`);
  return `Linked to ${refs.length ? refs.join(', ') : 'other cards'}: remove those blocker links first.`;
}

/**
 * The subtasks launching the container `c` may start, as the service picks them: confirmed, not
 * held, planned or to do, anywhere under it.
 */
export function pendingUnder(c: Card, byId: ReadonlyMap<string, Card>): Card[] {
  return [...byId.values()].filter((x) => x.kind === 'subtask' && x.confirmed && !x.held_by && (x.status === 'planned' || x.status === 'todo') && x !== c && cardPath(x, byId).includes(c));
}

/**
 * The cards `card` may be blocked by (§3): links map one level at a time, so only cards of its
 * kind under its parent (epics with epics), not cancelled, not started, and not linked already.
 */
export function linkTargets(card: Card, cards: readonly Card[]): Card[] {
  const parent = card.parent_id ?? null;
  return cards.filter(
    (x) => x.id !== card.id && x.kind === card.kind && (x.parent_id ?? null) === parent && x.project_id === card.project_id && x.status !== 'cancelled' && !isStarted(x) && !card.blocked_by.includes(x.id) && !card.blocks.includes(x.id),
  );
}

/** The key of the loaded Board that holds card `id` (a Project id or `unassigned`), if any does. */
export function boardOf(boards: Readonly<Record<string, { data: BoardData | null }>>, id: string): string | undefined {
  return Object.keys(boards).find((k) => boards[k].data?.cards.some((c) => c.id === id));
}

/** Pending requests per Project key, for the Needs-you count. */
export function pendingRequests(boards: Readonly<Record<string, { data: BoardData | null }>>): number {
  let n = 0;
  for (const b of Object.values(boards)) n += b.data?.requests.length ?? 0;
  return n;
}

/* ---------- A Task's place in the plan (the story strip and the Plan panel) ---------- */

/** The subtask a Task works on: the one it holds, else one it finished. */
export function taskCard(cards: readonly Card[], taskId: string): Card | undefined {
  return cards.find((c) => c.held_by === taskId) ?? cards.find((c) => c.kind === 'subtask' && c.status === 'done' && c.worked_by === taskId);
}

/** An open card another waits for; `via` is the story or epic it waits through, absent for its own. */
export interface Wait {
  card: Card;
  via?: Card;
}

/** What a card waits for that is still open: its own blockers, then each parent's, nearest first (§3). */
export function waitsOf(card: Card, byId: ReadonlyMap<string, Card>): Wait[] {
  const out: Wait[] = [];
  for (let at: Card | undefined = card, depth = 0; at && depth < 8; at = at.parent_id ? byId.get(at.parent_id) : undefined, depth++) {
    for (const b of at.blocked_by.map((id) => byId.get(id)).filter(isOpen)) out.push(at === card ? { card: b } : { card: b, via: at });
  }
  return out;
}

/**
 * The subtask to start next under a container, other than `except`: the first in order that is
 * not started (planned or to do, not held), not flagged blocked, and waiting for nothing open,
 * its own blockers or its parents' (`waitsOf`, as the finishing guard counts them); a confirmed
 * one before a proposal. None when every one waits.
 */
export function nextSubtask(container: Card, index: ReadonlyMap<string, Card[]>, byId: ReadonlyMap<string, Card>, except?: string): Card | undefined {
  const free = (index.get(container.id) ?? []).filter((c) => c.kind === 'subtask' && c.id !== except && !c.held_by && !c.blocked && (c.status === 'planned' || c.status === 'todo') && waitsOf(c, byId).length === 0);
  return free.find((c) => c.confirmed) ?? free[0];
}

export type GraphDir = 'lr' | 'tb';

/** A graph node's box, and the gaps between layers (`main`) and within one (`cross`). */
export const GRAPH_NODE = { w: 172, h: 64, main: 40, cross: 12 } as const;

export interface GraphNode {
  card: Card;
  x: number;
  y: number;
}

export interface GraphLayout {
  nodes: GraphNode[];
  edges: { from: GraphNode; to: GraphNode }[];
  width: number;
  height: number;
  dir: GraphDir;
}

/**
 * One level of the plan as a layered graph (the cards of one container, §3): a card's layer is
 * its longest chain of blockers within the level, and a layer's cards sort by the average place
 * of what they wait for (fewer crossings), then by rank. Layers run left to right (`lr`) or top
 * to bottom (`tb`); an edge runs from the blocker to the card that waits for it.
 */
export function layoutLevel(cards: readonly Card[], dir: GraphDir): GraphLayout {
  const byId = new Map(cards.map((c) => [c.id, c]));
  const inLevel = (c: Card) => c.blocked_by.filter((b) => byId.has(b));
  const layerOf = new Map<string, number>();
  const layer = (c: Card, seen: ReadonlySet<string>): number => {
    const known = layerOf.get(c.id);
    if (known !== undefined) return known;
    // A cycle (the service refuses them) is drawn flat rather than followed.
    const next = new Set(seen).add(c.id);
    const l = Math.max(-1, ...inLevel(c).filter((b) => !seen.has(b)).map((b) => layer(byId.get(b)!, next))) + 1;
    layerOf.set(c.id, l);
    return l;
  };
  const layers: Card[][] = [];
  for (const c of cards) (layers[layer(c, new Set())] ??= []).push(c);
  const place = new Map<string, number>();
  layers.forEach((row, i) => {
    const bary = (c: Card) => {
      const ps = inLevel(c).map((b) => place.get(b)).filter((n): n is number => n !== undefined);
      return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : Number.MAX_SAFE_INTEGER;
    };
    if (i > 0) row.sort((a, b) => bary(a) - bary(b) || a.rank - b.rank || a.seq - b.seq);
    row.forEach((c, j) => place.set(c.id, j));
  });
  const { w, h, main, cross } = GRAPH_NODE;
  const lr = dir === 'lr';
  const most = Math.max(0, ...layers.map((r) => r.length));
  const crossStep = (lr ? h : w) + cross;
  const mainStep = (lr ? w : h) + main;
  const nodes: GraphNode[] = [];
  const placed = new Map<string, GraphNode>();
  layers.forEach((row, i) => {
    const offset = ((most - row.length) * crossStep) / 2;
    row.forEach((card, j) => {
      const a = i * mainStep;
      const b = offset + j * crossStep;
      const node = lr ? { card, x: a, y: b } : { card, x: b, y: a };
      nodes.push(node);
      placed.set(card.id, node);
    });
  });
  const mainLen = layers.length ? layers.length * mainStep - main : 0;
  const crossLen = most ? most * crossStep - cross : 0;
  const edges = nodes.flatMap((to) => inLevel(to.card).map((b) => ({ from: placed.get(b)!, to })));
  return { nodes, edges, width: lr ? mainLen : crossLen, height: lr ? crossLen : mainLen, dir };
}

/**
 * `text` in at most `lines` lines of at most `max` characters, broken between words (a word
 * longer than a line is cut); the last line ends in an ellipsis when text is left over. SVG
 * text does not wrap, so the graph wraps its titles here.
 */
export function wrapText(text: string, max: number, lines: number): string[] {
  const out: string[] = [];
  let line = '';
  let rest = false;
  for (const raw of text.trim().split(/\s+/).filter(Boolean)) {
    const word = raw.length > max ? `${raw.slice(0, max - 1)}…` : raw;
    if (!line) line = word;
    else if (line.length + 1 + word.length <= max) line += ` ${word}`;
    else {
      out.push(line);
      line = word;
      if (out.length === lines) {
        rest = true;
        line = '';
        break;
      }
    }
  }
  if (line) out.push(line);
  if (rest) {
    const last = out[out.length - 1];
    out[out.length - 1] = `${last.length >= max ? last.slice(0, max - 1) : last}…`;
  }
  return out;
}
