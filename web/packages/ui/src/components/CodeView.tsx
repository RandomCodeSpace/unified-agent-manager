import { useEffect, useMemo, useRef, useState } from 'react';
import { cn } from '../lib/cn';
import type { LensChange } from '../lib/lens';
import { Highlighted } from './common';

/** The highlight.js name for a path: its extension, or the whole name for `Makefile` and the like. */
export function language(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  return name.slice(name.lastIndexOf('.') + 1);
}

/** Lines highlighted per frame: a few KiB of code, so a 64 KiB preview never holds the page in one task. */
const CODE_SLICE = 150;

/**
 * Code with a line-number gutter that stays put while long lines scroll sideways; nothing wraps. The
 * whole text shows at once, plain; highlighting follows a slice per frame from the top. With `changes`
 * (Lens) the Thread's changes are marked on the file: added lines tinted, removed ones a pill away.
 */
export function CodeView({ path, text, line, changes, focus, onFocus }: Readonly<{
  path: string;
  text: string;
  line?: number;
  changes?: readonly LensChange[];
  /** The change in view, ringed and scrolled to. */
  focus?: number;
  onFocus?: (index: number) => void;
}>) {
  const slices = useMemo(() => {
    const lines = (text.endsWith('\n') ? text.slice(0, -1) : text).split('\n');
    const out: string[] = [];
    for (let i = 0; i < lines.length; i += CODE_SLICE) out.push(lines.slice(i, i + CODE_SLICE).join('\n') + (i + CODE_SLICE < lines.length ? '\n' : ''));
    return { lines: lines.length, parts: out };
  }, [text]);
  const [lit, setLit] = useState({ text, count: 1 });
  const count = lit.text === text ? lit.count : 1;
  useEffect(() => {
    if (count >= slices.parts.length) return;
    const frame = requestAnimationFrame(() => setLit({ text, count: count + 1 }));
    return () => cancelAnimationFrame(frame);
  }, [count, slices.parts.length, text]);
  // A cited line (`a.go:12`, `#L12`) is marked and scrolled into view once per text; a focused change wins.
  const focused = focus !== undefined ? changes?.[focus] : undefined;
  const target = focused ? Math.min(focused.line, slices.lines) : line;
  const marked = !focused && line !== undefined && line >= 1 && line <= slices.lines ? line : undefined;
  const mark = useRef<HTMLLIElement>(null);
  useEffect(() => { mark.current?.scrollIntoView({ block: 'center' }); }, [target, text]);
  const added = useMemo(() => {
    const set = new Set<number>();
    for (const c of changes ?? []) for (let i = 0; i < c.added; i++) set.add(c.line + i);
    return set;
  }, [changes]);
  const [opened, setOpened] = useState<number | null>(null);
  const top = (n: number) => ({ top: `calc(0.5rem + ${Math.min(n, slices.lines + 1) - 1}lh)` });
  return (
    <div className="relative grid w-max min-w-full grid-cols-[auto_minmax(0,1fr)] py-2 font-mono text-code">
      {marked && <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 h-[1lh] bg-accent/10" style={top(marked)} />}
      {changes?.map((c, i) => (
        <div key={`${c.hunk}-${c.run}`} aria-hidden="true" className={cn('pointer-events-none absolute inset-x-0', c.added ? 'bg-diff-add-bg shadow-[inset_3px_0_0_var(--color-success)]' : 'h-0.5 bg-error', i === focus && 'outline-1 outline-focus')} style={{ ...top(c.line), height: c.added ? `${c.added}lh` : undefined }} />
      ))}
      <ol aria-hidden="true" className="sticky left-0 z-10 bg-canvas pr-3 pl-3 text-right text-faint tabular-nums select-none">
        {Array.from({ length: slices.lines }, (_, i) => <li key={i} ref={i + 1 === target ? mark : undefined} className={cn(i + 1 === marked && 'font-medium text-ink', added.has(i + 1) && 'font-medium text-success')}>{i + 1}</li>)}
      </ol>
      {/* relative: paints the code over the absolute change bands before it. */}
      <pre translate="no" className="relative pr-6 whitespace-pre text-ink"><code>{slices.parts.map((part, i) => (i < count ? <Highlighted key={i} language={language(path)} code={part} /> : part))}</code></pre>
      {changes?.map((c, i) => c.removed.length > 0 && (
        <div key={`was-${c.hunk}-${c.run}`} className="absolute right-2 z-20 flex flex-col items-end" style={top(c.line)}>
          <button
            type="button"
            aria-expanded={opened === i}
            aria-label={`${c.removed.length} ${c.removed.length === 1 ? 'line' : 'lines'} removed here: ${opened === i ? 'hide' : 'show'}`}
            className="h-[1lh] rounded-full bg-error-wash px-2 font-sans text-meta leading-none text-error hover:bg-diff-del-bg"
            onClick={() => { setOpened(opened === i ? null : i); onFocus?.(i); }}
          >
            −{c.removed.length}
          </button>
          {opened === i && (
            <pre translate="no" className="mt-1 max-w-[min(48rem,80vw)] overflow-x-auto rounded-sm bg-diff-del-bg px-3 py-1 text-diff-del-text shadow-float">{c.removed.join('\n')}</pre>
          )}
        </div>
      ))}
    </div>
  );
}
