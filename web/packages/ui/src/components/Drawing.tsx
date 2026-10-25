// Drawings (DESIGN.md Drawing card, Drawing editor): a mermaid fence the user turned into a
// `.excalidraw` (or `.drawio`) file. The card in the conversation shows the agent's version, or
// the pair once the user has unsent changes; the editor is the anchored reader under the card's
// head, Excalidraw (or the draw.io embed) in Yours, read-only pictures in Agent's and Changes.
// The server keeps versions and diffs them; nothing is counted here.

import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, taskName, type Drawing, type DrawingDoc, type DrawingPut, type DrawingSaved, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { exportDrawioPng } from '../lib/drawio';
import { changeSummary, changesWord, parseExcalidraw, picture, token, type DrawnElement, type Picture } from '../lib/drawings';
import { useScheme } from '../lib/theme';
import { Note, Spinner, useApp } from './common';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { Chip } from './ui/chip';

// The editor is the heavy part: it loads on the first open, with Excalidraw behind it.
const DrawingEditor = lazy(() => import('./DrawingEditor'));

export type DrawingView = 'agent' | 'mine' | 'changes';

export interface OpenDrawing {
  path: string;
  view: DrawingView;
  /** The element the reader anchors to and lifts above the dim. */
  anchor: HTMLElement;
}

export interface DrawingsApi {
  sessionId: string;
  /** The Thread takes edits: not archived, settled or read-only. */
  writable: boolean;
  title: string;
  /** The session's list: which fences have a drawing, and which drawings carry unsent changes. */
  drawings: readonly Drawing[];
  /** The draw.io origin while Settings has it enabled; null for Excalidraw. */
  drawio: string | null;
  doc: (path: string) => DocState | undefined;
  load: (path: string) => Promise<DrawingDoc>;
  /** Creates a drawing from a fence (`origin: 'convert'`). */
  create: (path: string, body: DrawingPut) => Promise<void>;
  save: (path: string, content: string, baseVersion: number) => Promise<DrawingSaved>;
  open: (target: OpenDrawing) => void;
}

export interface DocState {
  doc?: DrawingDoc;
  error?: string;
}

export const DrawingsContext = createContext<DrawingsApi | null>(null);

const NONE: Drawing[] = [];

/** The drawing a fence of a reply has, when the session lists one. */
export function fenceDrawing(drawings: readonly Drawing[] | undefined, itemId: string | undefined, fenceIndex: number): Drawing | undefined {
  if (!itemId || !drawings) return undefined;
  return drawings.find((d) => d.item_id === itemId && d.fence_index === fenceIndex);
}

/**
 * The Thread's drawings for every surface: the cards, the composer's chip and the notice rows
 * read the docs here, and one editor at a time opens from here, anchored to what asked.
 */
export function DrawingsProvider({ session, writable, children }: Readonly<{ session: SessionDetail; writable: boolean; children: ReactNode }>) {
  const api = useApi();
  const { settings } = useApp();
  const [docs, setDocs] = useState<Record<string, DocState>>({});
  // The list: the detail's, until a save here reads it again (summary events do not carry it); a new detail load wins.
  const [listed, setListed] = useState<{ from: Drawing[] | undefined; list: Drawing[] } | null>(null);
  const drawings = useMemo(() => (listed && listed.from === session.drawings ? listed.list : session.drawings ?? NONE), [listed, session.drawings]);
  const [opened, setOpened] = useState<(OpenDrawing & { open: boolean; phone: boolean }) | null>(null);
  const loading = useRef(new Map<string, Promise<DrawingDoc>>());
  const sessionId = session.id;
  const drawio = settings.drawio?.enabled && settings.drawio.origin ? settings.drawio.origin : null;
  const relist = useCallback(() => {
    const from = session.drawings;
    api.drawings(sessionId).then((list) => setListed({ from, list }), () => {});
  }, [api, sessionId, session.drawings]);

  const load = useCallback((path: string) => {
    const pending = loading.current.get(path);
    if (pending) return pending;
    const p = api.drawing(sessionId, path).then(
      (doc) => {
        setDocs((d) => ({ ...d, [path]: { doc } }));
        return doc;
      },
      (err: unknown) => {
        setDocs((d) => ({ ...d, [path]: { ...d[path], error: describeError(err) } }));
        throw err;
      },
    ).finally(() => loading.current.delete(path));
    loading.current.set(path, p);
    return p;
  }, [api, sessionId]);

  const title = taskName(session);
  const value = useMemo<DrawingsApi>(() => ({
    sessionId,
    writable,
    title,
    drawings,
    drawio,
    doc: (path) => docs[path],
    load,
    create: async (path, body) => {
      const saved = await api.putDrawing(sessionId, path, body);
      setDocs((d) => ({ ...d, [path]: { doc: { path, format: body.format, content: body.content, baseline: null, item_id: body.item_id, fence_index: body.fence_index, ...saved } } }));
      relist();
    },
    save: async (path, content, baseVersion) => {
      const saved = await api.putDrawing(sessionId, path, { content, format: docs[path]?.doc?.format ?? 'excalidraw', base_version: baseVersion });
      setDocs((d) => {
        const doc = d[path]?.doc;
        if (!doc) return d;
        // The agent's text is the baseline; equal to the content again, the server says null.
        const baseline = saved.changes.length ? (doc.baseline ?? doc.content) : null;
        return { ...d, [path]: { doc: { ...doc, ...saved, content, baseline } } };
      });
      relist();
      return saved;
    },
    open: (target) => setOpened({ ...target, open: true, phone: window.matchMedia(PHONE).matches }),
  }), [api, sessionId, writable, title, drawings, drawio, docs, load, relist]);

  // The agent read a drawing, or the note went with a prompt: the list says so before the doc does.
  useEffect(() => {
    for (const entry of drawings) {
      const doc = docs[entry.path]?.doc;
      if (doc && (entry.version > doc.version || entry.agent_version > doc.agent_version)) void load(entry.path).catch(() => {});
    }
  }, [drawings, docs, load]);
  // A prompt carried the notes: each lands as a uam notice, and the session events carry no list, so the list is read again then.
  const notes = useMemo(() => session.items.reduce((n, item) => n + (item.kind === 'notice' && !!item.text?.startsWith('The user edited ') ? 1 : 0), 0), [session.items]);
  const seenNotes = useRef(notes);
  useEffect(() => {
    if (notes <= seenNotes.current) return;
    seenNotes.current = notes;
    relist();
  }, [notes, relist]);

  return (
    <DrawingsContext.Provider value={value}>
      {children}
      {opened && (
        <Suspense fallback={null}>
          <DrawingEditor target={opened} onClose={() => setOpened((o) => o && { ...o, open: false })} onClosed={() => setOpened(null)} />
        </Suspense>
      )}
    </DrawingsContext.Provider>
  );
}

/** The canvas background: the `raised` token in light; white in dark, which Excalidraw's dark filter inverts along with the file's light colours. */
export function useBackground(): string {
  const scheme = useScheme();
  return useMemo(() => (scheme === 'dark' ? '#ffffff' : token('raised')), [scheme]);
}

/** A PNG of the elements, drawn again when the scheme changes; null until it is here. `tokens` elements (the Changes overlay) carry the scheme's colours already and skip the dark filter. */
export function usePicture(elements: readonly DrawnElement[] | null, files: Record<string, unknown>, tokens = false): Picture | null {
  const dark = useScheme() === 'dark' && !tokens;
  const [pic, setPic] = useState<{ key: unknown; picture: Picture } | null>(null);
  useEffect(() => {
    if (!elements) return;
    let on = true;
    picture(elements, files, dark).then((p) => on && setPic({ key: elements, picture: p }), () => {});
    return () => {
      on = false;
    };
  }, [elements, files, dark]);
  return pic?.key === elements ? pic.picture : null;
}

/** A PNG of a draw.io file from the embed's exporter; the message when it does not answer. */
export function useDrawioPicture(origin: string | null, xml: string | null): { picture: Picture | null; error?: string } {
  const [state, setState] = useState<{ key: string | null; picture: Picture | null; error?: string }>({ key: null, picture: null });
  useEffect(() => {
    if (!origin || xml === null) return;
    let on = true;
    exportDrawioPng(origin, xml).then(
      (p) => on && setState({ key: xml, picture: p }),
      (err: unknown) => on && setState({ key: xml, picture: null, error: describeError(err) }),
    );
    return () => {
      on = false;
    };
  }, [origin, xml]);
  return state.key === xml ? state : { picture: null };
}

export const fileName = (path: string) => path.slice(path.lastIndexOf('/') + 1);

/** A drawing's picture in a card or a view: the PNG, or a quiet wait, or why there is none. */
export function Figure({ picture, alt, error, className }: Readonly<{ picture: Picture | null; alt: string; error?: string; className?: string }>) {
  if (error) return <Note tone="warn" className="px-3 py-2">{error}</Note>;
  if (!picture) return <span className={cn('flex min-h-24 items-center justify-center', className)}><Spinner className="border-muted" /></span>;
  return <img src={picture.url} width={picture.width} height={picture.height} alt={alt} decoding="async" className={cn('mx-auto block h-auto max-h-[480px] w-auto max-w-full bg-raised object-contain animate-fade-in', className)} />;
}

const pill = (n: number) => `Yours v${n}`;

/** The elements and files of a doc's current and baseline texts (Excalidraw), memoised on the texts. */
export function useParsed(doc: DrawingDoc | undefined) {
  const current = useMemo(() => (doc && doc.format === 'excalidraw' ? parseExcalidraw(doc.content) : null), [doc?.content, doc?.format]); // eslint-disable-line react-hooks/exhaustive-deps -- the texts are the inputs
  const base = useMemo(() => (doc && doc.format === 'excalidraw' ? (doc.baseline === null ? current : parseExcalidraw(doc.baseline)) : null), [doc?.baseline, doc?.format, current]); // eslint-disable-line react-hooks/exhaustive-deps
  return { current, base };
}

/**
 * The card in the conversation: the file name, the version pills, one picture or, with unsent
 * changes, the pair (Agent's and Yours side by side, stacked on a phone) with the first changes
 * under it. The picture opens the editor; Compare opens the Changes view.
 */
export function DrawingCard({ path }: Readonly<{ path: string }>) {
  const d = useContext(DrawingsContext);
  const [head, setHead] = useState<HTMLDivElement | null>(null);
  const state = d?.doc(path);
  const doc = state?.doc;
  const load = d?.load;
  useEffect(() => {
    if (!doc && load) void load(path).catch(() => {});
  }, [doc, load, path]);
  const { current, base } = useParsed(doc);
  const mine = usePicture(current?.elements ?? null, current?.files ?? {});
  const agents = usePicture(doc?.changes.length ? (base?.elements ?? null) : null, base?.files ?? {});
  const mineDrawio = useDrawioPicture(doc?.format === 'drawio' ? d?.drawio ?? null : null, doc?.content ?? null);
  const agentsDrawio = useDrawioPicture(doc?.format === 'drawio' && doc.changes.length ? d?.drawio ?? null : null, doc?.baseline ?? null);
  if (!d) return null;
  const open = (view: DrawingView) => head && d.open({ path, view, anchor: head });
  const changed = !!doc && doc.changes.length > 0;
  const yours = doc?.format === 'drawio' ? mineDrawio : { picture: mine };
  const theirs = doc?.format === 'drawio' ? agentsDrawio : { picture: agents };
  const editable = d.writable && !!doc;
  const figure = (p: { picture: Picture | null; error?: string }, alt: string, view: DrawingView, label: string) =>
    editable ? (
      <button type="button" aria-label={label} className="block w-full cursor-pointer px-3 py-3 outline-hidden hover:bg-tint-hover focus-visible:outline-2 focus-visible:-outline-offset-2" onClick={() => open(view)}>
        <Figure picture={p.picture} error={p.error} alt={alt} />
      </button>
    ) : (
      <div className="px-3 py-3"><Figure picture={p.picture} error={p.error} alt={alt} /></div>
    );
  return (
    <div data-drawing={path} className="group/drawing relative my-2.5 overflow-hidden rounded-md bg-code-bg shadow-well">
      <div ref={setHead} className="flex min-h-7 items-center gap-2 px-3 py-1 text-caption text-muted">
        <span className="min-w-0 truncate font-mono text-code-sm text-file-link" title={path}>{fileName(path)}</span>
        {doc && <Chip fill="well">Agent’s v{doc.agent_version}</Chip>}
        {changed && <Chip tone="warning" fill="well">{pill(doc.version)} · {changesWord(doc.changes.length)}</Chip>}
        <span className="flex-1" />
        {changed && <Button size="sm" variant="subtle" className="h-6 px-1.5 text-caption font-normal" onClick={() => open('changes')}>Compare</Button>}
        {editable && <Button size="sm" variant="subtle" className="h-6 px-1.5 text-caption font-normal" onClick={() => open('mine')}>Edit</Button>}
      </div>
      {state?.error && <Note tone="error" role="alert" className="px-3 py-2">{state.error}</Note>}
      {doc && !changed && figure(yours, 'Drawing', 'mine', 'Edit drawing')}
      {doc && changed && (
        <div className="grid grid-cols-2 max-sm:grid-cols-1">
          <div className="flex flex-col border-r border-hairline max-sm:border-r-0 max-sm:border-b">
            <span className="px-3 pt-2 text-eyebrow uppercase text-muted">Agent’s v{doc.agent_version}</span>
            {figure(theirs, 'Agent’s version', 'agent', 'Agent’s version')}
          </div>
          <div className="flex flex-col">
            <span className="px-3 pt-2 text-eyebrow uppercase text-warning">{pill(doc.version)}</span>
            {figure(yours, 'Your version', 'mine', 'Edit your version')}
          </div>
        </div>
      )}
      {doc && changed && (
        <div className="flex items-center gap-2.5 border-t border-hairline px-3 py-1.5 text-caption text-muted">
          <span className="min-w-0 truncate text-ink">{changeSummary(doc.changes)}</span>
          <span className="flex-1" />
          <span className="shrink-0">Goes to the agent with your next message</span>
        </div>
      )}
      {!doc && !state?.error && <div className="flex min-h-24 items-center justify-center"><Spinner className="border-muted" /></div>}
    </div>
  );
}

/** Above the composer's field while a drawing has unsent changes: "<file> · k changes by you · View", one chip per drawing. */
export function DrawingChips() {
  const d = useContext(DrawingsContext);
  const changed = d?.drawings.filter((x) => x.changes_count > 0) ?? [];
  if (!d || changed.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1.5 px-3.5 pt-3">
      {changed.map((x) => (
        <span key={x.path} className="flex h-6 items-center gap-1.5 rounded-xs bg-warning-wash px-2 text-caption text-warning">
          <span className="min-w-0 truncate font-mono text-code-sm">{fileName(x.path)}</span>
          <span>· {changesWord(x.changes_count)} by you ·</span>
          <button type="button" aria-haspopup="dialog" aria-label={`View the changes to ${fileName(x.path)}`} className="rounded-xs font-medium hover:underline" onClick={(e) => d.open({ path: x.path, view: 'changes', anchor: e.currentTarget.parentElement ?? e.currentTarget })}>View</button>
        </span>
      ))}
    </div>
  );
}

