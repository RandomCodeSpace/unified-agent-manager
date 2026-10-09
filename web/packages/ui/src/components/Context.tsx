import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type ContextAttribution, type ContextBreakdown, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Tip } from './ui/tooltip';

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

/** Mounted only while the reader is open. Closing, navigating or changing selection aborts and drops the data. */
function Breakdown({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const [view, setView] = useState<ContextBreakdown | null>(null);
  const [error, setError] = useState('');
  const [sources, setSources] = useState(false);
  const reading = useRef<AbortController | null>(null);
  useEffect(() => {
    if (!session.open) return;
    const controller = new AbortController();
    reading.current = controller;
    api.contextBreakdown(session.id, false, controller.signal).then(
      (data) => { if (!controller.signal.aborted) setView(data); },
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err)); },
    );
    return () => { controller.abort(); reading.current?.abort(); };
  }, [api, session.id, session.open]);
  const showSources = () => {
    setSources(true);
    reading.current?.abort();
    const controller = new AbortController();
    reading.current = controller;
    api.contextBreakdown(session.id, true, controller.signal).then(
      (data) => { if (!controller.signal.aborted) setView(data); },
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err)); },
    );
  };
  if (!session.open) return <p className="text-caption text-muted">Context breakdown is unavailable for a closed Task.</p>;
  if (error) return <p role="status" className="text-caption text-muted">{error}</p>;
  if (!view) return <p role="status" className="text-caption text-muted">Loading context breakdown…</p>;
  const info = view.attribution ?? view.info;
  if (!info) return <p role="status" className="text-caption text-muted">Context breakdown has not been initialized by this conversation.</p>;
  const attribution = view.attribution;
  const occupied: readonly (readonly [string, number])[] = attribution ? [
    ['System prompt', attribution.categories.system_prompt], ['Custom instructions', attribution.categories.custom_instructions], ['System tools', attribution.categories.system_tools], ['MCP tools', attribution.categories.mcp_tools], ['Messages', attribution.categories.messages],
  ] : view.info ? [
    ['System and instructions', view.info.system_tokens], ['Messages', view.info.conversation_tokens], ['Tool definitions', view.info.tool_definition_tokens], ['Including MCP tools', view.info.mcp_tools_tokens],
  ] : [];
  return <>
    <PanelSection label="Current context">
      <p className="mb-2 text-ui text-ink">{tokens(info.total_tokens)} occupied{info.limit > 0 ? ` of ${tokens(info.limit)} advertised prompt capacity` : '; prompt capacity not reported'}</p>
      {info.model && <p className="mb-2 text-caption text-muted">Counted for {info.model}{attribution?.model_source === 'autoResolved' ? ' (Auto resolved)' : ''}. Token counts are model-specific.{attribution?.model_source === 'autoResolved' && ' Mixed-model Auto sessions use this model as an approximation.'}</p>}
      <Counts rows={occupied} />
      {!attribution && <p className="mt-2 text-caption text-muted">MCP tools are included in tool definitions.</p>}
    </PanelSection>
    <PanelSection label="Capacity">
      <Counts rows={[
        ['Effective prompt budget', info.prompt_token_limit], ['Compaction starts at', info.compaction_threshold], ['Output and compaction buffer', info.buffer_tokens],
        ...(attribution ? [['Free space', attribution.categories.free_space] as const] : []),
      ]} />
      <p className="mt-2 text-caption text-muted">Free space and reserved buffer are capacity, not occupied tokens. Output and compaction reservations can overlap.</p>
      {attribution && <p className="mt-1 text-caption text-muted">{number.format(attribution.compactions)} compactions reported.</p>}
    </PanelSection>
    <PanelSection label="Sources">
      {attribution ? <Sources attribution={attribution} /> : sources ? <p role="status" className="text-caption text-muted">Loading context sources…</p> : <Button size="sm" variant="subtle" onClick={showSources}>Show sources</Button>}
    </PanelSection>
  </>;
}

export function ContextValue({ session, label, face, children, className }: Readonly<{ session: SessionDetail; label: string; face: ReactNode; children: ReactNode; className?: string }>) {
  const api = useApi();
  const [owner, setOwner] = useState(api);
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  if (owner !== api) {
    setOwner(api);
    setReader(null);
  }
  const close = () => setReader(null);
  const body = (closeButton: ReactNode) => <div className="flex min-h-0 flex-col">
    <PanelHead className="pr-2 pl-4"><div className="flex min-h-7 items-center gap-2"><h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 text-title text-ink outline-hidden">Context usage</h2>{closeButton}</div></PanelHead>
    <div className="flex min-h-0 flex-col gap-4 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4">
      <PanelSection label="Latest report"><div className="space-y-1 tabular-nums">{children}</div></PanelSection>
      {reader?.open && <Breakdown session={session} />}
    </div>
    <PanelFoot><span className="text-caption text-muted">Read when opened; reopen for current counts.</span></PanelFoot>
  </div>;
  return <>
    <Tip label={label}><Button ref={setAnchor} id="composer-context-usage" size="sm" variant="subtle" aria-label={label} aria-haspopup="dialog" aria-expanded={!!reader?.open} aria-controls={reader?.open ? id : undefined} className={cn('px-1.5 text-caption tabular-nums pointer-coarse:min-w-11', className)} onClick={() => setReader({ open: true, phone: window.matchMedia(PHONE).matches })}>{face}</Button></Tip>
    {reader && (reader.phone || !anchor ? <BottomSheet id={id} open={reader.open} onClose={close} onClosed={close} label="Context usage" initialFocus={heading} finalFocus={() => anchor}>
      {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
    </BottomSheet> : <BasePopover.Root open={reader.open} modal onOpenChange={(open) => !open && close()}>
      <BasePopover.Portal><BasePopover.Backdrop className={backdropClass} />{anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side="top" align="end" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup id={id} data-popup="" aria-label="Context usage" aria-modal="true" initialFocus={heading} finalFocus={() => anchor} className="flex max-h-[min(80vh,var(--available-height))] w-[440px] max-w-(--available-width) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden">
            {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>)}
  </>;
}
