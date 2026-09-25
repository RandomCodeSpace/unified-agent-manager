import mermaid from 'mermaid';
import figtree from '@fontsource-variable/figtree/files/figtree-latin-wght-normal.woff2?inline';
import { DIAGRAM_FONT, intrinsicSize, parseRequest, type DiagramReply } from '../lib/diagram';

// The page's font, bundled as a data URL: the frame cannot fetch the page's font files (an
// opaque origin fails CORS), so it builds the face from bytes, which is no fetch at all, and
// embeds the same data URL in each SVG, since an SVG shown as an image loads nothing else.
const FONT_WEIGHTS = '300 900';
const fontFace = `@font-face{font-family:'Figtree Variable';font-weight:${FONT_WEIGHTS};src:url(${figtree}) format('woff2')}`;
const fontReady = (async () => {
  const bytes = Uint8Array.from(atob(figtree.slice(figtree.indexOf(',') + 1)), (c) => c.charCodeAt(0));
  document.fonts.add(await new FontFace('Figtree Variable', bytes, { weight: FONT_WEIGHTS }).load());
})().catch(() => {}); // Without it Mermaid measures and draws with the system fallback in DIAGRAM_FONT.

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
    await fontReady;
    const { svg } = await mermaid.render(id, source);
    const sized = intrinsicSize(svg);
    if (!sized) return { id, error: 'The diagram has no size' };
    return { id, ...sized, svg: sized.svg.replace(/^<svg\b[^>]*>/, (tag) => `${tag}<style>${fontFace}</style>`) };
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
