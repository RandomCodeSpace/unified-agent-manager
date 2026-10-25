import { useId } from 'react';
import { cn } from '../lib/cn';
import { fileKind } from '../lib/fileIcons';
import { FILE_MARKS } from '../lib/fileMarks';

/** A file's type mark in its own colours (Material Icon Theme), a folder's with `folder`. */
export function FileIcon({ path, folder = false, className }: Readonly<{ path: string; folder?: boolean; className?: string }>) {
  const u = useId();
  const mark = FILE_MARKS[fileKind(path, folder)];
  return (
    <svg viewBox={mark.viewBox} aria-hidden="true" className={cn('size-4 shrink-0', className)}>
      {mark.draw(u)}
    </svg>
  );
}
