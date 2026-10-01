import { Check, X } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { api, describeError, isStatus, type Answer, type Ask, type SessionSummary } from '../api';
import { recommendedChoice } from '../lib/answer';
import { cn } from '../lib/cn';
import { Note } from './common';
import { Button } from './ui/button';

/** Chips shown before "N more…": a long list waits behind it, in place. */
const CHIPS = 6;

/**
 * A Needs you row's answer, in place (the summary's `ask`): a question's choices as chips, the
 * recommended one staged as the composer stages it, with Answer and "Reply…" (which opens the
 * Task); a permission's Allow and Don't allow beside what it asks to run. The first answer from
 * any tab wins, as on the Task's own card. Keyed on the ask, so a new request starts fresh.
 */
export function TaskAsk({ session, ask, onReply }: Readonly<{ session: SessionSummary; ask: Ask; onReply: () => void }>) {
  const [busy, setBusy] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [all, setAll] = useState(false);
  const choices = ask.kind === 'question' && (ask.questions ?? 1) === 1 ? (ask.choices ?? []) : [];
  const [staged, setStaged] = useState<string[]>(() => {
    const recommended = recommendedChoice(choices);
    return recommended ? [recommended] : [];
  });

  async function respond(answer: Answer, action: string) {
    setBusy(action);
    setNote(null);
    try {
      await api.respond(session.id, ask.id, answer);
      // The row leaves Needs you with the next summary; until then nothing can be sent twice.
      setSent(true);
    } catch (e) {
      if (isStatus(e, 409)) setNote('Already answered elsewhere.');
      else if (isStatus(e, 410)) setNote('This request expired before it was answered.');
      else setNote(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  const off = !!busy || sent;
  const name = session.name || session.title || 'New task';
  let controls: ReactNode;
  if (ask.kind === 'permission') {
    const allow = ask.options?.find((o) => !o.reject);
    const deny = ask.options?.find((o) => o.reject);
    controls = (
      <>
        {session.capabilities.permissions && allow && (
          <Button size="sm" variant="primary" loading={busy === allow.id} disabled={off} onClick={() => void respond({ decision: allow.id }, allow.id)}>
            <Check />
            Allow
          </Button>
        )}
        {session.capabilities.permissions && deny && (
          <Button size="sm" variant="danger" loading={busy === deny.id} disabled={off} onClick={() => void respond({ decision: deny.id }, deny.id)}>
            <X />
            Don’t allow
          </Button>
        )}
        {ask.detail && (
          <code translate="no" className="min-w-0 basis-full truncate font-mono text-code-sm text-muted" title={ask.detail}>
            {ask.detail}
          </code>
        )}
      </>
    );
  } else {
    const multiple = !!ask.multiple;
    const toggle = (c: string) => {
      if (multiple) setStaged((s) => (s.includes(c) ? s.filter((x) => x !== c) : [...s, c]));
      else setStaged((s) => (s.includes(c) ? [] : [c]));
    };
    const can = session.capabilities.questions;
    controls = (
      <>
        {can && choices.length > 0 && (
          <div role="group" aria-label={multiple ? 'Choose one or more' : 'Choose one'} className="flex min-w-0 basis-full flex-wrap gap-1.5">
            {(all ? choices : choices.slice(0, CHIPS)).map((c) => {
              const on = staged.includes(c);
              return (
                <button
                  key={c}
                  type="button"
                  aria-pressed={on}
                  disabled={off}
                  className={cn(
                    'min-h-7 max-w-full rounded-full px-2.5 py-1 text-left text-caption transition-[background-color,color] duration-100 disabled:opacity-45 pointer-coarse:min-h-11 pointer-coarse:px-3.5',
                    on ? 'bg-accent text-on-accent' : 'bg-raised text-body shadow-raised hover:bg-tint-hover',
                  )}
                  onClick={() => toggle(c)}
                >
                  {c}
                </button>
              );
            })}
            {!all && choices.length > CHIPS && (
              <button type="button" className="min-h-7 rounded-full px-2 text-caption text-muted hover:bg-tint-hover pointer-coarse:min-h-11" onClick={() => setAll(true)}>
                {choices.length - CHIPS} more…
              </button>
            )}
          </div>
        )}
        {can && choices.length > 0 && (
          <Button size="sm" variant="primary" loading={busy === 'answer'} disabled={off || staged.length === 0} onClick={() => void respond({ answers: [staged] }, 'answer')}>
            Answer
          </Button>
        )}
        <Button size="sm" className="text-muted" disabled={!!busy} onClick={onReply}>
          Reply…
        </Button>
      </>
    );
  }

  return (
    <div role="group" aria-label={`Answer ${name}`} className="flex flex-col gap-1 pl-6">
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">{controls}</div>
      {note && (
        <Note tone="warn" role="alert">
          {note}
        </Note>
      )}
    </div>
  );
}
