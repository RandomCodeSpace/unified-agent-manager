// draw.io (opt-in, Settings → Diagram editor): the editor is an embedded page at the checked
// origin, spoken to with its JSON protocol. The pictures a drawing card shows come from one
// hidden exporter frame, which loads an XML and asks for a PNG, one request at a time.

import type { Picture } from './drawings';

/** The embed page: JSON protocol, no exit button, no "save and exit", the version beacon off (`stealth`). */
export const drawioEmbedUrl = (origin: string): string => `${origin.replace(/\/+$/, '')}/?embed=1&proto=json&spin=1&stealth=1&noExitBtn=1&saveAndExit=0`;

export const DRAWIO_SANDBOX = 'allow-scripts allow-same-origin allow-downloads';

/** How long an export may take before the picture says it did not answer. */
export const EXPORT_TIMEOUT = 5_000;

/** One message from the embed, as far as this module reads it. */
export function drawioMessage(data: unknown): { event: string; xml?: string; data?: string } | null {
  if (typeof data === 'string') {
    try {
      data = JSON.parse(data);
    } catch {
      return null;
    }
  }
  if (typeof data !== 'object' || data === null || typeof (data as { event?: unknown }).event !== 'string') return null;
  const m = data as { event: string; xml?: unknown; data?: unknown };
  return { event: m.event, ...(typeof m.xml === 'string' ? { xml: m.xml } : {}), ...(typeof m.data === 'string' ? { data: m.data } : {}) };
}

/** A data URL's picture with its own size. */
export function measure(url: string): Promise<Picture> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve({ url, width: img.naturalWidth, height: img.naturalHeight });
    img.onerror = () => reject(new Error('The exported picture could not be read'));
    img.src = url;
  });
}

interface Job {
  xml: string;
  resolve: (p: Picture) => void;
  reject: (e: Error) => void;
}

let exporter: { origin: string; frame: HTMLIFrameElement; ready: Promise<void> } | null = null;
const jobs: Job[] = [];
let busy = false;

function ensureExporter(origin: string) {
  if (exporter && exporter.origin === origin) return exporter;
  exporter?.frame.remove();
  const frame = document.createElement('iframe');
  frame.setAttribute('sandbox', DRAWIO_SANDBOX);
  frame.setAttribute('aria-hidden', 'true');
  frame.tabIndex = -1;
  frame.title = 'draw.io exporter';
  frame.className = 'pointer-events-none fixed top-0 left-0 -z-10 h-150 w-200 opacity-0';
  const ready = new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('The draw.io page did not start')), EXPORT_TIMEOUT * 4);
    const onMessage = (e: MessageEvent) => {
      if (e.source !== frame.contentWindow) return;
      if (drawioMessage(e.data)?.event !== 'init') return;
      clearTimeout(timer);
      window.removeEventListener('message', onMessage);
      resolve();
    };
    window.addEventListener('message', onMessage);
  });
  frame.src = drawioEmbedUrl(origin);
  document.body.append(frame);
  exporter = { origin, frame, ready };
  return exporter;
}

async function run(origin: string, job: Job) {
  const { frame, ready } = ensureExporter(origin);
  try {
    await ready;
    const target = frame.contentWindow;
    if (!target) throw new Error('The draw.io page is gone');
    const url = await new Promise<string>((resolve, reject) => {
      const timer = setTimeout(() => {
        window.removeEventListener('message', onMessage);
        reject(new Error('draw.io did not answer the export in time'));
      }, EXPORT_TIMEOUT);
      const onMessage = (e: MessageEvent) => {
        if (e.source !== target) return;
        const m = drawioMessage(e.data);
        if (m?.event === 'load') target.postMessage(JSON.stringify({ action: 'export', format: 'png' }), '*');
        if (m?.event !== 'export' || !m.data) return;
        clearTimeout(timer);
        window.removeEventListener('message', onMessage);
        resolve(m.data);
      };
      window.addEventListener('message', onMessage);
      target.postMessage(JSON.stringify({ action: 'load', xml: job.xml }), '*');
    });
    job.resolve(await measure(url));
  } catch (err) {
    job.reject(err instanceof Error ? err : new Error(String(err)));
  }
}

async function drain(origin: string) {
  if (busy) return;
  busy = true;
  try {
    while (jobs.length) await run(origin, jobs.shift()!);
  } finally {
    busy = false;
  }
}

/** A PNG of a draw.io file, exported by the embed at `origin`; one export at a time, each given `EXPORT_TIMEOUT`. */
export function exportDrawioPng(origin: string, xml: string): Promise<Picture> {
  return new Promise((resolve, reject) => {
    jobs.push({ xml, resolve, reject });
    void drain(origin);
  });
}
