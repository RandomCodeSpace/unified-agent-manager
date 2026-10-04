import { createContext, useContext } from 'react';
import type { BoardJob, Card, CardKind, Project, SessionSummary } from '../../api';
import type { BoardState } from '../../state';

export type PlannerViewKind = 'tree' | 'board' | 'map';

/**
 * The Planner view's state, one for the whole app (ADR 0005 §10): leaving the Planner and coming
 * back keeps the Board, view, selection and filters. A Task's Plan panel keeps its own, so
 * following Tasks never moves this one.
 */
export interface PlannerUi {
  /** The Board shown: a Project id, or `unassigned`. */
  project: string | null;
  view: PlannerViewKind;
  selected: string | null;
  /** Show only this epic's subtree. */
  epic: string | null;
  showCancelled: boolean;
  /** Containers folded in the Tree (open unless false). */
  folded: Record<string, boolean>;
  /** "+N suggested" rows opened in the Tree, by parent id (`''` for the root). */
  suggestedOpen: Record<string, boolean>;
  /** The side panel of the main pane. */
  panel: 'card' | 'inbox' | null;
  /** A card the owner is adding in the Tree: its kind, under this parent (`''` for the root). */
  creating: PlannerCreating | null;
}

export interface PlannerCreating {
  parent: string;
  kind: CardKind;
}

export const INITIAL_UI: PlannerUi = { project: null, view: 'tree', selected: null, epic: null, showCancelled: false, folded: {}, suggestedOpen: {}, panel: null, creating: null };

export interface PlannerNotice {
  tone: 'muted' | 'error';
  text: string;
  /** A Task the notice offers to open (a launch, a planning Task). */
  task?: string;
}

export interface PlannerContextValue {
  ui: PlannerUi;
  setUi: (patch: Partial<PlannerUi> | ((ui: PlannerUi) => Partial<PlannerUi>)) => void;
  boards: Record<string, BoardState>;
  jobs: Record<string, BoardJob>;
  projects: Project[];
  /** Select a card and show it in the main pane's card panel, on its own Board (opening the Planner view when it is elsewhere); in a Task's Plan panel, open it there. */
  openCard: (id: string) => void;
  /** Show a Task (a held subtask's chip, a transcript link). */
  openTask: (id: string) => void;
  /** Fetch the shown Board again (a failed load, Retry). */
  reload: (key: string) => void;
  notice: PlannerNotice | null;
  notify: (n: PlannerNotice | null) => void;
  /** Settings → Planner is on. */
  enabled: boolean;
}

export const PlannerContext = createContext<PlannerContextValue | null>(null);

/**
 * The Tasks, for the chips that name one (a held subtask, a request, a transcript link). Apart
 * from the planner's context, so a Task update re-renders those chips and nothing else.
 */
export interface PlannerTasksValue {
  sessions: SessionSummary[];
  openTask: (id: string) => void;
}

export const PlannerTasks = createContext<PlannerTasksValue>({ sessions: [], openTask: () => {} });

export const usePlannerTasks = () => useContext(PlannerTasks);

export function usePlanner(): PlannerContextValue {
  const ctx = useContext(PlannerContext);
  if (!ctx) throw new Error('usePlanner outside PlannerContext');
  return ctx;
}

/** Inside a Task: opens a card in that Task's Plan panel, so a card link never leaves the Task. */
export const TaskCardOpener = createContext<((id: string) => void) | null>(null);

/**
 * Opens a card from outside the planner (a transcript's card chip): in the Task's Plan panel
 * inside a Task, else the context's openCard; null while the planner is off or outside its
 * provider, so the caller shows plain text instead.
 */
export function usePlannerOpenCard(): ((id: string) => void) | null {
  const ctx = useContext(PlannerContext);
  const inTask = useContext(TaskCardOpener);
  if (!ctx?.enabled) return null;
  return inTask ?? ctx.openCard;
}

/** The shown Board's cards (empty until it loads) and an id index. */
export function useShownBoard() {
  const p = usePlanner();
  const board = p.ui.project ? p.boards[p.ui.project] : undefined;
  const cards: Card[] = board?.data?.cards ?? EMPTY;
  return { ...p, board, cards };
}

const EMPTY: Card[] = [];
