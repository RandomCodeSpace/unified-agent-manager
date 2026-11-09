// The todo dependencies as a board, the other way to draw them beside the rails (`todoLinks`):
// turns across, agents down, arrows for the waits. Which one a reader shows is a setting kept per
// browser (`uam.todoGraph`).

import type { Todo } from '../api';
import { turnSections, type TurnOrigin, type TurnSection } from './todos.ts';

/** "graph" is the board: the value the Mermaid graph it replaced stored, kept so existing settings show the board. */
export type TodoGraphStyle = 'rails' | 'graph';

export const TODO_GRAPH_KEY = 'uam.todoGraph';

/** A stored value as a setting: "graph" as chosen; nothing stored, or garbage, is the rails. */
export const parseTodoGraph = (raw: string | null): TodoGraphStyle => (raw === 'graph' ? 'graph' : 'rails');

export function loadTodoGraph(): TodoGraphStyle {
  try {
    return parseTodoGraph(localStorage.getItem(TODO_GRAPH_KEY));
  } catch {
    return 'rails';
  }
}

/** Stores the choice; storage that refuses keeps the rails after a reload. */
export function saveTodoGraph(style: TodoGraphStyle): void {
  try {
    localStorage.setItem(TODO_GRAPH_KEY, style);
  } catch {
    // Private mode or a full quota.
  }
}

/** A card on the board: its row, its column (a turn, oldest first) and its lane (the agent that wrote it). */
export interface BoardCard { todo: Todo; col: number; lane: number }

/** An arrow on the board, from the row waited on to the row that waits: a blocked row's wait on a row not done is a `wait`, any other a `link`. */
export interface BoardEdge { from: string; to: string; kind: 'wait' | 'link' }

/** The board: its columns (the turns that added rows, oldest first), its lanes (the main agent, undefined, first, then each subagent by id in the order its rows come), its cards and its arrows. */
export interface Board { columns: TurnSection[]; lanes: (string | undefined)[]; cards: BoardCard[]; edges: BoardEdge[] }

/**
 * The dependency board for `rows`: a fixed grid, column = the turn that added a row (`origin`),
 * lane = the agent that wrote it, cards in each cell in the rows' order; an arrow per dependency
 * between rows on it. Recorded data only: placement never reads a title.
 */
export function todoBoard(rows: readonly Todo[], origin: (id: string) => TurnOrigin): Board {
  const columns = turnSections(rows, origin).reverse();
  const agents = [...new Set(rows.map((t) => t.agent_id || undefined))];
  const lanes = agents.includes(undefined) ? [undefined, ...agents.filter(Boolean)] : agents;
  const cards = columns.flatMap((c, col) => c.todos.map((todo) => ({ todo, col, lane: lanes.indexOf(todo.agent_id || undefined) })));
  const byId = new Map(rows.map((t) => [t.id, t]));
  const edges = rows.flatMap((t) =>
    [...new Set(t.depends_on ?? [])].flatMap((id): BoardEdge[] => {
      const from = byId.get(id);
      if (!from || id === t.id) return [];
      return [{ from: id, to: t.id, kind: t.status === 'blocked' && from.status !== 'done' ? 'wait' : 'link' }];
    }),
  );
  return { columns, lanes, cards, edges };
}
