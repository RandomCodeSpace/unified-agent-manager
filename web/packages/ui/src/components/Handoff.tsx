import { taskName, type Item, type SessionSummary } from '../api';
import { Markdown } from './common';
import { Button } from './ui/button';

/** The facts without their own heading and byline: the card says whose they are. */
const FACTS_HEAD = /^## Facts\s*\n+(?:_[^\n]*_\s*\n+)?/;

/**
 * The first message of a new Thread that continues from a handoff: a line naming the Thread it
 * comes from, the agent's handoff reply when it is loaded, then uam's facts.
 */
export function handoffPrompt(session: Pick<SessionSummary, 'name' | 'title' | 'handoff'>, items: readonly Item[]): string {
  const h = session.handoff;
  if (!h) return '';
  const reply = h.reply_id ? items.find((i) => i.id === h.reply_id)?.text?.trim() : undefined;
  const from = `Continue the work from this handoff. It comes from the Thread "${taskName(session) || 'New thread'}", which stopped at its budget.`;
  const missing = h.reply_id && !reply ? 'The agent\'s handoff is the last reply of that Thread.' : '';
  return [from, reply, missing, h.facts].filter(Boolean).join('\n\n');
}

/**
 * The end of a Thread that reached its budget: uam's facts under the agent's handoff reply (or in
 * its place), and the suggestion to continue in a new Thread. Continue only opens a draft in this
 * Project with the handoff as its message; nothing starts until the owner sends it.
 */
export function HandoffCard({ session, items, onContinue }: Readonly<{
  session: Pick<SessionSummary, 'name' | 'title' | 'handoff'>;
  items: readonly Item[];
  /** Absent where a draft cannot be opened (a read-only Thread). */
  onContinue?: (text: string) => void;
}>) {
  const h = session.handoff;
  if (!h) return null;
  const lead = h.reply_id
    ? 'The reply above is the agent’s handoff. uam recorded these facts:'
    : `The agent wrote no handoff${h.no_reply ? ` (${h.no_reply})` : ''}. uam recorded these facts:`;
  return (
    <section aria-label="Handoff" className="flex min-w-0 flex-col gap-2 rounded-md bg-tint-well px-4 py-3 shadow-raised">
      <h3 className="text-eyebrow uppercase text-muted">Handoff</h3>
      <p className="text-ui text-body">{lead}</p>
      <Markdown text={h.facts.replace(FACTS_HEAD, '')} className="text-ui" />
      <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-2">
        {onContinue && <Button size="sm" variant="primary" onClick={() => onContinue(handoffPrompt(session, items))}>Continue in a new Thread</Button>}
        <p className="min-w-48 flex-1 text-caption text-muted">
          {onContinue ? 'Opens a draft in this Project with the handoff as its message; nothing is sent until you send it. ' : ''}
          To go on here, raise the budget in Tools → Usage.
        </p>
      </div>
    </section>
  );
}

/**
 * The end of a turn a uam restart cut while a tool call ran: the call is never replayed. Run again
 * only drafts a prompt to run it again; nothing runs until the owner sends it, under the usual
 * permission rules.
 */
export function InterruptedCard({ lead, call, onRunAgain }: Readonly<{
  lead: string;
  call: { text: string; prompt: string };
  /** Absent where the composer takes no message (a read-only Thread). */
  onRunAgain?: (prompt: string) => void;
}>) {
  return (
    <section aria-label="Interrupted" className="flex min-w-0 flex-col gap-2 rounded-md bg-tint-well px-4 py-3 shadow-raised">
      <h3 className="text-eyebrow uppercase text-muted">Interrupted</h3>
      <p className="text-ui text-body">{lead} This call had started and did not finish:</p>
      <code className="block rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm text-ink [overflow-wrap:anywhere] whitespace-pre-wrap">{call.text}</code>
      {onRunAgain && (
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-2">
          <Button size="sm" variant="primary" onClick={() => onRunAgain(call.prompt)}>Run again</Button>
          <p className="min-w-48 flex-1 text-caption text-muted">Puts a prompt to run it again in the composer; nothing runs until you send it.</p>
        </div>
      )}
    </section>
  );
}
