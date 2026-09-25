import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import type { CSSProperties, ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface Segment {
  value: string;
  label: ReactNode;
}

/**
 * A segmented control on Base UI's radio group: one inset `sunken` track (the `well` ring)
 * with equal-width segments and one floating `raised` thumb that slides to the chosen one
 * on its transform (`base`; it snaps under Motion: Match system). The segment count and the
 * chosen index reach the thumb as custom properties through the CSSOM (React's `style`
 * prop), never as inline markup. Arrow keys move the choice; the group carries the label.
 * `sm` (28px, `caption`) fits a panel header.
 */
export function Segmented({
  value,
  onValueChange,
  items,
  disabled,
  size = 'md',
  className,
  'aria-label': ariaLabel,
  'aria-labelledby': labelledBy,
  'aria-describedby': describedBy,
}: {
  value: string;
  onValueChange: (value: string) => void;
  items: Segment[];
  disabled?: boolean;
  size?: 'sm' | 'md';
  className?: string;
  'aria-label'?: string;
  'aria-labelledby'?: string;
  'aria-describedby'?: string;
}) {
  const index = Math.max(0, items.findIndex((it) => it.value === value));
  const vars = { '--seg-n': items.length, '--seg-i': index } as CSSProperties;
  return (
    <RadioGroup
      value={value}
      onValueChange={(v) => onValueChange(v as string)}
      disabled={disabled}
      aria-label={ariaLabel}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      style={vars}
      className={cn('relative isolate grid shrink-0 auto-cols-fr grid-flow-col rounded-sm bg-sunken p-0.5 shadow-well', size === 'sm' ? 'h-7 pointer-coarse:h-9' : 'h-8 pointer-coarse:h-12', className)}
    >
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-y-0.5 left-0.5 -z-10 w-[calc((100%-4px)/var(--seg-n))] translate-x-[calc(100%*var(--seg-i))] rounded-xs bg-raised shadow-raised transition-transform duration-160 ease-app"
      />
      {items.map((it) => (
        <Radio.Root
          key={it.value}
          value={it.value}
          className={cn(
            'flex h-full cursor-pointer items-center justify-center rounded-xs font-medium whitespace-nowrap text-muted transition-colors duration-100 hover:text-ink focus-visible:-outline-offset-2 data-checked:text-ink data-disabled:cursor-not-allowed data-disabled:opacity-45',
            size === 'sm' ? 'px-2 text-caption' : 'min-w-16 px-3 text-ui',
          )}
        >
          {it.label}
        </Radio.Root>
      ))}
    </RadioGroup>
  );
}
