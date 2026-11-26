import { useApi } from '../ApiContext';
import { MessageCircleQuestion, Shield, ShieldCheck, ShieldQuestion, ShieldX, TriangleAlert, type LucideIcon } from 'lucide-react';
import { lazy, Suspense, useEffect, useId, useRef, useState, type ComponentProps, type KeyboardEvent, type SubmitEvent , type ReactNode } from 'react';
import { describeError, isStatus, modelDisplayName, type Answer, type Interaction, type Meta, type Option, type PlanReview, type Question, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { fieldHint } from '../lib/answer';
import { shellRisk } from '../lib/shellRisk';
import { approvalMark } from '../lib/transcript';
import { Markdown, Note, useApp } from './common';
import { Button, rowClass } from './ui/button';
import { Checkbox, Radio, RadioGroup } from './ui/checkbox';
import { Chip } from './ui/chip';
import { Input } from './ui/input';
import { Textarea } from './ui/textarea';
import { Tip } from './ui/tooltip';
import { PLAN_ACTION_LABEL, ReadPlan } from './Plan';

/** A touch screen, where focusing a field would raise the keyboard over the conversation. */
const COARSE = '(pointer: coarse)';

const InlinePatch = lazy(() => import('./Changes').then((m) => ({ default: m.InlinePatch })));

/** A write request's detail is the path, a blank line and the CLI's git patch (CLI 1.0.113 starts it with a blank line of its own): those two parts, or null for any other detail. */
function writePatch(detail?: string): { path: string; patch: string } | null {
  const cut = detail?.indexOf('\n\n') ?? -1;
  const patch = !detail || cut < 0 ? '' : detail.slice(cut + 2).replace(/^\n+/, '');
  if (!detail || !patch.startsWith('diff --git ')) return null;
  return { path: detail.slice(0, cut), patch };
}

/**
 * A decided request that no tool row claims: one quiet row of the tool rows' kind, in its
 * turn at its time. The title, the first line of the request, and the outcome word; the
 * full resolution in the tooltip and the accessible name.
 */
/** The shield for a decided permission: crossed when denied, plain when it lapsed, checked otherwise. */
export const APPROVAL_ICONS: Record<'ok' | 'denied' | 'gone', LucideIcon> = { ok: ShieldCheck, denied: ShieldX, gone: Shield };

/** The default option reads as the primary, the rest (Deny included) secondary; a risky request has no primary. */
function optionVariant(option: Option, primary: Option | undefined, risky: boolean): 'primary' | 'secondary' {
  return option === primary && !risky ? 'primary' : 'secondary';
}


/** Why an assisted Task still asks: the reviewer's outcome, in words. */
const REVIEW_TEXT: Record<string, string> = { approve: 'approved it', requireApproval: 'asks for your decision', excluded: 'did not review it', error: 'could not review it' };
function assistedNote(review: NonNullable<Interaction['assisted']>, meta: Meta | null): string {
  const outcome = REVIEW_TEXT[review.recommendation] ?? 'did not approve it';
  return `Assisted review${review.model ? ` (${modelDisplayName(meta, review.model)})` : ''} ${outcome}${review.reason ? `: ${review.reason}` : '.'}`;
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
  const { meta } = useApp();
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
  // A write shows its change as the Changes diff: the path once, as its caption, and no git headers.
  const write = permission ? writePatch(interaction.detail) : null;

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

  // A question with options is uam's own form (`uam_form`): they are its actions.
  const actions = permission ? [] : (interaction.options ?? []);
  // Footer order: reject at the left, the first (default) option as the primary at the right.
  const options = interaction.options ?? [];
  const primary = options.find((o) => !o.reject);
  const ordered = [...options.filter((o) => o.reject), ...options.filter((o) => !o.reject && o !== primary), ...(primary ? [primary] : [])];
  const risk = permission && interaction.title === 'Run shell command' && interaction.detail ? shellRisk(interaction.detail, [session.workdir, session.real_workdir].filter((r): r is string => !!r)) : '';

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
      {write ? (
        <div role="region" aria-label="Proposed change" className="mt-2 overflow-x-auto rounded-sm bg-sunken">
          {/^@@ /m.test(write.patch) ? (
            <Suspense fallback={<p role="status" className="p-2 text-caption text-muted">Loading the diff viewer…</p>}>
              <InlinePatch path={write.path} text={write.patch} />
            </Suspense>
          ) : (
            <p className="px-3 py-2 text-caption text-muted"><span className="font-mono text-ink">{write.path}</span>: no text change to show.</p>
          )}
        </div>
      ) : interaction.detail && actions.length > 0 ? (
        // A form's body is the agent's plain text, never markdown.
        <p className="mt-1 whitespace-pre-wrap [overflow-wrap:anywhere] text-ui text-body">{interaction.detail}</p>
      ) : interaction.detail && (
        <pre translate="no" className="mt-2 whitespace-pre-wrap [overflow-wrap:anywhere] rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm text-ink">
          {interaction.detail}
        </pre>
      )}
      {permission && interaction.intention && <p className="mt-1 [overflow-wrap:anywhere] text-caption text-muted">{interaction.intention}</p>}
      {risk && (
        <p className="mt-1 flex items-center gap-1.5 text-caption text-warning">
          <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0" />
          {risk}
        </p>
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
          {interaction.assisted && <Note className="mt-2">{assistedNote(interaction.assisted, meta)}</Note>}
          {!permitted && <Note className="mt-2">This agent does not take decisions from here.</Note>}
          {permitted && ordered.length > 0 && (
            <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
              {ordered.map((o) => {
                const button = (
                  <Button key={o.id} variant={optionVariant(o, primary, !!risk)} loading={busy === o.id} disabled={!!busy} onClick={() => respond({ decision: o.id }, o.id)}>
                    {o.label}
                  </Button>
                );
                return o.hint ? <Tip key={o.id} label={o.hint}>{button}</Tip> : button;
              })}
            </div>
          )}
        </>
      ) : actions.length > 0 ? (
        <ActionForm
          questions={interaction.questions ?? []}
          actions={actions}
          disabled={!permitted}
          sending={busy}
          onSubmit={(decision, answers) => respond({ decision, answers }, decision)}
          onDismiss={() => respond({ cancel: true }, 'cancel')}
        />
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

/** Enter in a radio submits its form through the form's default button, as a native radio does; Base UI's takes the key. */
function submitOnEnter(event: KeyboardEvent<HTMLElement>) {
  if (event.key !== 'Enter') return;
  const form = event.currentTarget.closest('form');
  const button = form?.querySelector<HTMLButtonElement>('[type="submit"]');
  if (button) button.click();
  else form?.requestSubmit();
}

/**
 * The fieldset around a question's ChoiceRows. With one answer it is their RadioGroup too, which reports the choice
 * (`onPick`); it keeps a fieldset's `group` role. Base UI controls ignore a fieldset's `disabled`, so each row takes it.
 */
function ChoiceSet({ multiple, name, value, onPick, disabled, ...props }: Pick<ComponentProps<'fieldset'>, 'className' | 'children' | 'disabled' | 'aria-labelledby' | 'aria-describedby'> & { 'data-field'?: number; multiple: boolean; name: string; value: string | undefined; onPick: (choice: string) => void }) {
  if (multiple) return <fieldset disabled={disabled} {...props} />;
  return <RadioGroup render={<fieldset disabled={disabled} />} role="group" name={name} value={value ?? ''} disabled={disabled} onValueChange={(v) => onPick(v as string)} {...props} />;
}

/**
 * One choice as a selectable row inside a ChoiceSet: a radio (`value`, the choice unless given) or a checkbox per
 * `multiple`, 44px on a coarse pointer. A checkbox toggles through `onToggle`; a radio turns on through its set.
 * `again` sends a pointer's click on the chosen radio to `onToggle` too, and `hint` sits at the chosen row's end to
 * say what it does.
 */
function ChoiceRow({ name, choice, value = choice, multiple, on, again = false, hint, describedBy, disabled, onToggle }: Readonly<{ name: string; choice: string; value?: string; multiple: boolean; on: boolean; again?: boolean; hint?: ReactNode; describedBy?: string; disabled?: boolean; onToggle: () => void }>) {
  const hintId = useId();
  const shown = on && !!hint;
  // The name stays the choice; the hint is its description, so a screen reader announces it too.
  const control = { 'aria-label': choice, 'aria-describedby': shown ? hintId : describedBy };
  return (
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events -- The click is the radio's own (it bubbles from the control or its label); `detail` leaves a key's click out.
    <label data-selected={on || undefined} data-disabled={disabled || undefined} className={cn(rowClass, 'min-h-8 cursor-pointer py-0 pointer-coarse:min-h-11 data-disabled:pointer-events-none')} onClick={!multiple && again && on && !disabled ? (e) => { if (e.detail > 0) onToggle(); } : undefined}>
      {multiple
        ? <Checkbox name={name} checked={on} disabled={disabled} onCheckedChange={onToggle} {...control} />
        : <Radio value={value} onKeyDown={submitOnEnter} {...control} />}
      <span className="min-w-0 flex-1">{choice}</span>
      {shown && <span id={hintId} className="shrink-0 text-caption text-muted animate-fade-in">{hint}</span>}
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
          <ChoiceSet multiple={!!question.multiple} name={`q-${interactionId}-0`} value={chosen[0]} onPick={toggle} aria-labelledby={labelId} className="mt-1.5 flex min-w-0 flex-col gap-0.5" disabled={disabled}>
            {question.choices!.map((c) => (
              <ChoiceRow key={c} name={`q-${interactionId}-0`} choice={c} multiple={!!question.multiple} on={chosen.includes(c)} again hint={question.multiple ? undefined : AGAIN_HINT} disabled={disabled} onToggle={() => toggle(c)} />
            ))}
          </ChoiceSet>
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
    <ChoiceSet multiple={false} name={`plan-${plan.request_id}`} value={chosen[0]} onPick={(action) => onChoose([action])} aria-labelledby={labelId} className="flex min-w-0 flex-col gap-0.5" disabled={disabled || !!plan.truncated}>
      {plan.actions?.map((action) => <ChoiceRow key={action} name={`plan-${plan.request_id}`} choice={PLAN_ACTION_LABEL[action]} value={action} multiple={false} on={chosen.includes(action)} again hint={AGAIN_HINT} disabled={disabled || !!plan.truncated} onToggle={onAnswer} />)}
    </ChoiceSet>
    <div className="fade-rule" aria-hidden="true" />
  </div>;
}

/** A form field's answer as sent: typed text trimmed (nothing when blank), else the chosen options. */
const fieldAnswer = (q: Question, v: string[]): string[] => (q.custom ? v.map((t) => t.trim()).filter(Boolean) : v);

/**
 * uam's own form for an agent's `uam_form` call (DESIGN.md Forms): every field at once in the agent's order, then
 * Dismiss and the actions, the first the default (Enter in a line chooses it). Every string is plain text. Choosing
 * an action with a required field empty says so under that field and moves focus to it; nothing is sent. The first
 * field takes focus on a fine pointer unless the person is typing elsewhere or another form holds it.
 */
function ActionForm({ questions, actions, disabled, sending, onSubmit, onDismiss }: Readonly<{
  questions: Question[];
  actions: Option[];
  /** The provider takes no answers from UAM. */
  disabled: boolean;
  /** The action in flight (an action's id or `cancel`): the buttons stay, disabled, the chosen one spinning. */
  sending: string | null;
  onSubmit: (decision: string, answers: string[][]) => void;
  onDismiss: () => void;
}>) {
  const id = useId();
  const formRef = useRef<HTMLFormElement>(null);
  const [values, setValues] = useState<string[][]>(() => questions.map((q) => q.field?.default ?? []));
  const [tried, setTried] = useState(false);
  const missing = questions.map((q, i) => !!q.field?.required && fieldAnswer(q, values[i] ?? []).length === 0);
  const set = (i: number, v: string[]) => setValues((prev) => prev.map((x, k) => (k === i ? v : x)));

  useEffect(() => {
    if (window.matchMedia(COARSE).matches) return;
    const active = document.activeElement;
    // Never from someone typing, nor from another form already holding it.
    if (((active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement) && active.value) || active?.closest('[data-uam-form]')) return;
    formRef.current?.querySelector<HTMLElement>('input, textarea, button[type="submit"]')?.focus();
  }, []);

  function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (sending || disabled) return;
    // The button pressed, or the first action for Enter in a line.
    const decision = (e.nativeEvent.submitter as HTMLButtonElement | null)?.value || actions[0].id;
    const first = missing.indexOf(true);
    if (first >= 0) {
      setTried(true);
      formRef.current?.querySelector<HTMLElement>(`[data-field="${first}"] :is(input, textarea)`)?.focus();
      return;
    }
    onSubmit(decision, questions.map((q, i) => fieldAnswer(q, values[i] ?? [])));
  }

  return (
    <form ref={formRef} onSubmit={submit} noValidate data-uam-form className="mt-2 flex flex-col gap-3">
      {questions.map((q, i) => {
        const f = q.field;
        const key = `${id}-${i}`;
        const v = values[i] ?? [];
        const error = tried && missing[i] ? (q.custom ? 'Fill this in to continue.' : 'Choose an option to continue.') : null;
        const describedBy = [f?.help && `${key}-help`, error && `${key}-error`].filter(Boolean).join(' ') || undefined;
        const help = f?.help && <p id={`${key}-help`} className="text-caption text-muted">{f.help}</p>;
        const errorLine = error && <p id={`${key}-error`} className="text-caption text-error">{error}</p>;
        const required = f?.required && <span className="ml-1.5 text-caption text-muted">Required</span>;
        if (q.custom) {
          const common = { id: key, value: v[0] ?? '', maxLength: f?.max_length, disabled: disabled || !!sending, 'aria-invalid': !!error || undefined, 'aria-required': f?.required || undefined, 'aria-describedby': describedBy };
          return (
            <div key={i} data-field={i} className="flex min-w-0 flex-col gap-1">
              <label htmlFor={key} className="text-ui text-ink">{q.text}{required}</label>
              {f?.multiline ? (
                <Textarea {...common} className="min-h-20" onChange={(e) => set(i, [e.target.value])} />
              ) : (
                <Input {...common} onChange={(e) => set(i, [e.target.value])} />
              )}
              {help}
              {errorLine}
            </div>
          );
        }
        if (f?.type === 'boolean') {
          return (
            <div key={i} data-field={i} className="flex min-w-0 flex-col gap-1">
              <ChoiceRow name={key} choice={q.text} multiple on={v[0] === 'Yes'} describedBy={describedBy} disabled={disabled || !!sending} onToggle={() => set(i, [v[0] === 'Yes' ? 'No' : 'Yes'])} />
              {help}
            </div>
          );
        }
        return (
          <ChoiceSet key={i} multiple={!!q.multiple} name={key} value={v[0]} onPick={(c) => set(i, [c])} data-field={i} className="flex min-w-0 flex-col gap-0.5" disabled={disabled || !!sending} aria-describedby={describedBy}>
            <legend className="mb-1 text-ui text-ink">{q.text}{required}</legend>
            {(q.choices ?? []).map((c) => (
              <ChoiceRow key={c} name={key} choice={c} multiple={!!q.multiple} on={v.includes(c)} disabled={disabled || !!sending} onToggle={() => set(i, v.includes(c) ? v.filter((x) => x !== c) : [...v, c])} />
            ))}
            {help}
            {errorLine}
          </ChoiceSet>
        );
      })}
      {!disabled && (
        <div className="flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
          <Button variant="secondary" loading={sending === 'cancel'} disabled={!!sending} onClick={onDismiss}>
            Dismiss
          </Button>
          {actions.map((a, k) => (
            <Button key={a.id} type="submit" value={a.id} variant={k === 0 ? 'primary' : 'secondary'} loading={sending === a.id} disabled={!!sending}>
              {a.label}
            </Button>
          ))}
        </div>
      )}
    </form>
  );
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
        <ChoiceSet key={qi} multiple={!!q.multiple} name={`q-${interactionId}-${qi}`} value={chosen[qi]?.[0]} onPick={(c) => toggle(qi, c, false)} className="mb-3 min-w-0" disabled={disabled || !!sending}>
          <legend className="mb-1.5 text-ui text-ink">
            {q.header && <span className="mr-1.5 text-caption text-muted">{q.header}</span>}
            {q.text}
            {q.field && <span className="block text-caption text-muted">{fieldHint(q.field)}</span>}
          </legend>
          <div className="flex flex-col gap-0.5">
            {(q.choices ?? []).map((c) => (
              <ChoiceRow key={c} name={`q-${interactionId}-${qi}`} choice={c} multiple={!!q.multiple} on={(chosen[qi] ?? []).includes(c)} disabled={disabled || !!sending} onToggle={() => toggle(qi, c, true)} />
            ))}
            {q.custom && (
              <label htmlFor={`q-${interactionId}-${qi}-custom`} className="mt-1 flex items-center">
                <span className="sr-only">Your answer</span>
                <Input id={`q-${interactionId}-${qi}-custom`} placeholder="Your answer" value={custom[qi] ?? ''} onChange={(e) => type(qi, e.target.value)} />
              </label>
            )}
          </div>
        </ChoiceSet>
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
