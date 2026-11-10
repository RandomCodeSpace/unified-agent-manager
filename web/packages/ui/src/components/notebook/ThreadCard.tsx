import { memo, useEffect, useState } from 'react';
import { type Interaction, type Project, type SessionDetail, type SessionSummary } from '../../api';
import { useApi } from '../../ApiContext';
import { shownState, taskStatus } from '../../lib/tasks';
import { awaitsUser } from '../../lib/transcript';
import { InteractionCard } from '../Interactions';
import { TaskRowContent } from '../Sidebar';
import { taskMenuItems, useTaskActions } from '../taskActions';
import { Button } from '../ui/button';

/**
 * What another Thread waits on, answered from its card. The Thread list holds only the request's
 * title (`ask`); the request itself is in the Thread's detail. A plan to
 * review, or a detail that could not be read, is answered in the Thread.
 */
function ThreadAsk({ session: s, text }: Readonly<{ session: SessionSummary; text: string }>) {
  const api = useApi();
  const a = useTaskActions();
  // Undefined until first read; null when the read failed.
  const [detail, setDetail] = useState<SessionDetail | null>();
  // The list names a request by its kind and title only, which two requests can share: any news of
  // the Thread reads it again, and the form on screen stays until that answer arrives.
  useEffect(() => {
    let current = true;
    api.session(s.id).then((next) => current && setDetail(next), () => current && setDetail((held) => held ?? null));
    return () => {
      current = false;
    };
  }, [api, s.id, s.updated_at, s.ask?.kind, s.ask?.title]);
  const pending = detail?.interactions.find(awaitsUser);
  const update = (next: Interaction) => setDetail((held) => held && { ...held, interactions: held.interactions.map((i) => (i.id === next.id ? next : i)) });
  if (detail && pending && pending.kind !== 'plan_review') {
    // Keyed by the request, so what was typed for one never shows under the next.
    return <InteractionCard key={pending.id} session={detail} interaction={pending} onUpdate={update} place="card" />;
  }
  // Answered here a moment ago: the Thread's next state arrives with the list.
  if (detail && !pending) return null;
  return (
    <>
      <p className="[overflow-wrap:anywhere] text-caption text-body">{text}</p>
      {detail !== undefined && <Button size="sm" variant="primary" onClick={() => a.select(s.id)}>Open to answer</Button>}
    </>
  );
}

/**
 * Another Thread that is active, as a small card in the Contents column ("Also running"): the
 * sidebar row's own two lines (Project, state, title), the one line that matters, and its actions
 * in words. A Thread that waits on you is answered here; `folded`, behind "Answer here".
 */
export const ThreadCard = memo(function ThreadCard({ session: s, project, unread, folded = false }: Readonly<{ session: SessionSummary; project: Project; unread: boolean; /** The list's shared clock refreshes the relative time. */ minute: number; /** With many Threads waiting, this one shows its request's line until asked for its form (Contents). */ folded?: boolean }>) {
  const a = useTaskActions();
  const [answering, setAnswering] = useState(false);
  const status = taskStatus(s, unread);
  const waits = status.tone === 'attention';
  // Folded, it says what it asks, and "Answer here" brings the form.
  const asks = waits && folded && !answering;
  const finished = shownState(s) === 'completed';
  // The row menu's own Settle, with its rules and its guard.
  const settle = finished ? taskMenuItems(s, a, 'row').find((item) => item.key === 'settle' && !item.disabled) : undefined;
  // A finished Thread's outcome; else what the state says beyond its word (quiet for how long, why it failed).
  const line = finished ? s.outcome : !waits && status.text !== status.label ? status.text : undefined;
  return (
    <li className="flex flex-col gap-2 rounded-md bg-raised p-2.5 text-caption shadow-raised">
      <button type="button" tabIndex={-1} className="flex w-full flex-col gap-0.5 rounded-sm text-left font-medium text-ink" onClick={() => a.select(s.id)}>
        <TaskRowContent session={s} project={project} selected={false} unread={unread} />
      </button>
      {line && <p className="line-clamp-2 [overflow-wrap:anywhere] text-caption text-muted">{line}</p>}
      {waits && (asks ? <p className="line-clamp-2 [overflow-wrap:anywhere] text-caption text-body">{s.ask?.title ?? status.text}</p> : <ThreadAsk session={s} text={status.text} />)}
      <div className="flex gap-1.5">
        {asks && <Button size="sm" variant="secondary" className="flex-1" onClick={() => setAnswering(true)}>Answer here</Button>}
        {/* The row above opens the Thread too: this one says so in a word, and is the one in the tab order. */}
        <Button size="sm" variant="secondary" className="flex-1" onClick={() => a.select(s.id)}>Open</Button>
        {settle && <Button size="sm" variant="secondary" className="flex-1" onClick={() => settle.onSelect()}>{settle.label}</Button>}
      </div>
    </li>
  );
});
