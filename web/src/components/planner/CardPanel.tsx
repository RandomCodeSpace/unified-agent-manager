import { ArrowLeft, Ban, Check, CheckCheck, FolderInput, GitCommitHorizontal, Link2, ListChecks, MoveRight, Pencil, Play, RotateCcw, Sparkles, Split, SquareTerminal, Stethoscope, Undo2, Workflow, X } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, plannerErrorText, type AcceptRun, type Card, type CardDetail, type TriageVerdict } from '../../api';
import { cardPath, openBlockerSeqs } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Loading, Markdown, Note, relTime, useApp } from '../common';
import { PanelHeader, SidePanel } from '../Subagents';
import { Button } from '../ui/button';
import { Chip } from '../ui/chip';
import { Input } from '../ui/input';
import { Segmented } from '../ui/segmented';
import { Select } from '../ui/select';
import { usePlannerTasks, useShownBoard } from './context';
import { BriefDialog, DoneDialog, MoveDialog, ReasonDialog, SplitDialog, moveTargets, type BriefAsk, type ReasonAsk } from './dialogs';
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
            <KindIcon kind={card.kind} />
            <span className="shrink-0 text-caption tabular-nums text-muted">#{card.seq}</span>
            <span className="min-w-0 flex-1 truncate text-title text-ink" title={card.title}>{card.title}</span>
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
  const { notify, projects, jobs, cards } = useShownBoard();
  const { sessions } = usePlannerTasks();
  const [dialog, setDialog] = useState<'done' | 'move' | 'split' | null>(null);
  const [detail, setDetail] = useState<CardDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [reason, setReason] = useState<ReasonAsk | null>(null);
  const [brief, setBrief] = useState<BriefAsk | null>(null);
  const [check, setCheck] = useState<AcceptRun | null>(null);
  const [triage, setTriage] = useState<{ verdict: TriageVerdict; sentence: string; head: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [comment, setComment] = useState('');
  const unassigned = !c.project_id;
  const leaf = c.kind === 'subtask';
  const path = cardPath(c, byId).slice(0, -1);
  const job = Object.values(jobs).filter((j) => j.card_id === c.id).at(-1);

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
  }, [c.id, c.revision]);

  async function run<T>(key: string, verb: string, op: () => Promise<T>): Promise<T | undefined> {
    setBusy(key);
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
  const launch = async (label: string) => {
    const res = await run('launch', label === 'Launch' ? 'launch the subtask' : 'start the story', () => api.planner.launch(c.id));
    if (res) notify({ tone: 'muted', text: `Launched #${res.card.seq} ${res.card.title} in a new task.`, task: res.session.id });
  };

  // The owner's actions for this card as it stands (§10).
  const actions: { key: string; label: string; icon: ReactNode; onClick: () => void; primary?: boolean; danger?: boolean }[] = [];
  if (!unassigned) {
    if (!c.confirmed) actions.push({ key: 'confirm', label: 'Confirm', icon: <Check />, primary: true, onClick: () => void run('confirm', 'confirm the card', () => api.planner.confirm(c.id)) });
    if (leaf && (c.status === 'planned' || c.status === 'todo')) actions.push({ key: 'launch', label: 'Launch', icon: <Play />, primary: c.confirmed, onClick: () => void launch('Launch') });
    if (c.kind === 'story' && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'launch', label: 'Do whole story', icon: <Play />, onClick: () => void launch('Do whole story') });
    if (!leaf && c.status !== 'cancelled') {
      actions.push({ key: 'plan', label: 'Plan with agent', icon: <Workflow />, onClick: () => setBrief({ kind: 'plan', title: `Plan #${c.seq} with an agent`, run: async ({ brief: b }) => { const r = await api.planner.plan(c.id, { brief: b }); notify({ tone: 'muted', text: `A planning task started for #${c.seq}.`, task: r.session.id }); } }) });
      actions.push({ key: 'suggest', label: c.kind === 'epic' ? 'Suggest stories' : 'Suggest subtasks', icon: <Sparkles />, onClick: () => setBrief({ kind: 'suggest', title: c.kind === 'epic' ? `Suggest stories for #${c.seq}` : `Suggest subtasks for #${c.seq}`, run: (body) => api.planner.suggest(c.id, body) }) });
    }
    if (leaf && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'done', label: 'Mark done', icon: <CheckCheck />, onClick: () => setDialog('done') });
    if (leaf && c.status === 'doing') actions.push({ key: 'release', label: 'Release', icon: <Undo2 />, onClick: () => setReason({ title: `Release #${c.seq}?`, description: 'The subtask goes back to To do and its Task stops holding it. Pending requests are withdrawn.', label: 'Comment (optional)', confirm: 'Release', required: false, run: (t) => api.planner.release(c.id, t) }) });
    if (leaf && c.confirmed && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'check', label: 'Check at HEAD', icon: <SquareTerminal />, onClick: () => void run('check', 'run the acceptance command', () => api.planner.check(c.id)).then((r) => r && setCheck(r.accept)) });
    if (leaf && c.stale) actions.push({ key: 'triage', label: 'Triage', icon: <Stethoscope />, onClick: () => void run('triage', 'triage the subtask', () => api.planner.triage(c.id)).then((r) => r && setTriage(r)) });
    if (c.kind !== 'epic' && c.status !== 'cancelled' && (c.parent_id || moveTargets(c, cards).length > 0)) actions.push({ key: 'move', label: 'Move to…', icon: <MoveRight />, onClick: () => setDialog('move') });
    if (leaf && c.status !== 'done' && c.status !== 'cancelled') actions.push({ key: 'split', label: 'Split', icon: <Split />, onClick: () => setDialog('split') });
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
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pt-2 pb-6">
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-2">
          {path.length > 0 && (
            <nav aria-label="Card path" className="flex min-w-0 flex-wrap items-center gap-1 text-caption text-muted">
              {path.map((p) => (
                <span key={p.id} className="flex min-w-0 items-center gap-1">
                  <button type="button" className="truncate hover:text-ink hover:underline" onClick={() => onOpen(p.id)}>
                    #{p.seq} {p.title}
                  </button>
                  <span aria-hidden="true" className="text-faint">›</span>
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
              {!unassigned && (
                <Button size="icon" aria-label="Edit title and win condition" className="text-muted" onClick={() => setEditing(true)}>
                  <Pencil />
                </Button>
              )}
            </div>
          )}
          <div className="flex flex-wrap items-center gap-1.5 text-caption text-muted">
            <span>{kindLabel(c.kind)}</span>
            {!leaf && <ProgressText card={c} />}
            {!c.confirmed && <Chip><Sparkles aria-hidden="true" className="size-3" />Suggested, {expiresIn(c.expires_at)}</Chip>}
            {c.held_by && <TaskChip taskId={c.held_by} />}
            <CardMarkers card={c} blockers={openBlockerSeqs(c, byId)} />
            {c.pinned_sha && <span className="flex items-center gap-1" title="HEAD at the last owner touch"><GitCommitHorizontal aria-hidden="true" className="size-3" />{c.pinned_sha.slice(0, 7)}</span>}
            {c.effort && <span>Effort {c.effort}</span>}
            {c.due && <span>Due {c.due}</span>}
            {c.labels.map((l) => <Chip key={l} fill="well">{l}</Chip>)}
          </div>
          {job?.status === 'running' && <Loading label="Suggesting…" delay={0} />}
          {job?.status === 'failed' && <Note tone="error">Suggesting failed{job.error ? `: ${job.error}` : '.'}</Note>}
        </div>

        {unassigned ? <MoveToProject card={c} projects={projects} /> : actions.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {actions.map((a) => (
              <Button key={a.key} size="sm" variant={a.primary ? 'primary' : a.danger ? 'danger' : 'secondary'} loading={busy === a.key} disabled={!!busy && busy !== a.key} onClick={a.onClick}>
                {a.icon}
                {a.label}
              </Button>
            ))}
          </div>
        )}

        {check && (
          <Note tone={check.exit === 0 ? 'muted' : 'error'} className="flex flex-col gap-1">
            <span>
              <span className="font-mono text-code-sm">{check.cmd}</span> exited {check.exit} at {check.head}.
            </span>
            {check.tail && <pre className="max-h-40 overflow-auto rounded-sm bg-code-bg px-2 py-1.5 font-mono text-code-sm text-ink shadow-well">{check.tail}</pre>}
          </Note>
        )}
        {triage && (
          <div className="flex flex-col gap-1.5 rounded-md bg-tint-well px-3 py-2">
            <p className="text-ui text-ink">
              <span className="font-medium capitalize">{triage.verdict}</span>: {triage.sentence}
            </p>
            <div className="flex flex-wrap gap-1.5">
              {triage.verdict === 'valid' && <Button size="sm" variant="secondary" onClick={() => void run('repin', 're-pin the subtask', () => api.planner.confirm(c.id)).then(() => setTriage(null))}>Re-pin at {triage.head}</Button>}
              {triage.verdict === 'moot' && <Button size="sm" variant="danger" onClick={() => setReason({ title: `Cancel #${c.seq}?`, label: 'Why (required)', confirm: 'Cancel card', danger: true, required: true, initial: triage.sentence, run: (t) => api.planner.status(c.id, 'cancelled', t) })}>Cancel with this comment</Button>}
              {triage.verdict === 'conflicts' && <Button size="sm" variant="secondary" onClick={() => setComment(triage.sentence)}>Add as a comment</Button>}
            </div>
          </div>
        )}

        {c.desc && (
          <Group title="Description">
            <Markdown text={c.desc} />
          </Group>
        )}

        {c.checklist.length > 0 && (
          <Group title={`Checklist ${c.checklist.filter((i) => i.done).length}/${c.checklist.length}`}>
            <ul className="flex flex-col gap-0.5">
              {c.checklist.map((item, i) => (
                <li key={i}>
                  <label className="flex min-h-7 items-start gap-2 text-ui text-body pointer-coarse:min-h-11">
                    <input
                      type="checkbox"
                      className="mt-1 size-3.5 accent-accent"
                      checked={item.done}
                      disabled={unassigned || !!busy}
                      onChange={(e) => void run('checklist', 'save the checklist', () => api.planner.edit(c.id, { checklist: c.checklist.map((x, j) => (j === i ? { ...x, done: e.target.checked } : x)) }))}
                    />
                    <span className={cn('min-w-0', item.done && 'text-muted line-through')}>{item.text}</span>
                  </label>
                </li>
              ))}
            </ul>
          </Group>
        )}

        <Links card={c} byId={byId} onOpen={onOpen} readOnly={unassigned} />

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
                  <span>from {h.baseline_head.slice(0, 7)}, {relTime(h.started_at)} ago</span>
                  <span>{h.ended_at ? `ended ${relTime(h.ended_at)} ago${h.end_reason ? `: ${h.end_reason}` : ''}` : 'holding'}</span>
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
      <ReasonDialog ask={reason} onClose={() => setReason(null)} />
      <BriefDialog ask={brief} onClose={() => setBrief(null)} />
      <DoneDialog card={c} byId={byId} open={dialog === 'done'} onClose={() => setDialog(null)} />
      <MoveDialog card={c} cards={cards} open={dialog === 'move'} onClose={() => setDialog(null)} />
      <SplitDialog card={c} byId={byId} open={dialog === 'split'} onClose={() => setDialog(null)} />
    </div>
  );
}

const taskAuthor = (author: string, sessions: { id: string; name: string; title: string }[]) => {
  const s = sessions.find((x) => `task:${x.id}` === author);
  return s ? s.name || s.title || 'A task' : 'A task';
};

/** Blocked by and blocks, with a way to add a blocker (confirmed cards only) and to remove one. */
function Links({ card: c, byId, onOpen, readOnly }: Readonly<{ card: Card; byId: ReadonlyMap<string, Card>; onOpen: (id: string) => void; readOnly: boolean }>) {
  const { notify } = useShownBoard();
  const [adding, setAdding] = useState('');
  const blockers = c.blocked_by.map((id) => byId.get(id)).filter((x): x is Card => !!x);
  const blocks = c.blocks.map((id) => byId.get(id)).filter((x): x is Card => !!x);
  const candidates = [...byId.values()].filter((x) => x.id !== c.id && x.confirmed && x.kind === 'subtask' && !c.blocked_by.includes(x.id) && x.status !== 'cancelled');
  if (readOnly && !blockers.length && !blocks.length) return null;
  const act = (verb: string, op: () => Promise<unknown>) => op().catch((e: unknown) => notify({ tone: 'error', text: `Could not ${verb}: ${plannerErrorText(e)}` }));
  const row = (x: Card, remove?: () => void) => (
    <li key={x.id} className="flex min-w-0 items-center gap-1.5">
      <StatusMark status={x.status} />
      <button type="button" className="min-w-0 truncate text-left text-ui text-body hover:text-ink hover:underline" onClick={() => onOpen(x.id)}>
        #{x.seq} {x.title}
      </button>
      {remove && !readOnly && (
        <Button size="icon-sm" className="ml-auto text-muted" aria-label={`Remove the link to #${x.seq}`} onClick={remove}>
          <X />
        </Button>
      )}
    </li>
  );
  return (
    <Group title="Blocker links">
      {blockers.length > 0 && <p className="text-caption text-muted">Blocked by</p>}
      {blockers.length > 0 && <ul className="flex flex-col gap-0.5">{blockers.map((b) => row(b, () => void act('remove the link', () => api.planner.unlink(b.id, c.id))))}</ul>}
      {blocks.length > 0 && <p className="text-caption text-muted">Blocks</p>}
      {blocks.length > 0 && <ul className="flex flex-col gap-0.5">{blocks.map((b) => row(b))}</ul>}
      {!blockers.length && !blocks.length && <p className="text-caption text-muted">No links.</p>}
      {!readOnly && candidates.length > 0 && (
        <div className="flex items-center gap-1.5">
          <Select
            aria-label="Add a blocker"
            value={adding}
            className="h-8 min-w-0 flex-1"
            onValueChange={setAdding}
            items={[{ value: '', label: 'Add a blocker…' }, ...candidates.map((x) => ({ value: x.id, label: `#${x.seq} ${x.title}` }))]}
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
  const { notify } = useShownBoard();
  const [mode, setMode] = useState(modeOf(c.accept_cmd));
  const [cmd, setCmd] = useState(c.accept_cmd ?? '');
  const [paths, setPaths] = useState(c.paths.join('\n'));
  const storedPaths = c.paths.join('\n');
  const [base, setBase] = useState({ accept: c.accept_cmd, paths: storedPaths });
  if (base.accept !== c.accept_cmd || base.paths !== storedPaths) {
    const localAccept = mode === 'inherit' ? null : mode === 'none' ? '' : cmd.trim();
    if (localAccept === base.accept) {
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
  }, [c.project_id]);
  const accept_cmd = mode === 'inherit' ? null : mode === 'none' ? '' : cmd.trim();
  const nextPaths = paths.split('\n').map((p) => p.trim()).filter(Boolean);
  const changed = accept_cmd !== c.accept_cmd || nextPaths.join('\n') !== c.paths.join('\n');
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
