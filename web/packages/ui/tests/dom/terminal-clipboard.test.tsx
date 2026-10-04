import { afterEach, beforeEach, expect, test } from 'vitest';
import { mouseClipboard, type ClipboardNotice, type ClipboardTerminal } from '../../src/lib/terminal';

// The terminal itself needs WebGL, which the environment lacks: the mouse clipboard runs on a stand-in.
let host: HTMLElement;
let term: ClipboardTerminal & { pasted: string[]; selection: string; tracking: string; keys?: (e: KeyboardEvent) => boolean };
let notices: ClipboardNotice[];
let written: string[];
let clipboard: string | Error;
let stop: () => void;

function setup(mac = false) {
  host = document.body.appendChild(document.createElement('div'));
  const t = {
    pasted: [] as string[],
    selection: '',
    tracking: 'none',
    keys: undefined as ((e: KeyboardEvent) => boolean) | undefined,
    get modes() { return { mouseTrackingMode: t.tracking }; },
    hasSelection: () => t.selection !== '',
    getSelection: () => t.selection,
    paste: (data: string) => t.pasted.push(data),
    focus: () => {},
    attachCustomKeyEventHandler: (handler: (e: KeyboardEvent) => boolean) => { t.keys = handler; },
  };
  term = t;
  stop = mouseClipboard(term, host, mac, (n) => notices.push(n));
}

beforeEach(() => {
  notices = [];
  written = [];
  clipboard = 'echo pasted-ok';
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: async (text: string) => { written.push(text); },
      readText: async () => { if (clipboard instanceof Error) throw clipboard; return clipboard; },
    },
  });
  setup();
});

afterEach(() => {
  stop();
  host.remove();
});

const settle = () => new Promise((r) => setTimeout(r, 0));
const pointer = (button: number, pointerType = 'mouse', target: EventTarget = host) =>
  target.dispatchEvent(new PointerEvent('pointerdown', { button, pointerType, bubbles: true }));
const mouse = (type: string, button: number, init: MouseEventInit = {}, target: EventTarget = host) => {
  const e = new MouseEvent(type, { button, bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(e);
  return e;
};

test('releasing a mouse selection copies it, also outside the terminal', async () => {
  pointer(0);
  term.selection = 'hello-copy';
  mouse('mouseup', 0, {}, document);
  await settle();
  expect(written).toEqual(['hello-copy']);
  expect(notices).toEqual(['copied']);
  // A plain click (no selection) copies nothing.
  pointer(0);
  term.selection = '';
  mouse('mouseup', 0);
  await settle();
  expect(written).toEqual(['hello-copy']);
});

test('a touch never copies on release', async () => {
  pointer(0, 'touch');
  term.selection = 'hello-copy';
  mouse('mouseup', 0);
  await settle();
  expect(written).toEqual([]);
});

test('right-click pastes the clipboard instead of opening the menu', async () => {
  pointer(2);
  const e = mouse('contextmenu', 2);
  expect(e.defaultPrevented).toBe(true);
  await settle();
  expect(term.pasted).toEqual(['echo pasted-ok']);
});

test('middle-click pastes and stops the browser autoscroll and primary paste', async () => {
  pointer(1);
  expect(mouse('mousedown', 1).defaultPrevented).toBe(true);
  expect(mouse('mouseup', 1).defaultPrevented).toBe(true);
  await settle();
  expect(term.pasted).toEqual(['echo pasted-ok']);
});

test('a long-press keeps the browser menu and pastes nothing', async () => {
  pointer(0, 'touch');
  const e = mouse('contextmenu', 0);
  expect(e.defaultPrevented).toBe(false);
  await settle();
  expect(term.pasted).toEqual([]);
});

test('with a program tracking the mouse, clicks are its own unless Shift is held', async () => {
  term.tracking = 'any';
  pointer(2);
  expect(mouse('contextmenu', 2).defaultPrevented).toBe(true);
  expect(mouse('mouseup', 1).defaultPrevented).toBe(false);
  await settle();
  expect(term.pasted).toEqual([]);
  pointer(2);
  mouse('contextmenu', 2, { shiftKey: true });
  mouse('mouseup', 1, { shiftKey: true });
  await settle();
  expect(term.pasted).toEqual(['echo pasted-ok', 'echo pasted-ok']);
});

test('a denied clipboard read says to paste from the keyboard', async () => {
  clipboard = new DOMException('denied', 'NotAllowedError');
  pointer(2);
  mouse('contextmenu', 2);
  await settle();
  expect(term.pasted).toEqual([]);
  expect(notices).toEqual(['paste-blocked']);
});

test('Ctrl+Shift+C copies the selection off macOS; Ctrl+Shift+V is left to the browser', async () => {
  term.selection = 'hello-copy';
  const copyKey = new KeyboardEvent('keydown', { key: 'C', ctrlKey: true, shiftKey: true, cancelable: true });
  expect(term.keys!(copyKey)).toBe(false);
  expect(copyKey.defaultPrevented).toBe(true);
  await settle();
  expect(written).toEqual(['hello-copy']);
  expect(term.keys!(new KeyboardEvent('keydown', { key: 'V', ctrlKey: true, shiftKey: true }))).toBe(true);
  expect(term.keys!(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true }))).toBe(true);
  stop();
  host.remove();
  setup(true);
  expect(term.keys!(new KeyboardEvent('keydown', { key: 'C', ctrlKey: true, shiftKey: true }))).toBe(true);
});
