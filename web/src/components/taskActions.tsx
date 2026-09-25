import { Archive, ArchiveRestore, Check, History, Pencil, PowerOff, Trash2 } from 'lucide-react';
import { createContext, useContext } from 'react';
import { LIVE, needsYou, type SessionSummary } from '../api';
import type { ActionItem } from './ui/menu';

/** Where an in-place rename is happening: the sidebar row or the Task header. */
export interface Renaming {
  id: string;
  place: 'row' | 'header';
}

/**
 * Task lifecycle shared by the sidebar rows, their context menus and the open Task's
 * header menu, so every entry point runs the same rules and the same confirmations.
 * The dialogs live in App; these are the verbs.
 */
export interface TaskActions {
  select: (id: string) => void;
  /** Start an in-place rename; the matching row or header swaps its title for an input. */
  startRename: (id: string, place: Renaming['place']) => void;
  renaming: Renaming | null;
  cancelRename: () => void;
  /** Saves the name (empty clears it so the provider's title shows again). */
  rename: (id: string, name: string) => Promise<void>;
  settle: (id: string) => void;
  reopen: (id: string) => void;
  /** Opens the archive confirmation: archive is final here. */
  archive: (id: string) => void;
  /** Opens the delete confirmation; only an archived Task can be deleted. */
  remove: (id: string) => void;
  /** Opens the close-conversation confirmation (active, open Tasks). */
  close: (id: string) => void;
  /** Tasks with a lifecycle request in flight, by id; several may run at once. */
  busy: Readonly<Record<string, boolean>>;
  /** Opens the Task's Project's previous CLI sessions to import, without filtering the sidebar to it. */
  previousSessions: (projectId: string) => void;
  /** A provider can import previous sessions; the entry is offered only then. */
  canImport: boolean;
}

export const TaskActionsContext = createContext<TaskActions>({
  select: () => {},
  startRename: () => {},
  renaming: null,
  cancelRename: () => {},
  rename: async () => {},
  settle: () => {},
  reopen: () => {},
  archive: () => {},
  remove: () => {},
  close: () => {},
  busy: {},
  previousSessions: () => {},
  canImport: false,
});

export const useTaskActions = () => useContext(TaskActionsContext);

export const STAGE_REASON = 'Stop the turn, answer what is waiting and clear queued prompts before settling or archiving.';

/** True while the lifecycle rules refuse a stage change (a busy Task). */
export function stageBlocked(s: SessionSummary): boolean {
  return LIVE.includes(s.state) || needsYou(s) || (s.queued ?? 0) > 0;
}

/** True when a rename may start: not archived and no lifecycle request in flight. Every entry point (menu, double-click, F2, the header pencil) asks this. */
export function canRename(s: SessionSummary, a: TaskActions): boolean {
  return s.stage !== 'archived' && !a.busy[s.id];
}

/**
 * The same items everywhere (T3 Code): Rename, Settle or Reopen, Archive, Delete; Close
 * for an open conversation. Delete appears only for archived Tasks. Rules unchanged:
 * settle → archive (from any stage, final) → delete only after archive.
 */
export function taskMenuItems(s: SessionSummary, a: TaskActions, place: Renaming['place']): ActionItem[] {
  const stage = s.stage ?? 'active';
  const blocked = stageBlocked(s);
  const busy = !!a.busy[s.id];
  const items: ActionItem[] = [
    { key: 'rename', label: 'Rename', icon: <Pencil />, disabled: !canRename(s, a), takesFocus: true, onSelect: () => a.startRename(s.id, place) },
  ];
  if (stage === 'active' && s.open) items.push({ key: 'close', label: 'Close conversation', icon: <PowerOff />, disabled: busy, onSelect: () => a.close(s.id) });
  if (stage === 'active') items.push({ key: 'settle', label: 'Settle', icon: <Check />, disabled: busy || blocked, reason: blocked ? STAGE_REASON : undefined, onSelect: () => a.settle(s.id), separator: true });
  if (stage === 'settled') items.push({ key: 'reopen', label: 'Reopen', icon: <ArchiveRestore />, disabled: busy, onSelect: () => a.reopen(s.id), separator: true });
  if (stage !== 'archived') items.push({ key: 'archive', label: 'Archive', icon: <Archive />, disabled: busy || (stage === 'active' && blocked), reason: stage === 'active' && blocked ? STAGE_REASON : undefined, onSelect: () => a.archive(s.id) });
  if (stage === 'archived') items.push({ key: 'delete', label: 'Delete', icon: <Trash2 />, danger: true, disabled: busy, onSelect: () => a.remove(s.id), separator: true });
  // The Task's Project, last: its previous CLI sessions, reachable without filtering the sidebar to it.
  if (a.canImport) items.push({ key: 'previous', label: 'Previous sessions', icon: <History />, onSelect: () => a.previousSessions(s.project_id), separator: true });
  return items;
}
