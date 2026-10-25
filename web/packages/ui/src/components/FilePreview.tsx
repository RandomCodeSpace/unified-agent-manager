import { useApi } from '../ApiContext';
import { ArrowLeft, ChevronDown, ChevronUp, Download, ExternalLink, MessageSquarePlus, RotateCw, Undo2, X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import { describeError, subscribeAuthLoss, type Changes } from '../api';
import { popupOpen, threadCovered } from '../App';
import { formatSize } from '../lib/attachments';
import { downloadUrl, previewMetadata, PreviewOwner, type PreviewMetadata, type TextPreview } from '../lib/preview';
import type { OpenPreview, PreviewTarget } from '../lib/previewContext';
import { Lightbox } from './Attachments';
import { FileMedia, Note, Spinner } from './common';
import { CodeView } from './CodeView';
import { FileIcon } from './FileIcon';
import { lensChanges, runText, type Hunk, type LensChange } from '../lib/lens';
import { turnFile } from '../lib/review';
import { useFileVersion } from './FileReferences';
import { isMarkdown, MarkdownFile } from './MarkdownFile';
import { PanelHeader, SidePanel } from './Subagents';
import { Button, buttonVariants } from './ui/button';

interface Selection {
  owner: object;
  target: PreviewTarget;
  file?: PreviewMetadata & Partial<TextPreview>;
  error?: string;
  /** Counts reloads, so a framed page loads again too. */
  revision?: number;
  reloading?: boolean;
}

/** The selected task owns one request and body; replacing or disposing it aborts both. */
export function useFilePreview(sessionId: string, lifetime: string, active: boolean, onOpen: () => void, fallback: RefObject<HTMLElement | null>) {
  const api = useApi();
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
  }, [active, owner, onOpen, api, sessionId]);
  // Reads the file again in place: what is shown stays until the new read answers.
  const reload = useCallback(() => {
    const current = selection;
    if (!current || current.owner !== owner || !current.target.url || current.target.image || current.target.temporary || current.target.tempPath !== undefined) return;
    const controller = owner.requests.start();
    const revision = (current.revision ?? 0) + 1;
    setSelection({ ...current, reloading: true });
    api.filePreview(current.target.url, controller.signal).then(
      file => { if (owner.requests.owns(controller)) setSelection({ ...current, file, error: undefined, revision, reloading: false }); },
      error => { if (owner.requests.owns(controller)) setSelection({ ...current, error: describeError(error), revision, reloading: false }); },
    );
  }, [selection, owner, api]);
  return { open, close, reload, selection: active && selection?.owner === owner ? selection : null };
}

/**
 * Lens: when the open file is one this Thread changed, its changes as marks on the file. The diff is read again when
 * the file's digest moves; `diff` loads with the first one, so the file viewer itself stays light.
 */
function useLens(sessionId: string, path: string | undefined, changes: Changes | null | undefined, text: string | undefined) {
  const api = useApi();
  const listed = changes?.supported ? changes.files : [];
  const changed = path ? turnFile(listed.map((f) => f.path), [path]) : null;
  const digest = listed.find((f) => f.path === changed)?.digest ?? '';
  const key = changed && text !== undefined ? `${changed}\n${digest}\n${text.length}` : '';
  const [lens, setLens] = useState<{ key: string; path: string; hunks: Hunk[]; changes: LensChange[] } | null>(null);
  useEffect(() => {
    if (!key || !changed) return;
    const controller = new AbortController();
    void Promise.all([api.changeFile(sessionId, 'task', changed, controller.signal), import('diff')]).then(([diff, { parsePatch }]) => {
      const hunks = diff.patch && !diff.binary ? parsePatch(diff.patch)[0]?.hunks ?? [] : [];
      if (!controller.signal.aborted) setLens({ key, path: changed, hunks, changes: lensChanges(hunks) });
    }, () => {});
    return () => controller.abort();
  }, [api, sessionId, changed, key]);
  return key && lens?.path === changed ? lens : null;
}

export function FilePreview({ selection, open, sessionId, workdir, inline, onClose, onClosed, onReload, changes, readOnly = false, onChanges, onComment, onReverted }: Readonly<{
  selection: Selection;
  /** False while it leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  sessionId: string;
  workdir: string;
  inline: boolean;
  onClose: () => void;
  onClosed: () => void;
  onReload?: () => void;
  /** This Thread's changed files: a file among them shows its changes (Lens). */
  changes?: Changes | null;
  readOnly?: boolean;
  /** Back to the Thread's Changes. */
  onChanges?: () => void;
  /** Comment on a line of a changed file, in Changes. */
  onComment?: (path: string, line: number) => void;
  onReverted?: () => void;
}>) {
  const api = useApi();
  const closeButton = useRef<HTMLButtonElement>(null);
  const { target, file, error } = selection;
  const lens = useLens(sessionId, target.path, changes, file?.kind === 'text' ? file.text : undefined);
  const [focus, setFocus] = useState(0);
  const at = lens?.changes.length ? Math.min(focus, lens.changes.length - 1) : -1;
  const [reverting, setReverting] = useState(false);
  const [revertError, setRevertError] = useState('');
  const revert = () => {
    const c = lens?.changes[at];
    if (!lens || !c) return;
    setReverting(true);
    setRevertError('');
    api.revertChange(sessionId, lens.path, runText(lens.hunks[c.hunk], c.run)).then(() => { onReload?.(); onReverted?.(); }, (e: unknown) => setRevertError(`Could not revert: ${describeError(e)}`)).finally(() => setReverting(false));
  };
  // The agent changed the open file: read it again.
  const version = useFileVersion(target.path);
  const opened = useRef({ target, version });
  useEffect(() => {
    if (opened.current.target !== target) opened.current = { target, version };
    else if (opened.current.version !== version) { opened.current.version = version; onReload?.(); }
  }, [target, version, onReload]);
  useEffect(() => { closeButton.current?.focus({ preventScroll: true }); }, [selection.target]);
  // Inline, Esc closes it from inside it or with nothing focused, unless a popup owns the key.
  useEffect(() => {
    if (!inline || !open || target.image || file?.kind === 'image') return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || popupOpen() || threadCovered()) return;
      const at = document.activeElement;
      if (at && at !== document.body && !closeButton.current?.closest('aside')?.contains(at)) return;
      event.preventDefault();
      onClose();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, target.image, file?.kind, onClose]);
  const image = target.image || file?.kind === 'image';
  const temporary = target.temporary || target.tempPath !== undefined;
  const limitation = "This file's contents can change. Temporary-file access expires after five minutes or when this preview closes, including in a new tab. Sibling assets are unavailable.";
  const reloadable = onReload && !temporary && !image && file;
  const actions = target.url && target.original !== false && <>
    {reloadable && <Button size="sm" variant="secondary" disabled={selection.reloading} onClick={onReload}><RotateCw />Reload</Button>}
    <a href={target.url} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'secondary', size: 'sm' })}><ExternalLink />{image ? 'Open original' : 'Open in new tab'}</a>
    <a href={downloadUrl(target.url)} download={target.name} className={buttonVariants({ variant: 'secondary', size: 'sm' })}><Download />Download</a>
  </>;
  if (image && target.url) {
    return <Lightbox open={open} onOpenChange={next => !next && onClose()} onClosed={onClosed} title={target.name} description={target.description} src={target.url} alt={target.name} size={target.size} footer={<div className="flex max-w-prose flex-col gap-2">
      {temporary && <p className="text-caption text-on-primary/70">{limitation}</p>}
      {actions && <div className="flex flex-wrap justify-end gap-2">{actions}</div>}
    </div>} />;
  }
  // The body: the failure, the wait, the text (or that there is none), the page in its frame, or the way out.
  let body: ReactNode;
  if (error) body = <Note tone="error">{error}</Note>;
  else if (!file) body = <output className="flex items-center gap-2 text-caption text-muted"><Spinner />Opening file…</output>;
  else if (file.kind === 'text') {
    // The same code view as Go to file: line numbers, highlighting, and the cited line marked.
    const line = Number(/#L(\d+)$/.exec(target.url ?? '')?.[1]) || undefined;
    const source = file.text
      ? <div className="min-h-64 flex-1 overflow-x-auto rounded-sm bg-canvas"><CodeView path={target.path ?? target.name} text={file.text} line={line} changes={lens?.changes} focus={at >= 0 ? at : undefined} onFocus={setFocus} /></div>
      : <Note>This file is empty.</Note>;
    body = <>
      {file.truncated && <Note>Showing the first 64 KiB. Open or download the file to read the rest.</Note>}
      {file.text && !temporary && isMarkdown(target.path) ? <MarkdownFile sessionId={sessionId} workdir={workdir} path={target.path!} text={file.text}>{source}</MarkdownFile> : source}
    </>;
  } else if (file.kind === 'html' && target.frameable) {
    body = <iframe
      key={`${target.url}#${selection.revision ?? 0}`}
      src={target.url}
      title={`Preview ${target.name}`}
      sandbox="allow-scripts allow-forms allow-popups allow-modals allow-downloads"
      referrerPolicy="no-referrer"
      className="min-h-64 w-full flex-1 rounded-sm bg-raised"
    />;
  } else if (target.url && (file.kind === 'pdf' || file.kind === 'audio' || file.kind === 'video')) {
    body = <FileMedia key={`${target.url}#${selection.revision ?? 0}`} kind={file.kind} url={target.url} name={target.name} />;
  } else body = <Note>Not shown here. Open it in a new tab or download it.</Note>;
  return (
    <SidePanel id="file-preview" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label={`Preview ${target.name}`}>
      <PanelHeader>
        {lens && onChanges && <Button size="icon" className="text-muted" aria-label="Back to Changes" onClick={onChanges}><ArrowLeft /></Button>}
        {target.path && <FileIcon path={target.path} />}
        <h2 className="min-w-0 flex-1 truncate text-title text-ink" title={target.name}>{target.name}</h2>
        <Button ref={closeButton} size="icon" className="text-muted" aria-label="Close preview" onClick={onClose}><X /></Button>
      </PanelHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overflow-x-hidden p-3">
        <p className="break-all text-caption text-muted">{target.description ?? target.name}{file?.size !== undefined ? ` · ${formatSize(file.size)}` : ''}</p>
        {temporary && <Note>{limitation}</Note>}
        {body}
      </div>
      {lens && lens.changes.length > 0 && (
        // Lens: one change at a time, with what can be done to it.
        <div className="flex shrink-0 flex-wrap items-center gap-1 px-3 pt-2 text-caption">
          <span className="mr-1 tabular-nums text-muted">Change {at + 1} of {lens.changes.length}</span>
          <Button size="icon-sm" aria-label="Previous change" className="text-muted" disabled={at <= 0} onClick={() => setFocus(at - 1)}><ChevronUp /></Button>
          <Button size="icon-sm" aria-label="Next change" className="text-muted" disabled={at >= lens.changes.length - 1} onClick={() => setFocus(at + 1)}><ChevronDown /></Button>
          <span className="flex-1" />
          {onComment && !readOnly && <Button size="sm" variant="subtle" onClick={() => onComment(lens.path, lens.changes[at].line)}><MessageSquarePlus />Comment</Button>}
          {!readOnly && <Button size="sm" variant="subtle" loading={reverting} onClick={revert}><Undo2 />Revert</Button>}
          {revertError && <Note tone="error" role="alert" className="w-full">{revertError}</Note>}
        </div>
      )}
      <div className="flex shrink-0 flex-wrap gap-2 p-3">{actions}</div>
    </SidePanel>
  );
}
