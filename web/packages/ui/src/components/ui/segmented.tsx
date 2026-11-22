import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import type { CSSProperties, ReactNode } from 'react';
import { cn } from '../../lib/cn';
import { corner } from './button';

export interface Segment {
  value: string;
  label: ReactNode;
  /** A segment that cannot be chosen now; its label or a line beside the control says why. */
  disabled?: boolean;
}

/**
 * A segmented control on Base UI's radio group (Tactile): one tonal track (28px, the grey `fill` under the
 * `well` shadow, 2px padding) with equal-width segments, their `caption` labels centred, and one `raised`
 * thumb (corners concentric with the track's) that slides to the chosen one on its transform (the spring; it
 * snaps under Motion: Match system) and presses to 0.95 with the control. The segment count and the chosen index reach the thumb as
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
  'aria-invalid': invalid,
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
  'aria-invalid'?: boolean;
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
      aria-invalid={invalid || undefined}
      style={vars}
      className={cn('group/seg relative isolate grid h-7 shrink-0 auto-cols-fr grid-flow-col bg-fill p-0.5 shadow-well pointer-coarse:h-9', corner.sm, className)}
      data-size={size}
    >
      <span
        aria-hidden="true"
        className={cn('pointer-events-none absolute inset-y-0.5 left-0.5 -z-10 w-[calc((100%-4px)/var(--seg-n))] translate-x-[calc(100%*var(--seg-i))] bg-raised shadow-thumb transition-[translate,scale] duration-(--spring-base) ease-spring group-[:active:not([data-disabled])]/seg:scale-95 in-data-[theme=dark]:bg-fill-press', corner.xs)}
      />
      {items.map((it) => (
        <Radio.Root
          key={it.value}
          value={it.value}
          disabled={it.disabled}
          className={cn('flex h-full cursor-pointer items-center justify-center px-2.5 text-center text-caption font-medium whitespace-nowrap text-muted hover:text-ink focus-visible:-outline-offset-2 data-checked:text-ink data-disabled:cursor-not-allowed data-disabled:opacity-45', corner.xs)}
        >
          {it.label}
        </Radio.Root>
      ))}
    </RadioGroup>
  );
}
