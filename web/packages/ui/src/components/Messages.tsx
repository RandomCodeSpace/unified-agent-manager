import { MessagesSquare } from 'lucide-react';
import { createContext, useContext, useId, useState } from 'react';
import { describeError, type MessagePair, type PeerStatus, type SessionSummary } from '../api';
import { useApi } from '../ApiContext';
import { Note } from './common';
import { TaskList, useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { AlertDialog, useConfirm } from './ui/dialog';

/** The pairs of Threads that message each other (`message_pairs`), from the app state. */
export const MessagePairsContext = createContext<MessagePair[]>([]);

const reasons = (limit: number): Record<NonNullable<MessagePair['held']>['reason'], string> => ({
  first: 'The first message between these threads waits for you.',
  limit: `These threads used the deliveries you allowed. Allow covers ${limit} more.`,
  link: 'One of these threads runs unattended (Yolo, Assisted or autopilot): link them to let messages through.',
});

const STATUS: Record<PeerStatus, string> = {
  held: 'waiting for you',
  queued: 'queued',
  working: 'being answered',
  replying: 'reply queued',
  replied: 'replied',
  stopped: 'stopped, no reply',
  failed: 'failed, no reply',
  interrupted: 'interrupted, no reply',
  cancelled: 'removed from the queue',
  declined: 'declined',
  undelivered: 'not delivered',
};

/** A pair's held message, or one line saying how it stands, for each pair of the Thread `session`. */
export function ThreadMessages({ session }: Readonly<{ session: SessionSummary }>) {
  const pairs = useContext(MessagePairsContext).filter((p) => p.a === session.id || p.b === session.id);
  if (pairs.length === 0) return null;
  return (
    <>
      {pairs.map((p) => (p.held ? <HeldMessage key={p.id} pair={p} self={session.id} /> : <PairLine key={p.id} pair={p} self={session.id} />))}
    </>
  );
}

function useThreadName() {
  const { sessions } = useContext(TaskList);
  return (id: string) => {
    const s = sessions.find((x) => x.id === id);
    return s ? s.name || s.title || 'Untitled thread' : 'another thread';
  };
}

/** No person decides the Thread's permission requests, as the service judges it: its messages need a linked pair. A Thread missing from the list is not judged here; the service's answer to Allow decides. */
export function unattended(s: SessionSummary | undefined): boolean {
  return !!s && (s.mode === 'yolo' || s.mode === 'assisted' || !!s.mode_unknown || s.execution?.mode === 'autopilot');
}

function usePairAction(pair: MessagePair) {
  const api = useApi();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const act = async (action: 'allow' | 'deny' | 'reset', link = false) => {
    setBusy(action);
    setError(null);
    try {
      await api.messagePair(pair.id, action, link);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  };
  return { busy, error, act };
}

/** The held message's two Threads: each name with its Project, and a short ID when the names alone collide. */
function usePairLabels(from: string, to: string) {
  const { sessions, projects } = useContext(TaskList);
  const name = useThreadName();
  const project = (id: string) => {
    const s = sessions.find((x) => x.id === id);
    return projects.find((p) => p.id === s?.project_id)?.name ?? 'an unknown project';
  };
  const same = name(from) === name(to) && project(from) === project(to);
  const label = (id: string) => (same ? `${name(id)} · ${id.slice(0, 8)}` : name(id));
  return { label, project };
}

function ThreadButton({ id, name }: Readonly<{ id: string; name: string }>) {
  const actions = useTaskActions();
  return <button type="button" className="underline decoration-hairline-strong underline-offset-2 hover:text-ink" onClick={() => actions.select(id)}>{name}</button>;
}

function HeldMessage({ pair, self }: Readonly<{ pair: MessagePair; self: string }>) {
  const held = pair.held!;
  const { label, project } = usePairLabels(held.from, held.to);
  const titleId = useId();
  const { busy, error, act } = usePairAction(pair);
  const confirm = useConfirm<true>();
  const { sessions } = useContext(TaskList);
  const link = !pair.linked && [held.from, held.to].some((id) => unattended(sessions.find((s) => s.id === id)));
  const sender = held.from === self ? 'This thread' : label(held.from);
  const receiver = held.to === self ? 'this thread' : label(held.to);
  return (
    <section className="rounded-lg bg-raised px-4 py-3 shadow-float animate-rise" role="group" aria-labelledby={titleId}>
      <div className="mb-1.5 flex items-center gap-2">
        <Chip tone="attention">
          <MessagesSquare aria-hidden="true" className="size-3.5" />
          Message waiting
        </Chip>
      </div>
      <h3 id={titleId} className="text-title text-ink">
        {held.from === self ? sender : <ThreadButton id={held.from} name={sender} />} <span className="text-muted">({project(held.from)})</span> to {held.to === self ? receiver : <ThreadButton id={held.to} name={receiver} />} <span className="text-muted">({project(held.to)})</span>
      </h3>
      <p className="mt-1 text-caption text-muted">{reasons(pair.limit)[held.reason]}</p>
      <pre translate="no" className="mt-2 max-h-64 overflow-y-auto whitespace-pre-wrap [overflow-wrap:anywhere] rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm text-ink">{held.text}</pre>
      <p className="mt-1 text-caption text-muted">It comes from an agent. Allowed, it starts a turn in {receiver}, whose reply goes back the same way.</p>
      <div className="mt-3 flex flex-wrap justify-end gap-2 max-sm:[&>button]:flex-1">
        <Button variant="secondary" loading={busy === 'deny'} disabled={!!busy} onClick={() => void act('deny')}>Deny</Button>
        {link ? (
          <Button variant="primary" loading={busy === 'allow'} disabled={!!busy} onClick={() => confirm.ask(true)}>Link and allow</Button>
        ) : (
          <Button variant="primary" loading={busy === 'allow'} disabled={!!busy} onClick={() => void act('allow')}>Allow</Button>
        )}
      </div>
      {error && <Note tone="warn" role="alert" className="mt-2">{error}</Note>}
      <AlertDialog
        {...confirm.props}
        className="max-w-88"
        title="Link these threads?"
        description={`${label(held.from)} (${project(held.from)}) and ${label(held.to)} (${project(held.to)}) keep messaging while either runs unattended, so their messages can start actions nobody approves. Deny ends it at any time.`}
        confirmLabel="Link and allow"
        busy={busy !== null}
        onConfirm={() => { confirm.close(); void act('allow', true); }}
      />
    </section>
  );
}

function PairLine({ pair, self }: Readonly<{ pair: MessagePair; self: string }>) {
  const name = useThreadName();
  const { busy, error, act } = usePairAction(pair);
  const other = pair.a === self ? pair.b : pair.a;
  const last = pair.messages.at(-1);
  if (pair.state === 'pending') return null;
  const denied = pair.state === 'denied';
  return (
    <Note role="status" className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span className="min-w-0 flex-1">
        Messages with <ThreadButton id={other} name={name(other)} />
        {denied ? ' declined' : ` · ${pair.used} of ${pair.limit} used${pair.linked ? ' · linked' : ''}`}
        {!denied && last && ` · last ${STATUS[last.status]}`}
        {!denied && last?.detail && <span className="text-muted"> ({last.detail})</span>}
      </span>
      {denied ? (
        <Button size="sm" variant="secondary" className="h-6" loading={busy === 'reset'} disabled={!!busy} onClick={() => void act('reset')}>Reset</Button>
      ) : (
        <Button size="sm" variant="secondary" className="h-6" loading={busy === 'deny'} disabled={!!busy} onClick={() => void act('deny')}>Deny</Button>
      )}
      {error && <span role="alert" className="basis-full text-caption text-error">{error}</span>}
    </Note>
  );
}
