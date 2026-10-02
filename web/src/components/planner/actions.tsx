import { Ban, Check, CheckCheck, Ellipsis, ListRestart, MoveRight, PanelRightOpen, Play, RotateCcw, Sparkles, Split, SquareTerminal, Stethoscope, Undo2, Workflow } from 'lucide-react';
import { useMemo, useRef, useState, type ComponentProps, type ReactElement, type ReactNode } from 'react';
import { api, plannerErrorText, type Card, type TriageVerdict } from '../../api';
import { cardPath, isStarted, startedUnderReason } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { ContextMenu, Menu, type ActionItem } from '../ui/menu';
import { useShownBoard } from './context';
import { BriefDialog, DoneDialog, LaunchDialog, MoveDialog, ReasonDialog, SplitDialog, moveTargets, type BriefAsk, type LaunchAsk, type ReasonAsk } from './dialogs';

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
}

/**
 * The owner's actions on the shown Board's cards (ADR 0005 §10) and the state they run on: the
 * one in flight and the dialogs they open. The card panel shows a card's actions as buttons; the
 * Tree and the Board offer the same ones in a row's "…" and context menus (DESIGN.md principle 6).
 * Check at HEAD reports through its job, which the card panel shows (`onCheck` opens it there);
 * Triage answers with a verdict only the panel has room for, so it is offered with `onTriage` only.
 */
export function useCardActions({ onTriage, onCheck }: Readonly<{ onTriage?: (card: Card, t: TriageResult) => void; onCheck?: (card: Card) => void }> = {}) {
  const { cards, notify, openCard } = useShownBoard();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const [busy, setBusy] = useState<{ card: string; key: string } | null>(null);
  // The card a dialog is about stays while the dialog animates out.
  const [target, setTarget] = useState<string | null>(null);
  const [dialog, setDialog] = useState<'done' | 'move' | 'split' | null>(null);
  const [reason, setReason] = useState<ReasonAsk | null>(null);
  const [brief, setBrief] = useState<BriefAsk | null>(null);
  const [launching, setLaunching] = useState<LaunchAsk | null>(null);

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

  /** A card's actions as it stands (§10); none for an Unassigned card, which only moves into a Project. */
  function actionsOf(c: Card): CardAction[] {
    if (!c.project_id) return [];
    const leaf = c.kind === 'subtask';
    const launched = (res: { card: Card; session: { id: string } }) => notify({ tone: 'muted', text: `Launched #${res.card.seq} ${res.card.title} in a new task.`, task: res.session.id });
    const launch = async (label: string) => {
      // Work starts only on confirmed cards (§5): a suggestion, or a subtask under one, is launched
      // through the confirm step, which names what launching confirms and the suggestions it waits on.
      const confirms = leaf ? cardPath(c, byId).filter((x) => !x.confirmed).reverse() : [];
      if (confirms.length) {
        // Its own suggested blockers, then its parents', which hold it back too.
        const waits = cardPath(c, byId)
          .reverse()
          .flatMap((at) =>
            at.blocked_by.flatMap((id) => {
              const b = byId.get(id);
              return b && !b.confirmed && b.status !== 'done' && b.status !== 'cancelled' ? [{ card: b, via: at === c ? undefined : at }] : [];
            }),
          );
        setLaunching({ card: c, confirms, waits, run: () => api.planner.launch(c.id, { confirm: true }).then(launched) });
        return;
      }
      const res = await run(c.id, 'launch', label === 'Launch' ? 'launch the subtask' : 'start the story', () => api.planner.launch(c.id));
      if (res) launched(res);
    };
    const actions: CardAction[] = [];
    if (!c.confirmed) actions.push({ key: 'confirm', label: 'Confirm', icon: <Check />, primary: true, onClick: () => void run(c.id, 'confirm', 'confirm the card', () => api.planner.confirm(c.id)) });
    if (leaf && (c.status === 'planned' || c.status === 'todo')) actions.push({ key: 'launch', label: 'Launch', icon: <Play />, primary: c.confirmed, onClick: () => void launch('Launch') });
    if (c.kind === 'story' && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'launch', label: 'Do whole story', icon: <Play />, onClick: () => void launch('Do whole story') });
    if (!leaf && c.status !== 'cancelled') {
      actions.push({ key: 'plan', label: 'Plan with agent', icon: <Workflow />, onClick: () => setBrief({ kind: 'plan', title: `Plan #${c.seq} with an agent`, run: async ({ brief: b }) => { const r = await api.planner.plan(c.id, { brief: b }); notify({ tone: 'muted', text: `A planning task started for #${c.seq}.`, task: r.session.id }); } }) });
      actions.push({ key: 'suggest', label: c.kind === 'epic' ? 'Suggest stories' : 'Suggest subtasks', icon: <Sparkles />, onClick: () => setBrief({ kind: 'suggest', title: c.kind === 'epic' ? `Suggest stories for #${c.seq}` : `Suggest subtasks for #${c.seq}`, run: (body) => api.planner.suggest(c.id, body) }) });
    }
    if (leaf && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'done', label: 'Mark done', icon: <CheckCheck />, onClick: () => open(c, 'done') });
    if (leaf && c.status === 'doing') actions.push({ key: 'release', label: 'Release', icon: <Undo2 />, onClick: () => setReason({ title: `Release #${c.seq}?`, description: 'The subtask goes back to To do and its Task stops holding it. Pending requests are withdrawn.', label: 'Comment (optional)', confirm: 'Release', required: false, run: (t) => api.planner.release(c.id, t) }) });
    // Not under a cancelled card: the service refuses it until that is restored.
    if (leaf && c.status === 'done' && !cardPath(c, byId).some((a) => a.status === 'cancelled')) {
      actions.push({ key: 'todo', label: 'Back to To do', icon: <ListRestart />, onClick: () => setReason({ title: `Move #${c.seq} back to To do?`, description: 'The subtask goes back to To do for another attempt.', label: 'Comment (optional)', confirm: 'Back to To do', required: false, run: (t) => api.planner.status(c.id, 'todo', t) }) });
    }
    if (leaf && c.confirmed && c.status !== 'done' && c.status !== 'cancelled') {
      actions.push({ key: 'check', label: 'Check at HEAD', icon: <SquareTerminal />, onClick: () => void run(c.id, 'check', 'run the acceptance command', () => api.planner.check(c.id)).then((r) => r && onCheck?.(c)) });
    }
    if (leaf && c.stale && onTriage) actions.push({ key: 'triage', label: 'Triage', icon: <Stethoscope />, onClick: () => void run(c.id, 'triage', 'triage the subtask', () => api.planner.triage(c.id)).then((r) => r && onTriage(c, r)) });
    // A started subtask keeps its plan until it is released: no move, no split. A story moves
    // only while nothing under it has started, so it says which subtask holds it in place.
    const started = isStarted(c);
    if (c.kind !== 'epic' && c.status !== 'cancelled' && !started && (c.parent_id || moveTargets(c, cards).length > 0)) actions.push({ key: 'move', label: 'Move to…', icon: <MoveRight />, reason: startedUnderReason(c, byId) ?? undefined, onClick: () => open(c, 'move') });
    if (leaf && c.status !== 'cancelled' && !started) actions.push({ key: 'split', label: 'Split', icon: <Split />, onClick: () => open(c, 'split') });
    if (c.status !== 'cancelled' && c.status !== 'done') {
      actions.push({
        key: 'cancel',
        label: 'Cancel',
        icon: <Ban />,
        danger: true,
        onClick: () => setReason({ title: `Cancel #${c.seq}?`, description: leaf ? 'The subtask is cancelled; its hold and pending requests end. Restore brings it back.' : 'Every open subtask under it is cancelled with this comment. Restore brings back exactly these.', label: 'Why (required)', confirm: 'Cancel card', danger: true, required: true, run: (t) => api.planner.status(c.id, 'cancelled', t) }),
      });
    }
    if (c.status === 'cancelled') actions.push({ key: 'restore', label: 'Restore', icon: <RotateCcw />, onClick: () => setReason({ title: `Restore #${c.seq}?`, description: 'The card and everything its cancel took with it reopen, confirmed.', label: 'Why (required)', confirm: 'Restore', required: true, run: (t) => api.planner.restore(c.id, t) }) });
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
  const dialogs = (
    <>
      <ReasonDialog ask={reason} onClose={() => setReason(null)} />
      <BriefDialog ask={brief} onClose={() => setBrief(null)} />
      <LaunchDialog ask={launching} onClose={() => setLaunching(null)} />
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
