import { useApi } from '../../ApiContext';
import { ArrowLeft, FolderInput, GitCommitHorizontal, Link2, ListChecks, Pencil, Plus, Sparkles, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { plannerErrorText, type Card, type CardDetail, type ChecklistItem } from '../../api';
import { cardPath, isStarted, linkTargets, lockedReason, openBlockerSeqs } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Loading, Markdown, Note, relTime, timeAgo, useApp } from '../common';
import { PanelHeader, SidePanel } from '../Subagents';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { Input } from '../ui/input';
import { Segmented } from '../ui/segmented';
import { Select } from '../ui/select';
import { useCardActions, type TriageResult } from './actions';
import { usePlannerTasks, useShownBoard } from './context';
import { CardMarkers, KindIcon, ProgressText, StatusMark, TaskChip, expiresIn, kindLabel } from './parts';
import { RequestItem } from './Requests';
import { CardEditor } from './TreeView';

/** A labelled group in the card panel: a caption heading over its rows, separated by spacing. */
function Group({ title, children, action }: Readonly<{ title: string; children: ReactNode; action?: ReactNode }>) {
  return (
    <section aria-label={title} className="flex flex-col gap-1.5">
      <h3 className="flex items-center gap-2 text-caption font-medium text-muted">
        <span>{title}</span>
        <span className="fade-rule flex-1" aria-hidden="true" />
        {action}
      </h3>
      {children}
    </section>
  );
}

/**
 * The card detail (ADR 0005 §10): a side panel with the card's fields, win condition,
 * checklist, blocker links, its evidence trail (every request, decided ones included), hold
 * history, comments, the owner-only acceptance command and paths, and the card's actions.
 */
export function CardPanel({ inline, open, onClose, onClosed }: Readonly<{ inline: boolean; open: boolean; onClose: () => void; onClosed: () => void }>) {
  const { ui, cards, openCard } = useShownBoard();
  const { narrow } = useApp();
  const byId = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const card = ui.selected ? byId.get(ui.selected) : undefined;
  return (
    <SidePanel id="planner-card" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label={card ? `Card #${card.seq}` : 'Card'}>
      <PanelHeader>
        {narrow && (
          <Button size="icon-md" aria-label="Back to the plan" className="-ml-1 text-muted" onClick={onClose}>
            <ArrowLeft />
          </Button>
        )}
        {card ? (
          <>
            {/* The title is the body's, where it is edited; the header names the card by its number. */}
            <KindIcon kind={card.kind} />
            <span className="min-w-0 flex-1 text-ui tabular-nums text-body">#{card.seq}</span>
            <StatusMark status={card.status} label />
          </>
        ) : (
          <span className="flex-1 text-title text-ink">Card</span>
        )}
        {!narrow && (
          <Button size="icon-md" aria-label="Close card" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        )}
      </PanelHeader>
      {card ? <CardBody key={card.id} card={card} byId={byId} onOpen={openCard} /> : <p className="px-4 py-6 text-ui text-muted">This card is no longer on the board.</p>}
    </SidePanel>
  );
}

function CardBody({ card: c, byId, onOpen }: Readonly<{ card: Card; byId: ReadonlyMap<string, Card>; onOpen: (id: string) => void }>) {
  const api = useApi();
  const { projects, jobs } = useShownBoard();
  const { sessions } = usePlannerTasks();
  const [detail, setDetail] = useState<CardDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [triage, setTriage] = useState<TriageResult | null>(null);
  const [comment, setComment] = useState('');
  const cardActions = useCardActions({ onTriage: (_, t) => setTriage(t) });
  const busy = cardActions.busy?.key ?? null;
  const run = <T,>(key: string, verb: string, op: () => Promise<T>) => cardActions.run(c.id, key, verb, op);
  const unassigned = !c.project_id;
  const leaf = c.kind === 'subtask';
  // A started subtask keeps its plan (ADR 0005 decision 8); ticks and the owner's own fields go on.
  const locked = lockedReason(c);
  const path = cardPath(c, byId).slice(0, -1);
  const job = Object.values(jobs).filter((j) => j.card_id === c.id && j.kind === 'suggest').at(-1);
  // Check at HEAD is a job, started here or from a row's menu: its run arrives in the job's last board_job frame.
  const checking = Object.values(jobs).filter((j) => j.card_id === c.id && j.kind === 'check').at(-1);
  const check = checking?.status === 'done' ? checking.accept : undefined;

  // The trail (comments, requests, holds) follows the card: fetched again on each of its revisions.
  useEffect(() => {
    const controller = new AbortController();
    api.planner
      .card(c.id, controller.signal)
      .then((d) => {
        setDetail(d);
        setDetailError(null);
      })
      .catch((e: unknown) => !controller.signal.aborted && setDetailError(plannerErrorText(e)));
    return () => controller.abort();
  }, [api.planner, c.id, c.revision]);

  const actions = cardActions.actionsOf(c);

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain px-4 pt-2 pb-6">
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-2">
          {path.length > 0 && (
            <nav aria-label="Card path" className="flex min-w-0 flex-wrap items-center gap-1 text-caption text-muted">
              {path.map((p, i) => (
                <span key={p.id} className="flex min-w-0 items-center gap-1">
                  {i > 0 && <span aria-hidden="true" className="text-faint">›</span>}
                  <button type="button" className="truncate hover:text-ink hover:underline" onClick={() => onOpen(p.id)}>
                    #{p.seq} {p.title}
                  </button>
                </span>
              ))}
            </nav>
          )}
          {editing ? (
            <CardEditor
              card={c}
              onCancel={() => setEditing(false)}
              onSave={async (patch) => {
                setEditing(false);
                await run('edit', 'save the card', () => api.planner.edit(c.id, patch));
              }}
            />
          ) : (
            <div className="group/title flex items-start gap-2">
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <p className="text-title text-ink [overflow-wrap:anywhere]">{c.title}</p>
                <p className={cn('text-ui [overflow-wrap:anywhere]', c.win_condition ? 'text-body' : 'text-muted')}>{c.win_condition ? <><span className="text-muted">Done means: </span>{c.win_condition}</> : 'No win condition yet.'}</p>
              </div>
              {!unassigned && !locked && (
                <Button size="icon" aria-label="Edit title and win condition" className="text-muted" onClick={() => setEditing(true)}>
                  <Pencil />
                </Button>
              )}
            </div>
          )}
          <div className="flex flex-wrap items-center gap-1.5 text-caption text-muted">
            <span>{kindLabel(c.kind)}</span>
            {!leaf && <ProgressText card={c} long />}
            {!c.confirmed && <Chip><Sparkles aria-hidden="true" className="size-3" />Suggested, {expiresIn(c.expires_at)}</Chip>}
            {c.held_by && <TaskChip taskId={c.held_by} />}
            <CardMarkers card={c} blockers={openBlockerSeqs(c, byId)} />
            {c.pinned_sha && <span className="flex items-center gap-1" title="HEAD at the last owner touch"><GitCommitHorizontal aria-hidden="true" className="size-3" />{c.pinned_sha.slice(0, 7)}</span>}
            {c.effort && <span>Effort {c.effort}</span>}
            {c.due && <span>Due {c.due}</span>}
            {c.labels.map((l) => <Chip key={l} fill="well">{l}</Chip>)}
          </div>
          {locked && !unassigned && <Note tone="muted">{locked}</Note>}
          {job?.status === 'running' && <Loading label="Suggesting…" delay={0} />}
          {job?.status === 'failed' && <Note tone="error">Suggesting failed{job.error ? `: ${job.error}` : '.'}</Note>}
          {checking?.status === 'running' && <Loading label="Checking at HEAD…" delay={0} />}
          {checking?.status === 'failed' && <Note tone="error">Check at HEAD failed{checking.error ? `: ${checking.error}` : '.'}</Note>}
        </div>

        {unassigned ? <MoveToProject card={c} projects={projects} /> : actions.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {actions.map((a) => (
              <Button key={a.key} size="sm" variant={a.primary ? 'primary' : a.danger ? 'danger' : 'secondary'} loading={busy === a.key} disabled={!!a.reason || (!!busy && busy !== a.key)} aria-describedby={a.reason ? `planner-action-${a.key}-reason` : undefined} onClick={a.onClick}>
                {a.icon}
                {a.label}
              </Button>
            ))}
          </div>
        )}
        {!unassigned && actions.map((a) => a.reason && <Note key={a.key} id={`planner-action-${a.key}-reason`} className="-mt-3">{a.reason}</Note>)}

        {check && (
          // The run's tail is a block of its own: a <pre> cannot sit inside the Note's paragraph.
          <div className="flex flex-col gap-1">
            <Note tone={check.exit === 0 ? 'muted' : 'error'}>
              <span className="font-mono text-code-sm">{check.cmd}</span> exited {check.exit} at {check.head}.
            </Note>
            {check.tail && <pre className="max-h-40 overflow-x-hidden overflow-y-auto whitespace-pre-wrap [overflow-wrap:anywhere] rounded-sm bg-code-bg px-2 py-1.5 font-mono text-code-sm text-ink shadow-well">{check.tail}</pre>}
          </div>
        )}
        {triage && (
          <div className="flex flex-col gap-1.5 rounded-md bg-tint-well px-3 py-2">
            <p className="text-ui text-ink">
              <span className="font-medium capitalize">{triage.verdict}</span>: {triage.sentence}
            </p>
            <div className="flex flex-wrap gap-1.5">
              {triage.verdict === 'valid' && <Button size="sm" variant="secondary" onClick={() => void run('repin', 're-pin the subtask', () => api.planner.confirm(c.id)).then(() => setTriage(null))}>Re-pin at {triage.head}</Button>}
              {triage.verdict === 'moot' && <Button size="sm" variant="danger" onClick={() => cardActions.ask({ title: `Cancel #${c.seq}?`, label: 'Why (required)', confirm: 'Cancel card', danger: true, required: true, initial: triage.sentence, run: (t) => api.planner.status(c.id, 'cancelled', t) })}>Cancel with this comment</Button>}
              {triage.verdict === 'conflicts' && <Button size="sm" variant="secondary" onClick={() => setComment(triage.sentence)}>Add as a comment</Button>}
            </div>
          </div>
        )}

        {c.desc && (
          <Group title="Description">
            <Markdown text={c.desc} />
          </Group>
        )}

        <Checklist
          card={c}
          // The service refuses edits to an Unassigned or a cancelled card ("restore it first").
          readOnly={unassigned || c.status === 'cancelled'}
          locked={!!locked}
          busy={!!busy}
          save={async (checklist) => {
            const saved = await run('checklist', 'save the checklist', async () => {
              await api.planner.edit(c.id, { checklist });
              return true;
            });
            return saved === true;
          }}
        />

        <Links card={c} byId={byId} onOpen={onOpen} readOnly={unassigned} locked={locked} />

        <Group title="Evidence trail">
          {detailError && <Note tone="error" role="alert">Could not load the trail: {detailError}</Note>}
          {!detail && !detailError && <Loading />}
          {detail && detail.requests.length === 0 && <p className="text-caption text-muted">No requests on this card.</p>}
          {detail && detail.requests.length > 0 && (
            <ul className="flex flex-col gap-2">
              {[...detail.requests].reverse().map((r) => (
                <li key={r.id}>
                  <RequestItem request={r} byId={byId} showCard={false} />
                </li>
              ))}
            </ul>
          )}
        </Group>

        {detail && detail.holds.length > 0 && (
          <Group title="Attempts">
            <ol className="flex flex-col gap-1">
              {detail.holds.map((h, i) => (
                <li key={h.id} className="flex min-w-0 flex-wrap items-center gap-1.5 text-caption text-muted">
                  <span className="tabular-nums">#{i + 1}</span>
                  <TaskChip taskId={h.task_id} />
                  <span>from {h.baseline_head.slice(0, 7)}, {timeAgo(h.started_at)}</span>
                  <span>{h.ended_at ? `ended ${timeAgo(h.ended_at)}${h.end_reason ? `: ${h.end_reason}` : ''}` : 'holding'}</span>
                </li>
              ))}
            </ol>
          </Group>
        )}

        <Group title="Comments">
          {detail?.comments.map((cm) => (
            <div key={cm.id} className={cn('flex flex-col gap-0.5 rounded-sm px-2 py-1.5', cm.automatic ? 'text-muted' : 'bg-tint-well')}>
              <span className="text-caption text-muted">
                {cm.author === 'owner' ? 'You' : cm.author === 'uam' ? 'UAM' : taskAuthor(cm.author, sessions)} · {relTime(cm.created_at)}
              </span>
              <span className="text-ui whitespace-pre-wrap text-body [overflow-wrap:anywhere]">{cm.body}</span>
            </div>
          ))}
          {detail && detail.comments.length === 0 && <p className="text-caption text-muted">No comments.</p>}
          {!unassigned && (
            <form
              className="flex items-center gap-1.5"
              onSubmit={(e) => {
                e.preventDefault();
                const body = comment.trim();
                if (body) void run('comment', 'add the comment', () => api.planner.comment(c.id, body)).then(() => setComment(''));
              }}
            >
              <Input size="md" aria-label="Add a comment" placeholder="Add a comment" value={comment} onChange={(e) => setComment(e.target.value)} />
              <Button type="submit" size="md" variant="secondary" disabled={!comment.trim() || busy === 'comment'}>
                Add
              </Button>
            </form>
          )}
        </Group>

        {leaf && !unassigned && <OwnerFields key={c.id} card={c} />}
      </div>
      {cardActions.dialogs}
    </div>
  );
}

const taskAuthor = (author: string, sessions: { id: string; name: string; title: string }[]) => {
  const s = sessions.find((x) => `task:${x.id}` === author);
  return s ? s.name || s.title || 'A task' : 'A task';
};

/**
 * The checklist: tick an item, click its text to rename it (Enter or leaving the field saves,
 * Esc cancels), remove it, or add one at the end. Each save sends the whole list, built from the
 * card as it stands at that moment. A read-only card shows its items only, and nothing when empty.
 */
/** The checklist. `locked`: a started subtask's items are its plan, so they are only ticked. */
export function Checklist({ card: c, readOnly, locked = false, busy, save }: Readonly<{ card: Card; readOnly: boolean; locked?: boolean; busy: boolean; save: (checklist: ChecklistItem[]) => Promise<boolean> }>) {
  const [editing, setEditing] = useState<number | null>(null);
  const [adding, setAdding] = useState('');
  const list = useRef<HTMLUListElement>(null);
  if ((readOnly || locked) && c.checklist.length === 0) return null;
  const fixed = readOnly || locked;
  const done = c.checklist.filter((i) => i.done).length;
  // Enter and Esc hand focus back to the item; a click elsewhere keeps it where it went.
  const close = (i: number, refocus: boolean) => {
    setEditing(null);
    if (refocus) requestAnimationFrame(() => list.current?.querySelector<HTMLElement>(`[data-item="${i}"]`)?.focus());
  };
  return (
    <Group title={c.checklist.length ? `Checklist ${done}/${c.checklist.length}` : 'Checklist'}>
      {c.checklist.length > 0 && (
        <ul ref={list} className="flex flex-col gap-0.5">
          {c.checklist.map((item, i) => (
            <li key={i} className="flex min-h-7 items-start gap-2 text-ui text-body pointer-coarse:min-h-11">
              <label className="flex h-7 shrink-0 items-center pointer-coarse:-mx-[15px] pointer-coarse:h-11 pointer-coarse:w-11 pointer-coarse:justify-center">
                <input
                  type="checkbox"
                  aria-label={item.text}
                  className="size-3.5 accent-accent"
                  checked={item.done}
                  disabled={readOnly || busy}
                  onChange={(e) => void save(c.checklist.map((x, j) => (j === i ? { ...x, done: e.target.checked } : x)))}
                />
              </label>
              {editing === i ? (
                <ItemEditor
                  text={item.text}
                  onCancel={(refocus) => close(i, refocus)}
                  onSave={async (text, refocus) => {
                    const ok = await save(c.checklist.map((x, j) => (j === i ? { ...x, text } : x)));
                    if (ok) close(i, refocus);
                    return ok;
                  }}
                />
              ) : fixed ? (
                <span className={cn('min-w-0 flex-1 py-1 [overflow-wrap:anywhere]', item.done && 'text-muted line-through')}>{item.text}</span>
              ) : (
                <>
                  <button
                    type="button"
                    data-item={i}
                    aria-label={`Edit ${item.text}`}
                    disabled={busy}
                    className={cn('min-w-0 flex-1 cursor-text py-1 text-left [overflow-wrap:anywhere] hover:text-ink pointer-coarse:py-3', item.done && 'text-muted line-through')}
                    onClick={() => setEditing(i)}
                  >
                    {item.text}
                  </button>
                  <Button size="icon-sm" className="mt-0.5 text-muted pointer-coarse:mt-2.5" aria-label={`Remove ${item.text}`} disabled={busy} onClick={() => void save(c.checklist.filter((_, j) => j !== i))}>
                    <X />
                  </Button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      {!fixed && (
        <form
          className="flex items-center gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            const text = adding.trim();
            if (text) void save([...c.checklist, { text, done: false }]).then((ok) => ok && setAdding(''));
          }}
        >
          <Input size="md" aria-label="Add an item" placeholder="Add an item" value={adding} onChange={(e) => setAdding(e.target.value)} />
          <Button type="submit" size="md" variant="secondary" disabled={!adding.trim() || busy}>
            <Plus />
            Add item
          </Button>
        </form>
      )}
    </Group>
  );
}

/** One checklist item's text being renamed; an empty text is refused here, and the field stays open. */
function ItemEditor({ text, onSave, onCancel }: Readonly<{ text: string; onSave: (text: string, refocus: boolean) => Promise<boolean>; onCancel: (refocus: boolean) => void }>) {
  const [value, setValue] = useState(text);
  const [error, setError] = useState('');
  // Enter, then the blur of the field going away, must not save twice.
  const settled = useRef(false);
  const commit = async (refocus: boolean) => {
    if (settled.current) return;
    const next = value.trim();
    if (!next) return setError('An item needs text. Esc keeps the old one, or remove the item.');
    settled.current = true;
    if (next === text) return onCancel(refocus);
    if (!(await onSave(next, refocus))) settled.current = false;
  };
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1">
      <Input
        size="md"
        aria-label="Item text"
        aria-invalid={!!error || undefined}
        aria-describedby={error ? 'checklist-item-error' : undefined}
        value={value}
        // eslint-disable-next-line jsx-a11y/no-autofocus -- Clicking the item's text opens this field to type into.
        autoFocus
        onChange={(e) => {
          setValue(e.target.value);
          setError('');
        }}
        onBlur={() => void commit(false)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            void commit(true);
          } else if (e.key === 'Escape') {
            e.stopPropagation();
            settled.current = true;
            onCancel(true);
          }
        }}
      />
      {error && <Note tone="error" role="alert" id="checklist-item-error">{error}</Note>}
    </div>
  );
}

/**
 * What the card waits for and what needs it, with a way to add a blocker and to remove one, then
 * what it waits for through its parents. Dependencies are planning, so either card may be a suggestion, marked as
 * one; links map one level at a time (§3), so the picker offers only cards of this kind under this
 * parent. A started subtask keeps its links (`locked`) until it is released.
 */
export function Links({ card: c, byId, onOpen, readOnly, locked }: Readonly<{ card: Card; byId: ReadonlyMap<string, Card>; onOpen: (id: string) => void; readOnly: boolean; locked: string | null }>) {
  const api = useApi();
  const { notify } = useShownBoard();
  const [adding, setAdding] = useState('');
  const blockers = c.blocked_by.map((id) => byId.get(id)).filter((x): x is Card => !!x);
  const blocks = c.blocks.map((id) => byId.get(id)).filter((x): x is Card => !!x);
  const candidates = linkTargets(c, [...byId.values()]);
  // Each parent's open blockers hold this card back too, nearest parent first.
  const inherited = cardPath(c, byId)
    .slice(0, -1)
    .reverse()
    .map((p) => ({ via: p, open: p.blocked_by.map((id) => byId.get(id)).filter((x): x is Card => !!x && x.status !== 'done' && x.status !== 'cancelled') }))
    .filter((w) => w.open.length > 0);
  if (readOnly && !blockers.length && !blocks.length) return null;
  const act = (verb: string, op: () => Promise<unknown>) => op().catch((e: unknown) => notify({ tone: 'error', text: `Could not ${verb}: ${plannerErrorText(e)}` }));
  const row = (x: Card, remove?: () => void) => (
    <li key={x.id} className="flex min-w-0 items-center gap-1.5">
      <StatusMark status={x.status} />
      <button type="button" className="min-w-0 truncate text-left text-ui text-body hover:text-ink hover:underline" onClick={() => onOpen(x.id)}>
        #{x.seq} {x.title}
      </button>
      {!x.confirmed && <span className="shrink-0 text-caption text-muted">Suggested</span>}
      {remove && !readOnly && !locked && !isStarted(x) && (
        <Button size="icon-sm" className="ml-auto text-muted" aria-label={`Remove the link to #${x.seq}`} onClick={remove}>
          <X />
        </Button>
      )}
    </li>
  );
  return (
    <Group title="Dependencies">
      {blockers.length > 0 && <p className="text-caption text-muted">Waits for</p>}
      {blockers.length > 0 && <ul className="flex flex-col gap-0.5">{blockers.map((b) => row(b, () => void act('remove the link', () => api.planner.unlink(b.id, c.id))))}</ul>}
      {blocks.length > 0 && <p className="text-caption text-muted">Needed by</p>}
      {blocks.length > 0 && <ul className="flex flex-col gap-0.5">{blocks.map((b) => row(b))}</ul>}
      {inherited.map((w) => (
        <div key={w.via.id} className="flex flex-col gap-0.5">
          <p className="text-caption text-muted">
            Waits for, through its {w.via.kind} #{w.via.seq}
          </p>
          <ul className="flex flex-col gap-0.5">{w.open.map((b) => row(b))}</ul>
        </div>
      ))}
      {!blockers.length && !blocks.length && !inherited.length && <p className="text-caption text-muted">No dependencies.</p>}
      {!readOnly && !locked && candidates.length > 0 && (
        <div className="flex items-center gap-1.5">
          <Select
            aria-label="Add a blocker"
            value={adding}
            className={cn('h-8 min-w-0 flex-1', !adding && 'text-muted')}
            onValueChange={setAdding}
            // The placeholder is the trigger's text only; the list offers the candidates.
            items={[{ value: '', label: 'Add a blocker…', hidden: true }, ...candidates.map((x) => ({ value: x.id, label: `#${x.seq} ${x.title}${x.confirmed ? '' : ' (suggested)'}` }))]}
          />
          <Button size="md" variant="secondary" disabled={!adding} onClick={() => void act('add the link', () => api.planner.link(adding, c.id)).then(() => setAdding(''))}>
            <Link2 />
            Link
          </Button>
        </div>
      )}
    </Group>
  );
}

const modeOf = (cmd: string | null): 'inherit' | 'none' | 'command' => {
  if (cmd === null) return 'inherit';
  return cmd === '' ? 'none' : 'command';
};

/**
 * The owner-only fields (§1): the acceptance command, tri-state (inherit the Project's, none,
 * or a command), and the paths staleness watches. No agent tool can write either. The form
 * lives as long as the card is shown; when the stored values change (a save, another window),
 * a field takes them only while it is untouched, so a card update never wipes unsaved input.
 */
function OwnerFields({ card: c }: Readonly<{ card: Card }>) {
  const api = useApi();
  const { notify } = useShownBoard();
  const [mode, setMode] = useState(modeOf(c.accept_cmd));
  const [cmd, setCmd] = useState(c.accept_cmd ?? '');
  const [paths, setPaths] = useState(c.paths.join('\n'));
  const storedPaths = c.paths.join('\n');
  const accept_cmd = mode === 'inherit' ? null : mode === 'none' ? '' : cmd.trim();
  const [base, setBase] = useState({ accept: c.accept_cmd, paths: storedPaths });
  if (base.accept !== c.accept_cmd || base.paths !== storedPaths) {
    if (accept_cmd === base.accept) {
      setMode(modeOf(c.accept_cmd));
      setCmd(c.accept_cmd ?? '');
    }
    if (paths === base.paths) setPaths(storedPaths);
    setBase({ accept: c.accept_cmd, paths: storedPaths });
  }
  const [inherited, setInherited] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let alive = true;
    api.planner.project(c.project_id).then((p) => alive && setInherited(p.accept_cmd)).catch(() => {});
    return () => {
      alive = false;
    };
  }, [api.planner, c.project_id]);
  const nextPaths = paths.split('\n').map((p) => p.trim()).filter(Boolean);
  const changed = accept_cmd !== c.accept_cmd || nextPaths.join('\n') !== storedPaths;
  const save = async () => {
    setBusy(true);
    try {
      await api.planner.edit(c.id, { accept_cmd, paths: nextPaths });
    } catch (e) {
      notify({ tone: 'error', text: `Could not save the acceptance settings: ${plannerErrorText(e)}` });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Group title="Owner only">
      <div className="flex flex-col gap-1.5">
        <span id={`accept-${c.id}`} className="text-ui font-medium text-ink">Acceptance command</span>
        <Segmented
          size="sm"
          aria-labelledby={`accept-${c.id}`}
          value={mode}
          onValueChange={(v) => setMode(v as typeof mode)}
          items={[
            { value: 'inherit', label: 'Inherit' },
            { value: 'none', label: 'None' },
            { value: 'command', label: 'Command' },
          ]}
        />
        {mode === 'inherit' && <Note>{inherited === null ? 'Uses the project default.' : inherited ? <>Uses the project default: <span className="font-mono text-code-sm">{inherited}</span></> : 'The project has no default: nothing runs.'}</Note>}
        {mode === 'none' && <Note>Nothing runs; a done request goes by its diff and commits. Use this for a subtask that is red by design.</Note>}
        {mode === 'command' && <Input size="md" aria-label="Command" className="font-mono text-code-sm" spellCheck={false} placeholder="make test" value={cmd} onChange={(e) => setCmd(e.target.value)} />}
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor={`paths-${c.id}`} className="text-ui font-medium text-ink">Paths</label>
        <textarea id={`paths-${c.id}`} spellCheck={false} placeholder="One glob per line, e.g. web/src/lib/*.ts" className="min-h-16 w-full resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none" value={paths} onChange={(e) => setPaths(e.target.value)} />
        <Note>Files changed since the pin that match these mark the subtask stale.</Note>
      </div>
      <div className="flex justify-end">
        <Button size="sm" variant="primary" loading={busy} disabled={!changed || (mode === 'command' && !cmd.trim())} onClick={() => void save()}>
          <ListChecks />
          Save
        </Button>
      </div>
    </Group>
  );
}

/** An Unassigned card is read-only until the owner moves it into a git Project (§11); the move counts as a touch. */
function MoveToProject({ card: c, projects }: Readonly<{ card: Card; projects: { id: string; name: string; no_git?: string }[] }>) {
  const api = useApi();
  const { notify, setUi } = useShownBoard();
  const git = projects.filter((p) => !p.no_git);
  const [target, setTarget] = useState(git[0]?.id ?? '');
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col gap-2 rounded-md bg-tint-well px-3 py-2.5">
      <p className="text-caption text-muted">Unassigned cards are read-only. Move this one into a project to plan and launch it.</p>
      <div className="flex items-center gap-1.5">
        <Select aria-label="Project" className="h-8 min-w-0 flex-1" value={target} onValueChange={setTarget} items={git.map((p) => ({ value: p.id, label: p.name }))} />
        <Button
          size="md"
          variant="primary"
          loading={busy}
          disabled={!target}
          onClick={async () => {
            setBusy(true);
            try {
              await api.planner.edit(c.id, { project_id: target });
              setUi({ project: target, selected: c.id, panel: 'card' });
            } catch (e) {
              notify({ tone: 'error', text: `Could not move the card: ${plannerErrorText(e)}` });
            } finally {
              setBusy(false);
            }
          }}
        >
          <FolderInput />
          Move
        </Button>
      </div>
    </div>
  );
}
