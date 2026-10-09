import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { diffLines } from 'diff';
import { FileText, X } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type Item, type PlanAction, type PlanReview, type PlanDraft } from '../api';
import { cn } from '../lib/cn';
import { Markdown, Note } from './common';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';

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
    <button ref={setAnchor} type="button" aria-haspopup="dialog" aria-expanded={!!reader?.open} aria-controls={reader?.open ? id : undefined} onClick={show} className="inline-flex h-7 shrink-0 items-center gap-1 rounded-sm px-1.5 text-caption text-muted hover:bg-tint-hover hover:text-ink pointer-coarse:min-h-11">
      <FileText aria-hidden="true" className="size-3.5" />Read plan
    </button>
    {reader && <PlanReader id={id} open={reader.open} phone={reader.phone} anchor={anchor} plan={snapshot} error={error} current={current} draft={draft?.version === planVersion ? draft.value ?? null : null} draftError={!draftAvailable ? 'The current draft is unavailable while this Task is closed.' : draft?.version === planVersion ? draft.error ?? '' : ''} onCurrent={sessionId ? () => { setCurrent((value) => !value); setDraft(null); } : undefined} onClose={close} onClosed={() => { setReader(null); setSnapshot(null); setDraft(null); }} />}
  </>;
}

function PlanReader({ id, open, phone, anchor, plan, error, current, draft, draftError, onCurrent, onClose, onClosed }: Readonly<{ id: string; open: boolean; phone: boolean; anchor: HTMLElement | null; plan: PlanReview | null; error: string; current: boolean; draft: PlanDraft | null; draftError: string; onCurrent?: () => void; onClose: () => void; onClosed: () => void }>) {
  const heading = useRef<HTMLHeadingElement>(null);
  const back = () => anchor;
  const body = (close: ReactNode) => <PlanBody plan={plan} error={error} current={current} draft={draft} draftError={draftError} onCurrent={onCurrent} heading={heading} close={close} />;
  if (phone || !anchor) return <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label="Plan" className="h-[85dvh] duration-160" backdropClassName="duration-160" initialFocus={heading} finalFocus={back}>
    {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
  </BottomSheet>;
  return <BasePopover.Root open={open} modal onOpenChange={(value) => !value && onClose()} onOpenChangeComplete={(value) => !value && onClosed()}>
    <BasePopover.Portal>
      <BasePopover.Backdrop className={cn(backdropClass, 'duration-160')} />
      {open && anchor.isConnected && <LiftedRow row={anchor} />}
      <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
        <BasePopover.Popup id={id} aria-label="Plan" aria-modal="true" initialFocus={heading} finalFocus={back} className="flex max-h-[min(80vh,var(--available-height))] w-[600px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0">
          {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
        </BasePopover.Popup>
      </BasePopover.Positioner>
    </BasePopover.Portal>
  </BasePopover.Root>;
}

function PlanBody({ plan, error, current, draft, draftError, onCurrent, heading, close }: Readonly<{ plan: PlanReview | null; error: string; current: boolean; draft: PlanDraft | null; draftError: string; onCurrent?: () => void; heading: RefObject<HTMLHeadingElement | null>; close: ReactNode }>) {
  const [changes, setChanges] = useState(false);
  const diff = useMemo(() => changes && plan?.previous !== undefined ? diffLines(plan.previous, plan.content ?? '', { timeout: 100 }) : undefined, [changes, plan]);
  return <>
    <div className="flex shrink-0 items-center gap-2 border-b border-rule px-4 py-3">
      <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 text-title outline-hidden">Plan{!current && plan?.revision ? <span className="ml-2 text-caption text-muted">Revision {plan.revision}</span> : null}</h2>
      {onCurrent && <button type="button" aria-pressed={current} className="rounded-sm px-2 py-1 text-caption text-muted hover:bg-tint-hover" onClick={onCurrent}>{current ? 'Reviewed snapshot' : 'Current draft'}</button>}
      {!current && plan?.previous && <button type="button" aria-pressed={changes} className="rounded-sm px-2 py-1 text-caption text-muted hover:bg-tint-hover" onClick={() => setChanges((value) => !value)}>{changes ? 'Read plan' : 'Show changes'}</button>}
      {close}
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
      {current ? draftError ? <Note tone="warn" role="alert">{draftError}</Note> : !draft ? <Note>Loading the current draft…</Note> : !draft.exists ? <Note>The current draft is missing or was deleted. The reviewed snapshot is still available.</Note> : <>
        {draft.truncated && <Note tone="warn">The current draft is shortened.</Note>}
        {draft.content ? <Markdown text={draft.content} /> : <Note>The current draft is empty.</Note>}
      </> : error ? <Note tone="warn" role="alert">{error}</Note> : !plan ? <Note>Loading the reviewed plan…</Note> : <>
        {plan.truncated && <Note tone="warn">This recorded plan is shortened. Do not approve a shortened snapshot.</Note>}
        {plan.previous_unavailable ? <Note tone="warn">The previous reviewed revision is unavailable.</Note> : plan.previous_truncated && <Note tone="warn">The previous revision is shortened. These changes are incomplete.</Note>}
        {changes ? diff ? <pre className="whitespace-pre-wrap break-words font-mono text-code-sm">{diff.map((part, index) => <span key={index} className={cn('block', part.added && 'bg-success/10 text-success', part.removed && 'bg-error/10 text-muted line-through')}>{part.value.split('\n').filter((line, i, all) => i < all.length - 1 || line).map((line) => `${part.added ? '+' : part.removed ? '−' : ' '} ${line}`).join('\n')}</span>)}</pre> : <Note>The revision is too large to compare here.</Note> : <Markdown text={plan.content ?? ''} />}
      </>}
    </div>
    <div className="shrink-0 border-t border-rule px-4 py-2 text-caption text-faint">{current ? 'Current draft · This does not replace the reviewed snapshot' : 'Reviewed snapshot'} · Escape to close</div>
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
