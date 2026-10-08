import type { OutputLine } from '../api';
import { cn } from '../lib/cn';

/**
 * A running command's newest output lines (DESIGN.md live step), oldest first: 10 at most, 7 on a
 * phone, in a box that tall at most, the older ones clipped above it; it never scrolls. A stderr
 * line carries "err" in its gutter as well as the error tone, so colour is not the only sign. It
 * is not a live region: a screen reader would read every line as it arrives.
 */
export function LiveOutput({ lines, className }: Readonly<{ lines?: OutputLine[]; className?: string }>) {
  if (!lines?.length) return null;
  return (
    // 18px rows (code-sm) between 4px borders, not padding: a clipped line never shows in them.
    <pre translate="no" role="group" aria-label="Live output, last lines" className={cn('flex max-h-[188px] flex-col justify-end overflow-hidden rounded-sm border-y-4 border-transparent bg-code-bg pr-2 font-mono text-code-sm max-sm:max-h-[134px]', className)}>
      {lines.map((line, i) => (
        <span key={i} className="flex shrink-0 gap-2">
          <span aria-hidden="true" className={cn('w-[4ch] shrink-0 text-right select-none', line.err ? 'font-medium text-error' : 'text-faint')}>{line.err ? 'err' : ''}</span>
          <span className={cn('min-w-0 flex-1 whitespace-pre-wrap [overflow-wrap:anywhere]', line.err ? 'text-error' : 'text-body')}>
            {line.err && <span className="sr-only">stderr: </span>}
            {line.text || ' '}
          </span>
        </span>
      ))}
    </pre>
  );
}
