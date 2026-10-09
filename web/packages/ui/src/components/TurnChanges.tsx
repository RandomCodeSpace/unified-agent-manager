import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { X } from 'lucide-react';
import { useContext, useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type TurnChanges, type TurnTiming } from '../api';
import { SessionContext } from './common';
import { BottomSheet, LiftedRow } from './Subagents';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead } from './ui/panel';

const REASON = {
  unknown: 'The provider could not prove this turn’s captured changes before another activity began.',
  busy: 'Native captures were still busy when this turn ended. No historical counts were guessed.',
  unsupported: 'Native file captures were unavailable for this turn.',
};

/** Historical captured metadata only. Current patches belong to All changes;
 * exact individual edit patches belong to their tool rows. This reader never
 * polls or resumes a closed conversation and retains only its bounded rows. */
export function TurnChangesReader({ timing, anchor, phone, open, onClose, onClosed, onAllChanges }: Readonly<{
  timing: TurnTiming;
  anchor: HTMLElement | null;
  phone: boolean;
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
  onAllChanges?: () => void;
}>) {
  const api = useApi();
  const sessionId = useContext(SessionContext);
  const heading = useRef<HTMLHeadingElement>(null);
  const id = useId();
  const [read, setRead] = useState<TurnChanges | { error: string } | null>(null);
  useEffect(() => {
    if (!open || !sessionId || timing.changes?.status !== 'available') return;
    const controller = new AbortController();
    api.turnChanges(sessionId, timing.id, controller.signal).then(rows => {
      if (controller.signal.aborted) return;
      if (rows.timing_id !== timing.id || rows.counts.event_id !== timing.changes?.event_id || rows.ended_at !== timing.ended_at) {
        setRead({ error: 'The Task changed. Close this reader and open it again.' });
      } else setRead(rows);
    }, (error: unknown) => {
      if (!controller.signal.aborted) setRead({ error: Array.from(describeError(error).slice(0, 512)).join('') });
    });
    return () => controller.abort();
  }, [api, open, sessionId, timing.id, timing.ended_at, timing.changes?.event_id, timing.changes?.status]);
  const counts = timing.changes;
  const rows = read && 'files' in read ? read.files : null;
  const body = (close: ReactNode) => <>
    <PanelHead className="px-4 pt-3 pb-2">
      <div className="flex items-center gap-2">
        <h2 ref={heading} tabIndex={-1} className="flex-1 text-title text-ink outline-hidden">This turn’s changes</h2>
        {close}
      </div>
      <p className="text-caption text-muted">As this turn left it. Native captured changes kept when the turn ended.</p>
      {counts?.status === 'available' && <p className="mt-1 text-ui tabular-nums text-body">{counts.files ?? 0} {(counts.files ?? 0) === 1 ? 'file' : 'files'} <span className="text-success">+{counts.additions ?? 0}</span> <span className="text-error">−{counts.deletions ?? 0}</span></p>}
    </PanelHead>
    <div className="min-h-0 overflow-y-auto overscroll-contain px-4 py-2">
      {counts?.status !== 'available' ? <p className="text-ui text-muted">{REASON[counts?.status ?? 'unknown']}</p> : read && 'error' in read ? <p role="alert" className="text-ui text-error">{read.error}</p> : !rows ? <p role="status" className="text-ui text-muted">Loading captured files…</p> : rows.length === 0 ? <p className="text-ui text-muted">No captured file changes.</p> : <ul className="flex flex-col gap-2">{rows.map(file => <li key={file.path} className="flex flex-wrap items-baseline gap-x-2 text-ui">
        <span className="min-w-0 flex-1 break-all font-mono text-body">{file.path}</span>
        <span className="text-caption text-muted">{file.kind}</span>
        <span className="whitespace-nowrap text-caption tabular-nums"><span className="text-success">+{file.additions ?? 0}</span> <span className="text-error">−{file.deletions ?? 0}</span></span>
      </li>)}</ul>}
      {!!counts?.omitted && <p className="mt-2 text-caption text-muted">{counts.omitted} more captured files not shown.</p>}
      <p className="mt-3 text-caption text-muted">These are historical facts. The files may have changed since. Individual edit patches are available from their tool rows when recorded.</p>
    </div>
    <PanelFoot>
      <span>Read only</span><span className="flex-1" />
      {onAllChanges && <button type="button" className="text-accent hover:underline" onClick={() => { onClose(); onAllChanges(); }}>All changes · current files</button>}
    </PanelFoot>
  </>;
  const back = () => anchor;
  if (phone || !anchor) return <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label="This turn's changes" initialFocus={heading} finalFocus={back} className="duration-160" backdropClassName="duration-160">
    {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
  </BottomSheet>;
  return <BasePopover.Root open={open} modal onOpenChange={o => !o && onClose()} onOpenChangeComplete={o => !o && onClosed()}>
    <BasePopover.Portal>
      <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
      {open && anchor.isConnected && <LiftedRow row={anchor} />}
      <BasePopover.Positioner anchor={anchor} side="bottom" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
        <BasePopover.Popup id={id} data-popup="" aria-label="This turn's changes" aria-modal="true" initialFocus={heading} finalFocus={back} className="flex max-h-[min(80vh,var(--available-height))] w-[520px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0">
          {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
        </BasePopover.Popup>
      </BasePopover.Positioner>
    </BasePopover.Portal>
  </BasePopover.Root>;
}
