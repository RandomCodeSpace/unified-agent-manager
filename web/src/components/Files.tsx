import { ChevronRight, Copy, Download, ExternalLink, FolderTree, RefreshCw, X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { api, describeError, type FileList, type SessionSummary } from '../api';
import { popupOpen } from '../App';
import { formatSize } from '../lib/attachments';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { downloadUrl, type PreviewMetadata, type TextPreview } from '../lib/preview';
import { statusLetter } from '../lib/review';
import { PreviewContext, type OpenPreview } from '../lib/previewContext';
import { CodeBlock, Highlighted, Note, Skeleton } from './common';
import { isMarkdown, MarkdownFile } from './MarkdownFile';
import { PanelHeader, SidePanel } from './Subagents';
import { Button, buttonVariants } from './ui/button';
import { Tip } from './ui/tooltip';

type Listing = FileList | { error: string };
interface Shown { path: string; file?: PreviewMetadata & Partial<TextPreview>; error?: string }

/** Beyond this depth rows stop indenting, so a deep path cannot push names out of the panel. */
const MAX_INDENT = 8;

/** A changed file's label and tone, by its git status as Changes names it; its letter is Changes' (`statusLetter`). */
const STATUS_MARK: Record<string, { label: string; tone: string }> = {
  modified: { label: 'Modified', tone: 'text-warning' },
  added: { label: 'Added', tone: 'text-success' },
  untracked: { label: 'New, not tracked by Git', tone: 'text-success' },
  deleted: { label: 'Deleted', tone: 'text-error' },
  renamed: { label: 'Renamed', tone: 'text-info' },
  copied: { label: 'Copied', tone: 'text-info' },
  conflicted: { label: 'Merge conflict', tone: 'text-error' },
};

/** The highlight.js name for a path: its extension, or the whole name for `Makefile` and the like. */
function language(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  return name.slice(name.lastIndexOf('.') + 1);
}

/**
 * The Files sheet: the Task directory's tree, one folder read per expansion, with the chosen
 * file below it. Nothing is read until the sheet opens, and nothing refreshes on its own.
 */
export default function FilesSheet({ session, inline, open, onClose, onClosed }: Readonly<{
  session: SessionSummary;
  inline: boolean;
  /** False while the sheet leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const [listings, setListings] = useState<Record<string, Listing>>({});
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const [shown, setShown] = useState<Shown | null>(null);
  const lists = useRef<AbortController | null>(null);
  const reading = useRef<AbortController | null>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const tree = useRef<HTMLUListElement>(null);

  // Focus goes back to the header's Files button, as Changes and Terminal do with theirs.
  const close = useCallback(() => {
    onClose();
    document.getElementById('files-link')?.focus({ preventScroll: true });
  }, [onClose]);
  // Esc closes the open file first, with focus on its row, then the panel.
  const closeFile = useCallback(() => {
    reading.current?.abort();
    tree.current?.querySelector<HTMLElement>('[aria-current="true"]')?.focus({ preventScroll: true });
    setShown(null);
  }, []);

  const load = useCallback((dir: string) => {
    const signal = lists.current?.signal;
    if (!signal) return;
    api.tree(session.id, dir, signal).then(
      (list) => { if (!signal.aborted) setListings((l) => ({ ...l, [dir]: list })); },
      (error: unknown) => { if (!signal.aborted) setListings((l) => ({ ...l, [dir]: { error: describeError(error) } })); },
    );
  }, [session.id]);

  const read = useCallback((path: string) => {
    reading.current?.abort();
    const controller = new AbortController();
    reading.current = controller;
    setShown({ path });
    api.filePreview(api.viewFileUrl(session.id, path), controller.signal, undefined, true).then(
      (file) => { if (!controller.signal.aborted) setShown({ path, file }); },
      (error: unknown) => { if (!controller.signal.aborted) setShown({ path, error: describeError(error) }); },
    );
  }, [session.id]);

  // A link in a rendered Markdown file opens its target here; anything else opens in a new tab.
  const openHere = useCallback<OpenPreview>((target) => {
    if (target.path) read(target.path);
    else if (target.url) window.open(target.url, '_blank', 'noopener,noreferrer');
  }, [read]);

  useEffect(() => {
    closeRef.current?.focus({ preventScroll: true });
    lists.current = new AbortController();
    load('');
    return () => {
      lists.current?.abort();
      lists.current = null;
      reading.current?.abort();
    };
  }, [load]);

  // Inline, the sheet is no dialog: Esc closes the open file, then the panel, unless a popup owns the key, and
  // only from inside the panel or with nothing focused (Esc in the conversation or the composer is theirs).
  // As a sheet, the dialog owns Esc; the panel's own handler below still closes the file first.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || popupOpen()) return;
      const at = document.activeElement;
      if (at && at !== document.body && !closeRef.current?.closest('aside')?.contains(at)) return;
      if (shown) closeFile();
      else close();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, shown, close, closeFile]);

  const refresh = () => {
    lists.current?.abort();
    lists.current = new AbortController();
    setListings({});
    load('');
    for (const dir of expanded) load(dir);
    if (shown) read(shown.path);
  };

  const toggle = (dir: string) => {
    const next = new Set(expanded);
    if (next.delete(dir)) {
      setExpanded(next);
      return;
    }
    next.add(dir);
    setExpanded(next);
    const known = listings[dir];
    if (!known || 'error' in known) load(dir);
  };

  const rows = (dir: string, depth: number): ReactNode[] => {
    const listing = listings[dir];
    const indent = { paddingLeft: `${0.5 + Math.min(depth, MAX_INDENT) * 0.875}rem` };
    if (!listing) {
      return [<li key={`${dir}:loading`} style={indent} className="py-1 pr-2"><Skeleton label="Loading the folder…" rows={dir ? 1 : 4} className="gap-1.5" rowClassName="h-5 w-full" /></li>];
    }
    if ('error' in listing) {
      return [
        <li key={`${dir}:error`} style={indent} className="py-1 pr-2">
          <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">Could not list the folder: {listing.error}</span>
            <Button size="sm" variant="secondary" onClick={() => load(dir)}>Retry</Button>
          </Note>
        </li>,
      ];
    }
    const out: ReactNode[] = [];
    if (listing.reason) out.push(<li key={`${dir}:reason`} style={indent} className="py-1 pr-2"><Note className="[overflow-wrap:anywhere]">{listing.reason}</Note></li>);
    else if (listing.files.length === 0) out.push(<li key={`${dir}:empty`} style={indent} className="py-1 pr-2"><Note>{dir ? 'Empty folder.' : 'No files.'}</Note></li>);
    for (const entry of listing.files) {
      const name = entry.path.slice(entry.path.lastIndexOf('/') + 1);
      const folder = entry.type === 'directory';
      const isOpen = folder && expanded.has(entry.path);
      const selected = !folder && shown?.path === entry.path;
      const deleted = entry.status === 'deleted';
      const mark = folder ? undefined : STATUS_MARK[entry.status ?? ''];
      out.push(
        <li key={entry.path}>
          <button
            type="button"
            aria-expanded={folder ? isOpen : undefined}
            aria-current={selected ? 'true' : undefined}
            title={mark ? `${entry.path} · ${mark.label}` : `${entry.path}${folder && entry.status === 'changed' ? ' · Contains changes' : ''}`}
            disabled={deleted}
            style={{ paddingLeft: `${0.25 + Math.min(depth, MAX_INDENT) * 0.875}rem` }}
            className={cn('flex min-h-7 w-full items-start gap-1 rounded-sm py-1 pr-2 text-left text-caption transition-colors focus-visible:-outline-offset-2 disabled:cursor-default pointer-coarse:min-h-11 pointer-coarse:items-center', selected ? 'bg-raised text-ink shadow-raised' : 'text-body enabled:hover:bg-tint-hover')}
            onClick={() => (folder ? toggle(entry.path) : read(entry.path))}
          >
            {folder
              ? <ChevronRight aria-hidden="true" className={cn('mt-px size-3.5 shrink-0 text-muted transition-transform duration-160 ease-app', isOpen && 'rotate-90')} />
              : <span aria-hidden="true" className="size-3.5 shrink-0" />}
            <span className={cn('min-w-0 flex-1 [overflow-wrap:anywhere]', folder && 'font-medium', mark && !deleted && 'text-ink', deleted && 'text-muted line-through')}>{name}</span>
            {mark && <span className={cn('w-3 shrink-0 text-center font-mono text-code-sm font-semibold', mark.tone)} aria-label={mark.label}>{statusLetter(entry.status ?? '')}</span>}
            {folder && entry.status === 'changed' && <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-warning pointer-coarse:mt-0" role="img" aria-label="Contains changes" title="Contains changes" />}
          </button>
        </li>,
      );
      if (isOpen) out.push(...rows(entry.path, depth + 1));
    }
    return out;
  };

  return (
    <SidePanel id="files" inline={inline} open={open} onClose={close} onClosed={onClosed} label="Files" defaultWidth={440}>
      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- every control inside is focusable; this only takes Escape from them, for the open file, before the sheet's dialog closes. */}
      <div className="contents" onKeyDown={(e) => {
        if (e.key !== 'Escape' || !shown || e.defaultPrevented) return;
        e.preventDefault();
        e.stopPropagation();
        closeFile();
      }}>
        <PanelHeader>
          <FolderTree aria-hidden="true" className="size-4 text-muted" />
          <span className="text-title text-ink">Files</span>
          <span className="flex-1" />
          <Tip label="Refresh">
            <Button size="icon-md" aria-label="Refresh files" className="text-muted" onClick={refresh}>
              <RefreshCw />
            </Button>
          </Tip>
          <Button ref={closeRef} size="icon-md" aria-label="Close files" className="text-muted" onClick={close}>
            <X />
          </Button>
        </PanelHeader>
        <ul ref={tree} aria-label="Project files" className={cn('overflow-y-auto overflow-x-hidden p-1', shown ? 'max-h-[45%] shrink-0' : 'min-h-0 flex-1')}>{rows('', 0)}</ul>
        {shown && <>
          <div className="fade-rule mx-3 shrink-0" aria-hidden="true" />
          <PreviewContext.Provider value={openHere}>
            <FileView key={shown.path} sessionId={session.id} workdir={session.workdir} shown={shown} />
          </PreviewContext.Provider>
        </>}
      </div>
    </SidePanel>
  );
}

function FileView({ sessionId, workdir, shown: { path, file, error } }: Readonly<{ sessionId: string; workdir: string; shown: Shown }>) {
  const [copied, copy] = useCopied();
  const url = api.viewFileUrl(sessionId, path);
  const name = path.slice(path.lastIndexOf('/') + 1);
  const lang = language(path);
  let body;
  if (error) body = <Note tone="error" role="alert" className="[overflow-wrap:anywhere]">{error}</Note>;
  else if (!file) body = <Skeleton label="Opening the file…" rows={6} className="gap-2" rowClassName="h-4 w-full" />;
  else if (file.kind === 'image') body = <img src={url} alt={name} className="h-auto max-w-full rounded-sm" />;
  // Text, and an HTML page's source (Open in new tab shows the page itself).
  else if (file.text === undefined) body = <Note>This file is not shown here. Open it in a new tab or download it.</Note>;
  else if (!file.text) body = <Note>This file is empty.</Note>;
  else {
    const source = (
      <CodeBlock
        language={lang}
        text={file.text}
        className="my-0 shrink-0"
        body={<pre translate="no" className="px-3 pt-0.5 pb-2.5 font-mono text-code whitespace-pre-wrap text-ink [overflow-wrap:anywhere]"><code><Highlighted language={lang} code={file.text} /></code></pre>}
      >
        {null}
      </CodeBlock>
    );
    body = <>
      {file.truncated && <Note>Showing the first 64 KiB. Open or download the file to read the rest.</Note>}
      {isMarkdown(path) ? <MarkdownFile sessionId={sessionId} workdir={workdir} path={path} text={file.text}>{source}</MarkdownFile> : source}
    </>;
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overflow-x-hidden p-3">
      <div className="flex items-start gap-1">
        <p className="min-w-0 flex-1 pt-1 text-caption text-body [overflow-wrap:anywhere]">
          {path}{file?.size !== undefined && <span className="text-muted"> · {formatSize(file.size)}</span>}
        </p>
        <Tip label={copied ? 'Copied' : 'Copy path'}>
          <Button size="icon-sm" aria-label="Copy path" className="text-muted" onClick={() => copy(path)}><Copy /></Button>
        </Tip>
        <Tip label="Open in new tab">
          <a href={url} target="_blank" rel="noopener noreferrer" aria-label="Open in new tab" className={cn(buttonVariants({ size: 'icon-sm' }), 'text-muted')}><ExternalLink /></a>
        </Tip>
        <Tip label="Download">
          <a href={downloadUrl(url)} download={name} aria-label="Download" className={cn(buttonVariants({ size: 'icon-sm' }), 'text-muted')}><Download /></a>
        </Tip>
      </div>
      {body}
    </div>
  );
}
