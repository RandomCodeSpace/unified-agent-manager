import { useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { Field } from '../TaskDefaults';
import { Button } from '../ui/button';
import { Dialog } from '../ui/dialog';
import { Input } from '../ui/input';

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
