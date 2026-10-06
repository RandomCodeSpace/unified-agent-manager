import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import type { CSSProperties, ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface Segment {
  value: string;
  label: ReactNode;
}

/**
 * A segmented control on Base UI's radio group: one compact inset `sunken` track (24px, the
 * `well` ring, 1px padding) with equal-width segments, their `caption` labels centred, and one
 * floating `raised` thumb that slides to the chosen one on its transform (`base`; it snaps
 * under Motion: Match system). The segment count and the chosen index reach the thumb as
 * custom properties through the CSSOM (React's `style` prop), never as inline markup. Arrow
 * keys move the choice; the group carries the label. `size` is accepted for its callers; both
 * sizes are this one track.
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
}: Readonly<{
  value: string;
  onValueChange: (value: string) => void;
  items: Segment[];
  disabled?: boolean;
  size?: 'sm' | 'md';
  className?: string;
  'aria-label'?: string;
  'aria-labelledby'?: string;
  'aria-describedby'?: string;
}>) {
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
      className={cn('relative isolate grid h-6 shrink-0 auto-cols-fr grid-flow-col rounded-sm bg-sunken p-px shadow-well pointer-coarse:h-9', className)}
      data-size={size}
    >
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-y-px left-px -z-10 w-[calc((100%-2px)/var(--seg-n))] translate-x-[calc(100%*var(--seg-i))] rounded-[5px] bg-raised shadow-raised transition-transform duration-160 ease-app"
      />
      {items.map((it) => (
        <Radio.Root
          key={it.value}
          value={it.value}
          className="flex h-full cursor-pointer items-center justify-center rounded-[5px] px-2.5 text-center text-caption font-medium whitespace-nowrap text-muted transition-colors duration-100 hover:text-ink focus-visible:-outline-offset-2 data-checked:text-ink data-disabled:cursor-not-allowed data-disabled:opacity-45"
        >
          {it.label}
        </Radio.Root>
      ))}
    </RadioGroup>
  );
}
