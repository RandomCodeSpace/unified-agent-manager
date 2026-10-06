import { useApi } from '../../ApiContext';
import { Pencil, Plus, X } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { plannerErrorText, doneGuard, errorCode, errorRefs, resolveTaskDefaults, type BoardProject, type Card, type CardKind, type DoneGuard, type Meta, type RevertPreview, type Settings, type TaskDefaults } from '../../api';
import { KIND_LABEL, cardPath, childIndex, isStarted, leavesUnder, waitsOf } from '../../lib/board';
import { cn } from '../../lib/cn';
import { Note, useApp } from '../common';
import { Field, TaskDefaultsFields } from '../TaskDefaults';
import { Button } from '../ui/button';
import { AlertDialog, Dialog } from '../ui/dialog';
import { Input } from '../ui/input';
import { Segmented } from '../ui/segmented';
import { Select } from '../ui/select';
import { useShownBoard } from './context';
import { splitSentence } from './Requests';

const areaClass =
  'min-h-20 w-full resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well transition-[background-color] placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none';

export interface ReasonAsk {
  title: string;
  description?: ReactNode;
  label: string;
  confirm: string;
  danger?: boolean;
  /** A comment is required (Cancel, Restore); optional otherwise (Release). */
  required: boolean;
  initial?: string;
  run: (text: string) => Promise<unknown>;
}

/**
 * A dialog that asks for one comment before an action: Cancel and Restore require one (§8),
 * Release takes one if given. It stays open, with the refusal, when the service says no.
 */
export function ReasonDialog({ ask, onClose }: Readonly<{ ask: ReasonAsk | null; onClose: () => void }>) {
  const [text, setText] = useState(ask?.initial ?? '');
  const [shown, setShown] = useState(ask);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  if (ask && ask !== shown) {
    setShown(ask);
    setText(ask.initial ?? '');
    setError(null);
  }
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!shown || (shown.required && !text.trim())) return;
    setBusy(true);
    setError(null);
    try {
      await shown.run(text.trim());
      onClose();
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && onClose()} onClosed={() => setShown(null)} initialFocus={field} title={shown?.title ?? ''} description={shown?.description}>
      <form id="planner-reason" className="flex flex-col gap-2" onSubmit={(e) => void submit(e)}>
        <Field id="planner-reason-text" label={shown?.label ?? 'Comment'}>
          <textarea id="planner-reason-text" ref={field} className={areaClass} required={shown?.required} value={text} onChange={(e) => setText(e.target.value)} />
        </Field>
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Keep
          </Button>
          <Button type="submit" variant={shown?.danger ? 'danger' : 'primary'} className={shown?.danger ? 'bg-sunken' : undefined} loading={busy} disabled={!!shown?.required && !text.trim()}>
            {shown?.confirm ?? 'Save'}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export interface LaunchAsk {
  card: Card;
  /** Do whole story on a story; the subtask's own launch otherwise. */
  whole: boolean;
  /** The suggestions launching confirms: the subtask, then its suggested parents; none for a confirmed one. */
  confirms: Card[];
  /** Suggested blockers still open, its own then its parents' (`via`): they stay suggestions and keep blocking. */
  waits: { card: Card; via?: Card }[];
  run: (body: TaskDefaults & { brief: string }) => Promise<unknown>;
}

const cardItem = (c: Card) => (
  <li key={c.id}>
    #{c.seq} {c.title} <span className="text-muted">· {KIND_LABEL[c.kind]}</span>
  </li>
);

/**
 * Launch and Do whole story (§5): the new Task's model, effort, context size and mode, starting
 * from the New task defaults in Settings, and an optional brief its first prompt carries. Work
 * starts only on confirmed cards, so launching a suggestion confirms it and its suggested parents
 * in the same action, and the dialog names them. Suggested blockers are listed, and stay as they
 * are. It stays open, with the refusal, when the service says no.
 */
export function LaunchDialog({ ask, onClose }: Readonly<{ ask: LaunchAsk | null; onClose: () => void }>) {
  const { meta, settings } = useApp();
  const [shown, setShown] = useState(ask);
  // Null until changed here: the New task defaults.
  const [picked, setPicked] = useState<TaskDefaults | null>(null);
  const [brief, setBrief] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (ask && ask !== shown) {
    setShown(ask);
    setPicked(null);
    setBrief('');
    setError(null);
  }
  const selection = picked ?? resolveTaskDefaults(meta, settings.task_defaults, settings.hidden_models);
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!shown || !selection) return;
    setBusy(true);
    setError(null);
    try {
      await shown.run({ ...selection, brief: brief.trim() });
      onClose();
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  const seq = shown ? `#${shown.card.seq}` : '';
  const confirms = !!shown?.confirms.length;
  let title = `Launch ${seq}?`;
  let description = 'A new task starts work on this subtask.';
  let label = 'Launch';
  if (confirms) {
    title = `Confirm and launch ${seq}?`;
    description = 'A task starts work only on confirmed cards. Launching confirms these suggestions:';
    label = 'Confirm and launch';
  } else if (shown?.whole) {
    title = `Do whole story ${seq}?`;
    description = 'A new task starts on the first confirmed subtask waiting in it, then takes the next ones in order.';
    label = 'Do whole story';
  }
  return (
    <Dialog open={!!ask} onOpenChange={(o) => !o && onClose()} onClosed={() => setShown(null)} title={title} description={description}>
      <form className="flex flex-col gap-2" onSubmit={(e) => void submit(e)}>
        {confirms && (
          <ul aria-label="Launching confirms" className="list-disc pl-4 text-ui text-body">
            {shown?.confirms.map(cardItem)}
          </ul>
        )}
        {!!shown?.waits.length && (
          <div className="mt-1 flex flex-col gap-1 rounded-md bg-warning-wash px-3 py-2 text-caption text-body">
            <span className="font-medium text-ink">Still waits on</span>
            <ul aria-label="Still waits on" className="list-disc pl-4">
              {shown.waits.map(({ card: b, via }) => (
                <li key={`${b.id}:${via?.id ?? ''}`}>
                  #{b.seq} {b.title} <span className="text-muted">· {KIND_LABEL[b.kind]}{via && ` (via its ${via.kind} #${via.seq})`}</span>
                </li>
              ))}
            </ul>
            <span className="text-muted">They stay suggestions and block {seq} until they are done or cancelled.</span>
          </div>
        )}
        <div className="mt-2 flex flex-col gap-3">
          {selection && <TaskDefaultsFields prefix="planner-launch" value={selection} disabled={busy} onChange={setPicked} />}
          <Field id="planner-launch-brief" label="Brief">
            <textarea id="planner-launch-brief" className={areaClass} placeholder="Anything the task should know first (optional)" value={brief} onChange={(e) => setBrief(e.target.value)} />
          </Field>
        </div>
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            {confirms ? 'Keep' : 'Cancel'}
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!selection}>
            {label}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export interface ApproveAsk {
  epic: Card;
}

/** The epic and its live cards, depth first in rank order: what the Approve dialog shows and lists. */
function approvalList(epic: Card, cards: readonly Card[]): Card[] {
  const index = childIndex(cards);
  const out = [cards.find((c) => c.id === epic.id) ?? epic];
  const walk = (id: string) => {
    for (const c of index.get(id) ?? []) {
      if (c.status === 'cancelled') continue;
      out.push(c);
      if (c.kind !== 'subtask') walk(c.id);
    }
  };
  walk(epic.id);
  return out;
}

/**
 * The run's settings to start from: the epic's own when it was approved before, else the New
 * task defaults, keeping their model only when Settings names one (ADR 0006 §8): a run never
 * takes a fallback model without the owner picking it.
 */
function runStart(epic: Card, meta: Meta | null, settings: Settings): TaskDefaults | null {
  if (epic.run) return { provider: epic.run.provider, model: epic.run.model, effort: epic.run.effort, context_size: epic.run.context_size || 'default', mode: epic.run.mode };
  const base = resolveTaskDefaults(meta, settings.task_defaults, settings.hidden_models);
  if (!base) return null;
  const named = settings.task_defaults?.provider === base.provider && settings.task_defaults.model === base.model;
  return named ? base : { ...base, model: '', effort: '', context_size: 'default' };
}

const PARALLEL_ITEMS = ['1', '2', '3', '4'].map((value) => ({ value, label: value }));

const seqs = (cards: readonly Card[]) => cards.map((c) => `#${c.seq}`).join(', ');
const isAre = (n: number) => (n === 1 ? 'is' : 'are');

/**
 * Approve (ADR 0006 §6.2, §8): the owner's one approval of an epic. It shows the epic's live
 * cards as they were when it opened, grouped by story, with what each waits on, the proposals it
 * confirms ("new") and the paused ones, and says inline what the service would refuse. The run
 * needs a model the owner picks, a mode and how many subtasks run at a time. It posts the cards
 * with the revisions it showed: when one changed meanwhile, it names them, shows them as they are
 * now and asks again.
 */
export function ApproveDialog({ ask, onClose }: Readonly<{ ask: ApproveAsk | null; onClose: () => void }>) {
  const api = useApi();
  const { meta, settings } = useApp();
  const { cards, reload } = useShownBoard();
  const [shown, setShown] = useState(ask);
  // The cards as shown, and the Board they were taken from; taken again once the Board changes after a `stale`.
  const [snap, setSnap] = useState(() => ({ source: cards, list: ask ? approvalList(ask.epic, cards) : [] }));
  const [retake, setRetake] = useState(false);
  const [picked, setPicked] = useState<TaskDefaults | null>(null);
  const [parallel, setParallel] = useState('2');
  // The Project's planner settings (its default acceptance command among them), once loaded for the Project shown.
  const [loaded, setLoaded] = useState<{ project: string; settings: BoardProject } | null>(null);
  const [cmdDraft, setCmdDraft] = useState<string | null>(null);
  const [savingCmd, setSavingCmd] = useState(false);
  const [savingLimit, setSavingLimit] = useState(false);
  const [changed, setChanged] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (ask && ask !== shown) {
    setShown(ask);
    setSnap({ source: cards, list: approvalList(ask.epic, cards) });
    setRetake(false);
    setPicked(null);
    setParallel(String(ask.epic.run?.parallel ?? 2));
    setCmdDraft(null);
    setChanged([]);
    setError(null);
  }
  if (shown && retake && cards !== snap.source) {
    setSnap({ source: cards, list: approvalList(shown.epic, cards) });
    setRetake(false);
  }
  const projectId = shown?.epic.project_id ?? '';
  useEffect(() => {
    if (!projectId) return;
    let alive = true;
    api.planner.project(projectId).then((p) => alive && setLoaded({ project: projectId, settings: p })).catch(() => {});
    return () => {
      alive = false;
    };
  }, [api.planner, projectId]);
  const project = loaded?.project === projectId ? loaded.settings : null;
  const projectCmd = project ? project.accept_cmd : null;

  const epic = snap.list[0] ?? shown?.epic;
  const selection = picked ?? (epic ? runStart(epic, meta, settings) : null);
  const byId = new Map(cards.map((c) => [c.id, c]));
  const index = childIndex(snap.list);
  const subtasks = snap.list.filter((c) => c.kind === 'subtask');
  // Unknown until the Project default loads: nothing is refused for it meanwhile.
  const noCommand = (c: Card) => {
    const cmd = c.accept_cmd ?? projectCmd;
    return c.kind === 'subtask' && !isStarted(c) && cmd !== null && !cmd.trim();
  };
  // Held by hand, outside a lane: a lane's attempt is the approval's own and runs on.
  const handHeld = (c: Card) => !!c.held_by && !c.lane;
  const held = subtasks.filter(handHeld);
  const empty = snap.list.filter((c) => c.kind !== 'subtask' && leavesUnder(c.id, index).length === 0);
  const noCmd = subtasks.filter(noCommand);
  // A done container comes back open once the approval confirms a proposal under it, and a lane
  // running meanwhile that waits on one, itself or through a parent, would wait again (§6.3 rule 2).
  const reopened = new Map<string, Card>();
  for (const c of snap.list) if (c.kind !== 'subtask' && c.status === 'done' && leavesUnder(c.id, index).some((l) => !l.confirmed)) reopened.set(c.id, { ...c, status: 'todo' });
  const after = new Map([...byId, ...reopened]);
  const stalled = new Map(cards.filter((c) => c.held_by && c.lane).map((c) => [c.id, [...new Set(waitsOf(c, after).map((w) => w.card).filter((b) => reopened.has(b.id)))]] as const).filter(([, on]) => on.length > 0));
  const stalledCards = cards.filter((c) => stalled.has(c.id));
  const stalledOn = [...new Set([...stalled.values()].flat())];
  const refused = held.length + empty.length + noCmd.length + stalled.size > 0;
  const unapproved = epic ? epic.blocked_by.map((id) => byId.get(id)).filter((b): b is Card => !!b && b.kind === 'epic' && !b.run && b.status !== 'done' && b.status !== 'cancelled') : [];
  // Grouped by story: the subtasks right under the epic first, then each story's.
  const groups = epic ? [epic, ...(index.get(epic.id) ?? []).filter((c) => c.kind === 'story')].map((head) => ({ head, items: (index.get(head.id) ?? []).filter((c) => c.kind === 'subtask') })).filter((g) => g.head !== epic || g.items.length > 0) : [];

  const marks = (c: Card) => {
    const words: { text: string; refused?: boolean }[] = [];
    if (!c.confirmed) words.push({ text: 'new' });
    if (c.status === 'done') words.push({ text: 'done' });
    if (c.paused) words.push({ text: c.paused === 'uam' ? 'paused by uam' : 'paused' });
    const waits = waitsOf(c, byId).filter((w) => !w.via);
    if (waits.length) words.push({ text: `waits on ${seqs(waits.map((w) => w.card))}` });
    if (handHeld(c)) words.push({ text: 'held by a task', refused: true });
    else if (c.held_by) words.push({ text: 'running' });
    const on = stalled.get(c.id);
    if (on) words.push({ text: `would wait on ${seqs(on)} again`, refused: true });
    if (noCommand(c)) words.push({ text: 'no acceptance command', refused: true });
    if (c.kind !== 'subtask' && empty.includes(c)) words.push({ text: 'no subtask', refused: true });
    return words.map((w) => (
      <span key={w.text} className={w.refused ? 'text-error' : 'text-muted'}>
        {' · '}
        {w.text}
      </span>
    ));
  };

  const saveCmd = async () => {
    if (cmdDraft === null) return;
    setSavingCmd(true);
    setError(null);
    try {
      const saved = await api.planner.setProject(projectId, { accept_cmd: cmdDraft.trim() });
      setLoaded({ project: projectId, settings: saved });
      setCmdDraft(null);
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setSavingCmd(false);
    }
  };
  // The acceptance limit is the Project's, saved as it is picked: it holds for every epic there.
  const saveLimit = async (v: string) => {
    setSavingLimit(true);
    setError(null);
    try {
      const saved = await api.planner.setProject(projectId, { accept_parallel: Number(v) });
      setLoaded({ project: projectId, settings: saved });
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setSavingLimit(false);
    }
  };
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!shown || !selection?.model || refused) return;
    setBusy(true);
    setError(null);
    setChanged([]);
    try {
      await api.planner.approve(shown.epic.id, { ...selection, parallel: Number(parallel), items: snap.list.map((c) => ({ id: c.id, revision: c.revision })) });
      onClose();
    } catch (err) {
      if (errorCode(err) === 'stale') {
        // Shown again as they are now, once the Board has them.
        setChanged(errorRefs(err));
        setRetake(true);
        reload(projectId);
      } else setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  const seq = epic ? `#${epic.seq}` : '';
  return (
    <Dialog
      open={!!ask}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => setShown(null)}
      title={`Approve ${seq}?`}
      description={`Approving confirms the cards below and runs them with these settings. A card added under ${seq} later stays a proposal until you approve again.`}
    >
      <form aria-label={`Approve ${seq}`} className="flex flex-col gap-3" onSubmit={(e) => void submit(e)}>
        {changed.length > 0 && (
          <Note role="alert" tone="warn">
            {changed.join(', ')} changed since this opened. {retake ? 'Loading them again…' : 'They are shown as they are now: look again, then approve.'}
          </Note>
        )}
        {epic && (
          <div className="flex flex-col gap-2 rounded-md bg-tint-well px-3 py-2 text-ui text-body">
            <p className="font-medium text-ink">
              #{epic.seq} {epic.title}
              {marks(epic)}
            </p>
            {groups.map(({ head, items }) => (
              <section key={head.id} aria-label={`#${head.seq} ${head.title}`} className="flex flex-col gap-0.5">
                {head !== epic && (
                  <p className="text-ink">
                    #{head.seq} {head.title}
                    {marks(head)}
                  </p>
                )}
                <ul className="flex flex-col gap-0.5 pl-3 text-caption">
                  {items.map((c) => (
                    <li key={c.id}>
                      #{c.seq} {c.title}
                      {marks(c)}
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        )}
        {refused && (
          <div role="alert" className="flex flex-col gap-0.5 text-caption text-error">
            {held.length > 0 && <p>{seqs(held)} {isAre(held.length)} held by a task: finish or release {held.length === 1 ? 'it' : 'them'} first, as work started by hand and approved work never mix.</p>}
            {empty.length > 0 && <p>{seqs(empty)} {isAre(empty.length)} without a subtask, and a card with none never finishes: add one or cancel {empty.length === 1 ? 'it' : 'them'}.</p>}
            {noCmd.length > 0 && <p>{seqs(noCmd)} {noCmd.length === 1 ? 'has' : 'have'} no acceptance command, so done would always wait for you: set one on {noCmd.length === 1 ? 'it' : 'them'} or a Project default below.</p>}
            {stalledCards.length > 0 && (
              <p>
                {seqs(stalledCards)} {stalledCards.length === 1 ? 'runs in a lane' : 'run in lanes'} and would wait on {seqs(stalledOn)} again, which this approval reopens: Stop {stalledCards.length === 1 ? 'it first, or approve once it has' : 'them first, or approve once they have'} landed.
              </p>
            )}
          </div>
        )}
        {unapproved.length > 0 && <Note tone="warn">{seq} waits on {seqs(unapproved)}, not approved yet: nothing here runs before {unapproved.length === 1 ? 'it is' : 'they are'} done.</Note>}
        <div className="flex flex-col gap-1">
          <span id="planner-approve-cmd-label" className="text-caption text-muted">Project acceptance command</span>
          {cmdDraft === null ? (
            <span className="flex min-h-8 items-center gap-2">
              <span className={cn('min-w-0 truncate', projectCmd ? 'font-mono text-code-sm text-ink' : 'text-ui text-muted')}>{projectCmd === null ? 'Loading…' : projectCmd || 'None'}</span>
              <Button size="sm" className="text-muted" disabled={projectCmd === null || busy} onClick={() => setCmdDraft(projectCmd ?? '')}>
                <Pencil />
                Edit
              </Button>
            </span>
          ) : (
            <span className="flex items-center gap-1.5">
              <Input
                size="md"
                aria-labelledby="planner-approve-cmd-label"
                className="font-mono text-code-sm"
                spellCheck={false}
                placeholder="make test"
                value={cmdDraft}
                onChange={(e) => setCmdDraft(e.target.value)}
                onKeyDown={(e) => {
                  // Enter saves the command; it must not approve.
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    void saveCmd();
                  }
                }}
              />
              <Button size="sm" variant="secondary" loading={savingCmd} onClick={() => void saveCmd()}>
                Save
              </Button>
              <Button size="sm" onClick={() => setCmdDraft(null)}>
                Cancel
              </Button>
            </span>
          )}
          <Note>A subtask without its own command runs this one to check its work.</Note>
        </div>
        {selection && <TaskDefaultsFields prefix="planner-approve" value={selection} disabled={busy} onChange={setPicked} />}
        {selection && !selection.model && <Note tone="warn">Pick a model: an approved run never falls back to a default one.</Note>}
        {selection?.mode === 'safe' && <Note tone="warn">In Safe mode a permission prompt stops unattended work until you answer it.</Note>}
        <div className="flex flex-wrap gap-x-6 gap-y-3">
          <div className="flex flex-col gap-1">
            <span id="planner-approve-parallel" className="text-caption text-muted">Subtasks at a time</span>
            <Segmented aria-labelledby="planner-approve-parallel" className="w-40" value={parallel} disabled={busy} onValueChange={setParallel} items={PARALLEL_ITEMS} />
          </div>
          <div className="flex flex-col gap-1">
            <span id="planner-approve-accept-limit" className="text-caption text-muted">Acceptance runs at a time</span>
            <Segmented aria-labelledby="planner-approve-accept-limit" className="w-40" value={String(project?.accept_parallel ?? 1)} disabled={!project || busy || savingLimit} onValueChange={(v) => void saveLimit(v)} items={PARALLEL_ITEMS} />
          </div>
        </div>
        <Note>
          {project?.integration
            ? `Subtasks run in their own worktrees made from ${project.integration.branch}, which follows ${project.integration.base_ref}`
            : 'Subtasks run in their own worktrees made from the branch checked out in the project now'}
          ; uncommitted changes are not included. Their work lands on the integration branch.
        </Note>
        <Note>uam starts each subtask in its lane once nothing it waits on is open, up to the subtasks at a time above, and never one under a paused card.</Note>
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-2 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!selection?.model || refused || retake || cmdDraft !== null}>
            Approve and run
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export interface BriefAsk {
  kind: 'plan' | 'suggest';
  title: string;
  /** `task` is the planning Task's selection (Plan with agent); Suggest runs on the Utility model and has none. */
  run: (body: { brief: string; document: string; max: number; task: TaskDefaults | null }) => Promise<unknown>;
}

/**
 * Plan with agent (the new Task's model, effort, context size and mode from the New task
 * defaults, as Launch asks, and a brief) and Suggest (a brief, an optional document to split,
 * and how many; the Utility model runs it).
 */
export function BriefDialog({ ask, onClose }: Readonly<{ ask: BriefAsk | null; onClose: () => void }>) {
  const { meta, settings } = useApp();
  // Null until changed here: the New task defaults.
  const [picked, setPicked] = useState<TaskDefaults | null>(null);
  const [brief, setBrief] = useState('');
  const [doc, setDoc] = useState('');
  const [max, setMax] = useState('3');
  const [shown, setShown] = useState(ask);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  if (ask && ask !== shown) {
    setShown(ask);
    setPicked(null);
    setBrief('');
    setDoc('');
    setError(null);
  }
  const suggest = shown?.kind === 'suggest';
  const selection = suggest ? null : (picked ?? resolveTaskDefaults(meta, settings.task_defaults, settings.hidden_models));
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!shown) return;
    setBusy(true);
    setError(null);
    try {
      await shown.run({ brief: brief.trim(), document: doc.trim(), max: Math.max(1, Math.min(10, Number(max) || 3)), task: selection });
      onClose();
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={!!ask}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => setShown(null)}
      initialFocus={field}
      title={shown?.title ?? ''}
      description={suggest ? 'The Utility model proposes cards under this one. They arrive as suggestions to confirm or dismiss.' : 'A new task plans the work under this card. What it writes stays a suggestion until you confirm it.'}
    >
      <form className="flex flex-col gap-3" onSubmit={(e) => void submit(e)}>
        {selection && <TaskDefaultsFields prefix="planner-plan" value={selection} disabled={busy} onChange={setPicked} />}
        <Field id="planner-brief" label="Brief">
          <textarea id="planner-brief" ref={field} className={areaClass} placeholder="What the plan should aim at (optional)" value={brief} onChange={(e) => setBrief(e.target.value)} />
        </Field>
        {suggest && (
          <>
            <Field id="planner-document" label="Document to split (optional)">
              <textarea id="planner-document" className={areaClass} placeholder="Paste a spec or notes; each part becomes a suggestion" value={doc} onChange={(e) => setDoc(e.target.value)} />
            </Field>
            <Field id="planner-max" label="At most">
              <Input id="planner-max" type="number" min={1} max={10} className="w-24" value={max} onChange={(e) => setMax(e.target.value)} />
            </Field>
          </>
        )}
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-2 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy}>
            {suggest ? 'Suggest' : 'Start planning'}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/**
 * Owner Mark done (§6, §8): a comment is required. When the service refuses because of open
 * checklist items, open blockers or the blocked flag, the dialog shows what is open and offers
 * "Finish anyway", which sends the same close with `force`.
 */
export function DoneDialog({ card, byId, open, onClose }: Readonly<{ card: Card; byId: ReadonlyMap<string, Card>; open: boolean; onClose: () => void }>) {
  const api = useApi();
  const [text, setText] = useState('');
  const [guard, setGuard] = useState<DoneGuard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const finish = async (force: boolean) => {
    if (!text.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await api.planner.status(card.id, 'done', text.trim(), force);
      onClose();
    } catch (err) {
      const g = doneGuard(err);
      if (g) setGuard(g);
      else setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => {
        setText('');
        setGuard(null);
        setError(null);
      }}
      initialFocus={field}
      title={`Mark #${card.seq} done?`}
      description="The subtask closes as done with your comment. A Task holding it stops holding it."
    >
      <form
        className="flex flex-col gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          void finish(!!guard);
        }}
      >
        <Field id="planner-done-text" label="Comment (required)">
          <textarea id="planner-done-text" ref={field} className={areaClass} required value={text} onChange={(e) => setText(e.target.value)} />
        </Field>
        {guard && (
          <div role="alert" className="flex flex-col gap-1 rounded-md bg-warning-wash px-3 py-2 text-caption text-body">
            {guard.code === 'guard_open_items' && (
              <>
                <span className="font-medium text-ink">Open checklist items</span>
                <ul className="list-disc pl-4">
                  {guard.items.map((item) => (
                    <li key={item}>{item}</li>
                  ))}
                </ul>
              </>
            )}
            {guard.code === 'guard_blockers' && (
              <>
                <span className="font-medium text-ink">Open blockers</span>
                <ul className="list-disc pl-4">
                  {/* The service names each open blocker by its #seq. */}
                  {guard.blockers.map((ref) => {
                    const b = [...byId.values()].find((x) => `#${x.seq}` === ref);
                    return <li key={ref}>{b ? `#${b.seq} ${b.title}` : ref}</li>;
                  })}
                </ul>
              </>
            )}
            {guard.code === 'guard_blocked' && <span className="font-medium text-ink">The subtask is marked blocked.</span>}
            <span className="text-muted">Finish anyway closes it as done regardless.</span>
          </div>
        )}
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Keep
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!text.trim()}>
            {guard ? 'Finish anyway' : 'Mark done'}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

const TOP_LEVEL = '';

/** Where a card may move (§1): a story under an epic or to the top level; a subtask under an epic, a story or to the top level. */
export function moveTargets(card: Card, cards: readonly Card[]): Card[] {
  let kinds: CardKind[] = [];
  if (card.kind === 'story') kinds = ['epic'];
  else if (card.kind === 'subtask') kinds = ['epic', 'story'];
  return cards.filter((x) => kinds.includes(x.kind) && x.id !== card.id && x.status !== 'cancelled');
}

/** Owner Move (§3): a picker over the valid parents; the card goes last under its new parent (a move without a rank). */
export function MoveDialog({ card, cards, open, onClose }: Readonly<{ card: Card; cards: readonly Card[]; open: boolean; onClose: () => void }>) {
  const api = useApi();
  const [target, setTarget] = useState(card.parent_id ?? TOP_LEVEL);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Each opening starts from where the card is now.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setTarget(card.parent_id ?? TOP_LEVEL);
  }
  const byId = new Map(cards.map((c) => [c.id, c]));
  const items = [
    { value: TOP_LEVEL, label: 'Top level (no parent)' },
    ...moveTargets(card, cards).map((x) => {
      const parent = x.parent_id ? byId.get(x.parent_id) : undefined;
      return { value: x.id, label: `#${x.seq} ${x.title}${parent ? ` (in #${parent.seq})` : ''}` };
    }),
  ];
  const move = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.planner.move(card.id, target || null);
      onClose();
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} onClosed={() => setError(null)} title={`Move #${card.seq}`} description={card.confirmed ? 'It goes last under its new parent. Under a suggestion, moving it confirms that suggestion.' : 'It goes last under its new parent, and stays a suggestion.'}>
      <div className="flex flex-col gap-2">
        <Field id="planner-move-target" label="Move to">
          <Select id="planner-move-target" aria-label="Move to" value={target} onValueChange={setTarget} items={items} />
        </Field>
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} disabled={target === (card.parent_id ?? TOP_LEVEL)} onClick={() => void move()}>
            Move
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

/**
 * Owner Split (§7): the new subtasks, then the card's checklist items after them. Under a
 * story they become its subtasks right after this one, which is cancelled; elsewhere the card
 * turns into a story holding them.
 */
export function SplitDialog({ card, byId, open, onClose }: Readonly<{ card: Card; byId: ReadonlyMap<string, Card>; open: boolean; onClose: () => void }>) {
  const api = useApi();
  const [children, setChildren] = useState([{ title: '', win_condition: '' }]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const given = children.filter((c) => c.title.trim()).map((c) => ({ title: c.title.trim(), win_condition: c.win_condition.trim() }));
  const count = given.length + card.checklist.length;
  const set = (i: number, patch: Partial<{ title: string; win_condition: string }>) => setChildren((cs) => cs.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  const split = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!count) return;
    setBusy(true);
    setError(null);
    try {
      await api.planner.split(card.id, given);
      onClose();
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => {
        setChildren([{ title: '', win_condition: '' }]);
        setError(null);
      }}
      title={`Split #${card.seq}`}
      description={splitSentence(card, byId, count)}
    >
      <form aria-label={`Split #${card.seq}`} className="flex flex-col gap-3" onSubmit={(e) => void split(e)}>
        <ol className="flex flex-col gap-2">
          {children.map((c, i) => (
            <li key={i} className="flex flex-col gap-1.5 rounded-md bg-tint-well p-2">
              <span className="flex items-center gap-1.5">
                <Input size="md" aria-label={`Subtask ${i + 1} title`} placeholder={`Subtask ${i + 1}`} value={c.title} onChange={(e) => set(i, { title: e.target.value })} />
                {children.length > 1 && (
                  <Button size="icon-sm" className="text-muted" aria-label={`Remove subtask ${i + 1}`} onClick={() => setChildren((cs) => cs.filter((_, j) => j !== i))}>
                    <X />
                  </Button>
                )}
              </span>
              <Input size="md" aria-label={`Subtask ${i + 1} win condition`} placeholder="What done means, in one line" value={c.win_condition} onChange={(e) => set(i, { win_condition: e.target.value })} />
            </li>
          ))}
        </ol>
        <Button size="sm" className="self-start text-muted" onClick={() => setChildren((cs) => [...cs, { title: '', win_condition: '' }])}>
          <Plus />
          Another subtask
        </Button>
        {card.checklist.length > 0 && (
          <div className="flex flex-col gap-0.5 text-caption text-muted">
            <span>Then the checklist items, in order:</span>
            <ul className="flex flex-col gap-0.5 pl-1">
              {card.checklist.map((item) => (
                <li key={item.text} className="text-body">
                  {item.text}
                  {item.done && <span className="text-muted"> (ticked: waits as a done request)</span>}
                </li>
              ))}
            </ul>
          </div>
        )}
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-2 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!count}>
            Split
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** How many files of a landing the Revert panel names. */
const MAX_REVERT_FILES = 5;

/**
 * Revert (ADR 0006 §5.7), in the anchored confirmation: what reverting `card` takes along, from the
 * service's dry run — the subtasks, those that landed on top of it marked, each landing's commit and
 * files — and the revert itself, a job (`onStarted` once it runs). A subtask that started on top of it
 * and still runs is named to Stop first. A revert that would not apply says why, offers to take along
 * the later cards that changed the same files, and, on a subtask, Reopen without reverting code
 * (`onReopen`).
 */
export function RevertPanel({ card, onClose, onStarted, onReopen }: Readonly<{ card: Card | null; onClose: () => void; onStarted: (card: Card) => void; onReopen: (card: Card) => void }>) {
  const api = useApi();
  const { cards } = useShownBoard();
  const [shown, setShown] = useState(card);
  const [include, setInclude] = useState<string[]>([]);
  const [preview, setPreview] = useState<RevertPreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [comment, setComment] = useState('');
  const [busy, setBusy] = useState(false);
  // Read again after a refusal says what it reverts changed.
  const [round, setRound] = useState(0);
  if (card && card !== shown) {
    if (card.id !== shown?.id) {
      setInclude([]);
      setPreview(null);
      setComment('');
    }
    setShown(card);
    setError(null);
  }
  const id = card?.id;
  useEffect(() => {
    if (!id) return;
    let alive = true;
    api.planner
      .revertPreview(id, include)
      .then((p) => alive && setPreview(p))
      .catch((e: unknown) => alive && setError(plannerErrorText(e)));
    return () => {
      alive = false;
    };
  }, [api.planner, id, include, round]);

  const byId = new Map(cards.map((c) => [c.id, c]));
  const p = preview;
  const leaf = shown?.kind === 'subtask';
  // What it takes along besides what was asked: landed on top of it, as recorded when each started.
  const along = (x: Card | undefined) => !!x && !!shown && !cardPath(x, byId).includes(byId.get(shown.id) ?? shown) && !include.includes(`#${x.seq}`);
  const running = (p?.running ?? []).map((r) => byId.get(r)).filter((x): x is Card => !!x);
  const conflict = p?.conflict;
  const submit = async () => {
    if (!shown || !p) return;
    setBusy(true);
    setError(null);
    try {
      await api.planner.revert(shown.id, { include, expect: p.cards, comment: comment.trim() });
      onStarted(shown);
      onClose();
    } catch (e) {
      setError(plannerErrorText(e));
      if (errorCode(e) === 'stale') setRound((n) => n + 1);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog
      open={!!card}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => setShown(null)}
      title={leaf ? `Revert #${shown?.seq ?? ''}?` : `Revert what landed under #${shown?.seq ?? ''}?`}
      description={`Each landing gets a revert commit on ${p?.branch ?? 'the integration branch'}, newest first, and its subtask goes back to To do, paused.`}
      confirmLabel="Revert"
      busy={busy}
      disabled={!p || !!conflict || running.length > 0}
      onConfirm={() => void submit()}
    >
      {!p && !error && <p className="text-caption text-muted">Reading what it takes along…</p>}
      {p && (
        <ul aria-label="What it reverts" className="mt-1 flex max-h-60 flex-col gap-1.5 overflow-y-auto text-caption">
          {p.landings.map((l) => (
            <li key={l.sha} className="flex min-w-0 flex-col">
              <span className="truncate text-body">
                #{l.seq} {l.title}
                {along(byId.get(l.card_id)) && <span className="text-muted"> · landed on top of it</span>}
              </span>
              <span className="truncate text-muted">
                <span className="font-mono">{l.sha.slice(0, 7)}</span>
                {l.files.length > 0 && ` · ${l.files.slice(0, MAX_REVERT_FILES).join(', ')}${l.files.length > MAX_REVERT_FILES ? ` and ${l.files.length - MAX_REVERT_FILES} more` : ''}`}
              </span>
            </li>
          ))}
        </ul>
      )}
      {p?.merged && <p className="mt-1 text-caption text-body">The base branch already has some of this: uam merges the revert into it too.</p>}
      {running.length > 0 && (
        <p role="alert" className="mt-1 text-caption text-error">
          Stop {running.map((r) => `#${r.seq}`).join(', ')} first: {running.length === 1 ? 'it started' : 'they started'} on top of this and {running.length === 1 ? 'runs' : 'run'}.
        </p>
      )}
      {conflict && (
        <div className="mt-1 flex flex-col gap-1.5">
          <p role="alert" className="text-caption text-error">It would not apply: {conflict.error}</p>
          <div className="flex flex-wrap gap-1.5">
            {conflict.refs?.some((r) => !include.includes(r)) && (
              <Button size="sm" variant="secondary" onClick={() => setInclude((inc) => [...inc, ...(conflict.refs ?? []).filter((r) => !inc.includes(r))])}>
                Revert {conflict.refs.join(', ')} too
              </Button>
            )}
            {leaf && shown && (
              <Button size="sm" variant="secondary" onClick={() => onReopen(shown)}>
                Reopen without reverting code
              </Button>
            )}
          </div>
        </div>
      )}
      {p && !conflict && running.length === 0 && (
        <label className="mt-1 flex flex-col gap-1 text-caption text-muted">
          Why (optional)
          <textarea className={cn(areaClass, 'min-h-12')} value={comment} onChange={(e) => setComment(e.target.value)} />
        </label>
      )}
      {error && <p role="alert" className="mt-1 text-caption text-error">{error}</p>}
    </AlertDialog>
  );
}
