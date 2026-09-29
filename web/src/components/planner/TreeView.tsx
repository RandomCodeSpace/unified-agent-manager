import { Check, ChevronRight, Sparkles, X } from 'lucide-react';
import { useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type SubmitEvent } from 'react';
import { api, describeError, type Card } from '../../api';
import { STATUS_LABEL, buildOutline, type OutlineNode } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { Input } from '../ui/input';
import { useShownBoard } from './context';
import { CardMarkers, KindIcon, ProgressRing, ProgressText, StatusMark, TaskChip, expiresIn } from './parts';

type Row =
  | { type: 'card'; key: string; node: OutlineNode; level: number; parent: string; suggestion: boolean }
  | { type: 'suggested'; key: string; parent: string; count: number; level: number };

/** The rows on screen, depth first: folded containers hide their children, a closed "+N suggested" its suggestions. */
function flatten(roots: OutlineNode[], rootSuggested: OutlineNode[], folded: Record<string, boolean>, open: Record<string, boolean>): Row[] {
  const rows: Row[] = [];
  const walk = (nodes: OutlineNode[], suggested: OutlineNode[], level: number, parent: string, inSuggestion: boolean) => {
    for (const node of nodes) {
      rows.push({ type: 'card', key: node.card.id, node, level, parent, suggestion: inSuggestion || !node.card.confirmed });
      if (node.card.kind !== 'subtask' && !folded[node.card.id]) walk(node.children, node.suggested, level + 1, node.card.id, inSuggestion || !node.card.confirmed);
    }
    if (suggested.length) {
      rows.push({ type: 'suggested', key: `suggested:${parent}`, parent, count: suggested.length, level });
      if (open[parent]) walk(suggested, [], level, parent, true);
    }
  };
  walk(roots, rootSuggested, 1, '', false);
  return rows;
}

/**
 * The Tree (ADR 0005 §10): the plan as an outline, where most planning happens. Containers
 * show their progress; a parent's suggestions fold into one "+N suggested" row with Confirm and
 * Dismiss. Arrow keys move and fold, Enter opens the card, F2 edits its title and win condition
 * in place (an owner save confirms the card).
 */
export function TreeView({ readOnly = false }: Readonly<{ readOnly?: boolean }>) {
  const { ui, setUi, cards, sessions, openCard, openTask, notify } = useShownBoard();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const outline = useMemo(() => buildOutline(cards, { epic: ui.epic, showCancelled: ui.showCancelled }), [cards, ui.epic, ui.showCancelled]);
  const rows = flatten(outline.roots, outline.suggested, ui.folded, ui.suggestedOpen);
  const [focus, setFocus] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const tree = useRef<HTMLDivElement>(null);
  // The roving tab stop: the focused row, else the selection, else the first row.
  const current = rows.find((r) => r.key === focus) ?? rows.find((r) => r.key === ui.selected) ?? rows[0];

  const move = (key: string | undefined) => {
    if (!key) return;
    setFocus(key);
    requestAnimationFrame(() => tree.current?.querySelector<HTMLElement>(`[data-row="${CSS.escape(key)}"]`)?.focus());
  };
  const toggleFold = (id: string, fold?: boolean) => setUi((u) => ({ folded: { ...u.folded, [id]: fold ?? !u.folded[id] } }));
  const toggleSuggested = (parent: string, open?: boolean) => setUi((u) => ({ suggestedOpen: { ...u.suggestedOpen, [parent]: open ?? !u.suggestedOpen[parent] } }));

  async function run(id: string, verb: string, op: () => Promise<unknown>) {
    setBusy((b) => ({ ...b, [id]: true }));
    try {
      await op();
    } catch (e) {
      notify({ tone: 'error', text: `Could not ${verb}: ${describeError(e)}` });
    } finally {
      setBusy(({ [id]: _, ...rest }) => rest);
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLElement>, row: Row) {
    if (editing || e.target !== e.currentTarget) return;
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
        if (row.type === 'card' && !readOnly) setEditing(row.node.card.id);
        break;
      default:
        return;
    }
    e.preventDefault();
  }

  if (!rows.length) return <p className="px-4 py-6 text-ui text-muted">{ui.epic ? 'Nothing under this epic matches the filters.' : 'No cards yet.'}</p>;

  return (
    <div ref={tree} role="tree" aria-label="Plan outline" className="flex flex-col px-2 py-2">
      {rows.map((row) => {
        const tabIndex = row === current ? 0 : -1;
        const indent = { paddingLeft: `${(row.level - 1) * 20 + 8}px` };
        if (row.type === 'suggested') {
          const open = !!ui.suggestedOpen[row.parent];
          return (
            <div
              key={row.key}
              role="treeitem"
              aria-level={row.level}
              aria-expanded={open}
              aria-selected={false}
              tabIndex={tabIndex}
              data-row={row.key}
              style={indent}
              className="flex h-8 cursor-pointer items-center gap-1.5 rounded-sm pr-2 text-caption text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11"
              onClick={() => {
                setFocus(row.key);
                toggleSuggested(row.parent);
              }}
              onKeyDown={(e) => onKeyDown(e, row)}
            >
              <ChevronRight aria-hidden="true" className={cn('size-3.5 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
              <Sparkles aria-hidden="true" className="size-3.5 shrink-0" />
              <span>+{row.count} suggested</span>
            </div>
          );
        }
        const c = row.node.card;
        const container = c.kind !== 'subtask';
        const folded = !!ui.folded[c.id];
        const selected = ui.selected === c.id;
        if (editing === c.id) {
          return (
            <div key={row.key} role="treeitem" aria-level={row.level} aria-selected={selected} tabIndex={-1} data-row={row.key} style={indent} className="py-1 pr-2" onKeyDown={(e) => e.key === 'Escape' && (e.stopPropagation(), setEditing(null), move(c.id))}>
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
          <div
            key={row.key}
            role="treeitem"
            aria-level={row.level}
            aria-expanded={container ? !folded : undefined}
            aria-selected={selected}
            // The label stands in for the row's content, so it carries the status the glyph shows.
            aria-label={`#${c.seq} ${c.title}, ${STATUS_LABEL[c.status]}`}
            tabIndex={tabIndex}
            data-row={row.key}
            style={indent}
            className={cn(
              'group/row flex min-h-8 cursor-pointer items-center gap-1.5 rounded-sm pr-1.5 text-ui transition-colors duration-100 focus-visible:-outline-offset-2 pointer-coarse:min-h-11',
              selected ? 'bg-tint-selected text-ink' : 'text-body hover:bg-tint-hover',
              c.status === 'cancelled' && 'opacity-60',
            )}
            onClick={() => {
              setFocus(c.id);
              openCard(c.id);
            }}
            onDoubleClick={() => !readOnly && setEditing(c.id)}
            onKeyDown={(e) => onKeyDown(e, row)}
          >
            {container ? (
              <button
                type="button"
                tabIndex={-1}
                aria-label={folded ? `Open #${c.seq}` : `Fold #${c.seq}`}
                className="-ml-1 flex size-5 shrink-0 items-center justify-center rounded-xs text-faint hover:text-body"
                onClick={(e) => {
                  e.stopPropagation();
                  toggleFold(c.id);
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
            {c.win_condition && <span className="hidden min-w-0 flex-1 truncate text-caption text-muted lg:block" title={c.win_condition}>{c.win_condition}</span>}
            <span className="min-w-0 flex-1 lg:hidden" />
            <span className="flex shrink-0 items-center gap-1">
              {row.suggestion && (
                <Chip title={expiresIn(c.expires_at)}>
                  <Sparkles aria-hidden="true" className="size-3" />
                  <span className="max-sm:sr-only">Suggested</span>
                </Chip>
              )}
              <CardMarkers card={c} byId={byId} compact />
              {c.held_by && <TaskChip taskId={c.held_by} sessions={sessions} onOpen={openTask} className="max-sm:max-w-24" />}
              {container && <ProgressText card={c} />}
              {!c.confirmed && !readOnly && (
                <SuggestionActions busy={!!busy[c.id]} title={c.title} onConfirm={() => void run(c.id, 'confirm the card', () => api.planner.confirm(c.id))} onDismiss={() => void run(c.id, 'dismiss the card', () => api.planner.dismiss(c.id))} />
              )}
            </span>
          </div>
        );
      })}
    </div>
  );
}

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
    <form aria-label={`Edit #${card.seq}`} className="flex flex-col gap-1.5 rounded-md bg-tint-well p-2" onSubmit={submit}>
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
