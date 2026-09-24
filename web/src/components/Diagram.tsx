import { useEffect, useState, type ReactNode } from 'react';
import { cn } from '../lib/cn';
import { DiagramError, renderDiagram, svgDataUrl, type Rendered } from '../lib/diagram';
import { Lightbox } from './Attachments';
import { CodeBlock, Note, Spinner } from './common';
import { Button } from './ui/button';

type View = 'diagram' | 'code';

/**
 * A fenced `mermaid` block: the code-block chrome with a Diagram / Code toggle. The diagram is
 * rendered in the sandboxed frame and shown only as a `data:` image, never as inline markup;
 * clicking it opens the lightbox. Until the fence has closed (`ready`), and whenever the source
 * does not parse, the code shows instead, the latter with a quiet note.
 */
export function DiagramCard({ source, ready, children }: { source: string; ready: boolean; children: ReactNode }) {
  const [view, setView] = useState<View>('diagram');
  const [result, setResult] = useState<{ source: string; rendered?: Rendered; error?: string } | null>(null);
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState(false);

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
  }, [source, ready]);

  const rendered = result?.source === source ? result.rendered : undefined;
  const error = result?.source === source ? result.error : undefined;
  const pending = ready && !rendered && !error;
  const url = rendered ? svgDataUrl(rendered.svg) : '';
  const image = rendered && view === 'diagram' && (
    <button
      type="button"
      className="block w-full cursor-zoom-in px-3 py-3 outline-hidden focus-visible:outline-2 focus-visible:-outline-offset-2 animate-fade-in"
      aria-label="Open diagram"
      onClick={() => {
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
            {toggle}
          </>
        }
        body={image || undefined}
        foot={error && <Note className={cn('border-t border-hairline px-3 py-1.5')}>Diagram could not be rendered: {error.split('\n')[0]}</Note>}
      >
        {children}
      </CodeBlock>
      {shown && rendered && <Lightbox open={open} onOpenChange={setOpen} onClosed={() => setShown(false)} title="Diagram" description="mermaid" src={url} alt="Diagram" />}
    </>
  );
}
