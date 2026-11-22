import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';
import { inputVariants } from './input';

/**
 * DESIGN.md textarea: the Input's tonal well (`inputVariants` without a height), `ui` text, 3 rows by default,
 * resizable on its height only. A form field, not the composer.
 */
export function Textarea({ className, rows = 3, ...props }: ComponentProps<'textarea'>) {
  return <textarea rows={rows} className={cn(inputVariants({ size: null }), 'block min-h-16 resize-y px-2.5 py-2 text-ui', className)} {...props} />;
}
