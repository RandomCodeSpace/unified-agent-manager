import { Select as BaseSelect } from '@base-ui/react/select';
import { Check, ChevronDown } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';
import { itemClass, popupClass } from './menu';

export interface SelectOption {
  value: string;
  label: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
  /** Retain the current label without offering it in the menu. */
  hidden?: boolean;
}

/**
 * A form select on Base UI, used where a labelled control belongs (the project dialogs).
 * `items` drives both the list and the trigger's text. Renders below the trigger; the popup
 * hides its scrollbar with a class, not Base UI's inline <style> (CSP).
 */
export function Select({
  id,
  value,
  onValueChange,
  items,
  disabled,
  className,
  'aria-label': ariaLabel,
  'aria-describedby': describedBy,
}: {
  id?: string;
  value: string;
  onValueChange: (value: string) => void;
  items: SelectOption[];
  disabled?: boolean;
  className?: string;
  'aria-label'?: string;
  'aria-describedby'?: string;
}) {
  const plain = items.map(({ value: v, label }) => ({ value: v, label: typeof label === 'string' ? label : v }));
  return (
    <BaseSelect.Root value={value} onValueChange={(v) => onValueChange(v as string)} items={plain} disabled={disabled}>
      <BaseSelect.Trigger
        id={id}
        aria-label={ariaLabel}
        aria-describedby={describedBy}
        className={cn(
          'flex h-9 w-full min-w-0 items-center justify-between gap-2 rounded-sm border border-hairline-strong bg-raised px-2.5 text-ui text-ink select-none hover:not-data-disabled:bg-surface data-disabled:opacity-45 data-disabled:cursor-not-allowed pointer-coarse:min-h-11',
          className,
        )}
      >
        <BaseSelect.Value className="truncate" />
        <BaseSelect.Icon className="flex text-muted">
          <ChevronDown className="size-4" />
        </BaseSelect.Icon>
      </BaseSelect.Trigger>
      <BaseSelect.Portal>
        <BaseSelect.Positioner sideOffset={4} alignItemWithTrigger={false} collisionPadding={8} className="z-60 outline-hidden select-none">
          <BaseSelect.Popup data-popup="" className={cn(popupClass, 'max-h-(--available-height) min-w-(--anchor-width) overflow-hidden')}>
            <BaseSelect.List className="max-h-[min(320px,var(--available-height))] overflow-y-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
              {items.map((it) => (
                <BaseSelect.Item
                  key={it.value}
                  value={it.value}
                  disabled={it.disabled || it.hidden}
                  hidden={it.hidden}
                  className={cn(itemClass, 'items-start pr-3 pl-7', it.hidden && 'hidden')}
                >
                  <BaseSelect.ItemIndicator className="absolute top-2 left-2 flex text-accent [&_svg]:size-3.5 [&_svg]:text-accent">
                    <Check strokeWidth={2.5} />
                  </BaseSelect.ItemIndicator>
                  <span className="flex min-w-0 flex-col">
                    <BaseSelect.ItemText>{it.label}</BaseSelect.ItemText>
                    {it.description && <span className="font-sans text-caption text-muted">{it.description}</span>}
                  </span>
                </BaseSelect.Item>
              ))}
            </BaseSelect.List>
          </BaseSelect.Popup>
        </BaseSelect.Positioner>
      </BaseSelect.Portal>
    </BaseSelect.Root>
  );
}
