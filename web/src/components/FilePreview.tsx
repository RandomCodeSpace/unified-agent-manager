import { Download, ExternalLink, X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { api, describeError, subscribeAuthLoss } from '../api';
import { popupOpen } from '../App';
import { formatSize } from '../lib/attachments';
import { downloadUrl, previewMetadata, PreviewOwner, type PreviewMetadata, type TextPreview } from '../lib/preview';
import type { OpenPreview, PreviewTarget } from '../lib/previewContext';
import { Lightbox } from './Attachments';
import { CodeBlock, Note, Spinner } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { Button, buttonVariants } from './ui/button';

interface Selection {
  owner: object;
  target: PreviewTarget;
  file?: PreviewMetadata & Partial<TextPreview>;
  error?: string;
}

/** The selected task owns one request and body; replacing or disposing it aborts both. */
export function useFilePreview(sessionId: string, lifetime: string, active: boolean, onOpen: () => void, fallback: RefObject<HTMLElement | null>) {
  const owner = useMemo(() => ({ sessionId, lifetime, active, requests: new PreviewOwner() }), [sessionId, lifetime, active]);
  const openerRef = useRef<HTMLElement | null>(null);
  const [selection, setSelection] = useState<Selection | null>(null);
  // A hidden/inactive task must release its last decoded body, not merely stop drawing it.
  if (selection && selection.owner !== owner) setSelection(null);
  const close = useCallback((restoreFocus = true) => {
    const opener = openerRef.current;
    openerRef.current = null;
    owner.requests.stop();
    setSelection(null);
    if (restoreFocus && opener) {
      if (opener.isConnected) opener.focus({ preventScroll: true });
      else fallback.current?.focus({ preventScroll: true });
    }
  }, [owner, fallback]);
  useLayoutEffect(() => {
    const unsubscribe = subscribeAuthLoss(() => close(false));
    return () => {
      unsubscribe();
      owner.requests.stop();
      openerRef.current = null;
    };
  }, [owner, close]);
  const open = useCallback<OpenPreview>((target, opener) => {
    if (!active) return;
    const controller = owner.requests.start();
    openerRef.current = opener;
    onOpen();
    let next: Selection = { owner, target };
    setSelection(next);
    if (target.image) return;
    const load = async () => {
      let metadata: PreviewMetadata | undefined;
      if (target.tempPath !== undefined) {
        const grant = await api.createFileGrant(sessionId, target.tempPath, controller.signal);
        if (!owner.requests.retain(controller, () => api.revokeFileGrant(sessionId, grant.id))) return;
        next = { owner, target: { url: grant.url + (target.hash ?? ''), name: grant.name, description: target.tempPath, frameable: true, temporary: true } };
        setSelection(next);
        metadata = previewMetadata(new Headers({ 'Content-Type': grant.mime, 'Content-Length': String(grant.size) }));
      }
      if (!next.target.url) return;
      const file = await api.filePreview(next.target.url, controller.signal, metadata);
      if (owner.requests.owns(controller)) setSelection({ ...next, file });
    };
    void load().catch(error => {
      if (owner.requests.owns(controller)) setSelection({ ...next, error: describeError(error) });
    });
  }, [active, owner, onOpen, sessionId]);
  return { open, close, selection: active && selection?.owner === owner ? selection : null };
}

export function FilePreview({ selection, inline, onClose }: { selection: Selection; inline: boolean; onClose: () => void }) {
  const closeButton = useRef<HTMLButtonElement>(null);
  const { target, file, error } = selection;
  useEffect(() => { closeButton.current?.focus({ preventScroll: true }); }, [selection.target]);
  useEffect(() => {
    if (!inline || target.image || file?.kind === 'image') return;
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented && !popupOpen()) { event.preventDefault(); onClose(); }
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, target.image, file?.kind, onClose]);
  const image = target.image || file?.kind === 'image';
  const temporary = target.temporary || target.tempPath !== undefined;
  const limitation = "This file's contents can change. Temporary-file access expires after five minutes or when this preview closes, including in a new tab. Sibling assets are unavailable.";
  const actions = target.url && target.original !== false && <>
    <a href={target.url} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'secondary', size: 'sm' })}><ExternalLink />{image ? 'Open original' : 'Open in new tab'}</a>
    <a href={downloadUrl(target.url)} download={target.name} className={buttonVariants({ variant: 'secondary', size: 'sm' })}><Download />Download</a>
  </>;
  if (image && target.url) {
    return <Lightbox open onOpenChange={open => !open && onClose()} title={target.name} description={target.description} src={target.url} alt={target.name} footer={<div className="flex max-w-prose flex-col gap-2">
      {temporary && <p className="text-caption text-on-primary/70">{limitation}</p>}
      {actions && <div className="flex flex-wrap justify-end gap-2">{actions}</div>}
    </div>} />;
  }
  return (
    <SidePanel id="file-preview" inline={inline} open onClose={onClose} onClosed={() => {}} label={`Preview ${target.name}`}>
      <PanelHeader>
        <h2 className="min-w-0 flex-1 truncate text-title text-ink" title={target.name}>{target.name}</h2>
        <Button ref={closeButton} size="icon" className="text-muted" aria-label="Close preview" onClick={onClose}><X /></Button>
      </PanelHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">
        <p className="break-all text-caption text-muted">{target.description ?? target.name}{file?.size !== undefined ? ` · ${formatSize(file.size)}` : ''}</p>
        {temporary && <Note>{limitation}</Note>}
        {error ? <Note tone="error">{error}</Note> : !file ? <p role="status" className="flex items-center gap-2 text-caption text-muted"><Spinner />Opening file…</p> : file.kind === 'text' ? <>
          {file.truncated && <Note>Showing a text preview of at most 64 KiB. Open or download the full file to read the rest.</Note>}
          {file.text ? <CodeBlock language="text" text={file.text}><code>{file.text}</code></CodeBlock> : <Note>This file is empty.</Note>}
        </> : file.kind === 'html' && target.frameable ? <iframe
          key={target.url}
          src={target.url}
          title={`Preview ${target.name}`}
          sandbox="allow-scripts allow-forms allow-popups allow-modals allow-downloads"
          referrerPolicy="no-referrer"
          className="min-h-64 w-full flex-1 rounded-sm bg-raised"
        /> : <Note>Open this file in a new tab or download it.</Note>}
      </div>
      <div className="flex shrink-0 flex-wrap gap-2 p-3">{actions}</div>
    </SidePanel>
  );
}
