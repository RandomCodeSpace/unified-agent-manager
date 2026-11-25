import { lazy, Suspense, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { useApi } from '../ApiContext';
import type { ApiClient } from '../api';
import { cn } from '../lib/cn';
import { DiagramError, errorReason, renderDiagram, svgDataUrl, type Rendered } from '../lib/diagram';
import { usePreview } from '../lib/previewContext';
import { useScheme } from '../lib/theme';
import { Lightbox } from './Attachments';
import { CodeBlock, Note, SessionContext, Spinner } from './common';
import { Button } from './ui/button';
import type { ReactNode } from 'react';
import type { FlowGraph } from '../lib/flowGraph';

const FlowDiagram = lazy(() => import('./FlowDiagram'));

type View = 'flow' | 'mermaid' | 'code';
const VIEW_LABELS: Record<View, string> = { flow: 'React Flow', mermaid: 'Mermaid', code: 'Code' };

/** A diagram's header, after any front matter, directives and comments. */
const header = (types: string) => new RegExp(String.raw`^(?:\s*---\n[\s\S]*?\n---\s*\n)?(?:\s*%%.*\n|\s*\n)*\s*(?:${types})\b`);
/** A flowchart: the React Flow view reads it itself. */
const FLOWCHART = header('flowchart|graph');
/** A box-and-arrow diagram: the Utility model can turn one the view cannot read into its graph. */
const GRAPH = header('flowchart|graph|stateDiagram|classDiagram|erDiagram|mindmap|block|architecture|requirementDiagram|C4\\w*');

/** Why the React Flow view could not draw the source: Pele cannot read it, it is too big, or the model's conversion failed. */
export type FlowMiss = 'unreadable' | 'too-big' | { error: string };

// One conversion per source; a failed one is forgotten so the next look asks again.
const converted = new Map<string, Promise<FlowGraph>>();
function convertGraph(api: ApiClient, sessionId: string, source: string): Promise<FlowGraph> {
  let graph = converted.get(source);
  if (!graph) {
    graph = api.diagramGraph(sessionId, source);
    converted.set(source, graph);
    graph.catch(() => converted.delete(source));
  }
  return graph;
}

/**
 * A fenced `mermaid` block: the code-block chrome with a React Flow / Mermaid / Code toggle. A
 * box-and-arrow diagram opens in the React Flow view (FlowDiagram.tsx): Pele reads a flowchart, and
 * in a Thread the Utility model converts any other one, or a flowchart Pele cannot read. When neither
 * can, or the conversion fails, the card falls back to the Mermaid view, which also takes every other
 * diagram. The Mermaid view is rendered in the sandboxed frame and shown only as a `data:` image, never as inline markup, which opens the lightbox on a click. Mermaid renders
 * only once its view is shown. Until the fence has closed (`ready`), and whenever Mermaid cannot
 * parse the source in its view, the code shows instead, the latter with a quiet note.
 */
export function DiagramCard({ source, ready, children }: Readonly<{ source: string; ready: boolean; children: ReactNode }>) {
  const preview = usePreview();
  const api = useApi();
  const sessionId = useContext(SessionContext);
  const [miss, setMiss] = useState<FlowMiss | null>(null);
  const readable = FLOWCHART.test(source) && miss === null;
  const failed = miss !== null && typeof miss === 'object';
  const flowable = readable || (GRAPH.test(source) && !!sessionId && miss !== 'too-big');
  const [chosen, setView] = useState<View | null>(null);
  const view: View = chosen === 'flow' && !flowable ? 'mermaid' : (chosen ?? (flowable && !failed ? 'flow' : 'mermaid'));
  const unavailable = useCallback((why: FlowMiss) => {
    setMiss(why);
    if (typeof why === 'object') setView(null);
  }, []);
  const convert = useMemo(() => (sessionId ? () => convertGraph(api, sessionId, source) : undefined), [sessionId, api, source]);
  const [result, setResult] = useState<{ source: string; rendered?: Rendered; error?: string } | null>(null);
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState(false);
  // The SVG carries the scheme's colours, so a scheme change renders it again.
  const scheme = useScheme();

  useEffect(() => {
    if (!ready || view !== 'mermaid') return;
    let on = true;
    renderDiagram(source).then(
      (rendered) => on && setResult({ source, rendered }),
      (err: unknown) => on && setResult({ source, error: err instanceof DiagramError ? err.message : 'Rendering failed' }),
    );
    return () => {
      on = false;
    };
  }, [source, ready, scheme, view]);

  const rendered = result?.source === source ? result.rendered : undefined;
  const error = result?.source === source ? result.error : undefined;
  const pending = ready && view === 'mermaid' && !rendered && !error;
  const url = rendered ? svgDataUrl(rendered.svg) : '';

  const image = rendered && view === 'mermaid' && (
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
  const flow = ready && view === 'flow' && (
    <Suspense fallback={<div className="h-[120px]" />}>
      <FlowDiagram source={source} onUnavailable={unavailable} convert={convert} />
    </Suspense>
  );
  const views: View[] = flowable ? ['flow', 'mermaid', 'code'] : ['mermaid', 'code'];
  const toggle = ready && (
    <div role="group" aria-label="View" className="flex items-center gap-0.5">
      {views.map((v) => (
        <Button key={v} size="sm" className="h-5 px-1.5 text-code-sm font-normal text-muted" aria-pressed={view === v} onClick={() => setView(v)}>
          {VIEW_LABELS[v]}
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
            {toggle}
          </>
        }
        body={flow || image || undefined}
        foot={
          view === 'mermaid' && (error || failed) && (
            <>
              {error && <Note className={cn('px-3 py-1.5')}>Diagram could not be rendered: {errorReason(error)}</Note>}
              {failed && <Note className={cn('px-3 py-1.5')}>React Flow view unavailable: {miss.error}</Note>}
            </>
          )
        }
      >
        {children}
      </CodeBlock>
      {!preview && shown && rendered && <Lightbox open={open} onOpenChange={setOpen} onClosed={() => setShown(false)} title="Diagram" description="mermaid" src={url} alt="Diagram" size={{ width: rendered.width, height: rendered.height }} />}
    </>
  );
}
