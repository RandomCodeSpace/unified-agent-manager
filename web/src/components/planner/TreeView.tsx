import { Check, ChevronRight, ListPlus, Pencil, Plus, Sparkles, X } from 'lucide-react';
import { memo, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type SubmitEvent } from 'react';
import { api, plannerErrorText, type Card, type CardKind } from '../../api';
import { KIND_LABEL, STATUS_LABEL, buildOutline, lockedReason, openBlockerSeqs, type OutlineNode } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { Input } from '../ui/input';
import type { ActionItem } from '../ui/menu';
import { CardMenuButton, CardMenus, useCardActions, useCardMenuHandle, type CardMenuHandle } from './actions';
import { useShownBoard, type PlannerCreating, type PlannerUi } from './context';
import { CardMarkers, KindIcon, ProgressRing, ProgressText, StatusMark, TaskChip, expiresIn } from './parts';

type Row =
  | { type: 'card'; key: string; node: OutlineNode; level: number; parent: string; suggestion: boolean }
  | { type: 'suggested'; key: string; parent: string; count: number; level: number }
  | { type: 'create'; key: string; parent: string; kind: CardKind; level: number };

/**
 * The rows on screen, depth first: folded containers hide their children, a closed "+N suggested" its suggestions.
 * A card being added under a parent gets its form after that parent's last row (at the root, after the last root).
 */
function flatten(roots: OutlineNode[], rootSuggested: OutlineNode[], folded: Record<string, boolean>, open: Record<string, boolean>, creating: PlannerCreating | null): Row[] {
  const rows: Row[] = [];
  const create = (parent: string, level: number) => {
    if (creating && creating.parent === parent) rows.push({ type: 'create', key: 'create', parent, kind: creating.kind, level });
  };
  const walk = (nodes: OutlineNode[], suggested: OutlineNode[], level: number, parent: string, inSuggestion: boolean) => {
    for (const node of nodes) {
      rows.push({ type: 'card', key: node.card.id, node, level, parent, suggestion: inSuggestion || !node.card.confirmed });
      if (node.card.kind !== 'subtask' && !folded[node.card.id]) {
        walk(node.children, node.suggested, level + 1, node.card.id, inSuggestion || !node.card.confirmed);
        create(node.card.id, level + 1);
      }
    }
    if (suggested.length) {
      rows.push({ type: 'suggested', key: `suggested:${parent}`, parent, count: suggested.length, level });
      if (open[parent]) walk(suggested, [], level, parent, true);
    }
  };
  walk(roots, rootSuggested, 1, '', false);
  create('', 1);
  return rows;
}

/** What a row does, stable for the Tree's lifetime so the memoised rows keep their props. */
interface RowActions {
  keyDown: (e: KeyboardEvent<HTMLElement>, key: string) => void;
  click: (key: string) => void;
  fold: (id: string) => void;
  edit: (id: string) => void;
  add: (parent: string, kind: CardKind) => void;
  confirm: (id: string) => void;
  dismiss: (id: string) => void;
  /** The view's "…" menu the rows' buttons open; absent where no menu can open (a Picture-in-Picture window). */
  menu?: CardMenuHandle;
}

/**
 * The Tree (ADR 0005 §10): the plan as an outline, where most planning happens. Containers
 * show their progress; a parent's suggestions fold into one "+N suggested" row with Confirm and
 * Dismiss. Arrow keys move and fold, Enter opens the card, F2 edits its title and win condition
 * in place (an owner save confirms the card). Containers add stories and subtasks under them;
 * the root takes a subtask too (§3). Each row's "…" button and context menu hold its card's
 * actions (`menus`; off in a Picture-in-Picture window, where menus cannot open). Rows are
 * memoised on their card, so a `board` frame re-renders only the rows of the cards it changed.
 */
export function TreeView({ readOnly = false, menus = true }: Readonly<{ readOnly?: boolean; menus?: boolean }>) {
  const { ui, setUi, cards, openCard, notify } = useShownBoard();
  // Check at HEAD shows its run in the card panel, so a row's check opens the card there.
  const cardActions = useCardActions({ onCheck: (c) => openCard(c.id) });
  const menuHandle = useCardMenuHandle();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const outline = useMemo(() => buildOutline(cards, { epic: ui.epic, showCancelled: ui.showCancelled }), [cards, ui.epic, ui.showCancelled]);
  const creating = readOnly ? null : ui.creating;
  const rows = useMemo(() => flatten(outline.roots, outline.suggested, ui.folded, ui.suggestedOpen, creating), [outline, ui.folded, ui.suggestedOpen, creating]);
  const [focus, setFocus] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const tree = useRef<HTMLDivElement>(null);
  // The roving tab stop: the focused row, else the selection, else the first row.
  const current = rows.find((r) => r.key === focus) ?? rows.find((r) => r.key === ui.selected) ?? rows[0];

  const move = (key: string | undefined) => {
    if (!key) return;
    setFocus(key);
    // The Tree's own window: in a Picture-in-Picture pop-out the main page's frames may not run.
    const win = tree.current?.ownerDocument.defaultView ?? window;
    win.requestAnimationFrame(() => tree.current?.querySelector<HTMLElement>(`[data-row="${CSS.escape(key)}"]`)?.focus());
  };
  const toggleFold = (id: string, fold?: boolean) => setUi((u) => ({ folded: { ...u.folded, [id]: fold ?? !u.folded[id] } }));
  const toggleSuggested = (parent: string, open?: boolean) => setUi((u) => ({ suggestedOpen: { ...u.suggestedOpen, [parent]: open ?? !u.suggestedOpen[parent] } }));

  async function run(id: string, verb: string, op: () => Promise<unknown>) {
    setBusy((b) => ({ ...b, [id]: true }));
    try {
      await op();
    } catch (e) {
      notify({ tone: 'error', text: `Could not ${verb}: ${plannerErrorText(e)}` });
    } finally {
      setBusy(({ [id]: _, ...rest }) => rest);
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLElement>, key: string) {
    const row = rows.find((r) => r.key === key);
    if (!row || row.type === 'create' || editing || e.target !== e.currentTarget) return;
    const i = rows.indexOf(row);
    const container = row.type === 'card' && row.node.card.kind !== 'subtask';
    switch (e.key) {
      case 'ArrowDown':
        move(rows[i + 1]?.key);
        break;
      case 'ArrowUp':
        move(rows[i - 1]?.key);
        break;
      case 'Home':
        move(rows[0]?.key);
        break;
      case 'End':
        move(rows.at(-1)?.key);
        break;
      case 'ArrowRight':
        if (row.type === 'suggested') {
          if (!ui.suggestedOpen[row.parent]) toggleSuggested(row.parent, true);
          else move(rows[i + 1]?.key);
        } else if (container && ui.folded[row.node.card.id]) toggleFold(row.node.card.id, false);
        else if (container) move(rows[i + 1]?.key);
        break;
      case 'ArrowLeft':
        if (row.type === 'suggested' && ui.suggestedOpen[row.parent]) toggleSuggested(row.parent, false);
        else if (row.type === 'card' && container && !ui.folded[row.node.card.id]) toggleFold(row.node.card.id, true);
        else move(row.parent || undefined);
        break;
      case 'Enter':
      case ' ':
        if (row.type === 'suggested') toggleSuggested(row.parent);
        else openCard(row.node.card.id);
        break;
      case 'F2':
        if (row.type === 'card' && !readOnly && !lockedReason(row.node.card)) setEditing(row.node.card.id);
        break;
      default:
        return;
    }
    e.preventDefault();
  }

  /** A row's menu: what its hover buttons and keys do (add, edit, dismiss a suggestion), then the card's actions. */
  function menu(c: Card): ActionItem[] {
    if (readOnly || !c.project_id) return cardActions.menuOf(c, [{ key: 'edit', label: 'Edit', icon: <Pencil />, disabled: true, reason: 'An Unassigned card is read-only until it moves into a Project.', onSelect: () => {} }]);
    const lead: ActionItem[] = addsOf(c, readOnly).map((kind) => ({ key: `add-${kind}`, label: kind === 'story' ? 'Add story' : 'Add subtask', icon: kind === 'story' ? <ListPlus /> : <Plus />, takesFocus: true, onSelect: () => setUi(adding(c.id, kind)) }));
    const locked = lockedReason(c);
    lead.push(locked ? { key: 'edit', label: 'Edit', icon: <Pencil />, disabled: true, reason: locked, onSelect: () => {} } : { key: 'edit', label: 'Edit', icon: <Pencil />, takesFocus: true, onSelect: () => setEditing(c.id) });
    const trail: ActionItem[] = c.confirmed ? [] : [{ key: 'dismiss', label: 'Dismiss', icon: <X />, onSelect: () => void run(c.id, 'dismiss the card', () => api.planner.dismiss(c.id)) }];
    return cardActions.menuOf(c, lead, trail);
  }

  // The latest handlers, reached through one stable object: the rows' props stay equal across frames.
  const latest = useRef({ onKeyDown, run, openCard, toggleFold, toggleSuggested });
  useLayoutEffect(() => {
    latest.current = { onKeyDown, run, openCard, toggleFold, toggleSuggested };
  });
  const actions = useMemo<RowActions>(() => ({
    keyDown: (e, key) => latest.current.onKeyDown(e, key),
    click: (key) => {
      setFocus(key);
      if (key.startsWith('suggested:')) latest.current.toggleSuggested(key.slice('suggested:'.length));
      else latest.current.openCard(key);
    },
    fold: (id) => latest.current.toggleFold(id),
    edit: (id) => setEditing(id),
    add: (parent, kind) => setUi(adding(parent, kind)),
    confirm: (id) => void latest.current.run(id, 'confirm the card', () => api.planner.confirm(id)),
    dismiss: (id) => void latest.current.run(id, 'dismiss the card', () => api.planner.dismiss(id)),
    menu: menus ? menuHandle : undefined,
  }), [setUi, menus, menuHandle]);

  const project = ui.project && ui.project !== 'unassigned' ? ui.project : '';
  const addRoot = !readOnly && !!project && !creating;

  if (!rows.length) return <p className="px-4 py-6 text-ui text-muted">{ui.epic ? 'Nothing under this epic matches the filters.' : 'No cards yet.'}</p>;

  const treeProps = { ref: tree, role: 'tree', 'aria-label': 'Plan outline', className: 'flex flex-col' };
  const items = rows.map((row) => {
    const tabbable = row === current;
    if (row.type === 'suggested') return <SuggestedRow key={row.key} rowKey={row.key} count={row.count} level={row.level} open={!!ui.suggestedOpen[row.parent]} tabbable={tabbable} actions={actions} />;
    if (row.type === 'create') {
      return (
        <div key={row.key} role="treeitem" aria-level={row.level} aria-selected={false} tabIndex={-1} style={indentOf(row.level)} className="py-1 pr-2">
          <CreateForm
            kind={row.kind}
            parent={row.parent ? byId.get(row.parent) : undefined}
            onCancel={() => setUi({ creating: null })}
            onCreate={async (fields) => {
              try {
                const card = await api.planner.create({ project_id: project, kind: row.kind, parent_id: row.parent || null, ...fields });
                setUi({ creating: null, selected: card.id });
                move(card.id);
              } catch (e) {
                notify({ tone: 'error', text: `Could not add the ${KIND_LABEL[row.kind].toLowerCase()}: ${plannerErrorText(e)}` });
              }
            }}
          />
        </div>
      );
    }
    const c = row.node.card;
    if (editing === c.id) {
      return (
        <div key={row.key} role="treeitem" aria-level={row.level} aria-selected={ui.selected === c.id} tabIndex={-1} data-row={row.key} style={indentOf(row.level)} className="py-1 pr-2" onKeyDown={(e) => e.key === 'Escape' && (e.stopPropagation(), setEditing(null), move(c.id))}>
          <CardEditor
            card={c}
            onCancel={() => {
              setEditing(null);
              move(c.id);
            }}
            onSave={async (patch) => {
              setEditing(null);
              move(c.id);
              await run(c.id, 'save the card', () => api.planner.edit(c.id, patch));
            }}
          />
        </div>
      );
    }
    return (
      <CardRow
        key={row.key}
        card={c}
        level={row.level}
        selected={ui.selected === c.id}
        folded={!!ui.folded[c.id]}
        suggestion={row.suggestion}
        tabbable={tabbable}
        busy={!!busy[c.id]}
        blockers={openBlockerSeqs(c, byId)}
        readOnly={readOnly}
        actions={actions}
      />
    );
  });
  return (
    <div className="flex flex-col px-2 py-2">
      {menus ? (
        <CardMenus handle={menuHandle} items={(id) => (byId.has(id) ? menu(byId.get(id)!) : [])} render={<div />} {...treeProps}>
          {items}
        </CardMenus>
      ) : (
        <div {...treeProps}>{items}</div>
      )}
      {cardActions.dialogs}
      {addRoot && (
        <div className="flex pt-1 pl-2">
          <Button size="sm" className="text-muted" aria-label="Add a subtask at the root" onClick={() => actions.add('', 'subtask')}>
            <Plus />
            Add subtask
          </Button>
        </div>
      )}
    </div>
  );
}

const indentOf = (level: number) => ({ paddingLeft: `${(level - 1) * 20 + 8}px` });

/** The view state that opens the add form under `parent` (`''` for the root), unfolding it. */
const adding = (parent: string, kind: CardKind) => (u: PlannerUi): Partial<PlannerUi> => ({ creating: { parent, kind }, folded: parent ? { ...u.folded, [parent]: false } : u.folded });

/** What the owner adds under a card (§3): an epic takes stories and subtasks, a story subtasks. */
function addsOf(c: Card, readOnly: boolean): CardKind[] {
  if (readOnly || c.kind === 'subtask' || c.status === 'cancelled') return [];
  return c.kind === 'epic' ? ['story', 'subtask'] : ['subtask'];
}

const SuggestedRow = memo(function SuggestedRow({ rowKey, count, level, open, tabbable, actions }: Readonly<{ rowKey: string; count: number; level: number; open: boolean; tabbable: boolean; actions: RowActions }>) {
  return (
    <div
      role="treeitem"
      aria-level={level}
      aria-expanded={open}
      aria-selected={false}
      tabIndex={tabbable ? 0 : -1}
      data-row={rowKey}
      style={indentOf(level)}
      className="flex h-8 cursor-pointer items-center gap-1.5 rounded-sm pr-2 text-caption text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11"
      onClick={() => actions.click(rowKey)}
      onKeyDown={(e) => actions.keyDown(e, rowKey)}
    >
      <ChevronRight aria-hidden="true" className={cn('size-3.5 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
      <Sparkles aria-hidden="true" className="size-3.5 shrink-0" />
      <span>+{count} suggested</span>
    </div>
  );
});

/**
 * One card's row. Memoised: it renders again only when its card object or its own view state changes.
 * Its counts sit in one right-aligned column; the add and "…" buttons show with the row's hover or
 * focus (always on a touch screen, and while the menu is open), so a long outline stays quiet.
 */
const CardRow = memo(function CardRow({ card: c, level, selected, folded, suggestion, tabbable, busy, blockers, readOnly, actions }: Readonly<{
  card: Card;
  level: number;
  selected: boolean;
  folded: boolean;
  suggestion: boolean;
  tabbable: boolean;
  busy: boolean;
  blockers: string;
  readOnly: boolean;
  actions: RowActions;
}>) {
  const container = c.kind !== 'subtask';
  const adds = addsOf(c, readOnly);
  const menu = actions.menu;
  return (
    <div
      role="treeitem"
      aria-level={level}
      aria-expanded={container ? !folded : undefined}
      aria-selected={selected}
      // The label stands in for the row's content, so it carries the status the glyph shows.
      aria-label={`#${c.seq} ${c.title}, ${STATUS_LABEL[c.status]}`}
      tabIndex={tabbable ? 0 : -1}
      data-row={c.id}
      data-card={c.id}
      style={indentOf(level)}
      className={cn(
        'group/row flex min-h-8 cursor-pointer items-center gap-1.5 rounded-sm pr-1.5 text-ui transition-colors duration-100 focus-visible:-outline-offset-2 pointer-coarse:min-h-11',
        selected ? 'bg-tint-selected text-ink' : 'text-body hover:bg-tint-hover',
        c.status === 'cancelled' && 'opacity-60',
      )}
      onClick={() => actions.click(c.id)}
      onDoubleClick={() => !readOnly && !lockedReason(c) && actions.edit(c.id)}
      onKeyDown={(e) => actions.keyDown(e, c.id)}
    >
      {container ? (
        <button
          type="button"
          tabIndex={-1}
          aria-label={folded ? `Open #${c.seq}` : `Fold #${c.seq}`}
          className="-ml-1 flex size-5 shrink-0 items-center justify-center rounded-xs text-faint hover:text-body"
          onClick={(e) => {
            e.stopPropagation();
            actions.fold(c.id);
          }}
        >
          <ChevronRight aria-hidden="true" className={cn('size-3.5 transition-transform duration-160 ease-app', !folded && 'rotate-90')} />
        </button>
      ) : (
        <span aria-hidden="true" className="-ml-1 size-5 shrink-0" />
      )}
      {container ? (
        <>
          <ProgressRing card={c} />
          <KindIcon kind={c.kind} className="max-sm:hidden" />
        </>
      ) : (
        <StatusMark status={c.status} />
      )}
      <span className="shrink-0 text-caption tabular-nums text-muted">#{c.seq}</span>
      <span className={cn('min-w-0 truncate', c.kind === 'epic' ? 'font-semibold text-ink' : c.kind === 'story' && 'font-medium text-ink', c.status === 'cancelled' && 'line-through')} title={c.title}>
        {c.title}
      </span>
      {/* The win condition fills the room on a wide screen; without one, a spacer does, so the right cluster keeps the edge. */}
      {c.win_condition && <span className="hidden min-w-0 flex-1 truncate text-caption text-muted lg:block" title={c.win_condition}>{c.win_condition}</span>}
      <span className={cn('min-w-0 flex-1', c.win_condition && 'lg:hidden')} />
      <span className="flex shrink-0 items-center gap-1">
        {suggestion && (
          <Chip title={expiresIn(c.expires_at)}>
            <Sparkles aria-hidden="true" className="size-3" />
            <span className="max-sm:sr-only">Suggested</span>
          </Chip>
        )}
        <CardMarkers card={c} blockers={blockers} compact />
        {c.held_by && <TaskChip taskId={c.held_by} className="max-sm:max-w-24" />}
        {!c.confirmed && !readOnly && <SuggestionActions busy={busy} title={c.title} onConfirm={() => actions.confirm(c.id)} onDismiss={() => actions.dismiss(c.id)} />}
        {(adds.length > 0 || menu) && (
          <span className="flex items-center gap-0.5 opacity-0 transition-opacity duration-100 group-focus-within/row:opacity-100 group-hover/row:opacity-100 has-data-popup-open:opacity-100 pointer-coarse:opacity-100">
            {adds.map((kind) => (
              <Button
                key={kind}
                size="icon-sm"
                className="text-muted"
                aria-label={`Add ${kind === 'story' ? 'a story' : 'a subtask'} to #${c.seq}`}
                title={kind === 'story' ? 'Add story' : 'Add subtask'}
                onClick={(e) => {
                  e.stopPropagation();
                  actions.add(c.id, kind);
                }}
              >
                {kind === 'story' ? <ListPlus /> : <Plus />}
              </Button>
            ))}
            {menu && <CardMenuButton handle={menu} card={c.id} label={`Actions for #${c.seq}`} />}
          </span>
        )}
      </span>
      {/* The counts' column; a subtask keeps its room too, but on a phone, where the title needs it more. */}
      <span className={cn('min-w-8 shrink-0 text-right', !container && 'max-sm:hidden')}>{container && <ProgressText card={c} />}</span>
    </div>
  );
});

function SuggestionActions({ busy, title, onConfirm, onDismiss }: Readonly<{ busy: boolean; title: string; onConfirm: () => void; onDismiss: () => void }>) {
  const stop = (fn: () => void) => (e: { stopPropagation: () => void }) => {
    e.stopPropagation();
    fn();
  };
  return (
    <span className="flex items-center gap-0.5">
      <Button size="sm" variant="secondary" className="h-6 px-1.5" disabled={busy} aria-label={`Confirm ${title}`} title="Confirm" onClick={stop(onConfirm)}>
        <Check />
        <span className="max-sm:hidden">Confirm</span>
      </Button>
      <Button size="icon-sm" className="text-muted" disabled={busy} aria-label={`Dismiss ${title}`} title="Dismiss" onClick={stop(onDismiss)}>
        <X />
      </Button>
    </span>
  );
}

/** A new card in place: its title and win condition; Enter adds it, Esc leaves. Owner-created, so it is confirmed (and so are its parents). */
function CreateForm({ kind, parent, onCreate, onCancel }: Readonly<{ kind: CardKind; parent: Card | undefined; onCreate: (fields: { title: string; win_condition: string }) => Promise<void>; onCancel: () => void }>) {
  const [title, setTitle] = useState('');
  const [win, setWin] = useState('');
  const [busy, setBusy] = useState(false);
  const noun = KIND_LABEL[kind].toLowerCase();
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!title.trim() || busy) return;
    setBusy(true);
    await onCreate({ title: title.trim(), win_condition: win.trim() });
    setBusy(false);
  };
  return (
    // The browser's own context menu for its inputs (paste), not the view's card menu.
    <form aria-label={parent ? `New ${noun} in #${parent.seq}` : `New ${noun}`} className="flex flex-col gap-1.5 rounded-md bg-tint-well p-2" onSubmit={(e) => void submit(e)} onContextMenu={(e) => e.stopPropagation()}>
      {/* eslint-disable-next-line jsx-a11y/no-autofocus -- Add opens this form to type the new card's title into. */}
      <Input size="md" aria-label="Title" placeholder={`New ${noun}`} value={title} autoFocus onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && onCancel()} />
      <Input size="md" aria-label="Win condition" placeholder="What done means, in one line" value={win} onChange={(e) => setWin(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && onCancel()} />
      <span className="flex items-center gap-2">
        <Button type="submit" size="sm" variant="primary" loading={busy} disabled={!title.trim()}>
          Add {noun}
        </Button>
        <Button size="sm" onClick={onCancel}>
          Cancel
        </Button>
      </span>
    </form>
  );
}

/** Title and win condition in place; Enter saves both, Esc leaves them. Saving is an owner edit, so it confirms the card. */
export function CardEditor({ card, onSave, onCancel, extra }: Readonly<{ card: Card; onSave: (patch: { title: string; win_condition: string }) => void | Promise<void>; onCancel: () => void; extra?: ReactNode }>) {
  const [title, setTitle] = useState(card.title);
  const [win, setWin] = useState(card.win_condition);
  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    if (!title.trim()) return;
    void onSave({ title: title.trim(), win_condition: win.trim() });
  };
  return (
    // The browser's own context menu for its inputs (paste), not the view's card menu.
    <form aria-label={`Edit #${card.seq}`} className="flex flex-col gap-1.5 rounded-md bg-tint-well p-2" onSubmit={submit} onContextMenu={(e) => e.stopPropagation()}>
      {/* eslint-disable-next-line jsx-a11y/no-autofocus -- F2 and double-click move focus into the editor they open. */}
      <Input size="md" aria-label="Title" value={title} autoFocus onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && onCancel()} />
      <Input size="md" aria-label="Win condition" placeholder="What done means, in one line" value={win} onChange={(e) => setWin(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && onCancel()} />
      {extra}
      <span className="flex items-center gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={!title.trim()}>
          Save
        </Button>
        <Button size="sm" onClick={onCancel}>
          Cancel
        </Button>
        <span className="text-caption text-muted">Saving confirms the card.</span>
      </span>
    </form>
  );
}
