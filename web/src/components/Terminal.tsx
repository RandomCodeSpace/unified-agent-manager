import '@xterm/xterm/css/xterm.css';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import { Terminal, type ITheme } from '@xterm/xterm';
import { RotateCcw, SquareTerminal, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type { Project } from '../api';
import { exitCode, terminalUrl } from '../lib/terminal';
import { Button } from './ui/button';

/** Where the shell's socket stands, or the shell's exit code once it has exited. */
type Status = 'connecting' | 'connected' | 'disconnected' | 'failed' | 'webgl' | number;

const LABELS: Record<string, string> = { connecting: 'Connecting…', connected: 'Connected', disconnected: 'Disconnected' };

/**
 * The terminal panel's content, inside the Task's SidePanel: a shell in the Project folder. The shell
 * lives as long as its socket: closing the panel, Restart and leaving the Task end it. This chunk
 * uses Button and plain markup rather than PanelHeader, Note or Tip: the bundler moves modules that
 * two lazy chunks (this and Files) share with the page out of the page's bundle into another request.
 */
export default function TerminalPanel({ project, onClose }: { project: Project; onClose: () => void }) {
  const [status, setStatus] = useState<Status>('connecting');
  // Each shell is one mount of the screen: Restart and Retry remount it, which closes the old socket first.
  const [shell, setShell] = useState(0);
  const restart = () => {
    setStatus('connecting');
    setShell((n) => n + 1);
  };

  return (
    <>
      <div className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3">
        <SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <span className="text-title text-ink">Terminal</span>
        <span className="min-w-0 flex-1 truncate text-meta text-muted" title={project.dir}>{project.dir}</span>
        <span role="status" className="shrink-0 text-meta text-muted">{typeof status === 'number' ? `Exited (code ${status})` : LABELS[status]}</span>
        <Button size="md" className="px-2 text-muted" onClick={restart}>
          <RotateCcw />
          <span className="max-sm:sr-only">Restart</span>
        </Button>
        <Button size="icon-md" aria-label="Close terminal" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </div>
      {status === 'failed' && (
        <p role="alert" className="mx-3 mb-2 flex animate-fade-in flex-wrap items-center gap-2 text-caption text-error">
          <span className="min-w-0 flex-1">Could not open a terminal.</span>
          <Button size="sm" variant="secondary" onClick={restart}>Retry</Button>
        </p>
      )}
      {status === 'webgl'
        ? <p role="alert" className="mx-3 animate-fade-in text-caption text-error">The terminal needs WebGL, which this browser has turned off.</p>
        : <Screen key={shell} projectId={project.id} onStatus={setStatus} />}
    </>
  );
}

/** One shell: the terminal canvas and its socket, from mount to unmount. */
function Screen({ projectId, onStatus }: { projectId: string; onStatus: (status: Status) => void }) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const controller = new AbortController();
    let stop: (() => void) | undefined;
    void openTerminal(host.current!, projectId, onStatus, controller.signal).then((close) => {
      if (controller.signal.aborted) close();
      else stop = close;
    });
    return () => {
      controller.abort();
      stop?.();
    };
  }, [projectId, onStatus]);
  return <div ref={host} className="mx-2 mb-2 min-h-0 flex-1 overflow-hidden" />;
}

/**
 * The one theme (DESIGN.md Terminal): the panel's `canvas` under `ink`, and sixteen ANSI colours
 * at 5:1 or more on `canvas`, taken from the signal and badge tokens where one fits.
 */
const THEME: ITheme = {
  background: '#fcfcfd', // canvas
  foreground: '#25262b', // ink; index.css repeats it for the scrollbar slider
  cursor: '#25262b',
  cursorAccent: '#fcfcfd',
  selectionBackground: '#cfdaf3', // selection
  black: '#25262b', // ink
  red: '#b3261e', // error
  green: '#196a41', // success
  yellow: '#7a5500', // warning
  blue: '#2a55bd', // accent
  magenta: '#8e3a9a',
  cyan: '#0e7089', // badge-cyan
  white: '#494b53', // body
  brightBlack: '#686b77', // muted
  brightRed: '#b3352a', // badge-red
  brightGreen: '#287541', // badge-green
  brightYellow: '#876000', // badge-amber
  brightBlue: '#3260c4', // badge-blue
  brightMagenta: '#b0347c', // badge-pink
  brightCyan: '#13756b', // badge-teal
  brightWhite: '#25262b', // ink
};
/** index.css `--font-mono`. */
const FONT = "'JetBrains Mono Variable', ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace";
const FONT_SIZE = 13;

/**
 * Opens a terminal in `host` with a shell in the Project folder, unless `signal` aborts first; the
 * returned function closes the socket, which ends the shell, and disposes the terminal.
 */
async function openTerminal(host: HTMLElement, projectId: string, onStatus: (status: Status) => void, signal: AbortSignal): Promise<() => void> {
  // Cells are measured once, when the terminal opens, so the font has to be there first.
  await document.fonts.load(`${FONT_SIZE}px ${FONT}`).catch(() => undefined);
  return signal.aborted ? () => {} : connect(host, projectId, onStatus);
}

function connect(host: HTMLElement, projectId: string, onStatus: (status: Status) => void): () => void {
  // WebGL only, never the DOM renderer: its <style> elements are blocked by the CSP (DESIGN.md CSP
  // constraints). The addon loads before `open`, so the DOM renderer is never created; but a WebGL
  // failure inside `open` would silently fall back to it, hence the probe first.
  const probe = document.createElement('canvas').getContext('webgl2');
  if (!probe) {
    onStatus('webgl');
    return () => {};
  }
  probe.getExtension('WEBGL_lose_context')?.loseContext();
  const term = new Terminal({ theme: THEME, fontFamily: FONT, fontSize: FONT_SIZE });
  const fit = new FitAddon();
  term.loadAddon(fit);
  try {
    const webgl = new WebglAddon();
    webgl.onContextLoss(() => {
      stop();
      onStatus('webgl');
    });
    term.loadAddon(webgl);
  } catch {
    term.dispose();
    onStatus('webgl');
    return () => {};
  }
  term.open(host);
  fit.fit();

  const ws = new WebSocket(terminalUrl(window.location, projectId, term.cols, term.rows));
  ws.binaryType = 'arraybuffer';
  let sent = { cols: term.cols, rows: term.rows };
  let opened = false;
  let exited = false;
  const resize = () => {
    if (ws.readyState !== WebSocket.OPEN || (term.cols === sent.cols && term.rows === sent.rows)) return;
    sent = { cols: term.cols, rows: term.rows };
    ws.send(JSON.stringify({ type: 'resize', ...sent }));
  };
  ws.onopen = () => {
    opened = true;
    onStatus('connected');
    resize();
  };
  ws.onmessage = (e: MessageEvent<ArrayBuffer | string>) => {
    if (typeof e.data !== 'string') {
      term.write(new Uint8Array(e.data));
      return;
    }
    const code = exitCode(e.data);
    if (code === null) return;
    exited = true;
    onStatus(code);
  };
  // The upgrade's refusals (404, 409, 429) reach the page only as a close before open.
  ws.onclose = () => {
    if (!exited) onStatus(opened ? 'disconnected' : 'failed');
  };
  const encoder = new TextEncoder();
  const send = (bytes: Uint8Array<ArrayBuffer>) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(bytes);
  };
  term.onData((data) => send(encoder.encode(data)));
  term.onBinary((data) => send(Uint8Array.from(data, (c) => c.charCodeAt(0))));
  term.onResize(resize);
  let frame = 0;
  const observer = new ResizeObserver(() => {
    if (!frame) frame = requestAnimationFrame(() => {
      frame = 0;
      fit.fit();
    });
  });
  observer.observe(host);
  term.focus();

  let stopped = false;
  function stop() {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(frame);
    observer.disconnect();
    ws.onclose = null;
    ws.close();
    term.dispose();
  }
  return stop;
}
