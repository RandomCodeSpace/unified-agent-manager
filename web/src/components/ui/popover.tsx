import { Popover as BasePopover } from '@base-ui/react/popover';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/cn';
import { popupClass } from './menu';

/**
 * A small non-modal popover on Base UI for a value a chip cannot carry in full (the context
 * ring, the credits chip, the cost estimate): the level 2 floating surface of DESIGN.md,
 * opened above its trigger by a click or a tap, closed by Esc or a click outside. The
 * trigger keeps its own accessible name; the popup reads from its `Title` down.
 */
export const Popover = {
  Root: BasePopover.Root,
  Trigger: BasePopover.Trigger,
  Content({
    className,
    side = 'top',
    align = 'start',
    sideOffset = 6,
    children,
    ...props
  }: ComponentProps<typeof BasePopover.Popup> & Pick<ComponentProps<typeof BasePopover.Positioner>, 'side' | 'align' | 'sideOffset'>) {
    return (
      <BasePopover.Portal>
        <BasePopover.Positioner side={side} align={align} sideOffset={sideOffset} collisionPadding={8} className="z-50 outline-hidden">
          <BasePopover.Popup data-popup="" className={cn(popupClass, 'flex max-w-72 flex-col gap-1 px-3 py-2 text-ui text-body', className)} {...props}>
            {children}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    );
  },
  Title({ className, ...props }: ComponentProps<typeof BasePopover.Title>) {
    return <BasePopover.Title className={cn('text-ui font-medium text-ink', className)} {...props} />;
  },
  Description({ className, ...props }: ComponentProps<typeof BasePopover.Description>) {
    return <BasePopover.Description className={cn('text-caption text-muted', className)} {...props} />;
  },
};
