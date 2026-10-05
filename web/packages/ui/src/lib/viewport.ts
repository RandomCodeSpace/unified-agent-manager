/** Keyboard taller than this (CSS px) counts as open; smaller differences are browser chrome. */
const KEYBOARD_MIN = 100;

/**
 * Keeps the app shell on the visible part of the page. Mobile browsers (iOS WebKit always,
 * Chrome by default) open the on-screen keyboard over the page without resizing it or `dvh`,
 * then pan or scroll the page by their own guess, which can leave the composer far above the
 * keyboard. Here the shell takes the visual viewport's height (`--app-height`) and its top in
 * the page (`--app-top`, the window's scroll included), and `data-keyboard` on <html> marks an
 * open keyboard, which covers the bottom safe area. Once the keyboard closes, the page (which
 * never scrolls by itself) goes back to its top: iOS can leave it scrolled, the header under the
 * status bar and a gap under the composer. While pinch-zoomed nothing is touched, so zooming
 * still magnifies and pans.
 */
export function trackViewport(win: Window = window): () => void {
  const vv = win.visualViewport;
  if (!vv) return () => {};
  const root = win.document.documentElement;
  const sync = () => {
    if (Math.abs(vv.scale - 1) > 0.01) return;
    if (win.innerHeight - vv.height > KEYBOARD_MIN) {
      root.style.setProperty('--app-height', `${vv.height}px`);
      root.style.setProperty('--app-top', `${vv.pageTop}px`);
      root.dataset.keyboard = '';
      return;
    }
    root.style.removeProperty('--app-height');
    root.style.removeProperty('--app-top');
    delete root.dataset.keyboard;
    if (win.scrollY !== 0 || win.scrollX !== 0) win.scrollTo(0, 0);
  };
  vv.addEventListener('resize', sync);
  vv.addEventListener('scroll', sync);
  win.addEventListener('scroll', sync);
  sync();
  return () => {
    vv.removeEventListener('resize', sync);
    vv.removeEventListener('scroll', sync);
    win.removeEventListener('scroll', sync);
  };
}
