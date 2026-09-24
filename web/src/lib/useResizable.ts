import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type RefObject } from 'react';

/** The chat column keeps at least this much room beside an inline panel. */
const COLUMN_MIN = 480;
const RAIL = 264;
const STEP = 16;

export interface Resizable {
  /** Put on the panel; its width is the `--panel-w` custom property set here through CSSOM (no inline markup). */
  panelRef: RefObject<HTMLElement | null>;
  width: number;
  min: number;
  max: number;
  /** Spread on the drag handle: pointer, keyboard and double-click resizing. */
  handleProps: {
    role: 'separator';
    tabIndex: number;
    'aria-orientation': 'vertical';
    'aria-valuenow': number;
    'aria-valuemin': number;
    'aria-valuemax': number;
    'aria-label': string;
    onPointerDown: (e: PointerEvent<HTMLElement>) => void;
    onPointerMove: (e: PointerEvent<HTMLElement>) => void;
    onPointerUp: (e: PointerEvent<HTMLElement>) => void;
    onPointerCancel: (e: PointerEvent<HTMLElement>) => void;
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => void;
    onDoubleClick: () => void;
  };
}

/**
 * A right-hand panel whose width the user drags on its inner edge. The width is written
 * straight to a CSS custom property during the drag (no React re-render per move, no
 * layout thrash) and saved to localStorage per panel when the drag ends. Double-click
 * resets; arrow keys step 16px (64 with Shift); Home/End go to the limits.
 */
export function useResizable(key: string, fallback: number, minWidth = 320): Resizable {
  const storageKey = `uam.panel.${key}`;
  const panelRef = useRef<HTMLElement | null>(null);
  const [max, setMax] = useState(() => maxFor(minWidth));
  const [stored, setStored] = useState(() => read(storageKey) ?? fallback);
  // The effective width is derived, so a viewport change re-clamps without touching state.
  const width = clamp(stored, minWidth, max);
  const drag = useRef<{ x: number; w: number } | null>(null);

  useEffect(() => {
    const onResize = () => setMax(maxFor(minWidth));
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [minWidth]);

  const apply = useCallback((w: number) => {
    panelRef.current?.style.setProperty('--panel-w', `${w}px`);
  }, []);

  // Keep the element in step with the effective width (mount, reset, keyboard, viewport change).
  useEffect(() => {
    apply(width);
  }, [width, apply]);

  const commit = useCallback(
    (w: number) => {
      const next = clamp(w, minWidth, max);
      setStored(next);
      apply(next);
      localStorage.setItem(storageKey, String(next));
    },
    [minWidth, max, apply, storageKey],
  );

  const handleProps: Resizable['handleProps'] = {
    role: 'separator',
    tabIndex: 0,
    'aria-orientation': 'vertical',
    'aria-valuenow': width,
    'aria-valuemin': minWidth,
    'aria-valuemax': max,
    'aria-label': 'Resize panel',
    onPointerDown: (e) => {
      if (e.button !== 0) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      drag.current = { x: e.clientX, w: width };
      document.body.classList.add('cursor-col-resize', 'select-none');
    },
    onPointerMove: (e) => {
      if (!drag.current) return;
      apply(clamp(drag.current.w + (drag.current.x - e.clientX), minWidth, max));
    },
    onPointerUp: (e) => {
      if (!drag.current) return;
      const w = clamp(drag.current.w + (drag.current.x - e.clientX), minWidth, max);
      drag.current = null;
      document.body.classList.remove('cursor-col-resize', 'select-none');
      commit(w);
    },
    onPointerCancel: () => {
      if (!drag.current) return;
      drag.current = null;
      document.body.classList.remove('cursor-col-resize', 'select-none');
      apply(width);
    },
    onKeyDown: (e) => {
      const step = e.shiftKey ? STEP * 4 : STEP;
      let next: number | null = null;
      if (e.key === 'ArrowLeft') next = width + step;
      else if (e.key === 'ArrowRight') next = width - step;
      else if (e.key === 'Home') next = max;
      else if (e.key === 'End') next = minWidth;
      else if (e.key === 'Enter') next = fallback;
      if (next === null) return;
      e.preventDefault();
      commit(next);
    },
    onDoubleClick: () => commit(fallback),
  };

  return { panelRef, width, min: minWidth, max, handleProps };
}

function maxFor(min: number): number {
  return Math.max(min, Math.min(880, window.innerWidth - RAIL - COLUMN_MIN));
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, Math.round(v)));
}

function read(key: string): number | null {
  const v = Number(localStorage.getItem(key));
  return Number.isFinite(v) && v > 0 ? v : null;
}
