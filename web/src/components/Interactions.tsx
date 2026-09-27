import { MessageCircleQuestion, Shield, ShieldCheck, ShieldQuestion, ShieldX, type LucideIcon } from 'lucide-react';
import { useId, useState, type SubmitEvent } from 'react';
import { api, describeError, isStatus, type Answer, type Interaction, type Option, type Question, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { approvalMark } from '../lib/transcript';
import { Markdown, Note } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Input } from './ui/input';

/**
 * A decided request that no tool row claims: one quiet row of the tool rows' kind, in its
 * turn at its time. The title, the first line of the request, and the outcome word; the
 * full resolution in the tooltip and the accessible name.
 */
/** The shield for a decided permission: crossed when denied, plain when it lapsed, checked otherwise. */
export const APPROVAL_ICONS: Record<'ok' | 'denied' | 'gone', LucideIcon> = { ok: ShieldCheck, denied: ShieldX, gone: Shield };

/** Reject reads as danger, the default option as the primary, the rest secondary. */
function optionVariant(option: Option, primary: Option | undefined): 'danger' | 'primary' | 'secondary' {
  if (option.reject) return 'danger';
  return option === primary ? 'primary' : 'secondary';
}

export function DecidedRow({ interaction, className }: Readonly<{ interaction: Interaction; className?: string }>) {
  const { word, full, tone } = approvalMark(interaction);
  const Icon = interaction.kind === 'question' ? MessageCircleQuestion : APPROVAL_ICONS[tone];
  const detail = interaction.detail
    ?.split('\n')
    .map((l) => l.trim())
    .find(Boolean);
  return (
    <div className={cn('flex h-6 items-center gap-2 rounded-sm pl-1 text-code-sm text-muted', className)} title={full}>
      <span className="flex size-4 shrink-0 items-center justify-center">
        <Icon aria-hidden="true" className="size-3.5 text-faint" strokeWidth={2} />
      </span>
      <span className="shrink-0 text-ui text-body">{interaction.title}</span>
      {detail && <span className="min-w-0 truncate font-mono" title={detail}>{detail}</span>}
      <span className="ml-auto shrink-0 pr-1 text-caption">{word}</span>
      <span className="sr-only">: {full}</span>
    </div>
  );
}

/**
 * Permission or question card. The first answer from any tab wins: a 409 means someone
 * else answered, a 410 means the provider withdrew the request. While pending the header
 * carries the attention chip; that is the only orange on the card. A question with one
 * question is not a card: it is the composer's extension (`ComposerQuestion`).
 */
export function InteractionCard({ session, interaction, onUpdate }: Readonly<{ session: SessionDetail; interaction: Interaction; onUpdate: (i: Interaction) => void }>) {
  /** The action in flight: a permission option's id, `answer` or `decline`. */
  const [busy, setBusy] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const titleId = useId();
  const pending = interaction.state === 'pending';
  const permission = interaction.kind === 'permission';
  const permitted = permission ? session.capabilities.permissions : session.capabilities.questions;
  const Icon = permission ? ShieldQuestion : MessageCircleQuestion;

  async function respond(answer: Answer, action: string) {
    setBusy(action);
    setNote(null);
    try {
      onUpdate(await api.respond(session.id, interaction.id, answer));
    } catch (e) {
      if (isStatus(e, 409)) setNote('This request was already answered elsewhere.');
      else if (isStatus(e, 410)) setNote('This request expired before it was answered.');
      else setNote(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  // Footer order: reject at the left, the first (default) option as the primary at the right.
  const options = interaction.options ?? [];
  const primary = options.find((o) => !o.reject);
  const ordered = [...options.filter((o) => o.reject), ...options.filter((o) => !o.reject && o !== primary), ...(primary ? [primary] : [])];

  // Decided requests live in the transcript, on their tool row or as a quiet row of their own.
  if (!pending) return <DecidedRow interaction={interaction} />;

  return (
    <section className="rounded-lg bg-raised px-4 py-3 shadow-float animate-rise" role="group" aria-labelledby={titleId}>
      <div className="mb-1.5 flex items-center gap-2">
        <Chip tone="attention">
          <Icon aria-hidden="true" className="size-3.5" />
          {permission ? 'Needs permission' : 'Needs answer'}
        </Chip>
      </div>
      <h3 id={titleId} className="text-title text-ink">
        {interaction.title}
      </h3>
      {interaction.detail && (
        <pre translate="no" className="mt-2 overflow-x-auto rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm text-ink">
          {interaction.detail}
        </pre>
      )}
      {permission ? (
        <>
          {!permitted && <Note className="mt-2">This provider does not accept decisions from UAM.</Note>}
          {permitted && ordered.length > 0 && (
            <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
              {ordered.map((o) => (
                <Button key={o.id} variant={optionVariant(o, primary)} loading={busy === o.id} disabled={!!busy} onClick={() => respond({ decision: o.id }, o.id)}>
                  {o.label}
                </Button>
              ))}
            </div>
          )}
        </>
      ) : (
        <QuestionForm interactionId={interaction.id} questions={interaction.questions ?? []} disabled={!permitted} sending={busy === 'answer' || busy === 'decline' ? busy : null} onSubmit={(answers) => respond({ answers }, 'answer')} onDecline={() => respond({ reject: true }, 'decline')} />
      )}
      {!permission && !permitted && <Note className="mt-2">This provider does not accept answers from UAM.</Note>}
      {note && (
        <Note tone="warn" role="alert" className="mt-2">
          {note}
        </Note>
      )}
    </section>
  );
}

/**
 * One choice as a selectable row: a radio or checkbox per `multiple`, 44px on a coarse pointer.
 * A radio cannot be unchecked natively; `clearable` lets a click on the chosen one clear it (a
 * radio's click fires either way, its change only when it turns on).
 */
function ChoiceRow({ name, choice, multiple, on, clearable = false, onToggle }: Readonly<{ name: string; choice: string; multiple: boolean; on: boolean; clearable?: boolean; onToggle: () => void }>) {
  const change = !multiple && clearable ? { onClick: onToggle, readOnly: true } : { onChange: onToggle };
  return (
    <label className={cn('flex min-h-8 cursor-pointer items-center gap-2.5 rounded-sm px-2 text-ui transition-colors hover:bg-tint-hover pointer-coarse:min-h-11', on && 'bg-tint-hover text-ink')}>
      <input type={multiple ? 'checkbox' : 'radio'} name={name} checked={on} className="size-3.5 accent-accent" {...change} />
      {choice}
    </label>
  );
}

/**
 * A question with one question as the composer's extension (DESIGN.md Composer, answer mode):
 * the attention chip, the question as markdown and its choices as rows that choose the answer
 * in place. The text, the files, Decline and Answer are the composer's. A long question or
 * many options scroll inside a capped box, so the surface never outgrows the pane.
 */
export function ComposerQuestion({ interactionId, question, chosen, disabled, onChoose }: Readonly<{ interactionId: string; question: Question; chosen: string[]; disabled: boolean; onChoose: (choices: string[]) => void }>) {
  const labelId = useId();
  function toggle(choice: string) {
    if (question.multiple) onChoose(chosen.includes(choice) ? chosen.filter((c) => c !== choice) : [...chosen, choice]);
    else onChoose(chosen.includes(choice) ? [] : [choice]);
  }
  return (
    <div className="flex flex-col gap-1.5 px-3.5 pt-3">
      <div className="flex items-center gap-2">
        <Chip tone="attention">
          <MessageCircleQuestion aria-hidden="true" className="size-3.5" />
          Needs answer
        </Chip>
      </div>
      <div className="max-h-[min(240px,30dvh)] overflow-y-auto">
        <div id={labelId} className="text-ui text-ink">
          {question.header && <span className="mb-0.5 block text-caption text-muted">{question.header}</span>}
          <Markdown text={question.text} />
        </div>
        {(question.choices?.length ?? 0) > 0 && (
          <fieldset aria-labelledby={labelId} className="mt-1.5 flex min-w-0 flex-col gap-0.5" disabled={disabled}>
            {question.choices!.map((c) => (
              <ChoiceRow key={c} name={`q-${interactionId}-0`} choice={c} multiple={!!question.multiple} on={chosen.includes(c)} clearable onToggle={() => toggle(c)} />
            ))}
          </fieldset>
        )}
      </div>
      <div className="fade-rule" aria-hidden="true" />
    </div>
  );
}

function QuestionForm({
  interactionId,
  questions,
  disabled,
  sending,
  onSubmit,
  onDecline,
}: Readonly<{
  interactionId: string;
  questions: Question[];
  /** The provider takes no answers from UAM. */
  disabled: boolean;
  /** An answer or a decline is on its way: the buttons stay, disabled, the chosen one spinning. */
  sending: 'answer' | 'decline' | null;
  onSubmit: (answers: string[][]) => void;
  onDecline: () => void;
}>) {
  const [chosen, setChosen] = useState<string[][]>(() => questions.map(() => []));
  const [custom, setCustom] = useState<string[]>(() => questions.map(() => ''));

  // One source per answer: free text replaces the chosen options, and choosing an option drops the text.
  function toggle(qi: number, choice: string, multiple: boolean) {
    setCustom((prev) => prev.map((v, i) => (i === qi ? '' : v)));
    setChosen((prev) =>
      prev.map((arr, i) => {
        if (i !== qi) return arr;
        if (!multiple) return [choice];
        return arr.includes(choice) ? arr.filter((c) => c !== choice) : [...arr, choice];
      }),
    );
  }

  function type(qi: number, text: string) {
    setCustom((prev) => prev.map((v, i) => (i === qi ? text : v)));
    if (text.trim()) setChosen((prev) => prev.map((arr, i) => (i === qi ? [] : arr)));
  }

  const answers = questions.map((q, i) => {
    const c = (custom[i] ?? '').trim();
    return q.custom && c ? [c] : (chosen[i] ?? []).slice();
  });
  const complete = answers.every((a) => a.length > 0);

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (complete && !sending) onSubmit(answers);
  }

  if (disabled && questions.length === 0) return null;

  return (
    <form onSubmit={submit} className="mt-2">
      {questions.map((q, qi) => (
        <fieldset key={qi} className="mb-3 min-w-0" disabled={disabled || !!sending}>
          <legend className="mb-1.5 text-ui text-ink">
            {q.header && <span className="mr-1.5 text-caption text-muted">{q.header}</span>}
            {q.text}
          </legend>
          <div className="flex flex-col gap-0.5">
            {(q.choices ?? []).map((c) => (
              <ChoiceRow key={c} name={`q-${interactionId}-${qi}`} choice={c} multiple={!!q.multiple} on={(chosen[qi] ?? []).includes(c)} onToggle={() => toggle(qi, c, !!q.multiple)} />
            ))}
            {q.custom && (
              <label htmlFor={`q-${interactionId}-${qi}-custom`} className="mt-1 flex items-center">
                <span className="sr-only">Your answer</span>
                <Input id={`q-${interactionId}-${qi}-custom`} placeholder="Your answer" value={custom[qi] ?? ''} onChange={(e) => type(qi, e.target.value)} />
              </label>
            )}
          </div>
        </fieldset>
      ))}
      {!disabled && (
        <div className="flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="danger" loading={sending === 'decline'} disabled={!!sending} onClick={onDecline}>
            Decline
          </Button>
          <Button type="submit" variant="primary" loading={sending === 'answer'} disabled={!complete || !!sending}>
            Answer
          </Button>
        </div>
      )}
    </form>
  );
}
