import { Plus, X } from 'lucide-react';
import { useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { api, describeError, doneGuard, type Card, type CardKind, type DoneGuard } from '../../api';
import { Field } from '../TaskDefaults';
import { Button } from '../ui/button';
import { Dialog } from '../ui/dialog';
import { Input } from '../ui/input';
import { Select } from '../ui/select';
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
      setError(err instanceof Error ? err.message : String(err));
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

export interface BriefAsk {
  kind: 'plan' | 'suggest';
  title: string;
  run: (body: { brief: string; document: string; max: number }) => Promise<unknown>;
}

/** Plan with agent (a brief) and Suggest (a brief, an optional document to split, and how many). */
export function BriefDialog({ ask, onClose }: Readonly<{ ask: BriefAsk | null; onClose: () => void }>) {
  const [brief, setBrief] = useState('');
  const [doc, setDoc] = useState('');
  const [max, setMax] = useState('3');
  const [shown, setShown] = useState(ask);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  if (ask && ask !== shown) {
    setShown(ask);
    setBrief('');
    setDoc('');
    setError(null);
  }
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!shown) return;
    setBusy(true);
    setError(null);
    try {
      await shown.run({ brief: brief.trim(), document: doc.trim(), max: Math.max(1, Math.min(10, Number(max) || 3)) });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  const suggest = shown?.kind === 'suggest';
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
      else setError(describeError(err));
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
                  {guard.blockers.map((id) => {
                    const b = byId.get(id);
                    return <li key={id}>{b ? `#${b.seq} ${b.title}` : id}</li>;
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

/** Owner Move (§3): a picker over the valid parents; the card goes last under its new parent. */
export function MoveDialog({ card, cards, open, onClose }: Readonly<{ card: Card; cards: readonly Card[]; open: boolean; onClose: () => void }>) {
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
      const siblings = cards.filter((x) => (x.parent_id ?? TOP_LEVEL) === target && x.id !== card.id);
      await api.planner.move(card.id, target || null, Math.max(0, ...siblings.map((x) => x.rank)) + 1);
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()} onClosed={() => setError(null)} title={`Move #${card.seq}`} description="It goes last under its new parent. Moving counts as a touch: it confirms the card and any unconfirmed parent.">
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
      setError(describeError(err));
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
