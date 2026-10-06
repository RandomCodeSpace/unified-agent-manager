import { useApi } from '../../ApiContext';
import { Ban, BadgeCheck, Check, CheckCheck, Ellipsis, ListRestart, MoveRight, PanelRightOpen, Pause, Play, RotateCcw, Sparkles, Split, Square, SquareTerminal, Stethoscope, Undo2, Workflow } from 'lucide-react';
import { useMemo, useRef, useState, type ComponentProps, type ReactElement, type ReactNode } from 'react';
import { plannerErrorText, type Card, type TriageVerdict } from '../../api';
import { approvedEpicOf, cardPath, isStarted, linkedReason, pendingUnder, runningLanes, startedUnderReason } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { AlertDialog, useConfirm } from '../ui/dialog';
import { ContextMenu, Menu, type ActionItem } from '../ui/menu';
import { useShownBoard } from './context';
import { ApproveDialog, BriefDialog, DoneDialog, LaunchDialog, MoveDialog, ReasonDialog, SplitDialog, moveTargets, type ApproveAsk, type BriefAsk, type LaunchAsk, type ReasonAsk } from './dialogs';

export interface TriageResult {
  verdict: TriageVerdict;
  sentence: string;
  head: string;
}

/** One of the owner's actions on a card: a button in the card panel, an item in a row's menus. */
export interface CardAction {
  key: string;
  label: string;
  icon: ReactNode;
  onClick: () => void;
  primary?: boolean;
  danger?: boolean;
  /** Why the service refuses it now: shown off, with this reason. */
  reason?: string;
  /** What it does, where a button has room to say (its tooltip). */
  title?: string;
}

/**
 * The owner's actions on the shown Board's cards (ADR 0005 §10) and the state they run on: the
 * one in flight and the dialogs they open. The card panel shows a card's actions as buttons; the
 * Tree and the Board offer the same ones in a row's "…" and context menus (DESIGN.md principle 6).
 * Check at HEAD reports through its job, which the card panel shows (`onCheck` opens it there);
 * Triage answers with a verdict only the panel has room for, so it is offered with `onTriage` only.
 */
export function useCardActions({ onTriage, onCheck }: Readonly<{ onTriage?: (card: Card, t: TriageResult) => void; onCheck?: (card: Card) => void }> = {}) {
  const api = useApi();
  const { cards, notify, openCard } = useShownBoard();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const [busy, setBusy] = useState<{ card: string; key: string } | null>(null);
  // The card a dialog is about stays while the dialog animates out.
  const [target, setTarget] = useState<string | null>(null);
  const [dialog, setDialog] = useState<'done' | 'move' | 'split' | null>(null);
  const [reason, setReason] = useState<ReasonAsk | null>(null);
  const [brief, setBrief] = useState<BriefAsk | null>(null);
  const [launching, setLaunching] = useState<LaunchAsk | null>(null);
  const [approving, setApproving] = useState<ApproveAsk | null>(null);
  // Stop (ADR 0006 §4.6): a lane subtask, or a container with the lane subtasks running under it.
  const stopping = useConfirm<{ card: Card; lanes: Card[] }>();

  async function run<T>(card: string, key: string, verb: string, op: () => Promise<T>): Promise<T | undefined> {
    setBusy({ card, key });
    notify(null);
    try {
      return await op();
    } catch (e) {
      notify({ tone: 'error', text: `Could not ${verb}: ${plannerErrorText(e)}` });
      return undefined;
    } finally {
      setBusy(null);
    }
  }
  const open = (c: Card, d: 'done' | 'move' | 'split') => {
    setTarget(c.id);
    setDialog(d);
  };

  /**
   * A card's actions as it stands (§10); none for an Unassigned card, which only moves into a
   * Project. Under an approved epic (ADR 0006 §8) the approval owns starting and confirming work:
   * uam starts each ready subtask itself, so Confirm, Launch and Do whole story give way to Pause
   * and Resume, and Stop ends the lane attempts at or under a card.
   */
  function actionsOf(c: Card): CardAction[] {
    if (!c.project_id) return [];
    const leaf = c.kind === 'subtask';
    const approved = approvedEpicOf(c, byId);
    const launched = (res: { card: Card; session: { id: string } }) => notify({ tone: 'muted', text: `Launched #${res.card.seq} ${res.card.title} in a new task.`, task: res.session.id });
    // The launch dialog picks the new Task's model and mode and takes a brief. Work starts only on
    // confirmed cards (§5): a suggestion, or a subtask under one, is launched through the confirm
    // step, which names what launching confirms and the suggestions it waits on.
    const launch = (whole: boolean) => {
      const confirms = leaf ? cardPath(c, byId).filter((x) => !x.confirmed).reverse() : [];
      // Its own suggested blockers, then its parents', which hold it back too.
      const waits = cardPath(c, byId)
        .reverse()
        .flatMap((at) =>
          at.blocked_by.flatMap((id) => {
            const b = byId.get(id);
            return b && !b.confirmed && b.status !== 'done' && b.status !== 'cancelled' ? [{ card: b, via: at === c ? undefined : at }] : [];
          }),
        );
      setLaunching({ card: c, whole, confirms, waits, run: (body) => api.planner.launch(c.id, confirms.length ? { ...body, confirm: true } : body).then(launched) });
    };
    const actions: CardAction[] = [];
    if (c.kind === 'epic' && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'approve', label: 'Approve and run…', icon: <BadgeCheck />, onClick: () => setApproving({ epic: c }) });
    if (approved && c.status !== 'done' && c.status !== 'cancelled') {
      actions.push(
        c.paused
          ? { key: 'pause', label: 'Resume', icon: <Play />, title: 'Let uam start this and what is under it again.', onClick: () => void run(c.id, 'pause', 'resume the card', () => api.planner.pause(c.id, false)) }
          : { key: 'pause', label: 'Pause', icon: <Pause />, title: "Block execution: uam won't start this or anything under it.", onClick: () => void run(c.id, 'pause', 'pause the card', () => api.planner.pause(c.id, true)) },
      );
    }
    if (!c.confirmed && !approved) actions.push({ key: 'confirm', label: 'Confirm', icon: <Check />, primary: true, onClick: () => void run(c.id, 'confirm', 'confirm the card', () => api.planner.confirm(c.id)) });
    if (leaf && !approved && (c.status === 'planned' || c.status === 'todo')) actions.push({ key: 'launch', label: 'Launch', icon: <Play />, primary: c.confirmed, onClick: () => launch(false) });
    const lanes = approved && c.status !== 'done' && c.status !== 'cancelled' ? runningLanes(c, byId) : [];
    if (lanes.length > 0) actions.push({ key: 'stop', label: 'Stop', icon: <Square />, danger: true, title: leaf ? 'Stop its task and pause it.' : 'Pause it and stop the subtasks running under it.', onClick: () => stopping.ask({ card: c, lanes }) });
    // A story's launch starts one of its confirmed subtasks waiting to start; with none, the service refuses it.
    if (c.kind === 'story' && !approved && c.status !== 'done' && c.status !== 'cancelled') {
      const reason = pendingUnder(c, byId).length ? undefined : 'No confirmed subtask is waiting to start: confirm or add one first.';
      actions.push({ key: 'launch', label: 'Do whole story', icon: <Play />, reason, onClick: () => launch(true) });
    }
    if (!leaf && c.status !== 'cancelled') {
      actions.push({ key: 'plan', label: 'Plan with agent', icon: <Workflow />, onClick: () => setBrief({ kind: 'plan', title: `Plan #${c.seq} with an agent`, run: async ({ brief: b, task }) => { const r = await api.planner.plan(c.id, { ...task, brief: b }); notify({ tone: 'muted', text: `A planning task started for #${c.seq}.`, task: r.session.id }); } }) });
      actions.push({ key: 'suggest', label: c.kind === 'epic' ? 'Suggest stories' : 'Suggest subtasks', icon: <Sparkles />, onClick: () => setBrief({ kind: 'suggest', title: c.kind === 'epic' ? `Suggest stories for #${c.seq}` : `Suggest subtasks for #${c.seq}`, run: ({ brief: b, document, max }) => api.planner.suggest(c.id, { brief: b, document, max }) }) });
    }
    if (leaf && c.status !== 'done' && c.status !== 'cancelled') {
      // Done confirms the subtask, which under an approved epic only the approval does.
      // Marked done, a lane's work would never land.
      const reason = approved && !c.confirmed ? `Approve #${approved.seq} again to confirm it first: under an approved epic only the approval confirms a card.` : c.held_by && c.lane?.branch ? 'It runs in a lane: accept its done request, or Stop it.' : undefined;
      actions.push({ key: 'done', label: 'Mark done', icon: <CheckCheck />, reason, onClick: () => open(c, 'done') });
    }
    if (leaf && c.status === 'doing' && !c.lane?.branch) actions.push({ key: 'release', label: 'Release', icon: <Undo2 />, onClick: () => setReason({ title: `Release #${c.seq}?`, description: 'The subtask goes back to To do and its Task stops holding it. Pending requests are withdrawn.', label: 'Comment (optional)', confirm: 'Release', required: false, run: (t) => api.planner.release(c.id, t) }) });
    // Not under a cancelled card: the service refuses it until that is restored.
    if (leaf && c.status === 'done' && !cardPath(c, byId).some((a) => a.status === 'cancelled')) {
      actions.push({ key: 'todo', label: 'Back to To do', icon: <ListRestart />, onClick: () => setReason({ title: `Move #${c.seq} back to To do?`, description: 'The subtask goes back to To do for another attempt.', label: 'Comment (optional)', confirm: 'Back to To do', required: false, run: (t) => api.planner.status(c.id, 'todo', t) }) });
    }
    if (leaf && c.confirmed && c.status !== 'done' && c.status !== 'cancelled') {
      actions.push({ key: 'check', label: 'Check at HEAD', icon: <SquareTerminal />, onClick: () => void run(c.id, 'check', 'run the acceptance command', () => api.planner.check(c.id)).then((r) => r && onCheck?.(c)) });
    }
    if (leaf && c.stale && onTriage) actions.push({ key: 'triage', label: 'Triage', icon: <Stethoscope />, onClick: () => void run(c.id, 'triage', 'triage the subtask', () => api.planner.triage(c.id)).then((r) => r && onTriage(c, r)) });
    // A started subtask keeps its plan until it is released: no move, no split. A story moves
    // only while nothing under it has started, so it says which subtask holds it in place. A
    // linked card leaves its level (a move, a split into a story) only once its links are removed.
    const started = isStarted(c);
    const linked = linkedReason(c, byId);
    if (c.kind !== 'epic' && c.status !== 'cancelled' && !started && (c.parent_id || moveTargets(c, cards).length > 0)) actions.push({ key: 'move', label: 'Move to…', icon: <MoveRight />, reason: linked ?? startedUnderReason(c, byId) ?? undefined, onClick: () => open(c, 'move') });
    if (leaf && c.status !== 'cancelled' && !started) {
      // Under a story the split makes siblings; elsewhere the subtask becomes a story.
      const parent = c.parent_id ? byId.get(c.parent_id) : undefined;
      actions.push({ key: 'split', label: 'Split', icon: <Split />, reason: (parent?.kind !== 'story' && linked) || undefined, onClick: () => open(c, 'split') });
    }
    if (c.status !== 'cancelled' && c.status !== 'done') {
      actions.push({
        key: 'cancel',
        label: 'Cancel',
        icon: <Ban />,
        danger: true,
        onClick: () => setReason({ title: `Cancel #${c.seq}?`, description: leaf ? 'The subtask is cancelled; its hold and pending requests end. Restore brings it back.' : 'Every open subtask under it is cancelled with this comment. Restore brings back exactly these.', label: 'Why (required)', confirm: 'Cancel card', danger: true, required: true, run: (t) => api.planner.status(c.id, 'cancelled', t) }),
      });
    }
    if (c.status === 'cancelled') {
      // Under an approved epic a restored card is a proposal again, until the epic is approved again;
      // the epic itself reopens confirmed.
      const description = !approved
        ? 'The card and everything its cancel took with it reopen, confirmed.'
        : approved.id === c.id
          ? `The epic reopens, and everything its cancel took with it comes back as proposals: approve #${c.seq} again to confirm them.`
          : `The card and everything its cancel took with it reopen as proposals: approve #${approved.seq} again to confirm them.`;
      actions.push({ key: 'restore', label: 'Restore', icon: <RotateCcw />, onClick: () => setReason({ title: `Restore #${c.seq}?`, description, label: 'Why (required)', confirm: 'Restore', required: true, run: (t) => api.planner.restore(c.id, t) }) });
    }
    return actions;
  }

  /**
   * A card's menu: Open, the view's own items (`lead`: add, edit), its actions, then `trail`.
   * While one of its actions runs, the others wait and say why.
   */
  function menuOf(c: Card, lead: ActionItem[] = [], trail: ActionItem[] = []): ActionItem[] {
    const waiting = busy?.card === c.id;
    const acts = actionsOf(c).map((a, i): ActionItem => ({
      key: a.key,
      label: a.label,
      icon: a.icon,
      danger: a.danger,
      separator: i === 0 || a.danger,
      disabled: waiting || !!a.reason,
      reason: a.reason ?? (waiting ? 'Wait for the action running on this card.' : undefined),
      onSelect: a.onClick,
    }));
    return [{ key: 'open', label: 'Open', icon: <PanelRightOpen />, onSelect: () => openCard(c.id) }, ...lead, ...acts, ...trail];
  }

  const shown = target ? byId.get(target) : undefined;
  const stop = stopping.target;
  // A container is paused first, so nothing new starts under it while its lanes stop.
  const stopAll = async ({ card: c, lanes }: { card: Card; lanes: Card[] }) => {
    if (c.kind !== 'subtask' && !c.paused) await api.planner.pause(c.id, true);
    for (const l of lanes) await api.planner.release(l.id, '');
  };
  const dialogs = (
    <>
      <AlertDialog
        {...stopping.props}
        title={stop?.card.kind === 'subtask' ? `Stop #${stop.card.seq}?` : `Stop #${stop?.card.seq ?? ''} and what runs under it?`}
        description={
          stop?.card.kind === 'subtask'
            ? 'Its task stops and is archived, and the subtask goes back to To do, paused. The attempt branch is kept.'
            : `#${stop?.card.seq ?? ''} is paused, so nothing new starts under it; the tasks of the subtasks running under it stop and are archived, and each goes back to To do, paused. Their attempt branches are kept.`
        }
        confirmLabel="Stop"
        busy={busy?.key === 'stop'}
        onConfirm={() => {
          if (!stop) return;
          stopping.close();
          void run(stop.card.id, 'stop', 'stop the work', () => stopAll(stop));
        }}
      >
        {stop && stop.card.kind !== 'subtask' && (
          <ul className="mt-1 flex flex-col gap-0.5 text-caption text-body">
            {stop.lanes.map((l) => (
              <li key={l.id} className="truncate">
                #{l.seq} {l.title}
              </li>
            ))}
          </ul>
        )}
      </AlertDialog>
      <ReasonDialog ask={reason} onClose={() => setReason(null)} />
      <BriefDialog ask={brief} onClose={() => setBrief(null)} />
      <LaunchDialog ask={launching} onClose={() => setLaunching(null)} />
      <ApproveDialog ask={approving} onClose={() => setApproving(null)} />
      {shown && (
        <>
          <DoneDialog card={shown} byId={byId} open={dialog === 'done'} onClose={() => setDialog(null)} />
          <MoveDialog card={shown} cards={cards} open={dialog === 'move'} onClose={() => setDialog(null)} />
          <SplitDialog card={shown} byId={byId} open={dialog === 'split'} onClose={() => setDialog(null)} />
        </>
      )}
    </>
  );
  return { busy, run, actionsOf, menuOf, dialogs, ask: setReason };
}

/** A menu's items, built only once it opens (its content mounts then). */
function Items({ build, context }: Readonly<{ build: () => ActionItem[]; context?: boolean }>) {
  const items = build();
  return context ? <ContextMenu.Actions items={items} /> : <Menu.Actions items={items} />;
}

export type CardMenuHandle = ReturnType<typeof Menu.createHandle<string>>;

/** The one "…" menu of a view, which each card's button opens for its card (a detached trigger with the card's id). */
export function useCardMenuHandle(): CardMenuHandle {
  return useMemo(() => Menu.createHandle<string>(), []);
}

/**
 * A view's card menus (DESIGN.md principle 6): the "…" menu its cards' buttons open, and the
 * context menu over the view (`render`, the view's element, holding `children`), which opens on
 * the card under the pointer (`data-card`; right click, long press on touch, the context-menu key)
 * with the same items. One of each per view, not per card, so a long plan mounts no menu per row.
 * The click that ends a long press is the menu's, not the card's.
 */
export function CardMenus({ handle, items, render, children, ...props }: Readonly<{ handle: CardMenuHandle; items: (id: string) => ActionItem[]; render: ReactElement; children: ReactNode } & Omit<ComponentProps<typeof ContextMenu.Trigger>, 'render' | 'children'>>) {
  const [context, setContext] = useState<{ open: boolean; card: string | null }>({ open: false, card: null });
  const target = useRef<string | null>(null);
  const opened = useRef(false);
  const aim = (e: { target: EventTarget }) => {
    target.current = (e.target as HTMLElement).closest<HTMLElement>('[data-card]')?.dataset.card ?? null;
  };
  return (
    <>
      <ContextMenu.Root
        open={context.open}
        onOpenChange={(open) => {
          // Only over a card: the gaps between them open nothing.
          if (open && !target.current) return;
          opened.current = open;
          setContext((c) => ({ open, card: open ? target.current : c.card }));
        }}
      >
        <ContextMenu.Trigger
          render={render}
          onContextMenuCapture={aim}
          onTouchStartCapture={aim}
          onClickCapture={(e) => {
            if (!opened.current) return;
            e.preventDefault();
            e.stopPropagation();
          }}
          {...props}
        >
          {children}
        </ContextMenu.Trigger>
        <ContextMenu.Content>{context.card && <Items build={() => items(context.card!)} context />}</ContextMenu.Content>
      </ContextMenu.Root>
      <Menu.Root handle={handle} modal={false}>
        {({ payload }) => (
          <Menu.Content align="end">
            {typeof payload === 'string' && <Items build={() => items(payload)} />}
          </Menu.Content>
        )}
      </Menu.Root>
    </>
  );
}

/** A card's "…" button, opening its view's menu on it; shown on hover or focus by the caller's classes, always on a touch screen. */
export function CardMenuButton({ handle, card, label, className }: Readonly<{ handle: CardMenuHandle; card: string; label: string; className?: string }>) {
  return (
    <Menu.Trigger
      handle={handle}
      payload={card}
      render={<Button size="icon-sm" aria-label={label} title="More actions" className={cn('text-muted', className)} />}
      // The card under it opens on a click; the button only opens the menu.
      onClick={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      <Ellipsis />
    </Menu.Trigger>
  );
}
