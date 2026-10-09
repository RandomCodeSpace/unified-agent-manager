import { useApi } from '../ApiContext';
import { MessageCircleQuestion, Shield, ShieldCheck, ShieldQuestion, ShieldX, type LucideIcon } from 'lucide-react';
import { useId, useState, type SubmitEvent , type ReactNode } from 'react';
import { describeError, isStatus, type Answer, type Interaction, type Option, type PlanReview, type Question, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { fieldHint } from '../lib/answer';
import { approvalMark } from '../lib/transcript';
import { Markdown, Note } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Input } from './ui/input';
import { PLAN_ACTION_LABEL, ReadPlan } from './Plan';

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

/** Why an assisted Task still asks: the reviewer's outcome, in words. */
const REVIEW_TEXT: Record<string, string> = { approve: 'approved it', requireApproval: 'asks for your decision', excluded: 'did not review it', error: 'could not review it' };
function assistedNote(review: NonNullable<Interaction['assisted']>): string {
  const outcome = REVIEW_TEXT[review.recommendation] ?? 'did not approve it';
  return `Assisted review${review.model ? ` (${review.model})` : ''} ${outcome}${review.reason ? `: ${review.reason}` : '.'}`;
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
  const api = useApi();
  /** The action in flight: a permission option's id, `answer`, `decline` or `cancel`. */
  const [busy, setBusy] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const titleId = useId();
  const pending = interaction.state === 'pending';
  const permission = interaction.kind === 'permission';
  const permitted = permission ? session.capabilities.permissions : session.capabilities.questions;
  const Icon = permission ? ShieldQuestion : MessageCircleQuestion;
  const elicitation = interaction.elicitation;
  // A link is shown for the user to open; only an https one, as the service sends it.
  const link = elicitation?.mode === 'url' && elicitation.url?.startsWith('https://') ? elicitation.url : undefined;

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
        <pre translate="no" className="mt-2 whitespace-pre-wrap [overflow-wrap:anywhere] rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm text-ink">
          {interaction.detail}
        </pre>
      )}
      {link && (
        <div className="mt-2 text-ui">
          <a href={link} target="_blank" rel="noopener noreferrer" className="break-all text-accent underline underline-offset-2">
            {link}
          </a>
          <p className="mt-1 text-caption text-muted">UAM does not open this link. Open it yourself, then choose Done.</p>
        </div>
      )}
      {permission ? (
        <>
          {interaction.assisted && <Note className="mt-2">{assistedNote(interaction.assisted)}</Note>}
          {!permitted && <Note className="mt-2">This agent does not take decisions from here.</Note>}
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
        <QuestionForm
          interactionId={interaction.id}
          questions={interaction.questions ?? []}
          disabled={!permitted}
          sending={busy === 'answer' || busy === 'decline' || busy === 'cancel' ? busy : null}
          submitLabel={elicitation?.mode === 'url' ? 'Done' : 'Answer'}
          onSubmit={(answers) => respond({ answers }, 'answer')}
          onDecline={() => respond({ reject: true }, 'decline')}
          onCancel={elicitation ? () => respond({ cancel: true }, 'cancel') : undefined}
        />
      )}
      {!permission && !permitted && <Note className="mt-2">This agent does not take answers from here.</Note>}
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
 * A radio's change fires only when it turns on; `again` takes the click on the chosen one too
 * (a radio's click fires either way), and `hint` sits at the chosen row's end to say what it does.
 */
function ChoiceRow({ name, choice, multiple, on, again = false, hint, onToggle }: Readonly<{ name: string; choice: string; multiple: boolean; on: boolean; again?: boolean; hint?: ReactNode; onToggle: () => void }>) {
  const change = !multiple && again ? { onClick: onToggle, readOnly: true } : { onChange: onToggle };
  return (
    <label className={cn('flex min-h-8 cursor-pointer items-center gap-2.5 rounded-sm px-2 text-ui transition-colors hover:bg-tint-hover pointer-coarse:min-h-11', on && 'bg-tint-hover text-ink')}>
      <input type={multiple ? 'checkbox' : 'radio'} name={name} checked={on} className="size-3.5 accent-accent" {...change} />
      <span className="min-w-0 flex-1">{choice}</span>
      {on && hint && <span aria-hidden="true" className="shrink-0 text-caption text-muted animate-fade-in">{hint}</span>}
    </label>
  );
}

/** What a second click on the chosen option does, worded for the pointer in use. */
const AGAIN_HINT = (
  <>
    <span className="pointer-coarse:hidden">Click again to answer</span>
    <span className="hidden pointer-coarse:inline">Tap again to answer</span>
  </>
);

/**
 * A question with one question as the composer's extension (DESIGN.md Composer, answer mode):
 * the attention chip, the question as markdown and its choices as rows that choose the answer
 * in place. The text, the files, Decline and Answer are the composer's. A long question or
 * many options scroll inside a capped box, so the surface never outgrows the pane.
 */
export function ComposerQuestion({ interaction, question, chosen, disabled, onChoose, onAnswer }: Readonly<{ interaction: Interaction; question: Question; chosen: string[]; disabled: boolean; onChoose: (choices: string[]) => void; /** Sends the staged answer: a second click on the chosen option of a single-choice question. */ onAnswer: () => void }>) {
  const labelId = useId();
  const interactionId = interaction.id;
  const hint = fieldHint(question.field);
  // One choice: a click stages it, a click on the staged one answers with it. Several: clicks toggle; Answer sends.
  function toggle(choice: string) {
    if (question.multiple) onChoose(chosen.includes(choice) ? chosen.filter((c) => c !== choice) : [...chosen, choice]);
    else if (chosen.includes(choice)) onAnswer();
    else onChoose([choice]);
  }
  return (
    <div className="flex flex-col gap-1.5 px-3.5 pt-3">
      <div className="flex items-center gap-2">
        <Chip tone="attention">
          <MessageCircleQuestion aria-hidden="true" className="size-3.5" />
          Needs answer
        </Chip>
        {question.multiple && !question.field && <span className="text-caption text-muted">Choose any that apply</span>}
        {hint && <span className="text-caption text-muted">{hint}</span>}
      </div>
      <div className="max-h-[min(240px,30dvh)] overflow-y-auto overflow-x-hidden">
        {interaction.elicitation && (
          // A form's own words, as sent: plain text, never markdown links.
          <div className="mb-1.5 text-ui">
            <span className="block text-caption text-muted">{interaction.title}</span>
            {interaction.detail && <p className="whitespace-pre-wrap [overflow-wrap:anywhere] text-ink">{interaction.detail}</p>}
          </div>
        )}
        <div id={labelId} className="text-ui text-ink">
          {question.header && <span className="mb-0.5 block text-caption text-muted">{question.header}</span>}
          <Markdown text={question.text} />
        </div>
        {(question.choices?.length ?? 0) > 0 && (
          <fieldset aria-labelledby={labelId} className="mt-1.5 flex min-w-0 flex-col gap-0.5" disabled={disabled}>
            {question.choices!.map((c) => (
              <ChoiceRow key={c} name={`q-${interactionId}-0`} choice={c} multiple={!!question.multiple} on={chosen.includes(c)} again hint={question.multiple ? undefined : AGAIN_HINT} onToggle={() => toggle(c)} />
            ))}
          </fieldset>
        )}
      </div>
      <div className="fade-rule" aria-hidden="true" />
    </div>
  );
}

export function ComposerPlan({ plan, sessionId, planVersion, chosen, disabled, onChoose, onAnswer }: Readonly<{ plan: PlanReview; sessionId?: string; planVersion?: number; chosen: string[]; disabled: boolean; onChoose: (choices: string[]) => void; onAnswer: () => void }>) {
  const labelId = useId();
  return <div className="flex flex-col gap-1.5 px-3.5 pt-3">
    <div className="flex items-center gap-2"><Chip tone="attention">Plan ready</Chip><ReadPlan key={plan.request_id} plan={plan} sessionId={sessionId} planVersion={planVersion} /></div>
    {/* Only the summary scrolls: a long one never pushes an offered action out of reach (on a phone above all). */}
    <div className="max-h-[min(120px,15dvh)] overflow-y-auto overflow-x-hidden">
      <p id={labelId} className="text-ui text-ink">{plan.summary || 'Choose how to continue, or send feedback to revise the plan.'}</p>
      {plan.truncated && <Note tone="warn">The plan is shortened. Send feedback to request a smaller plan.</Note>}
    </div>
    <fieldset aria-labelledby={labelId} className="flex min-w-0 flex-col gap-0.5" disabled={disabled || !!plan.truncated}>
      {plan.actions?.map((action) => <ChoiceRow key={action} name={`plan-${plan.request_id}`} choice={PLAN_ACTION_LABEL[action]} multiple={false} on={chosen.includes(action)} again hint={AGAIN_HINT} onToggle={() => chosen.includes(action) ? onAnswer() : onChoose([action])} />)}
    </fieldset>
    <div className="fade-rule" aria-hidden="true" />
  </div>;
}

function QuestionForm({
  interactionId,
  questions,
  disabled,
  sending,
  submitLabel,
  onSubmit,
  onDecline,
  onCancel,
}: Readonly<{
  interactionId: string;
  questions: Question[];
  /** The provider takes no answers from UAM. */
  disabled: boolean;
  /** An answer, a decline or a cancel is on its way: the buttons stay, disabled, the chosen one spinning. */
  sending: 'answer' | 'decline' | 'cancel' | null;
  submitLabel: string;
  onSubmit: (answers: string[][]) => void;
  onDecline: () => void;
  /** An elicitation may also be dismissed without declining it. */
  onCancel?: () => void;
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
  // An optional form field may stay empty; the service checks every field again.
  const complete = answers.every((a, i) => a.length > 0 || (!!questions[i].field && !questions[i].field.required));

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
            {q.field && <span className="block text-caption text-muted">{fieldHint(q.field)}</span>}
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
          {onCancel && (
            <Button variant="secondary" loading={sending === 'cancel'} disabled={!!sending} onClick={onCancel}>
              Cancel
            </Button>
          )}
          <Button type="submit" variant="primary" loading={sending === 'answer'} disabled={!complete || !!sending}>
            {submitLabel}
          </Button>
        </div>
      )}
    </form>
  );
}
