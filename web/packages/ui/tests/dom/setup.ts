// Component test environment: happy-dom plus the few browser APIs it lacks or leaves inert.
// Every test starts with fresh storage, an empty URL fragment and no rendered tree.
import { cleanup, configure } from '@testing-library/react';
import { afterEach, beforeEach } from 'vitest';

// The in-browser mock answers after 60 ms per request; a flow of several requests needs more than 1 s.
configure({
  asyncUtilTimeout: 4000,
  // Failures name what was missing; the whole-document dump would bury it.
  getElementError: (message) => Object.assign(new Error(message?.split('\n\n')[0] ?? ''), { name: 'TestingLibraryElementError' }),
});

// Happy DOM 20.14.5 queues hashchange from History.replaceState via Location.setURL.
// Browsers do not: https://html.spec.whatwg.org/dev/browsing-the-web.html#url-and-history-update-steps
// Suppress only those synthetic events; location.hash navigation still reaches the app.
const replacedHashes: { oldURL: string; newURL: string }[] = [];
const replaceState = history.replaceState.bind(history);
history.replaceState = (...args: Parameters<History['replaceState']>) => {
  const oldURL = window.location.href;
  replaceState(...args);
  const newURL = window.location.href;
  if (new URL(oldURL).hash !== new URL(newURL).hash) replacedHashes.push({ oldURL, newURL });
};
window.addEventListener('hashchange', event => {
  const index = replacedHashes.findIndex(entry => entry.oldURL === event.oldURL && entry.newURL === event.newURL);
  if (index < 0) return;
  replacedHashes.splice(index, 1);
  event.stopImmediatePropagation();
}, { capture: true });

// The mock draws stored screenshots on a 2D canvas; the environment has no canvas backend.
// WebGL stays absent (getContext('webgl2') is null), as on a browser with it turned off.
const getContext = HTMLCanvasElement.prototype.getContext;
HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, kind: string, ...rest: unknown[]) {
  if (kind !== '2d') return getContext.call(this, kind as '2d', ...(rest as []));
  return new Proxy({}, {
    get: (_t, key) => key === 'canvas' ? this : key === 'measureText'
      // ECharts uses text metrics even with its SVG renderer; layout assertions run in a real browser.
      ? (text: string) => ({ width: String(text).length * 7, actualBoundingBoxAscent: 10, actualBoundingBoxDescent: 3 })
      : () => undefined,
    set: () => true,
  });
} as typeof HTMLCanvasElement.prototype.getContext;

// The terminal waits for its font through the CSS Font Loading API, which happy-dom lacks.
Object.defineProperty(document, 'fonts', { configurable: true, value: { load: async () => [] } });

// happy-dom has no async clipboard (nor execCommand for the app's fallback): Copy gets a granted write.
Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => {}, readText: async () => '' } });

const saved = { fetch: window.fetch, EventSource: window.EventSource, WebSocket: window.WebSocket, XMLHttpRequest: window.XMLHttpRequest };

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  history.replaceState(null, '', '/');
});

afterEach(() => {
  cleanup();
  Object.assign(window, saved);
});
