import { useState, type SubmitEvent } from 'react';
import { plannerErrorText, type Card, type HoldDecision } from '../../api';
import { Button } from '../ui/button';
import { Dialog } from '../ui/dialog';
import { Input } from '../ui/input';
import { Segmented } from '../ui/segmented';
import { StatusMark } from './parts';

export interface SettleAsk {
  taskName: string;
  cards: Card[];
  settle: (holds: Record<string, HoldDecision>) => Promise<void>;
}

/** A subtask held in a lane: nobody works in its lane once the Task is settled, so it is not kept (ADR 0006 §4.6). */
const inLane = (c: Card) => !!c.lane?.branch;

/**
 * Settle a Task that holds subtasks (ADR 0005 §5): for each, keep it held (it resumes after
 * Reopen), release it to To do, or cancel it with a comment. Opened by 409 `holds_undecided`.
 * A lane's subtask is stopped (released and paused) or cancelled; it is never kept.
 */
export function SettleDialog({ ask, onClose }: Readonly<{ ask: SettleAsk | null; onClose: () => void }>) {
  const [shown, setShown] = useState(ask);
  const [decisions, setDecisions] = useState<Record<string, HoldDecision>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (ask && ask !== shown) {
    setShown(ask);
    setDecisions(Object.fromEntries(ask.cards.map((c) => [c.id, { action: inLane(c) ? 'release' : 'keep', comment: '' }])));
    setError(null);
  }
  const incomplete = !!shown?.cards.some((c) => decisions[c.id]?.action === 'cancel' && !decisions[c.id].comment.trim());
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (!shown || incomplete) return;
    setBusy(true);
    setError(null);
    try {
      await shown.settle(decisions);
      onClose();
    } catch (err) {
      setError(plannerErrorText(err));
    } finally {
      setBusy(false);
    }
  };
  const set = (id: string, patch: Partial<HoldDecision>) => setDecisions((d) => ({ ...d, [id]: { ...d[id], ...patch } }));
  return (
    <Dialog
      open={!!ask}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => setShown(null)}
      title={`Settle ${shown?.taskName ?? 'this task'}?`}
      description={`It holds ${shown?.cards.length === 1 ? 'a subtask' : `${shown?.cards.length ?? 0} subtasks`}. Decide what happens to ${shown?.cards.length === 1 ? 'it' : 'each'}.`}
    >
      <form id="settle-holds" className="flex flex-col gap-3" onSubmit={(e) => void submit(e)}>
        {shown?.cards.map((c) => {
          const lane = inLane(c);
          const d = decisions[c.id] ?? { action: lane ? 'release' : 'keep', comment: '' };
          return (
            <fieldset key={c.id} className="flex flex-col gap-2 rounded-md bg-tint-well px-3 py-2.5">
              <legend className="sr-only">#{c.seq} {c.title}</legend>
              <p className="flex min-w-0 items-center gap-1.5 text-ui text-ink" aria-hidden="true">
                <StatusMark status={c.status} />
                <span className="shrink-0 text-caption tabular-nums text-muted">#{c.seq}</span>
                <span className="truncate">{c.title}</span>
              </p>
              <Segmented
                size="sm"
                aria-label={`What happens to #${c.seq}`}
                value={d.action}
                onValueChange={(v) => set(c.id, { action: v as HoldDecision['action'] })}
                items={[
                  ...(lane ? [] : [{ value: 'keep', label: 'Keep held' }]),
                  { value: 'release', label: lane ? 'Stop' : 'Release' },
                  { value: 'cancel', label: 'Cancel' },
                ]}
              />
              <p className="text-caption text-muted">
                {d.action === 'keep' && 'The hold stays; the task picks the subtask up again after Reopen.'}
                {d.action === 'release' && (lane ? `The subtask goes back to To do, paused; its branch ${c.lane!.branch} is kept.` : 'The subtask goes back to To do for another attempt.')}
                {d.action === 'cancel' && 'The subtask is cancelled; say why.'}
              </p>
              {d.action !== 'keep' && (
                <Input size="md" aria-label={`Comment for #${c.seq}`} required={d.action === 'cancel'} placeholder={d.action === 'cancel' ? 'Why (required)' : 'Comment (optional)'} value={d.comment} onChange={(e) => set(c.id, { comment: e.target.value })} />
              )}
            </fieldset>
          );
        })}
        {error && <p role="alert" className="text-caption text-error">{error}</p>}
        <div className="mt-2 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" onClick={onClose}>
            Keep open
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={incomplete}>
            Settle
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
