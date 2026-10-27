import { diffLines } from 'diff';
import { FileText } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type Item, type PlanAction, type PlanReview, type PlanDraft } from '../api';
import { cn } from '../lib/cn';
import { Markdown, Note } from './common';
import { Key } from './InlinePicker';
import { Button } from './ui/button';
import { PHONE } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { AnchoredReader } from './ui/reader';

export const PLAN_ACTION_LABEL: Record<PlanAction, string> = {
  autopilot: 'Implement in autopilot',
  autopilot_fleet: 'Implement with subagents',
  interactive: 'Implement interactively',
  exit_only: 'Leave planning',
};

/** Bodies live only while this reader is open; historical reads use the exact native review. */
export function ReadPlan({ plan, sessionId, planVersion = 0, draftAvailable = true }: Readonly<{ plan: PlanReview; sessionId?: string; planVersion?: number; draftAvailable?: boolean }>) {
  const api = useApi();
  const id = useId();
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  const [snapshot, setSnapshot] = useState<PlanReview | null>(null);
  const [error, setError] = useState('');
  const [current, setCurrent] = useState(false);
  const [draft, setDraft] = useState<{ version: number; value?: PlanDraft; error?: string } | null>(null);
  const reading = useRef<AbortController | null>(null);
  useEffect(() => () => reading.current?.abort(), []);
  useEffect(() => {
    if (!reader?.open || !current || !sessionId || !draftAvailable) return;
    const controller = new AbortController();
    api.planDraft(sessionId, controller.signal).then((value) => {
      if (!controller.signal.aborted) setDraft({ version: planVersion, value });
    }, (err: unknown) => {
      if (!controller.signal.aborted) setDraft({ version: planVersion, error: describeError(err) });
    });
    return () => controller.abort();
  }, [api, current, sessionId, reader?.open, planVersion, draftAvailable]);
  function show() {
    setReader({ open: true, phone: window.matchMedia(PHONE).matches });
    setError('');
    setCurrent(false);
    setDraft(null);
    reading.current?.abort();
    if (plan.content !== undefined || plan.actions?.length) {
      setSnapshot(plan);
      return;
    }
    setSnapshot(null);
    if (!sessionId) {
      setError('This reviewed plan is unavailable.');
      return;
    }
    const controller = new AbortController();
    reading.current = controller;
    api.planReview(sessionId, plan.request_id, controller.signal).then((value) => {
      if (!controller.signal.aborted) setSnapshot(value);
    }, (err: unknown) => {
      if (!controller.signal.aborted) setError(describeError(err));
    });
  }
  function close() {
    reading.current?.abort();
    setReader((r) => r && { ...r, open: false });
  }
  return <>
    <Button ref={setAnchor} size="sm" variant="subtle" aria-haspopup="dialog" aria-expanded={!!reader?.open} aria-controls={reader?.open ? id : undefined} onClick={show} className="gap-1 px-1.5 text-caption text-muted [&_svg]:size-3.5">
      <FileText aria-hidden="true" />Read plan
    </Button>
    {reader && <PlanReader id={id} open={reader.open} phone={reader.phone} anchor={anchor} plan={snapshot} error={error} current={current} draft={draft?.version === planVersion ? draft.value ?? null : null} draftError={!draftAvailable ? 'The current draft is unavailable while this Thread is closed.' : draft?.version === planVersion ? draft.error ?? '' : ''} onCurrent={sessionId ? () => { setCurrent((value) => !value); setDraft(null); } : undefined} onClose={close} onClosed={() => { setReader(null); setSnapshot(null); setDraft(null); }} />}
  </>;
}

function PlanReader({ id, open, phone, anchor, plan, error, current, draft, draftError, onCurrent, onClose, onClosed }: Readonly<{ id: string; open: boolean; phone: boolean; anchor: HTMLElement | null; plan: PlanReview | null; error: string; current: boolean; draft: PlanDraft | null; draftError: string; onCurrent?: () => void; onClose: () => void; onClosed: () => void }>) {
  const heading = useRef<HTMLHeadingElement>(null);
  const sheet = phone || !anchor;
  return <AnchoredReader id={id} open={open} sheet={sheet} anchor={anchor} side="top" label="Plan" className="w-[600px]" sheetClassName="h-[85dvh]" initialFocus={heading} finalFocus={() => anchor} onClose={onClose} onClosed={onClosed}>
    {(close) => <PlanBody plan={plan} error={error} current={current} draft={draft} draftError={draftError} onCurrent={onCurrent} heading={heading} close={close} sheet={sheet} />}
  </AnchoredReader>;
}

function PlanBody({ plan, error, current, draft, draftError, onCurrent, heading, close, sheet }: Readonly<{ plan: PlanReview | null; error: string; current: boolean; draft: PlanDraft | null; draftError: string; onCurrent?: () => void; heading: RefObject<HTMLHeadingElement | null>; close: ReactNode; sheet: boolean }>) {
  const [changes, setChanges] = useState(false);
  const diff = useMemo(() => changes && plan?.previous !== undefined ? diffLines(plan.previous, plan.content ?? '', { timeout: 100 }) : undefined, [changes, plan]);
  const foot = current ? 'Current draft · This does not replace the reviewed snapshot' : 'Reviewed snapshot';
  return <>
    <PanelHead className={cn('gap-1 pt-2 pb-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
      <div className="flex min-h-7 min-w-0 items-center gap-2">
        <FileText aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-title text-ink outline-hidden">Plan{!current && plan?.revision ? <span className="ml-2 text-caption text-muted">Revision {plan.revision}</span> : null}</h2>
        {/* Toggles with fixed names: pressed shows the draft or the changes, released goes back to the reviewed plan. */}
        {onCurrent && <Button size="sm" aria-pressed={current} className="text-caption text-muted" onClick={onCurrent}>Current draft</Button>}
        {!current && plan?.previous && <Button size="sm" aria-pressed={changes} className="text-caption text-muted" onClick={() => setChanges((value) => !value)}>Show changes</Button>}
        {close}
      </div>
    </PanelHead>
    <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-3">
      <PanelSection label={current ? 'Draft' : changes ? 'Changes' : 'Reviewed plan'}>
        {current ? draftError ? <Note tone="warn" role="alert">{draftError}</Note> : !draft ? <Note>Loading the current draft…</Note> : !draft.exists ? <Note>The current draft is missing or was deleted. The reviewed snapshot is still available.</Note> : <>
          {draft.truncated && <Note tone="warn">The current draft is shortened.</Note>}
          {draft.content ? <Markdown text={draft.content} /> : <Note>The current draft is empty.</Note>}
        </> : error ? <Note tone="warn" role="alert">{error}</Note> : !plan ? <Note>Loading the reviewed plan…</Note> : <>
          {plan.truncated && <Note tone="warn">This recorded plan is shortened. Do not approve a shortened snapshot.</Note>}
          {plan.previous_unavailable ? <Note tone="warn">The previous reviewed revision is unavailable.</Note> : plan.previous_truncated && <Note tone="warn">The previous revision is shortened. These changes are incomplete.</Note>}
          {changes ? diff ? <pre className="whitespace-pre-wrap break-words font-mono text-code-sm">{diff.map((part, index) => <span key={index} className={cn('block', part.added && 'bg-success/10 text-success', part.removed && 'bg-error/10 text-muted line-through')}>{part.value.split('\n').filter((line, i, all) => i < all.length - 1 || line).map((line) => `${part.added ? '+' : part.removed ? '−' : ' '} ${line}`).join('\n')}</span>)}</pre> : <Note>The revision is too large to compare here.</Note> : <Markdown text={plan.content ?? ''} />}
        </>}
      </PanelSection>
    </div>
    {sheet ? <p className="flex min-h-11 shrink-0 items-center px-4 pb-2 text-caption text-muted">{foot}</p> : <PanelFoot>
      <span className="flex items-center gap-1.5"><Key>Esc</Key>close</span>
      <span className="flex-1" />
      <span className="min-w-0 truncate">{foot}</span>
    </PanelFoot>}
  </>;
}

/** Native decisions are literal two-line receipts; Markdown cannot collapse their boundary. */
export function PlanNotice({ item, sessionId, planVersion, draftAvailable, className }: Readonly<{ item: Item; sessionId?: string; planVersion?: number; draftAvailable?: boolean; className?: string }>) {
  return <div className={cn('flex items-start gap-2 rounded-sm px-1 py-1 text-caption text-muted', className)}>
    <FileText aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
    <div className="min-w-0 flex-1 whitespace-pre-wrap break-words">{item.text}</div>
    {item.plan && <ReadPlan plan={item.plan} sessionId={sessionId} planVersion={planVersion} draftAvailable={draftAvailable} />}
  </div>;
}
