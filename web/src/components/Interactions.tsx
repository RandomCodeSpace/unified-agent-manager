import { useId, useState, type FormEvent } from 'react';
import { api, describeError, isStatus, type Answer, type Interaction, type Question, type SessionDetail } from '../api';

const STATE_TEXT: Record<Interaction['state'], string> = {
  pending: 'Pending',
  answered: 'Answered',
  rejected: 'Declined',
  expired: 'Expired',
};

/**
 * Permission or question card. The first answer from any tab wins: a 409 means someone
 * else answered, a 410 means the provider withdrew the request.
 */
export function InteractionCard({
  session,
  interaction,
  onUpdate,
}: {
  session: SessionDetail;
  interaction: Interaction;
  onUpdate: (i: Interaction) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const titleId = useId();
  const pending = interaction.state === 'pending';
  const permission = interaction.kind === 'permission';
  const permitted = permission ? session.capabilities.permissions : session.capabilities.questions;

  async function respond(answer: Answer) {
    setBusy(true);
    setNote(null);
    try {
      onUpdate(await api.respond(session.id, interaction.id, answer));
    } catch (e) {
      if (isStatus(e, 409)) setNote('This request was already answered elsewhere.');
      else if (isStatus(e, 410)) setNote('This request expired before it was answered.');
      else setNote(describeError(e));
    } finally {
      setBusy(false);
    }
  }

  const cls = ['card', 'interaction', pending ? `interaction-pending ${permission ? 'bloom-peach' : 'bloom-sky'}` : 'interaction-done'].join(' ');

  return (
    <section className={cls} aria-labelledby={titleId}>
      <div className="label">
        {permission ? 'Permission' : 'Question'}
        {!pending && ` · ${STATE_TEXT[interaction.state]}`}
      </div>
      <h3 id={titleId} className="card-title">
        {interaction.title}
      </h3>
      {interaction.detail && <pre className="code">{interaction.detail}</pre>}
      {permission ? (
        <div className="row wrap">
          {pending && !permitted && <span className="muted small">This provider does not accept decisions from UAM.</span>}
          {pending &&
            permitted &&
            (interaction.options ?? []).map((o, k) => (
              <button
                key={o.id}
                type="button"
                className={o.reject ? 'pill pill-outline' : k === 0 ? 'pill pill-primary' : 'pill pill-outline'}
                disabled={busy}
                onClick={() => respond({ decision: o.id })}
              >
                {o.label}
              </button>
            ))}
        </div>
      ) : (
        <QuestionForm
          interactionId={interaction.id}
          questions={interaction.questions ?? []}
          disabled={!pending || !permitted || busy}
          onSubmit={(answers) => respond({ answers })}
          onDecline={() => respond({ reject: true })}
        />
      )}
      {pending && !permission && !permitted && <p className="muted small">This provider does not accept answers from UAM.</p>}
      {!pending && (
        <p className="muted small">
          {STATE_TEXT[interaction.state]}
          {interaction.resolution ? ` · ${interaction.resolution}` : ''}
        </p>
      )}
      {note && (
        <p className="warn" role="alert">
          {note}
        </p>
      )}
    </section>
  );
}

function QuestionForm({
  interactionId,
  questions,
  disabled,
  onSubmit,
  onDecline,
}: {
  interactionId: string;
  questions: Question[];
  disabled: boolean;
  onSubmit: (answers: string[][]) => void;
  onDecline: () => void;
}) {
  const [chosen, setChosen] = useState<string[][]>(() => questions.map(() => []));
  const [custom, setCustom] = useState<string[]>(() => questions.map(() => ''));

  function toggle(qi: number, choice: string, multiple: boolean) {
    setChosen((prev) =>
      prev.map((arr, i) => {
        if (i !== qi) return arr;
        if (!multiple) return [choice];
        return arr.includes(choice) ? arr.filter((c) => c !== choice) : [...arr, choice];
      }),
    );
  }

  const answers = questions.map((q, i) => {
    const a = (chosen[i] ?? []).slice();
    const c = (custom[i] ?? '').trim();
    if (q.custom && c) a.push(c);
    return a;
  });
  const complete = answers.every((a) => a.length > 0);

  function submit(e: FormEvent) {
    e.preventDefault();
    if (complete) onSubmit(answers);
  }

  if (disabled && questions.length === 0) return null;

  return (
    <form onSubmit={submit}>
      {questions.map((q, qi) => (
        <fieldset key={qi} className="question" disabled={disabled}>
          <legend>
            {q.header && <span className="q-header">{q.header}</span>}
            {q.text}
          </legend>
          {(q.choices ?? []).map((c) => (
            <label key={c} className="choice">
              <input
                type={q.multiple ? 'checkbox' : 'radio'}
                name={`q-${interactionId}-${qi}`}
                checked={(chosen[qi] ?? []).includes(c)}
                onChange={() => toggle(qi, c, !!q.multiple)}
              />
              {c}
            </label>
          ))}
          {q.custom && (
            <label className="choice choice-custom">
              <span className="sr-only">Your answer</span>
              <input
                className="input"
                type="text"
                placeholder="Your answer"
                value={custom[qi] ?? ''}
                onChange={(e) => setCustom((prev) => prev.map((v, i) => (i === qi ? e.target.value : v)))}
              />
            </label>
          )}
        </fieldset>
      ))}
      {!disabled && (
        <div className="row wrap">
          <button type="submit" className="pill pill-primary" disabled={!complete}>
            Answer
          </button>
          <button type="button" className="pill pill-outline" onClick={onDecline}>
            Decline
          </button>
        </div>
      )}
    </form>
  );
}
