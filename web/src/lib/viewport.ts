/** Keyboard taller than this (CSS px) counts as open; smaller differences are browser chrome. */
const KEYBOARD_MIN = 100;

/**
 * Keeps the app shell on the visible part of the page. Mobile browsers (iOS WebKit always,
 * Chrome by default) open the on-screen keyboard over the page without resizing it or `dvh`,
 * then pan the page by their own guess, which can leave the composer far above the keyboard.
 * Here the shell takes the visual viewport's height (`--app-height`) and offset (`--app-top`),
 * and `data-keyboard` on <html> marks an open keyboard, which covers the bottom safe area.
 * While pinch-zoomed the shell keeps the layout viewport, so zooming still magnifies.
 */
export function trackViewport(win: Window = window): () => void {
  const vv = win.visualViewport;
  if (!vv) return () => {};
  const root = win.document.documentElement;
  const sync = () => {
    const zoomed = Math.abs(vv.scale - 1) > 0.01;
    const keyboard = !zoomed && win.innerHeight - vv.height > KEYBOARD_MIN;
    if (keyboard) {
      root.style.setProperty('--app-height', `${vv.height}px`);
      root.style.setProperty('--app-top', `${vv.offsetTop}px`);
      root.dataset.keyboard = '';
    } else {
      root.style.removeProperty('--app-height');
      root.style.removeProperty('--app-top');
      delete root.dataset.keyboard;
    }
  };
  vv.addEventListener('resize', sync);
  vv.addEventListener('scroll', sync);
  sync();
  return () => {
    vv.removeEventListener('resize', sync);
    vv.removeEventListener('scroll', sync);
  };
}
