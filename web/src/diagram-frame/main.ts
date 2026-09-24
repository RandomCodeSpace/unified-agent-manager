import mermaid from 'mermaid';
import { DIAGRAM_FONT, intrinsicSize, parseRequest, type DiagramReply } from '../lib/diagram';

/**
 * The diagram frame: a classic script (module scripts cannot load from an opaque origin
 * without CORS) in `/diagram-frame.html`, sandboxed with `allow-scripts` only. It renders
 * Mermaid on request from its parent and posts the SVG back; it can reach nothing else.
 */

function configure(theme: Record<string, string>) {
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    htmlLabels: false,
    flowchart: { htmlLabels: false },
    class: { htmlLabels: false },
    // Notes spanning two participants have a fixed width; wrap their text to fit it.
    sequence: { wrap: true },
    theme: 'base',
    themeVariables: { fontFamily: DIAGRAM_FONT, ...theme },
    // The global family and size also become the sequence diagram's actor, note and message
    // fonts, which otherwise default to "Open Sans" at 16px; 14 matches the theme's fontSize.
    fontFamily: DIAGRAM_FONT,
    fontSize: 14,
    suppressErrorRendering: true,
    maxTextSize: 100_000,
    maxEdges: 1_000,
  });
}

async function handle(id: string, source: string, theme: Record<string, string>): Promise<DiagramReply> {
  try {
    configure(theme);
    const { svg } = await mermaid.render(id, source);
    const sized = intrinsicSize(svg);
    if (!sized) return { id, error: 'The diagram has no size' };
    return { id, ...sized };
  } catch (err) {
    return { id, error: err instanceof Error ? err.message : String(err) };
  }
}

window.addEventListener('message', (e: MessageEvent) => {
  if (e.source !== window.parent) return;
  const req = parseRequest(e.data);
  if (!req) return;
  void handle(req.id, req.source, req.theme).then((reply) => window.parent.postMessage(reply, e.origin));
});
