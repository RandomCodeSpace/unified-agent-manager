// Drawings (DESIGN.md Drawing card, Drawing editor): a mermaid fence the user turned into a
// `.excalidraw` (or `.drawio`) file. The card in the conversation shows the agent's version, or
// the pair once the user has unsent changes; the editor is the anchored reader under the card's
// head, Excalidraw (or the draw.io embed) in Yours, read-only pictures in Agent's and Changes.
// The server keeps versions and diffs them; nothing is counted here.

import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, taskName, type Drawing, type DrawingDoc, type DrawingPut, type DrawingSaved, type SessionDetail } from '../api';
import { useApp } from './common';
import { PHONE } from './Todos';

// Everything that draws (the cards, the chips, the editor, Excalidraw behind them) loads on the
// first drawing shown or made; this module keeps only the Thread's list and the loaders.
const DrawingEditor = lazy(() => import('./DrawingEditor'));
const DrawingCardLazy = lazy(() => import('./DrawingCards').then((m) => ({ default: m.DrawingCard })));
const DrawingChipRow = lazy(() => import('./DrawingCards').then((m) => ({ default: m.DrawingChipRow })));

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
  // A Thread without drawings has no notes to count, so its items are not scanned.
  const notes = useMemo(() => (drawings.length === 0 ? 0 : session.items.reduce((n, item) => n + (item.kind === 'notice' && !!item.text?.startsWith('The user edited ') ? 1 : 0), 0)), [drawings.length, session.items]);
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

/** A drawing's card in the conversation (DrawingCards.tsx), the block's chrome holding its place while that loads. */
export function DrawingCard({ path, edit }: Readonly<{ path: string; edit?: boolean }>) {
  return (
    <Suspense fallback={<div className="my-2.5 min-h-24 rounded-md bg-code-bg shadow-well" />}>
      <DrawingCardLazy path={path} edit={edit} />
    </Suspense>
  );
}

/** Above the composer's field: the chips of the drawings with unsent changes, loaded only while there is one. */
export function DrawingChips() {
  const d = useContext(DrawingsContext);
  if (!d?.drawings.some((x) => x.changes_count > 0)) return null;
  return (
    <Suspense fallback={null}>
      <DrawingChipRow />
    </Suspense>
  );
}

