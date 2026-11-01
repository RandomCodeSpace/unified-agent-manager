import { useEffect, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { LIVE, describeError, newRequestId, type ContextAttribution, type ContextBreakdown, type ContextHeaviest, type SessionDetail } from '../api';
import { contextLabel } from './ComposerUsage';
import { Button } from './ui/button';
import { PanelSection } from './ui/panel';

const number = new Intl.NumberFormat('en-US');
const tokens = (n: number) => `${number.format(n)} tokens`;

function Counts({ rows }: Readonly<{ rows: readonly (readonly [string, number])[] }>) {
  return <dl className="space-y-1 tabular-nums">{rows.map(([label, count]) => <div key={label} className="flex items-start justify-between gap-4"><dt className="text-muted">{label}</dt><dd className="shrink-0 text-body">{tokens(count)}</dd></div>)}</dl>;
}

/** Native entries overlap their parents. Grouping is for navigation, never an additive rollup. */
function Sources({ attribution }: Readonly<{ attribution: ContextAttribution }>) {
  const groups = new Map<string, ContextAttribution['entries']>();
  const names = new Map(attribution.entries.map((entry) => [entry.id, entry.label || entry.kind || 'Source']));
  for (const entry of attribution.entries) {
    const kind = entry.kind || 'Other';
    const group = groups.get(kind) ?? [];
    group.push(entry);
    groups.set(kind, group);
  }
  return <>
    <p className="text-caption text-muted">Source counts can overlap. A parent includes its children; do not add these rows together.</p>
    {[...groups].map(([kind, entries]) => <details key={kind} className="py-2" open={groups.size === 1}>
      <summary className="cursor-pointer text-ui text-ink">{kind} <span className="text-caption text-muted">({entries.length})</span></summary>
      <ul className="mt-2 space-y-2">{entries.map((entry) => <li key={entry.id} className="flex items-start justify-between gap-4">
        <span className="min-w-0 break-words text-ui">{entry.label || entry.kind || 'Source'}{entry.parent_id && <span className="block text-caption text-muted">Included in {names.get(entry.parent_id) || 'another source'}</span>}</span>
        <span className="shrink-0 text-caption tabular-nums">{tokens(entry.tokens)}</span>
      </li>)}</ul>
    </details>)}
    {!attribution.entries.length && <p className="text-caption text-muted">No source entries reported.</p>}
    {attribution.truncated && <p className="text-caption text-muted">Some source entries are not shown. The reported total remains unchanged.</p>}
  </>;
}

/** Read once per opening of the tab (and again after Compact now), never polled. */
function Heaviest({ session, version }: Readonly<{ session: SessionDetail; version: number }>) {
  const api = useApi();
  const [data, setData] = useState<ContextHeaviest | null>();
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    api.contextHeaviest(session.id, controller.signal).then(
      (next) => { if (!controller.signal.aborted) { setData(next); setError(''); } },
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err)); },
    );
    return () => controller.abort();
  }, [api, session.id, version]);
  let status = '';
  if (error) status = error;
  else if (data === undefined) status = 'Loading the largest messages…';
  else if (!data?.messages.length) status = 'No messages reported.';
  if (status) return <p role="status" className="text-caption text-muted">{status}</p>;
  return <ul className="space-y-2">{data!.messages.map((message) => <li key={message.id} className="flex items-start justify-between gap-4">
    <span className="min-w-0 break-words text-ui">{message.label || message.role || 'Message'}</span>
    <span className="shrink-0 text-caption tabular-nums">{tokens(message.tokens)}</span>
  </li>)}</ul>;
}

/** Runs the Thread's own `/compact` command (the CLI's command calls the same history.compact RPC). */
function CompactNow({ session, total, onDone }: Readonly<{ session: SessionDetail; total: number; onDone: () => void }>) {
  const api = useApi();
  const [running, setRunning] = useState(false);
  const [note, setNote] = useState('');
  const turn = LIVE.includes(session.state) || !!session.compacting;
  async function run() {
    setRunning(true);
    setNote('');
    try {
      const sub = await api.command(session.id, 'compact', '', newRequestId());
      if (sub.status === 'accepted') {
        const after = await api.contextBreakdown(session.id).catch(() => undefined);
        const now = (after?.info ?? after?.attribution)?.total_tokens;
        setNote(now === undefined ? 'Compacted.' : `Compacted: ${number.format(total)} → ${tokens(now)} in context.`);
        onDone();
      } else setNote(sub.error || 'The compaction outcome is unknown.');
    } catch (err: unknown) {
      setNote(describeError(err));
    } finally {
      setRunning(false);
    }
  }
  return <div className="mt-2 space-y-1">
    <Button size="sm" variant="subtle" disabled={turn || running} onClick={() => void run()} aria-describedby="compact-now-help">{running ? 'Compacting…' : 'Compact now'}</Button>
    <p id="compact-now-help" className="text-caption text-muted">Between turns only. Summarizes older messages, which uses model tokens.</p>
    {note && <p role="status" className="text-caption text-muted">{note}</p>}
  </div>;
}

/**
 * Mounted only while the reader is open. Closing, navigating or changing selection aborts and drops the data.
 * It reads again when the Task's pushed context report or state changes, so the counts stay current while open.
 */
function Breakdown({ session, children }: Readonly<{ session: SessionDetail; children: ReactNode }>) {
  const api = useApi();
  const [view, setView] = useState<ContextBreakdown | null>(null);
  const [error, setError] = useState('');
  const [sources, setSources] = useState(false);
  const [compacted, setCompacted] = useState(0);
  const live = [session.context?.used, session.context?.limit, session.state, session.compacting].join('\u0000');
  useEffect(() => {
    if (!session.open) return;
    const controller = new AbortController();
    api.contextBreakdown(session.id, sources, controller.signal).then(
      (data) => { if (!controller.signal.aborted) { setView(data); setError(''); } },
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err)); },
    );
    return () => controller.abort();
  }, [api, session.id, session.open, sources, live]);
  const info = view ? view.attribution ?? view.info : undefined;
  let status = '';
  // A draft (NewTaskPane) has no id yet: it is not closed, it has not started.
  if (!session.id) status = 'Context breakdown is reported once the Thread starts.';
  else if (!session.open) status = 'Context breakdown is unavailable for a closed Thread.';
  else if (error) status = error;
  else if (!view) status = 'Loading context breakdown…';
  else if (!info) status = 'Context breakdown has not been initialized by this conversation.';
  const shown = !status && info;
  const attribution = view?.attribution;
  const occupied: readonly (readonly [string, number])[] = attribution ? [
    ['System prompt', attribution.categories.system_prompt], ['Custom instructions', attribution.categories.custom_instructions], ['System tools', attribution.categories.system_tools], ['MCP tools', attribution.categories.mcp_tools], ['Messages', attribution.categories.messages],
  ] : view?.info ? [
    ['System and instructions', view.info.system_tokens], ['Messages', view.info.conversation_tokens], ['Tool definitions', view.info.tool_definition_tokens], ['Including MCP tools', view.info.mcp_tools_tokens],
  ] : [];
  return <>
    <PanelSection label="Context">
      <div className="space-y-1 tabular-nums">
        <p className="text-ui text-ink">{shown ? (shown.limit > 0 ? `${number.format(shown.total_tokens)} of ${tokens(shown.limit)}` : `${tokens(shown.total_tokens)}; capacity not reported`) : contextLabel(session)}</p>
        {status && <p role="status" className="text-caption text-muted">{status}</p>}
        {shown && shown.model && <p className="text-caption text-muted">Counted for {shown.model}{attribution?.model_source === 'autoResolved' ? ' (Auto resolved)' : ''}</p>}
        {children}
      </div>
      {shown && <div className="mt-2"><Counts rows={occupied} /></div>}
      {shown && <CompactNow session={session} total={shown.total_tokens} onDone={() => setCompacted((n) => n + 1)} />}
    </PanelSection>
    {shown && <>
      <PanelSection label="Capacity">
        <Counts rows={[
          ['Effective prompt budget', shown.prompt_token_limit], ['Compaction starts at', shown.compaction_threshold], ['Output and compaction buffer', shown.buffer_tokens],
          ...(attribution ? [['Free space', attribution.categories.free_space] as const] : []),
        ]} />
        {attribution && <p className="mt-2 text-caption text-muted">{number.format(attribution.compactions)} compactions reported.</p>}
      </PanelSection>
      <PanelSection label="Sources">
        {attribution ? <Sources attribution={attribution} /> : sources ? <p role="status" className="text-caption text-muted">Loading context sources…</p> : <Button size="sm" variant="subtle" onClick={() => setSources(true)}>Show sources</Button>}
      </PanelSection>
      <PanelSection label="Largest messages"><Heaviest session={session} version={compacted} /></PanelSection>
    </>}
  </>;
}

/**
 * The Tools panel's Context tab: one Context section with the headline, the latest report's lines
 * (`children`) and, where the provider reads one, the native breakdown. Mounted only while shown.
 */
export function ContextTab({ session, children }: Readonly<{ session: SessionDetail; children: ReactNode }>) {
  if (session.capabilities.context_breakdown) return <Breakdown session={session}>{children}</Breakdown>;
  return <PanelSection label="Context"><div className="space-y-1 tabular-nums"><p className="text-ui text-ink">{contextLabel(session)}</p>{children}</div></PanelSection>;
}
