import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { MessageCircleQuestion, X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, readOnly, type AsideAnswer, type SessionDetail } from '../api';
import { Markdown } from './common';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Tip } from './ui/tooltip';

type Asked = { question: string; answer?: AsideAnswer; error?: string };

/** The question box and its one answer. They live only while the reader is open: closing aborts the wait and drops both. */
function Aside({ session, field }: Readonly<{ session: SessionDetail; field: RefObject<HTMLTextAreaElement | null> }>) {
  const api = useApi();
  const [draft, setDraft] = useState('');
  const [asked, setAsked] = useState<Asked | null>(null);
  const waiting = useRef<AbortController | null>(null);
  useEffect(() => () => waiting.current?.abort(), []);
  const pending = !!asked && !asked.answer && asked.error === undefined;
  if (!session.open || readOnly(session)) return <p className="text-caption text-muted">Ask aside needs the Task&apos;s conversation open. Send a prompt to open it first.</p>;
  const ask = () => {
    const question = draft.trim();
    if (!question || pending) return;
    const controller = new AbortController();
    waiting.current = controller;
    setAsked({ question });
    setDraft('');
    api.askAside(session.id, question, controller.signal).then(
      (answer) => { if (!controller.signal.aborted) setAsked({ question, answer }); },
      // Copy bounded code units so a substring cannot retain a large remote error.
      (err: unknown) => { if (!controller.signal.aborted) setAsked({ question, error: describeError(err).slice(0, 512).split('').join('') }); },
    );
  };
  return <>
    <form className="flex flex-col gap-1.5" onSubmit={(e) => { e.preventDefault(); ask(); }}>
      <textarea
        ref={field}
        rows={3}
        value={draft}
        aria-label="Aside question"
        placeholder="Ask about this Task without adding to it"
        className="w-full resize-y rounded-sm bg-sunken px-2 py-1.5 text-ui text-ink shadow-well placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none"
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); ask(); } }}
      />
      <span className="flex justify-end"><Button size="sm" variant="primary" type="submit" disabled={!draft.trim() || pending}>Ask</Button></span>
    </form>
    {asked && <PanelSection label="Answer" meta={pending ? 'Waiting for the answer…' : asked.answer?.truncated ? 'Shortened' : undefined}>
      <p className="break-words text-caption text-muted">{asked.question}</p>
      {asked.error !== undefined ? <p role="status" className="text-caption text-error">{asked.error}</p>
        : asked.answer && <><Markdown text={asked.answer.text} className="text-ui" />{asked.answer.truncated && <p className="text-caption text-muted">The answer was longer; only its start is shown.</p>}</>}
    </PanelSection>}
  </>;
}

/**
 * A transient question to the open conversation, answered by its current model and kept out of the transcript.
 * A change of Task, conversation, selection, stage or open state remounts the reader: it closes and drops a late answer.
 */
export function AskAside({ session }: Readonly<{ session: SessionDetail }>) {
  return <AsideReader key={`${session.id}:${session.provider}:${session.conversation_id}:${session.model}:${session.effort}:${session.context_size}:${session.stage}:${session.open}`} session={session} />;
}

function AsideReader({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const [owner, setOwner] = useState(api);
  const id = useId();
  const field = useRef<HTMLTextAreaElement>(null);
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  if (owner !== api) { setOwner(api); setReader(null); }
  const close = () => setReader(null);
  const body = (closeButton: ReactNode) => <div className="flex min-h-0 flex-col">
    <PanelHead className="pr-2 pl-4">
      <div className="flex min-h-7 items-center gap-2"><h2 className="min-w-0 flex-1 text-title text-ink">Ask aside</h2>{closeButton}</div>
      <p className="pb-1 text-caption text-muted">Answered from this conversation with its current model, without tools. Nothing is added to the transcript.</p>
    </PanelHead>
    <div className="flex min-h-0 flex-col gap-4 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4">
      {reader?.open && <Aside session={session} field={field} />}
    </div>
    {!reader?.phone && <PanelFoot>
      <span className="flex items-center gap-1.5"><Key>Enter</Key>ask</span>
      <span className="flex items-center gap-1.5"><Key>Esc</Key>close</span>
      <span className="flex-1" />
      <span className="text-caption">Counts toward the account&apos;s usage</span>
    </PanelFoot>}
  </div>;
  return <>
    <Tip label="Ask aside">
      <Button ref={setAnchor} id="composer-aside" size="icon" variant="subtle" aria-label="Ask aside" aria-haspopup="dialog" aria-expanded={!!reader?.open} aria-controls={reader?.open ? id : undefined} className="text-muted" onClick={() => setReader({ open: true, phone: window.matchMedia(PHONE).matches })}>
        <MessageCircleQuestion aria-hidden="true" />
      </Button>
    </Tip>
    {reader && (reader.phone || !anchor ? <BottomSheet id={id} open={reader.open} onClose={close} onClosed={close} label="Ask aside" initialFocus={field} finalFocus={() => anchor}>
      {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
    </BottomSheet> : <BasePopover.Root open={reader.open} modal onOpenChange={(open) => !open && close()}>
      <BasePopover.Portal><BasePopover.Backdrop className={backdropClass} />{anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup id={id} data-popup="" aria-label="Ask aside" aria-modal="true" initialFocus={field} finalFocus={() => anchor} className="flex max-h-[min(80vh,var(--available-height))] w-[440px] max-w-(--available-width) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden">
            {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>)}
  </>;
}
