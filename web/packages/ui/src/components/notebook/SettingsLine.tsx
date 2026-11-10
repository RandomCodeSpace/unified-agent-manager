// The Thread's settings as a labelled line under its title (the notebook layout, lib/layout). The
// controls and their state stay the composer's: the header gives an element (`SettingsLine`), the
// Thread hands it down (`SettingsLineContext`), and the composer draws its settings controls into
// it with a portal (`InSettingsLine`), each behind its name in words (`Setting`). So a control is
// one component in both layouts, and one added to the composer's row later shows in both. With no
// element (the current layout, a subagent's bar) the controls render where the composer wrote
// them, unwrapped.

import { createContext, useContext, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { cn } from '../../lib/cn';
import { useLayout } from '../../lib/layout';

/** The element under the Thread's title that takes the composer's settings controls; null where there is none. */
export const SettingsLineContext = createContext<HTMLElement | null>(null);

/**
 * The header's second line, in the notebook layout only: an empty element handed to `to`, which
 * the composer fills. It wraps instead of folding, so no control turns into a glyph; the
 * composer row's own separators are not drawn, since each control has its name before it.
 */
export function SettingsLine({ to }: Readonly<{ to: (el: HTMLElement | null) => void }>) {
  if (useLayout() !== 'notebook') return null;
  return <div ref={to} role="group" aria-label="Thread settings" className="relative z-10 flex min-w-0 shrink-0 flex-wrap items-center gap-x-2 px-2 empty:hidden sm:px-2.5 [&>span[aria-hidden=true]]:hidden" />;
}

/** The composer's settings controls: in the header's line when the Thread has one, else in place. */
export function InSettingsLine({ children }: Readonly<{ children: ReactNode }>) {
  const line = useContext(SettingsLineContext);
  return line ? createPortal(children, line) : children;
}

/** One control behind its name in words on the header's line; the control alone in the composer's row. A name whose control draws nothing is not shown. */
export function Setting({ label, className, children }: Readonly<{ label: string; className?: string; children: ReactNode }>) {
  if (!useContext(SettingsLineContext)) return children;
  return (
    <span className={cn('flex min-w-0 items-center', className)}>
      <span className="shrink-0 pl-1 text-caption text-muted only:hidden">{label}</span>
      {children}
    </span>
  );
}
