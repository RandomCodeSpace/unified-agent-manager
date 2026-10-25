// The Excalidraw editor, in its own chunk: Drawing.tsx loads this module on the first drawing
// opened (`React.lazy`), so the cold start never carries it. Its asset path is set before the
// package evaluates (lib/drawings.ts `loadExcalidraw`), so its fonts come from this origin.

import { Excalidraw, MainMenu } from '@excalidraw/excalidraw';
import type { ExcalidrawImperativeAPI } from '@excalidraw/excalidraw/types';
import '@excalidraw/excalidraw/index.css';
import { useCallback, useRef, useState } from 'react';
import type { DrawnElement } from '../lib/drawings';
import { NUNITO } from '../lib/drawings';

const I = MainMenu.DefaultItems;

/** Excalidraw's own menu, minus the two items that reach outside: social links and live collaboration. */
function FullMenu() {
  return (
    <MainMenu>
      <I.LoadScene />
      <I.SaveToActiveFile />
      <I.Export />
      <I.SaveAsImage />
      <I.SearchMenu />
      <I.CommandPalette />
      <I.Help />
      <I.ClearCanvas />
      <MainMenu.Separator />
      <I.ToggleTheme />
      <I.ChangeCanvasBackground />
    </MainMenu>
  );
}

export interface CanvasProps {
  elements: readonly DrawnElement[];
  files: Record<string, unknown>;
  theme: 'light' | 'dark';
  /** The canvas colour: the `raised` token. */
  background: string;
  /** Every change the user made, debounced 200 ms, with the live elements and files. */
  onChange: (elements: DrawnElement[], files: Record<string, unknown>) => void;
  /** The scene as Excalidraw restored it (added defaults, re-measured text) on mount or after `load`: the text the next change compares against. */
  onRestored: (elements: DrawnElement[], files: Record<string, unknown>) => void;
  /** The imperative API once it is here, and `load`, which puts a file's elements on the canvas as a restore, not a change. */
  onApi: (api: ExcalidrawImperativeAPI, load: (elements: readonly DrawnElement[]) => void) => void;
}

export type { ExcalidrawImperativeAPI };

export default function ExcalidrawCanvas({ elements, files, theme, background, onChange, onRestored, onApi }: Readonly<CanvasProps>) {
  const timer = useRef(0);
  // Excalidraw reports the scene it restored (on mount, after `load`) as changes: only what
  // follows the user's first pointer, key, drop or paste is one.
  const touched = useRef(false);
  const touch = () => {
    touched.current = true;
  };
  // Excalidraw mutates the elements it is given, so it never gets the owner's copy.
  const [initial] = useState(() => ({ elements: structuredClone(elements) as never, files: files as never, appState: { viewBackgroundColor: background, currentItemRoughness: 0, currentItemFontFamily: NUNITO, currentItemStrokeWidth: 1.5 }, scrollToContent: true }));
  const changed = useCallback<NonNullable<Parameters<typeof Excalidraw>[0]['onChange']>>((els, _state, f) => {
    clearTimeout(timer.current);
    const live = () => structuredClone(els.filter((e) => !e.isDeleted)) as unknown as DrawnElement[];
    if (!touched.current) return onRestored(live(), { ...f });
    timer.current = window.setTimeout(() => onChange(live(), { ...f }), 200);
  }, [onChange, onRestored]);
  const ready = useCallback((api: ExcalidrawImperativeAPI) => onApi(api, (next) => {
    touched.current = false;
    api.updateScene({ elements: structuredClone(next) as never });
  }), [onApi]);
  return (
    <div className="h-full w-full" onPointerDownCapture={touch} onKeyDownCapture={touch} onDropCapture={touch} onPasteCapture={touch}>
      {/* The editor is the reader's reason to open: the keys (R, A, Del) must work at once, so the canvas takes focus. */}
      {/* eslint-disable-next-line jsx-a11y/no-autofocus */}
      <Excalidraw initialData={initial} theme={theme} autoFocus excalidrawAPI={ready} onChange={changed}>
        <FullMenu />
      </Excalidraw>
    </div>
  );
}
