import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface Segment {
  value: string;
  label: ReactNode;
}

/**
 * A segmented control on Base UI's radio group: one `sunken` track, the chosen segment
 * raised one step (DESIGN.md). Arrow keys move the choice; the group carries the label.
 */
export function Segmented({
  value,
  onValueChange,
  items,
  disabled,
  className,
  'aria-label': ariaLabel,
  'aria-labelledby': labelledBy,
  'aria-describedby': describedBy,
}: {
  value: string;
  onValueChange: (value: string) => void;
  items: Segment[];
  disabled?: boolean;
  className?: string;
  'aria-label'?: string;
  'aria-labelledby'?: string;
  'aria-describedby'?: string;
}) {
  return (
    <RadioGroup
      value={value}
      onValueChange={(v) => onValueChange(v as string)}
      disabled={disabled}
      aria-label={ariaLabel}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      className={cn('inline-flex h-8 shrink-0 items-center gap-0.5 rounded-sm bg-sunken p-0.5 pointer-coarse:h-12', className)}
    >
      {items.map((it) => (
        <Radio.Root
          key={it.value}
          value={it.value}
          className="flex h-full min-w-16 cursor-pointer items-center justify-center rounded-xs px-3 text-ui font-medium text-muted transition-[background-color,color,box-shadow] duration-100 hover:text-ink focus-visible:-outline-offset-2 data-checked:bg-raised data-checked:text-ink data-checked:shadow-raised data-disabled:cursor-not-allowed data-disabled:opacity-45"
        >
          {it.label}
        </Radio.Root>
      ))}
    </RadioGroup>
  );
}
