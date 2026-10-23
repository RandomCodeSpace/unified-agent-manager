import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { AtSign, ChevronRight, Copy, CornerDownLeft, Download, Ellipsis, ExternalLink, FileCode2, FileDiff, FileImage, FileText, Folder, FolderGit2, FolderOpen, GitBranch, Search, X } from 'lucide-react';
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { useFileVersion } from './FileReferences';
import { describeError, type ChangeFile, type FileDiff as FileDiffData, type FileEntry, type Project, type SessionSummary } from '../api';
import { popupOpen, threadCovered } from '../App';
import { formatSize } from '../lib/attachments';
import { copyText, useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { downloadUrl, type PreviewMetadata, type TextPreview } from '../lib/preview';
import { PreviewContext, type OpenPreview } from '../lib/previewContext';
import { statusLetter } from '../lib/review';
import { FileView as DiffView, restoreScroll } from './Changes';
import { FileMedia, Highlighted, Note, Skeleton } from './common';
import { Key } from './InlinePicker';
import { isMarkdown, MarkdownFile } from './MarkdownFile';
import { BottomSheet, LiftedRow, PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { backdropClass, readerPopupClass } from './ui/dialog';
import { Menu } from './ui/menu';
import { PanelFoot, PanelHead } from './ui/panel';
import { Segmented } from './ui/segmented';
import { Tip } from './ui/tooltip';

/** What a file can be asked to do from Go to file and the file panel. */
export interface FileActions {
  /** Opens the file in the side panel. */
  open: (path: string) => void;
  /** Puts `@path` in the composer. */
  mention: (path: string) => void;
  /** Opens Changes on the file (workspace scope). */
  changes: (path: string) => void;
}

/** Which folder and branch a Thread's files are: its working folder, on its worktree's branch, else its own, else the Project's. */
interface Place { dir: string; name: string; branch?: string; worktree: boolean }

function placeOf(session: SessionSummary, project: Project | undefined): Place {
  return { dir: session.workdir, name: baseName(session.workdir) || session.workdir, branch: session.worktree?.branch ?? session.branch ?? project?.branch, worktree: !!session.worktree };
}

const baseName = (p: string) => p.slice(p.lastIndexOf('/') + 1);
const dirName = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '');

/** The highlight.js name for a path: its extension, or the whole name for `Makefile` and the like. */
function language(path: string): string {
  const name = baseName(path).toLowerCase();
  return name.slice(name.lastIndexOf('.') + 1);
}

/* ---------- Git status: a letter and a word, never colour alone ---------- */

const FROM_LETTER: Record<string, string> = { M: 'modified', A: 'added', U: 'untracked', '?': 'untracked', D: 'deleted', R: 'renamed', C: 'copied', '!': 'conflicted' };
const STATUS: Record<string, { word: string; tone: string }> = {
  modified: { word: 'Modified', tone: 'text-warning' },
  added: { word: 'Added', tone: 'text-success' },
  untracked: { word: 'Untracked', tone: 'text-success' },
  deleted: { word: 'Deleted', tone: 'text-error' },
  renamed: { word: 'Renamed', tone: 'text-info' },
  copied: { word: 'Copied', tone: 'text-info' },
  conflicted: { word: 'Merge conflict', tone: 'text-error' },
};

/** A file's git status: its letter, and with `word` the word beside it; the word names it to screen readers either way. */
function StatusTag({ status, word = false }: Readonly<{ status?: string; word?: boolean }>) {
  const name = status ? FROM_LETTER[status] ?? status : '';
  const s = STATUS[name];
  if (!s) return null;
  return (
    <span className={cn('inline-flex shrink-0 items-center gap-1 text-caption', s.tone)} title={s.word}>
      <span aria-hidden="true" className="w-3 text-center font-mono text-code-sm font-semibold">{statusLetter(name)}</span>
      {word ? <span>{s.word}</span> : <span className="sr-only">{s.word}</span>}
    </span>
  );
}

const isDeleted = (file?: ChangeFile) => file?.status === 'deleted' || file?.status === 'D';

/** +added −removed: counts, so never colour alone. */
function Counts({ file }: Readonly<{ file?: ChangeFile }>) {
  if (!file || (!file.additions && !file.deletions)) return null;
  return (
    <span className="shrink-0 text-caption tabular-nums">
      {file.additions > 0 && <span className="text-success">+{file.additions}</span>}
      {file.additions > 0 && file.deletions > 0 && ' '}
      {file.deletions > 0 && <span className="text-error">−{file.deletions}</span>}
    </span>
  );
}

function FileGlyph({ path, folder = false }: Readonly<{ path: string; folder?: boolean }>) {
  let Icon = FileCode2;
  if (folder) Icon = Folder;
  else if (/\.(png|jpe?g|gif|webp|svg)$/i.test(path)) Icon = FileImage;
  else if (/\.(md|markdown|txt)$/i.test(path) || /(^|\/)(LICENSE|README)$/.test(path)) Icon = FileText;
  return <Icon aria-hidden="true" className="size-4 shrink-0 text-faint" />;
}

/** The query's letters in the name as the search matched them: one run where it appears whole, else each letter in order. */
function Marked({ text, q }: Readonly<{ text: string; q: string }>) {
  if (!q) return <>{text}</>;
  const lower = text.toLowerCase();
  const hits = new Set<number>();
  const at = lower.indexOf(q);
  if (at >= 0) for (let i = 0; i < q.length; i++) hits.add(at + i);
  else {
    let j = 0;
    for (let i = 0; i < lower.length && j < q.length; i++) if (lower[i] === q[j]) { hits.add(i); j++; }
    if (j < q.length) return <>{text}</>;
  }
  return <>{[...text].map((ch, i) => (hits.has(i) ? <mark key={i} className="bg-transparent font-semibold text-ink underline decoration-accent decoration-2 underline-offset-2">{ch}</mark> : ch))}</>;
}

/* ---------- Reads ---------- */

/** The folder's uncommitted files (Changes' workspace scope), read once per opening: the Changed list and each file's status. */
function useChanged(sessionId: string) {
  const api = useApi();
  const [files, setFiles] = useState<ChangeFile[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    api.changes(sessionId, 'workspace', controller.signal).then((d) => setFiles(d.supported ? d.files : []), () => {});
    return () => controller.abort();
  }, [api, sessionId]);
  return useMemo(() => {
    const byPath = new Map(files.map((f) => [f.path, f]));
    return { files, byPath, under: (dir: string) => files.filter((f) => f.path.startsWith(`${dir}/`)).length };
  }, [files]);
}

interface Opened { path: string; file?: PreviewMetadata & Partial<TextPreview>; error?: string }

/** One file's preview (its first 64 KiB of text, or its kind), read while `path` is set. */
function useFile(sessionId: string, path: string | null): Opened | null {
  const api = useApi();
  // Reloads in place when the agent changes the file; the old text stays until the new arrives.
  const version = useFileVersion(path);
  const [shown, setShown] = useState<Opened | null>(null);
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    api.filePreview(api.viewFileUrl(sessionId, path), controller.signal, undefined, true).then(
      (file) => setShown({ path, file }),
      (error: unknown) => { if (!controller.signal.aborted) setShown({ path, error: describeError(error) }); },
    );
    return () => controller.abort();
  }, [api, sessionId, path, version]);
  if (!path) return null;
  return shown?.path === path ? shown : { path };
}

/* ---------- The file itself ---------- */

/** Lines highlighted per frame: a few KiB of code, so a 64 KiB preview never holds the page in one task. */
const CODE_SLICE = 150;

/**
 * Code with a line-number gutter that stays put while long lines scroll sideways; nothing wraps. The
 * whole text shows at once, plain; highlighting follows a slice per frame from the top.
 */
export function CodeView({ path, text, line }: Readonly<{ path: string; text: string; line?: number }>) {
  const slices = useMemo(() => {
    const lines = (text.endsWith('\n') ? text.slice(0, -1) : text).split('\n');
    const out: string[] = [];
    for (let i = 0; i < lines.length; i += CODE_SLICE) out.push(lines.slice(i, i + CODE_SLICE).join('\n') + (i + CODE_SLICE < lines.length ? '\n' : ''));
    return { lines: lines.length, parts: out };
  }, [text]);
  const [lit, setLit] = useState({ text, count: 1 });
  const count = lit.text === text ? lit.count : 1;
  useEffect(() => {
    if (count >= slices.parts.length) return;
    const frame = requestAnimationFrame(() => setLit({ text, count: count + 1 }));
    return () => cancelAnimationFrame(frame);
  }, [count, slices.parts.length, text]);
  // A cited line (`a.go:12`, `#L12`) is marked and scrolled into view once per text.
  const marked = line !== undefined && line >= 1 && line <= slices.lines ? line : undefined;
  const mark = useRef<HTMLLIElement>(null);
  useEffect(() => { mark.current?.scrollIntoView({ block: 'center' }); }, [marked, text]);
  return (
    <div className="relative grid w-max min-w-full grid-cols-[auto_minmax(0,1fr)] py-2 font-mono text-code">
      {marked && <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 h-[1lh] bg-accent/10" style={{ top: `calc(0.5rem + ${marked - 1}lh)` }} />}
      <ol aria-hidden="true" className="sticky left-0 z-10 bg-canvas pr-3 pl-3 text-right text-faint tabular-nums select-none">
        {Array.from({ length: slices.lines }, (_, i) => <li key={i} ref={i + 1 === marked ? mark : undefined} className={i + 1 === marked ? 'font-medium text-ink' : undefined}>{i + 1}</li>)}
      </ol>
      <pre translate="no" className="pr-6 whitespace-pre text-ink"><code>{slices.parts.map((part, i) => (i < count ? <Highlighted key={i} language={language(path)} code={part} /> : part))}</code></pre>
    </div>
  );
}

/** The file's body: code, rendered Markdown (with its source a switch away), an image, or why it is not shown. */
function FileBody({ sessionId, workdir, opened }: Readonly<{ sessionId: string; workdir: string; opened: Opened }>) {
  const api = useApi();
  const { path, file, error } = opened;
  if (error) return <Note tone="error" role="alert" className="m-3 [overflow-wrap:anywhere]">{error}</Note>;
  if (!file) return <Skeleton label="Opening the file…" rows={8} className="gap-2 p-3" rowClassName="h-4 w-full" />;
  if (file.kind === 'image') return <div className="p-3"><img src={api.viewFileUrl(sessionId, path)} alt={baseName(path)} className="h-auto max-w-full rounded-sm" /></div>;
  if (file.kind === 'pdf' || file.kind === 'audio' || file.kind === 'video') return <div className="flex h-full flex-col p-3"><FileMedia kind={file.kind} url={api.viewFileUrl(sessionId, path)} name={baseName(path)} /></div>;
  if (file.text === undefined) return <Note className="m-3">Not shown here. Open it in a new tab or download it.</Note>;
  if (!file.text) return <Note className="m-3">This file is empty.</Note>;
  const code = <CodeView path={path} text={file.text} />;
  return <>
    {file.truncated && <Note className="mx-3 mt-2">Showing the first 64 KiB. Open or download the file to read the rest.</Note>}
    {isMarkdown(path)
      ? <div className="flex flex-col gap-3 px-4 pt-2 pb-6"><MarkdownFile sessionId={sessionId} workdir={workdir} path={path} text={file.text}>{code}</MarkdownFile></div>
      : code}
  </>;
}

/** Folder › folder › name, the name in ink. */
function Crumbs({ path, root }: Readonly<{ path: string; root?: string }>) {
  const parts = [...(root ? [root] : []), ...path.split('/')];
  return (
    <nav aria-label="Path" className="flex min-w-0 flex-1 items-center gap-0.5 text-caption text-muted">
      {parts.map((p, i) => (
        <span key={i} className={cn('flex items-center gap-0.5', i === parts.length - 1 ? 'min-w-0' : 'min-w-0 shrink')}>
          {i > 0 && <ChevronRight aria-hidden="true" className="size-3 shrink-0 text-faint" />}
          <span className={cn('truncate', i === parts.length - 1 && 'font-medium text-ink')}>{p}</span>
        </span>
      ))}
    </nav>
  );
}

/** The ⋯ menu on a file: mention it, open it in Changes, copy its path, open or download it. */
function FileMenu({ sessionId, path, changed, actions }: Readonly<{ sessionId: string; path: string; changed: boolean; actions: Pick<FileActions, 'mention' | 'changes'> }>) {
  const api = useApi();
  const url = api.viewFileUrl(sessionId, path);
  return (
    <Menu.Root modal={false}>
      <Tip label="File actions">
        <Menu.Trigger render={<Button size="icon" aria-label={`Actions for ${baseName(path)}`} className="text-muted" />}>
          <Ellipsis />
        </Menu.Trigger>
      </Tip>
      <Menu.Content align="end" className="min-w-56">
        <Menu.Actions items={[
          { key: 'mention', label: 'Mention in the message', icon: <AtSign />, hint: 'Alt+Enter', takesFocus: true, onSelect: () => actions.mention(path) },
          { key: 'changes', label: 'Open in Changes', icon: <FileDiff />, disabled: !changed, reason: changed ? undefined : 'No uncommitted changes', onSelect: () => actions.changes(path) },
          { key: 'copy', label: 'Copy path', icon: <Copy />, separator: true, onSelect: () => void copyText(path) },
          { key: 'tab', label: 'Open in a new tab', icon: <ExternalLink />, onSelect: () => window.open(url, '_blank', 'noopener,noreferrer') },
          { key: 'download', label: 'Download', icon: <Download />, onSelect: () => {
            const a = document.createElement('a');
            a.href = downloadUrl(url);
            a.download = baseName(path);
            a.click();
          } },
        ]} />
      </Menu.Content>
    </Menu.Root>
  );
}

/** The file's head: where it is, its status in words, Mention and the menu. */
function FileHead({ sessionId, path, root, change, size, actions, children }: Readonly<{ sessionId: string; path: string; root?: string; change?: ChangeFile; size?: number; actions: Pick<FileActions, 'mention' | 'changes'>; children?: ReactNode }>) {
  return (
    <div className="flex shrink-0 flex-col gap-1 pt-1.5 pr-2 pb-1 pl-3">
      <div className="flex min-h-7 min-w-0 items-center gap-2">
        <FileGlyph path={path} />
        <Crumbs path={path} root={root} />
      </div>
      <div className="flex min-h-8 min-w-0 flex-wrap items-center gap-x-2 gap-y-1 pl-6">
        {change ? <><StatusTag status={change.status} word /><Counts file={change} /></> : <span className="text-caption text-muted">Unchanged</span>}
        {size !== undefined && <span className="shrink-0 text-caption text-muted">· {formatSize(size)}</span>}
        <span className="flex-1" />
        {children}
        <Tip label={<>Mention in the message<span className="block text-on-primary/70">Alt+Enter</span></>}>
          <Button size="icon" aria-label="Mention in the message" className="text-muted" onClick={() => actions.mention(path)}><AtSign /></Button>
        </Tip>
        <FileMenu sessionId={sessionId} path={path} changed={!!change} actions={actions} />
      </div>
    </div>
  );
}

/* ---------- Go to file ---------- */

interface Row { path: string; folder: boolean; section: 'match' | 'changed' | 'folder' }

/**
 * Go to file: an anchored reader over the dimmed page (a bottom sheet on a phone). Typing matches
 * paths across the Thread's folder, letters in order; with nothing typed it lists the uncommitted
 * files and one folder to browse. The highlighted file previews on the right. Enter opens it in the
 * side panel, Alt+Enter mentions it, Backspace on an empty box goes up a folder.
 */
export default function GoToFile({ session, project, anchor, phone, open, onClose, onClosed, actions }: Readonly<{
  session: SessionSummary;
  project: Project | undefined;
  /** What it opens from (the Files button under the composer); none, or a phone, opens a bottom sheet. */
  anchor: HTMLElement | null;
  phone: boolean;
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
  actions: FileActions;
}>) {
  const api = useApi();
  const place = placeOf(session, project);
  const changed = useChanged(session.id);
  const listId = useId();
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  /** An action moved focus itself (the file panel, the composer): the closing reader leaves it there. */
  const tookFocus = useRef(false);
  const [q, setQ] = useState('');
  const query = q.trim();
  const [dir, setDir] = useState('');
  const [listing, setListing] = useState<{ dir: string; files: FileEntry[]; reason: string } | { dir: string; error: string } | null>(null);
  const [matches, setMatches] = useState<{ q: string; files: FileEntry[] } | { q: string; error: string } | null>(null);
  const [active, setActive] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    api.tree(session.id, dir, controller.signal).then(
      (l) => setListing({ dir, files: l.files, reason: l.reason }),
      (error: unknown) => { if (!controller.signal.aborted) setListing({ dir, error: describeError(error) }); },
    );
    return () => controller.abort();
  }, [api, session.id, dir]);

  // Matches as the query settles; a stale answer never replaces a newer one.
  useEffect(() => {
    if (!query) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      api.files(session.id, query, 50, controller.signal).then(
        (l) => setMatches({ q: query, files: l.files }),
        (error: unknown) => { if (!controller.signal.aborted) setMatches({ q: query, error: describeError(error) }); },
      );
    }, 120);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [api, session.id, query]);

  const shownMatches = query && matches?.q === query ? matches : null;
  const shownListing = listing?.dir === dir ? listing : null;
  const rows = useMemo<Row[]>(() => {
    if (query) return shownMatches && 'files' in shownMatches ? shownMatches.files.map((f) => ({ path: f.path, folder: f.type === 'directory', section: 'match' as const })) : [];
    const mine = dir ? [] : changed.files.map((f) => ({ path: f.path, folder: false, section: 'changed' as const }));
    const here = shownListing && 'files' in shownListing ? shownListing.files.map((f) => ({ path: f.path, folder: f.type === 'directory', section: 'folder' as const })) : [];
    return [...mine, ...here];
  }, [query, shownMatches, dir, changed.files, shownListing]);
  const index = Math.min(active, rows.length - 1);
  const current = index >= 0 ? rows[index] : undefined;
  const gone = !!current && isDeleted(changed.byPath.get(current.path));
  const preview = useFile(session.id, !phone && current && !current.folder && !gone ? current.path : null);

  useEffect(() => {
    list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: 'nearest' });
  }, [index, rows]);

  const browse = (next: string) => {
    setDir(next);
    setQ('');
    setActive(0);
    input.current?.focus();
  };
  const choose = (row: Row | undefined, mention = false) => {
    if (!row) return;
    if (row.folder && !mention) return browse(row.path);
    tookFocus.current = true;
    onClose();
    if (mention) actions.mention(row.path);
    // A deleted file has nothing to read: Changes shows what it was.
    else if (isDeleted(changed.byPath.get(row.path))) actions.changes(row.path);
    else actions.open(row.path);
  };
  // Actions from the preview's head close the reader first, as Enter does.
  const previewActions = {
    mention: (path: string) => { tookFocus.current = true; onClose(); actions.mention(path); },
    changes: (path: string) => { tookFocus.current = true; onClose(); actions.changes(path); },
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (rows.length) setActive((index + (e.key === 'ArrowDown' ? 1 : rows.length - 1)) % rows.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      choose(current, e.altKey);
    } else if (e.key === 'Backspace' && !q && dir) {
      e.preventDefault();
      browse(dirName(dir));
    }
  };

  const holdFocus = useCallback((el: HTMLDivElement | null) => {
    list.current = el;
    if (!el) return;
    const keep = (e: MouseEvent) => e.preventDefault();
    el.addEventListener('mousedown', keep);
    return () => el.removeEventListener('mousedown', keep);
  }, []);
  const optionId = (i: number) => `${listId}-${i}`;
  const row = (r: Row, i: number) => {
    const status = r.folder ? undefined : changed.byPath.get(r.path)?.status;
    const count = r.folder ? changed.under(r.path) : 0;
    const on = i === index;
    const label = `${r.folder ? 'Folder ' : ''}${r.path}${status ? `, ${STATUS[FROM_LETTER[status] ?? status]?.word ?? status}` : ''}${count ? `, ${count} changed` : ''}`;
    return (
      <button
        key={`${r.section}:${r.path}`}
        id={optionId(i)}
        type="button"
        role="option"
        tabIndex={-1}
        aria-selected={on}
        aria-label={label}
        title={r.path}
        onMouseMove={() => !on && setActive(i)}
        onClick={(e) => choose(r, e.altKey)}
        className={cn('flex min-h-9 w-full cursor-default items-center gap-2 rounded-sm px-2 text-left transition-colors duration-100 pointer-coarse:min-h-11', on && 'bg-hairline text-ink')}
      >
        <FileGlyph path={r.path} folder={r.folder} />
        <span className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className="shrink-0 truncate text-ui text-ink"><Marked text={baseName(r.path)} q={r.section === 'match' ? query.toLowerCase() : ''} /></span>
          {r.section !== 'folder' && <span className="min-w-0 truncate text-caption text-muted">{dirName(r.path)}</span>}
        </span>
        {count > 0 && <span aria-hidden="true" className="shrink-0 text-caption tabular-nums text-muted">{count}</span>}
        <StatusTag status={status} word={r.section === 'changed'} />
        {r.folder ? <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-faint" /> : on && <CornerDownLeft aria-hidden="true" className="size-3.5 shrink-0 text-faint pointer-coarse:hidden" />}
      </button>
    );
  };
  const changedRows = rows.filter((r) => r.section === 'changed');
  const folderRows = rows.filter((r) => r.section === 'folder');

  let body: ReactNode;
  if (query) {
    let content: ReactNode;
    if (!shownMatches) content = <Skeleton label="Searching…" rows={5} className="gap-1.5 px-2" rowClassName="h-6 w-full" />;
    else if ('error' in shownMatches) content = <Note tone="error" role="alert" className="px-2">Could not search: {shownMatches.error}</Note>;
    else if (!rows.length) content = <p className="px-2 text-caption text-muted">No file matches “{query}”.</p>;
    else content = rows.map(row);
    body = <div role="group" aria-label="Matches" className="flex flex-col">
      <h3 aria-hidden="true" className="flex h-6 items-center gap-2 px-2 text-eyebrow uppercase text-muted">Matches{shownMatches && 'files' in shownMatches && <span className="text-caption normal-case tracking-normal">{rows.length}</span>}</h3>
      {content}
    </div>;
  } else {
    let content: ReactNode;
    if (!shownListing) content = <Skeleton label="Loading the folder…" rows={6} className="gap-1.5 px-2" rowClassName="h-6 w-full" />;
    else if ('error' in shownListing) content = <Note tone="error" role="alert" className="px-2 [overflow-wrap:anywhere]">Could not list the folder: {shownListing.error}</Note>;
    else if (shownListing.reason) content = <Note className="px-2 [overflow-wrap:anywhere]">{shownListing.reason}</Note>;
    else if (!folderRows.length) content = <p className="px-2 text-caption text-muted">Empty folder.</p>;
    else content = folderRows.map((r, i) => row(r, changedRows.length + i));
    const parts = dir ? dir.split('/') : [];
    body = <>
      {changedRows.length > 0 && (
        <div role="group" aria-label="Changed" className="flex flex-col">
          <h3 aria-hidden="true" className="flex h-6 items-center gap-2 px-2 text-eyebrow uppercase text-muted">Changed<span className="text-caption normal-case tracking-normal">{changedRows.length} not committed</span></h3>
          {changedRows.map((r, i) => row(r, i))}
        </div>
      )}
      <div role="group" aria-label={dir ? `Browse ${dir}` : 'Browse'} className="flex flex-col">
        <div className="flex h-6 min-w-0 items-center gap-1 px-2">
          <h3 aria-hidden="true" className="shrink-0 text-eyebrow uppercase text-muted">Browse</h3>
          <span className="flex min-w-0 items-center gap-0.5 pl-1 text-caption text-muted">
            <button type="button" tabIndex={-1} className="truncate hover:text-ink hover:underline" onClick={() => browse('')}>{place.name}</button>
            {parts.map((p, i) => (
              <span key={i} className="flex min-w-0 items-center gap-0.5">
                <ChevronRight aria-hidden="true" className="size-3 shrink-0 text-faint" />
                <button type="button" tabIndex={-1} className={cn('truncate hover:underline', i === parts.length - 1 ? 'text-ink' : 'hover:text-ink')} onClick={() => browse(parts.slice(0, i + 1).join('/'))}>{p}</button>
              </span>
            ))}
          </span>
        </div>
        {content}
      </div>
    </>;
  }

  const links = useCallback<OpenPreview>((t) => {
    if (t.path) { tookFocus.current = true; onClose(); actions.open(t.path); }
    else if (t.url) window.open(t.url, '_blank', 'noopener,noreferrer');
  }, [actions, onClose]);
  let side: ReactNode = null;
  if (!phone) {
    side = (
      <section aria-label="Preview" className="hidden min-h-0 min-w-0 flex-1 flex-col bg-canvas md:flex">
        {preview ? (
          <PreviewContext.Provider value={links}>
            <FileHead sessionId={session.id} path={preview.path} change={changed.byPath.get(preview.path)} actions={previewActions} />
            <div className="min-h-0 flex-1 overflow-auto">
              <FileBody sessionId={session.id} workdir={session.workdir} opened={preview} />
            </div>
          </PreviewContext.Provider>
        ) : (
          <p className="flex flex-1 flex-col items-center justify-center gap-1 px-6 text-center text-caption text-muted">
            <FolderOpen aria-hidden="true" className="size-5 text-faint" />
            {current?.folder && <span><span className="font-mono text-code-sm">{current.path}</span> · Enter opens the folder</span>}
            {gone && <span><span className="font-mono text-code-sm">{current?.path}</span> is deleted · Enter opens it in Changes</span>}
            {!current?.folder && !gone && 'Nothing to preview'}
          </p>
        )}
      </section>
    );
  }

  const content = (close: ReactNode) => <>
    <PanelHead className="gap-1 pt-2 pr-2 pb-2 pl-4">
      <div className="flex min-h-7 items-center gap-2">
        <h2 className="flex-1 text-title text-ink">Go to file</h2>
        {close}
      </div>
      <PlaceLine place={place} changed={changed.files.length} />
    </PanelHead>
    <div className="flex min-h-0 flex-1">
      <div className={cn('flex min-h-0 w-full shrink-0 flex-col', !phone && 'md:w-[420px]')}>
        <label className="mx-3 mb-2 flex h-8 shrink-0 items-center gap-2 rounded-sm bg-sunken px-2 shadow-well">
          <Search aria-hidden="true" className="size-4 shrink-0 text-faint" />
          <input
            ref={input}
            value={q}
            role="combobox"
            aria-label="Go to file"
            aria-expanded="true"
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={current ? optionId(index) : undefined}
            placeholder={dir ? `Go to file, or browse ${dir}` : 'Go to file'}
            autoComplete="off"
            spellCheck={false}
            className="min-w-0 flex-1 bg-transparent text-ui text-ink outline-hidden placeholder:text-muted"
            onChange={(e) => { setQ(e.target.value); setActive(0); }}
            onKeyDown={onKeyDown}
          />
        </label>
        {/* A press in the list keeps focus in the box, which the keyboard drives. */}
        <div ref={holdFocus} id={listId} role="listbox" aria-label="Files" className="flex min-h-0 flex-1 flex-col gap-3 overflow-x-hidden overflow-y-auto overscroll-contain px-2 pb-3">
          {body}
        </div>
      </div>
      {side && <span aria-hidden="true" className="fade-rule-y hidden w-px shrink-0 md:block" />}
      {side}
    </div>
    <PanelFoot className="pointer-coarse:hidden">
      <span className="flex items-center gap-1.5"><Key>↑</Key><Key>↓</Key>move</span>
      <span className="flex items-center gap-1.5"><Key>↵</Key>open</span>
      <span className="flex items-center gap-1.5"><Key>Alt</Key><Key>↵</Key>mention</span>
      {dir && <span className="flex items-center gap-1.5"><Key>⌫</Key>up a folder</span>}
      <span className="flex-1" />
      <span className="flex items-center gap-1.5"><Key>Alt</Key><Key>P</Key>open</span>
    </PanelFoot>
  </>;

  const back = () => (tookFocus.current ? false : anchor?.isConnected ? anchor : true);
  if (phone || !anchor) {
    return (
      <BottomSheet open={open} onClose={onClose} onClosed={onClosed} label="Go to file" initialFocus={input} finalFocus={back} className="h-[85dvh] duration-160" backdropClassName="duration-160">
        {content(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
      </BottomSheet>
    );
  }
  const below = anchor.getBoundingClientRect().top < window.innerHeight / 2;
  return (
    <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={cn(backdropClass, 'duration-160')} />
        {anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side={below ? 'bottom' : 'top'} align="end" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup data-popup="" aria-label="Go to file" initialFocus={input} finalFocus={back} className={cn('flex h-[min(620px,var(--available-height))] w-[min(980px,var(--available-width))]', readerPopupClass)}>
            {content(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

/** Which folder and branch the files are: the branch (a worktree says so), the changed count, the folder path a click copies. */
function PlaceLine({ place, changed }: Readonly<{ place: Place; changed: number }>) {
  const [copied, copy] = useCopied();
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <div className="flex min-w-0 items-center gap-1.5">
        {place.worktree ? <FolderGit2 aria-hidden="true" className="size-4 shrink-0 text-muted" /> : <GitBranch aria-hidden="true" className="size-4 shrink-0 text-muted" />}
        <span className="min-w-0 truncate text-ui font-medium text-ink" title={place.branch}>{place.branch || 'No branch'}</span>
        {place.worktree && <Chip fill="outline">Worktree</Chip>}
        <span className="shrink-0 text-caption text-muted">{changed ? `${changed} changed` : 'No changes'}</span>
      </div>
      <Tip label={copied ? 'Copied' : 'Copy the folder path'}>
        <button type="button" tabIndex={-1} className="min-w-0 truncate rounded-xs pl-5.5 text-left font-mono text-code-sm text-muted hover:text-ink" onClick={() => copy(place.dir)}>{place.dir}</button>
      </Tip>
    </div>
  );
}

/* ---------- The file panel ---------- */

/** Where the file panel was left in a Thread: reopened on the same file, it shows it as it was. */
export interface FilePlace { path: string; mode: string; scrollTop: number }

/**
 * One file in the side panel, the panel's full width: its head (where it is, its status, Mention and
 * the menu) and its body. A changed file switches between the File and its Diff against HEAD. A
 * link in a rendered Markdown file opens its target here.
 */
export function FilePanel({ session, project, path, inline, open, onClose, onClosed, actions, memory }: Readonly<{
  session: SessionSummary;
  project: Project | undefined;
  path: string;
  inline: boolean;
  /** False while the panel leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
  actions: FileActions;
  /** This Thread's last place in the panel, written as it leaves. */
  memory?: RefObject<FilePlace | null>;
}>) {
  const api = useApi();
  const place = placeOf(session, project);
  const changed = useChanged(session.id);
  const change = changed.byPath.get(path);
  const [left] = useState(() => (memory?.current?.path === path ? memory.current : null));
  const [view, setView] = useState<{ path: string; mode: string }>({ path, mode: left?.mode ?? 'file' });
  const mode = view.path === path && change ? view.mode : 'file';
  const opened = useFile(session.id, path);
  const [diff, setDiff] = useState<{ path: string; file?: FileDiffData; error?: string } | null>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  // The place is written as the panel leaves; its scroll comes back once the same file shows in the same view.
  const body = useRef<HTMLDivElement>(null);
  const scrolled = useRef(0);
  const restore = useRef(left);
  useEffect(() => () => { if (memory) memory.current = { path, mode, scrollTop: scrolled.current }; }, [memory, path, mode]);
  const loaded = mode === 'diff' ? diff?.path === path && !!diff.file : !!opened?.file;
  useLayoutEffect(() => {
    const at = restore.current;
    if (at?.path !== path) restore.current = null;
    else if (loaded && at.mode === mode && body.current) {
      restore.current = null;
      return restoreScroll(body.current, at.scrollTop);
    }
  }, [loaded, mode, path]);

  useEffect(() => {
    if (mode !== 'diff') return;
    const controller = new AbortController();
    api.changeFile(session.id, 'workspace', path, controller.signal).then(
      (file) => setDiff({ path, file }),
      (error: unknown) => { if (!controller.signal.aborted) setDiff({ path, error: describeError(error) }); },
    );
    return () => controller.abort();
  }, [api, session.id, path, mode]);
  useEffect(() => { closeRef.current?.focus({ preventScroll: true }); }, [path]);

  // Inline, the panel is no dialog: Esc closes it from inside it or with nothing focused, unless a popup owns the key.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: globalThis.KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || popupOpen() || threadCovered()) return;
      const at = document.activeElement;
      if (at && at !== document.body && !closeRef.current?.closest('aside')?.contains(at)) return;
      onClose();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, onClose]);

  const links = useCallback<OpenPreview>((t) => {
    if (t.path) actions.open(t.path);
    else if (t.url) window.open(t.url, '_blank', 'noopener,noreferrer');
  }, [actions]);
  const shownDiff = diff?.path === path ? diff : null;
  return (
    <SidePanel id="file" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label={`File ${baseName(path)}`} defaultWidth={620}>
      <PanelHeader>
        <FileGlyph path={path} />
        <h2 className="min-w-0 shrink truncate text-title text-ink" title={path}>{baseName(path)}</h2>
        <span className="min-w-0 flex-1 truncate text-caption text-muted">{place.branch}{place.worktree ? ' · worktree' : ''}</span>
        <Button ref={closeRef} size="icon-md" aria-label="Close the file" className="text-muted" onClick={onClose}><X /></Button>
      </PanelHeader>
      <FileHead sessionId={session.id} path={path} root={place.name} change={change} size={opened?.file?.size} actions={actions}>
        {change && <Segmented size="sm" aria-label="Show" className="w-32" value={mode} onValueChange={(m) => setView({ path, mode: m })} items={[{ value: 'file', label: 'File' }, { value: 'diff', label: 'Diff' }]} />}
      </FileHead>
      <div ref={body} className="min-h-0 flex-1 overflow-auto" onScroll={(e) => { scrolled.current = e.currentTarget.scrollTop; }}>
        {mode === 'diff'
          ? <DiffView path={path} file={shownDiff?.file ?? null} error={shownDiff?.error ?? null} />
          : opened && <PreviewContext.Provider value={links}><FileBody sessionId={session.id} workdir={session.workdir} opened={opened} /></PreviewContext.Provider>}
      </div>
    </SidePanel>
  );
}
