import { memo, useEffect, useState } from 'react';
import { type Interaction, type Project, type SessionDetail, type SessionSummary } from '../../api';
import { useApi } from '../../ApiContext';
import { shownState, taskStatus } from '../../lib/tasks';
import { awaitsUser } from '../../lib/transcript';
import { InteractionCard } from '../Interactions';
import { TaskRowContent } from '../Sidebar';
import { taskMenuItems, useTaskActions } from '../taskActions';
import { Button } from '../ui/button';

// The request's own card (Interactions) drawn flat inside this one: this card is its surface, and its
// state line already carries the one orange tag, so the request's chip row is left out.
const FLAT = '[&>section]:animate-none [&>section]:rounded-none [&>section]:bg-transparent [&>section]:p-0 [&>section]:shadow-none [&>section>div:first-child]:hidden [&_h3]:text-ui';

/**
 * What another Thread waits on, answered from its card. The Thread list holds only the request's
 * title (`ask`); the request itself is in the Thread's detail, read once per request. A plan to
 * review, or a detail that could not be read, is answered in the Thread.
 */
function ThreadAsk({ session: s, text }: Readonly<{ session: SessionSummary; text: string }>) {
  const api = useApi();
  const a = useTaskActions();
  const [read, setRead] = useState<{ ask: string; detail: SessionDetail | null } | null>(null);
  const ask = `${s.id}\n${s.ask?.kind}\n${s.ask?.title}`;
  useEffect(() => {
    let current = true;
    api.session(s.id).then((detail) => current && setRead({ ask, detail }), () => current && setRead({ ask, detail: null }));
    return () => {
      current = false;
    };
  }, [api, s.id, ask]);
  const detail = read?.ask === ask ? read.detail : undefined;
  const pending = detail?.interactions.find(awaitsUser);
  const update = (next: Interaction) => setRead((held) => held?.detail ? { ...held, detail: { ...held.detail, interactions: held.detail.interactions.map((i) => (i.id === next.id ? next : i)) } } : held);
  if (detail && pending && pending.kind !== 'plan_review') {
    return <div className={FLAT}><InteractionCard session={detail} interaction={pending} onUpdate={update} /></div>;
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
 * in words. A Thread that waits on you is answered here.
 */
export const ThreadCard = memo(function ThreadCard({ session: s, project, unread }: Readonly<{ session: SessionSummary; project: Project; unread: boolean; /** The list's shared clock refreshes the relative time. */ minute: number }>) {
  const a = useTaskActions();
  const status = taskStatus(s, unread);
  const waits = status.tone === 'attention';
  const finished = shownState(s) === 'completed';
  // The row menu's own Settle, with its rules and its guard.
  const settle = finished ? taskMenuItems(s, a, 'row').find((item) => item.key === 'settle' && !item.disabled) : undefined;
  // A finished Thread's outcome; else what the state says beyond its word (quiet for how long, why it failed).
  const line = finished ? s.outcome : !waits && status.text !== status.label ? status.text : undefined;
  return (
    <li className="flex flex-col gap-2 rounded-md bg-raised p-2.5 text-caption shadow-raised">
      <button type="button" className="flex w-full flex-col gap-0.5 rounded-sm text-left font-medium text-ink" onClick={() => a.select(s.id)}>
        <TaskRowContent session={s} project={project} selected={false} unread={unread} />
      </button>
      {line && <p className="line-clamp-2 [overflow-wrap:anywhere] text-caption text-muted">{line}</p>}
      {waits && <ThreadAsk session={s} text={status.text} />}
      <div className="flex gap-1.5">
        <Button size="sm" variant="secondary" className="flex-1" onClick={() => a.select(s.id)}>Open</Button>
        {settle && <Button size="sm" variant="secondary" className="flex-1" onClick={() => settle.onSelect()}>{settle.label}</Button>}
      </div>
    </li>
  );
});
