import { useContext, useEffect, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError } from '../api';
import { cn } from '../lib/cn';
import { DiagramError, convertDiagram, convertible, errorReason, renderDiagram, svgDataUrl, type Rendered } from '../lib/diagram';
import { drawingPath, excalidrawFile, loadExcalidraw, styleSkeletons, toDrawioXml, type DrawnElement, type Skeleton } from '../lib/drawings';
import { usePreview } from '../lib/previewContext';
import { useScheme } from '../lib/theme';
import { Lightbox } from './Attachments';
import { CodeBlock, Note, Spinner } from './common';
import { DrawingCard, DrawingsContext, fenceDrawing } from './Drawing';
import { Button } from './ui/button';

type View = 'diagram' | 'code';

/** Which reply and which of its mermaid fences a block is, when known: what its drawing is filed under. */
export interface Fence {
  itemId: string;
  index: number;
}

/**
 * A fenced `mermaid` block: the code-block chrome with a Diagram / Code toggle. The diagram is
 * rendered in the sandboxed frame and shown only as a `data:` image, never as inline markup;
 * clicking it opens the lightbox. Until the fence has closed (`ready`), and whenever the source
 * does not parse, the code shows instead, the latter with a quiet note. A flowchart or sequence
 * diagram in a writable Thread offers "Edit as drawing"; a fence whose drawing exists is the
 * drawing's card instead.
 */
export function DiagramCard({ source, ready, fence, children }: Readonly<{ source: string; ready: boolean; fence?: Fence; children: ReactNode }>) {
  const drawings = useContext(DrawingsContext);
  const [created, setCreated] = useState<string | null>(null);
  const existing = fenceDrawing(drawings?.drawings, fence?.itemId, fence?.index ?? -1)?.path ?? created;
  if (existing) return <DrawingCard path={existing} />;
  return <MermaidCard source={source} ready={ready} fence={fence} onCreated={setCreated}>{children}</MermaidCard>;
}

function MermaidCard({ source, ready, fence, onCreated, children }: Readonly<{ source: string; ready: boolean; fence?: Fence; onCreated: (path: string) => void; children: ReactNode }>) {
  const api = useApi();
  const preview = usePreview();
  const drawings = useContext(DrawingsContext);
  const [view, setView] = useState<View>('diagram');
  const [result, setResult] = useState<{ source: string; rendered?: Rendered; error?: string } | null>(null);
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState(false);
  const [convert, setConvert] = useState<{ busy: boolean; error?: string }>({ busy: false });
  // The SVG carries the scheme's colours, so a scheme change renders it again.
  const scheme = useScheme();

  useEffect(() => {
    if (!ready) return;
    let on = true;
    renderDiagram(source).then(
      (rendered) => on && setResult({ source, rendered }),
      (err: unknown) => on && setResult({ source, error: err instanceof DiagramError ? err.message : 'Rendering failed' }),
    );
    return () => {
      on = false;
    };
  }, [source, ready, scheme]);

  const rendered = result?.source === source ? result.rendered : undefined;
  const error = result?.source === source ? result.error : undefined;
  const pending = ready && !rendered && !error;
  const url = rendered ? svgDataUrl(rendered.svg) : '';
  const editable = !!drawings?.writable && !!fence && !!rendered && convertible(rendered.diagramType) && api.supports('drawings-v1');

  /** Converts in the frame, styles the skeletons in the page's look, and files the result as the agent's v1. */
  async function edit() {
    if (!drawings || !fence) return;
    setConvert({ busy: true });
    try {
      const { elements } = await convertDiagram(source);
      const { convertToExcalidrawElements } = await loadExcalidraw();
      const converted = convertToExcalidrawElements(styleSkeletons(elements as Skeleton[]) as never, { regenerateIds: false }) as unknown as DrawnElement[];
      const format = drawings.drawio ? 'drawio' : 'excalidraw';
      const path = drawingPath(fence.itemId, fence.index, format);
      await drawings.create(path, { content: format === 'drawio' ? toDrawioXml(converted) : excalidrawFile(converted, {}), format, origin: 'convert', item_id: fence.itemId, fence_index: fence.index });
      onCreated(path);
    } catch (err) {
      setConvert({ busy: false, error: err instanceof DiagramError ? errorReason(err.message) : describeError(err) });
    }
  }

  const image = rendered && view === 'diagram' && (
    <button
      type="button"
      className="block w-full cursor-zoom-in px-3 py-3 outline-hidden focus-visible:outline-2 focus-visible:-outline-offset-2 animate-fade-in"
      aria-label="Open diagram"
      onClick={event => {
        if (preview) {
          preview({ url, name: 'Diagram', description: 'mermaid', image: true, original: false, size: { width: rendered.width, height: rendered.height } }, event.currentTarget);
          return;
        }
        setShown(true);
        setOpen(true);
      }}
    >
      <img src={url} width={rendered.width} height={rendered.height} alt="Diagram" decoding="async" className="mx-auto block h-auto max-h-[480px] w-auto max-w-full object-contain" />
    </button>
  );
  const toggle = rendered && (
    <div role="group" aria-label="View" className="flex items-center gap-0.5">
      {(['diagram', 'code'] as const).map((v) => (
        <Button key={v} size="sm" className="h-5 px-1.5 text-code-sm font-normal text-muted" aria-pressed={view === v} onClick={() => setView(v)}>
          {v === 'diagram' ? 'Diagram' : 'Code'}
        </Button>
      ))}
    </div>
  );
  return (
    <>
      <CodeBlock
        language="mermaid"
        text={source}
        head={
          <>
            {pending && <Spinner className="border-muted" />}
            <span className="flex-1" />
            {editable && (
              <Button size="sm" className="h-5 px-1.5 text-code-sm font-normal text-muted" loading={convert.busy} onClick={() => void edit()}>
                Edit as drawing
              </Button>
            )}
            {toggle}
          </>
        }
        body={image || undefined}
        foot={
          <>
            {error && <Note className={cn('px-3 py-1.5')}>Diagram could not be rendered: {errorReason(error)}</Note>}
            {convert.error && <Note tone="error" role="alert" className="px-3 py-1.5">Could not make a drawing: {convert.error}</Note>}
          </>
        }
      >
        {children}
      </CodeBlock>
      {!preview && shown && rendered && <Lightbox open={open} onOpenChange={setOpen} onClosed={() => setShown(false)} title="Diagram" description="mermaid" src={url} alt="Diagram" size={{ width: rendered.width, height: rendered.height }} />}
    </>
  );
}
