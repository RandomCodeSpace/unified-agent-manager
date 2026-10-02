import { copyText } from './clipboard.ts';

/** The Project's terminal socket on the page's own host (ws, or wss under https), opened at the terminal's first size. */
export function terminalUrl(page: { protocol: string; host: string }, projectId: string, cols: number, rows: number): string {
  return `${page.protocol === 'https:' ? 'wss' : 'ws'}://${page.host}/api/projects/${encodeURIComponent(projectId)}/terminal?cols=${cols}&rows=${rows}`;
}

/** The shell's exit code from a text frame (`{"type":"exit","code":N}`); null for anything else. */
export function exitCode(text: string): number | null {
  try {
    const message = JSON.parse(text) as { type?: unknown; code?: unknown };
    return message.type === 'exit' && Number.isInteger(message.code) ? (message.code as number) : null;
  } catch {
    return null;
  }
}

/** What the terminal's mouse clipboard needs from xterm.js's Terminal. */
export interface ClipboardTerminal {
  readonly modes: { readonly mouseTrackingMode: string };
  hasSelection(): boolean;
  getSelection(): string;
  paste(data: string): void;
  focus(): void;
  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void;
}

/** A short-lived line for the panel header: a selection was copied, or the browser would not let a click paste. */
export type ClipboardNotice = 'copied' | 'paste-blocked';

/**
 * Windows Terminal and PuTTY's mouse clipboard on the terminal in `host`: releasing a mouse
 * selection copies it, right-click and middle-click paste the clipboard (through xterm.js's paste,
 * so bracketed paste applies), and Ctrl+Shift+C copies off macOS (Cmd+C is xterm.js's own copy).
 * A program tracking the mouse gets its clicks unless Shift is held, as for xterm.js's selection.
 * Touch is left alone: a long-press keeps the browser's own menu and never pastes. Returns the
 * function that removes the listeners.
 */
export function mouseClipboard(term: ClipboardTerminal, host: HTMLElement, mac: boolean, notify: (notice: ClipboardNotice) => void): () => void {
  const copy = () => {
    const text = term.getSelection();
    if (!text) return;
    void copyText(text).then((ok) => {
      if (ok) notify('copied');
      // copyText's fallback selects a textarea of its own; give the keys back to the shell.
      if (document.activeElement === document.body) term.focus();
    });
  };
  const paste = async () => {
    try {
      const text = await navigator.clipboard.readText();
      if (text) term.paste(text);
    } catch {
      // No async clipboard (an insecure context), or reading it denied: keyboard paste still works.
      notify('paste-blocked');
    }
  };
  const ours = (e: MouseEvent) => term.modes.mouseTrackingMode === 'none' || e.shiftKey;

  let selecting = false;
  let rightDown = false;
  const onPointerDown = (e: PointerEvent) => {
    selecting = e.pointerType === 'mouse' && e.button === 0;
    rightDown = e.pointerType === 'mouse' && e.button === 2;
  };
  // On the document: a drag may end outside the terminal.
  const onMouseUp = (e: MouseEvent) => {
    if (e.button !== 0 || !selecting) return;
    selecting = false;
    if (term.hasSelection()) copy();
  };
  const onContextMenu = (e: MouseEvent) => {
    // A long-press or the keyboard's menu key keeps the browser's menu.
    if (!rightDown) return;
    rightDown = false;
    e.preventDefault();
    e.stopPropagation();
    if (ours(e)) void paste();
  };
  // Holding the middle button down would start autoscroll, and releasing it would paste the
  // primary selection on Linux as well.
  const onMiddle = (e: MouseEvent) => {
    if (e.button !== 1 || !ours(e)) return;
    e.preventDefault();
    if (e.type === 'mouseup') void paste();
  };
  term.attachCustomKeyEventHandler((e) => {
    if (mac || !e.ctrlKey || !e.shiftKey || e.altKey || e.metaKey || e.key.toLowerCase() !== 'c') return true;
    if (e.type === 'keydown') {
      e.preventDefault();
      copy();
    }
    return false;
  });
  host.addEventListener('pointerdown', onPointerDown, true);
  host.addEventListener('contextmenu', onContextMenu, true);
  host.addEventListener('mousedown', onMiddle, true);
  host.addEventListener('mouseup', onMiddle, true);
  document.addEventListener('mouseup', onMouseUp);
  return () => {
    host.removeEventListener('pointerdown', onPointerDown, true);
    host.removeEventListener('contextmenu', onContextMenu, true);
    host.removeEventListener('mousedown', onMiddle, true);
    host.removeEventListener('mouseup', onMiddle, true);
    document.removeEventListener('mouseup', onMouseUp);
  };
}
