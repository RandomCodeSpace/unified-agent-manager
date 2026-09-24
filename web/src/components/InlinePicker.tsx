import { useEffect, useRef, type ReactNode } from 'react';
import { cn } from '../lib/cn';
import { Loading } from './common';

export interface PickerItem {
  /** Stable key; also the option's DOM id suffix. */
  key: string;
  /** Group label rendered above the first item of each run. */
  group?: string;
  label: string;
  disabled?: boolean;
  render: ReactNode;
}

/**
 * The composer's inline popover for `/` and `@`: a listbox above the textarea that the
 * textarea drives through `aria-activedescendant`. Focus never leaves the textarea; the
 * keyboard is handled there, the mouse and touch here (`mousedown` is cancelled so a click
 * does not blur the field). Level 2 floating surface; rows are 30px, 44px on touch.
 */
export function InlinePicker({
  id,
  title,
  items,
  highlighted,
  loading = false,
  empty,
  note,
  onHighlight,
  onPick,
  popupRef,
}: {
  id: string;
  title: string;
  items: PickerItem[];
  highlighted: number;
  loading?: boolean;
  /** Shown instead of rows when there are none and nothing is loading. */
  empty?: ReactNode;
  /** A line under the rows (a reason, an error). */
  note?: ReactNode;
  onHighlight: (index: number) => void;
  onPick: (item: PickerItem) => void;
  popupRef: React.RefObject<HTMLDivElement | null>;
}) {
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`#${CSS.escape(`${id}-${items[highlighted]?.key ?? ''}`)}`)?.scrollIntoView({ block: 'nearest' });
  }, [highlighted, id, items]);
  // A press anywhere in the popover (rows, header, scrollbar) must not take focus from the textarea.
  useEffect(() => {
    const el = popupRef.current;
    if (!el) return;
    const keep = (e: MouseEvent) => e.preventDefault();
    el.addEventListener('mousedown', keep);
    return () => el.removeEventListener('mousedown', keep);
  }, [popupRef]);

  const rows = items.map((item, i) => ({ item, label: item.group !== items[i - 1]?.group ? item.group : undefined }));
  return (
    <div
      ref={popupRef}
      data-popup=""
      className="absolute bottom-full left-0 z-40 mb-1.5 flex w-full origin-bottom-left flex-col overflow-hidden rounded-md border border-hairline-strong bg-raised text-ink shadow-float animate-rise sm:w-[440px]"
    >
      <div className="flex h-7 shrink-0 items-center gap-2 px-3 text-caption text-muted">
        <span className="font-medium">{title}</span>
        <span className="flex-1" />
        <span aria-hidden="true" className="flex items-center gap-1.5 pointer-coarse:hidden">
          <Key>↑↓</Key>
          <Key>↵</Key>
          <Key>esc</Key>
        </span>
      </div>
      <div ref={list} id={id} role="listbox" aria-label={title} className="max-h-[min(300px,40dvh)] overflow-y-auto p-1 pt-0">
        {loading && items.length === 0 && <Loading className="px-2" />}
        {!loading && items.length === 0 && empty && <div className="px-2 py-2 text-caption text-muted">{empty}</div>}
        {rows.map(({ item, label }, i) => {
          return (
            <div key={item.key} className="contents">
              {label && <div className="px-2 pt-2 pb-1 text-caption text-muted">{label}</div>}
              <button
                type="button"
                role="option"
                tabIndex={-1}
                id={`${id}-${item.key}`}
                aria-selected={i === highlighted}
                aria-disabled={item.disabled || undefined}
                aria-label={item.label}
                className={cn(
                  'flex min-h-[30px] w-full cursor-default items-center gap-2 rounded-sm px-2 py-1 text-left text-ui text-body outline-hidden transition-colors duration-100 pointer-coarse:min-h-11 [&_svg]:size-4 [&_svg]:shrink-0',
                  i === highlighted && 'bg-canvas text-ink',
                  item.disabled && 'text-muted',
                )}
                onMouseMove={() => i !== highlighted && onHighlight(i)}
                onClick={() => onPick(item)}
              >
                {item.render}
              </button>
            </div>
          );
        })}
      </div>
      {note && <div className="border-t border-hairline px-3 py-1.5 text-caption text-muted">{note}</div>}
    </div>
  );
}

function Key({ children }: { children: ReactNode }) {
  return <kbd className="inline-flex h-4 min-w-4 items-center justify-center rounded-xs bg-sunken px-1 font-mono text-keycap text-muted">{children}</kbd>;
}
