// The drawing surfaces that need Excalidraw or the draw.io exporter: the card in the
// conversation, the composer's chips, the pictures, and converting a fence. Drawing.tsx loads
// this module on demand (`lazy`), so a Thread without drawings never carries it.

import { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { describeError, type DrawingDoc } from '../api';
import { cn } from '../lib/cn';
import { convertDiagram } from '../lib/diagram';
import { exportDrawioPng } from '../lib/drawio';
import { changeSummary, changesWord, drawingPath, excalidrawFile, loadExcalidraw, parseExcalidraw, picture, styleSkeletons, toDrawioXml, token, type DrawnElement, type Picture, type Skeleton } from '../lib/drawings';
import { useScheme } from '../lib/theme';
import { Note, Spinner } from './common';
import { DrawingsContext, type DrawingsApi, type DrawingView } from './Drawing';
import type { Fence } from './Diagram';
import { Button } from './ui/button';
import { Chip } from './ui/chip';

/** Converts a fence in the frame, styles the skeletons in the page's look, and files the result as the agent's v1; the new drawing's path. */
export async function convertFence(drawings: DrawingsApi, source: string, fence: Fence): Promise<string> {
  const { elements } = await convertDiagram(source);
  const { convertToExcalidrawElements } = await loadExcalidraw();
  const converted = convertToExcalidrawElements(styleSkeletons(elements as Skeleton[]) as never, { regenerateIds: false }) as unknown as DrawnElement[];
  const format = drawings.drawio ? 'drawio' : 'excalidraw';
  const path = drawingPath(fence.itemId, fence.index, format);
  await drawings.create(path, { content: format === 'drawio' ? toDrawioXml(converted) : excalidrawFile(converted, {}), format, origin: 'convert', item_id: fence.itemId, fence_index: fence.index });
  return path;
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
export function DrawingCard({ path, edit }: Readonly<{ path: string; edit?: boolean }>) {
  const d = useContext(DrawingsContext);
  const [head, setHead] = useState<HTMLDivElement | null>(null);
  // `edit`: the card was just made by Edit as drawing, so the editor opens once the head is there to anchor it.
  const editOpened = useRef(false);
  useEffect(() => {
    if (!edit || !head || !d || editOpened.current) return;
    editOpened.current = true;
    d.open({ path, view: 'mine', anchor: head });
  }, [edit, head, d, path]);
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

/** Above the composer's field: "<file> · k changes by you · View", one chip per drawing with unsent changes (Drawing.tsx shows this only when there is one). */
export function DrawingChipRow() {
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
