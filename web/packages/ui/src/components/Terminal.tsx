import { useApi } from '../ApiContext';
import '@xterm/xterm/css/xterm.css';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import { Terminal, type ITheme } from '@xterm/xterm';
import { RotateCcw, SquareTerminal, X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { ApiClient, Project } from '../api';
import { exitCode, mouseClipboard, type ClipboardNotice } from '../lib/terminal';
import { Button } from './ui/button';
import { AlertDialog, useConfirm } from './ui/dialog';

/** Where the shell's socket stands, or the shell's exit code once it has exited. */
type Status = 'connecting' | 'connected' | 'disconnected' | 'failed' | 'webgl' | number;

const LABELS: Record<string, string> = { connecting: 'Connecting…', connected: 'Connected', disconnected: 'Disconnected' };

const MAC = navigator.platform.startsWith('Mac');
const NOTICES: Record<ClipboardNotice, string> = {
  copied: 'Copied',
  'paste-blocked': `Clipboard blocked. Use ${MAC ? 'Cmd+V' : 'Ctrl+Shift+V'} to paste.`,
};

/**
 * The terminal dock's content (App.tsx TerminalDock, under the Task view): a shell in the Project
 * folder. The shell lives as long as its socket: Close and Restart end it; switching Tasks does not. This chunk
 * uses Button and plain markup rather than PanelHeader, Note or Tip: the bundler moves modules that
 * two lazy chunks (this and Files) share with the page out of the page's bundle into another request.
 */
export default function TerminalPanel({ project, onClose }: Readonly<{ project: Project; onClose: () => void }>) {
  const [status, setStatus] = useState<Status>('connecting');
  // Each shell is one mount of the screen: Restart and Retry remount it, which closes the old socket first.
  const [shell, setShell] = useState(0);
  const restart = () => {
    setStatus('connecting');
    setShell((n) => n + 1);
  };
  // A connected shell may be running something, and Restart and Close both kill it: they confirm first.
  const ending = useConfirm<'restart' | 'close'>();
  const end = (action: 'restart' | 'close') => {
    if (status === 'connected') ending.ask(action);
    else if (action === 'restart') restart();
    else onClose();
  };
  const [notice, setNotice] = useState<ClipboardNotice | null>(null);
  const timer = useRef<number>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const notify = useCallback((next: ClipboardNotice) => {
    setNotice(next);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setNotice(null), next === 'copied' ? 1400 : 6000);
  }, []);

  return (
    <>
      <div className="flex h-9 shrink-0 items-center gap-1.5 pr-1.5 pl-3">
        <SquareTerminal aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <span className="text-title text-ink">Terminal</span>
        <span className="min-w-0 flex-1 truncate text-meta text-muted" title={project.dir}>{project.dir}</span>
        <span role="status" className="min-w-0 truncate text-meta text-muted">{notice && NOTICES[notice]}</span>
        <output className="shrink-0 text-meta text-muted">{typeof status === 'number' ? `Exited (code ${status})` : LABELS[status]}</output>
        <Button size="sm" className="text-muted" onClick={() => end('restart')}>
          <RotateCcw />
          <span className="max-sm:sr-only">Restart</span>
        </Button>
        <span aria-hidden="true" className="fade-rule-y mx-1 h-5 w-px shrink-0" />
        <Button size="icon" aria-label="Close terminal" className="text-muted" onClick={() => end('close')}>
          <X />
        </Button>
      </div>
      <AlertDialog
        {...ending.props}
        title={ending.target === 'close' ? 'Close the terminal?' : 'Restart the terminal?'}
        description={`This ends the shell and whatever runs in it${ending.target === 'close' ? '' : ', then starts a new shell'}.`}
        confirmLabel={ending.target === 'close' ? 'Close terminal' : 'Restart'}
        onConfirm={() => {
          ending.close();
          if (ending.target === 'close') onClose();
          else restart();
        }}
      />
      {status === 'failed' && (
        <p role="alert" className="mx-3 mb-2 flex animate-fade-in flex-wrap items-center gap-2 text-caption text-error">
          <span className="min-w-0 flex-1">Could not open a terminal.</span>
          <Button size="sm" variant="secondary" onClick={restart}>Retry</Button>
        </p>
      )}
      {status === 'webgl'
        ? <p role="alert" className="mx-3 animate-fade-in text-caption text-error">The terminal needs WebGL, which this browser has turned off.</p>
        : <Screen key={shell} projectId={project.id} onStatus={setStatus} onNotice={notify} />}
    </>
  );
}

/** One shell: the terminal canvas and its socket, from mount to unmount. */
function Screen({ projectId, onStatus, onNotice }: Readonly<{ projectId: string; onStatus: (status: Status) => void; onNotice: (notice: ClipboardNotice) => void }>) {
  const api = useApi();
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const controller = new AbortController();
    let stop: (() => void) | undefined;
    void openTerminal(api, host.current!, projectId, onStatus, onNotice, controller.signal).then((close) => {
      if (controller.signal.aborted) close();
      else stop = close;
    }).catch(() => { if (!controller.signal.aborted) onStatus('failed'); });
    return () => {
      controller.abort();
      stop?.();
    };
  }, [api, projectId, onStatus, onNotice]);
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
const FONT_SIZE = 14;
const LINE_HEIGHT = 1.25;

/**
 * Opens a terminal in `host` with a shell in the Project folder, unless `signal` aborts first; the
 * returned function closes the socket, which ends the shell, and disposes the terminal.
 */
async function openTerminal(api: ApiClient, host: HTMLElement, projectId: string, onStatus: (status: Status) => void, onNotice: (notice: ClipboardNotice) => void, signal: AbortSignal): Promise<() => void> {
  // Cells are measured once, when the terminal opens, so the font has to be there first.
  await document.fonts.load(`${FONT_SIZE}px ${FONT}`).catch(() => undefined);
  return signal.aborted ? () => {} : connect(api, host, projectId, onStatus, onNotice);
}

/**
 * `term.open`, minus the one `<style>` element xterm.js's viewport appends as it opens (its scrollbar
 * slider colours). The CSP blocks it (DESIGN.md CSP constraints) and index.css carries the same rules,
 * so for the length of the call a `style` element is created as an inert `<template>`: nothing is blocked
 * and nothing is reported.
 */
function openWithoutStyle(term: Terminal, host: HTMLElement) {
  const create = document.createElement.bind(document);
  document.createElement = ((tag: string, options?: ElementCreationOptions) => create(tag.toLowerCase() === 'style' ? 'template' : tag, options)) as typeof document.createElement;
  try {
    term.open(host);
  } finally {
    Reflect.deleteProperty(document, 'createElement');
  }
}

function connect(api: ApiClient, host: HTMLElement, projectId: string, onStatus: (status: Status) => void, onNotice: (notice: ClipboardNotice) => void): () => void {
  // WebGL only, never the DOM renderer: its <style> elements are blocked by the CSP (DESIGN.md CSP
  // constraints). The addon loads before `open`, so the DOM renderer is never created; but a WebGL
  // failure inside `open` would silently fall back to it, hence the probe first.
  const probe = document.createElement('canvas').getContext('webgl2');
  if (!probe) {
    onStatus('webgl');
    return () => {};
  }
  probe.getExtension('WEBGL_lose_context')?.loseContext();
  // Right-click pastes (mouseClipboard), so it does not select a word first as on macOS by default.
  const term = new Terminal({ theme: THEME, fontFamily: FONT, fontSize: FONT_SIZE, lineHeight: LINE_HEIGHT, rightClickSelectsWord: false });
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
  openWithoutStyle(term, host);
  fit.fit();

  const path = api.url(`/api/projects/${encodeURIComponent(projectId)}/terminal?cols=${term.cols}&rows=${term.rows}`);
  const ws = new WebSocket(`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}${path}`);
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
  term.onBinary((data) => send(Uint8Array.from(data, (c) => c.codePointAt(0) ?? 0)));
  term.onResize(resize);
  let frame = 0;
  const observer = new ResizeObserver(() => {
    if (!frame) frame = requestAnimationFrame(() => {
      frame = 0;
      fit.fit();
    });
  });
  observer.observe(host);
  const unclip = mouseClipboard(term, host, MAC, onNotice);
  term.focus();

  let stopped = false;
  function stop() {
    if (stopped) return;
    stopped = true;
    cancelAnimationFrame(frame);
    observer.disconnect();
    unclip();
    ws.onclose = null;
    ws.close();
    term.dispose();
  }
  return stop;
}
