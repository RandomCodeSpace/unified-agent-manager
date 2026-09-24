import { ContextMenu as BaseContextMenu } from '@base-ui/react/context-menu';
import { Menu as BaseMenu } from '@base-ui/react/menu';
import { Check } from 'lucide-react';
import { createContext, useCallback, useContext, useMemo, useRef, type ComponentProps, type ReactNode } from 'react';
import { cn } from '../../lib/cn';

/**
 * Dropdown and context menus on Base UI. Level 2 "floating" surface from DESIGN.md:
 * raised fill, hairline-strong edge, float shadow, 10px radius; 30px rows.
 */
export const popupClass =
  'z-50 min-w-44 origin-(--transform-origin) rounded-md border border-hairline-strong bg-raised p-1 text-ink shadow-float outline-hidden transition-[opacity,scale] duration-100 data-starting-style:scale-[0.97] data-starting-style:opacity-0 data-ending-style:scale-[0.97] data-ending-style:opacity-0';

/** One row of any popup list (menus, selects, the composer's inline picker): the keyboard or pointer highlight is the `tint-hover` step. */
export const itemClass =
  'relative flex min-h-[30px] w-full cursor-default select-none items-center gap-2 rounded-sm px-2 py-1 text-ui text-body outline-hidden data-highlighted:bg-tint-hover data-highlighted:text-ink data-disabled:opacity-45 pointer-coarse:min-h-11 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-muted data-highlighted:[&_svg]:text-ink';

export const dangerItemClass = 'text-error data-highlighted:bg-error-wash data-highlighted:text-error [&_svg]:text-error data-highlighted:[&_svg]:text-error';

export const separatorClass = 'my-1 h-px bg-hairline';

export const labelClass = 'px-2 pt-2 pb-1 text-caption text-muted';

/** One action row shared by dropdown, context and header menus. */
export interface ActionItem {
  key: string;
  label: string;
  icon?: ReactNode;
  onSelect: () => void;
  disabled?: boolean;
  /** Why it is disabled; shown under the item so the rule is visible, not guessed. */
  reason?: string;
  /** The action moves focus itself (an inline editor); the closing menu then leaves focus alone. */
  takesFocus?: boolean;
  danger?: boolean;
  /** Draws a separator above this item. */
  separator?: boolean;
}

type MenuParts = {
  Item: typeof BaseMenu.Item;
  Separator: typeof BaseMenu.Separator;
};

/**
 * Action items run once the menu has finished closing, and a closing menu returns focus to
 * its trigger. An action that focuses something itself (the inline rename input) would race
 * that return and lose, so such items are marked `takesFocus` and the popup's `finalFocus`
 * then leaves focus alone. Outside a Root the action runs at once.
 */
interface Deferred {
  defer: (fn: () => void, takesFocus?: boolean) => void;
  /** Base UI `finalFocus`: `false` keeps the closing menu from moving focus. */
  finalFocus: () => boolean;
}

const DeferContext = createContext<Deferred>({ defer: (fn) => fn(), finalFocus: () => true });

function useDeferredActions(onOpenChangeComplete?: (open: boolean) => void) {
  const pending = useRef<{ fn: () => void; takesFocus: boolean } | null>(null);
  // Read by Base UI when the popup unmounts, which is after the action ran; kept until the next open.
  const leaveFocus = useRef(false);
  const ctx = useMemo<Deferred>(
    () => ({
      defer: (fn, takesFocus = false) => {
        pending.current = { fn, takesFocus };
      },
      finalFocus: () => !leaveFocus.current,
    }),
    [],
  );
  const onComplete = useCallback(
    (open: boolean) => {
      onOpenChangeComplete?.(open);
      if (open) {
        leaveFocus.current = false;
        return;
      }
      const p = pending.current;
      pending.current = null;
      leaveFocus.current = !!p?.takesFocus;
      p?.fn();
    },
    [onOpenChangeComplete],
  );
  return { ctx, onComplete };
}

function renderActions(parts: MenuParts, items: ActionItem[], defer: Deferred['defer']) {
  return items.map((it) => (
    <div key={it.key} className="contents">
      {it.separator && <parts.Separator className={separatorClass} />}
      <parts.Item className={cn(itemClass, it.danger && dangerItemClass)} disabled={it.disabled} onClick={() => defer(it.onSelect, it.takesFocus)}>
        {it.icon}
        <span className="flex-1">{it.label}</span>
      </parts.Item>
      {it.disabled && it.reason && <p className="max-w-64 px-2 pb-1.5 text-caption text-muted">{it.reason}</p>}
    </div>
  ));
}

/* ---------- Dropdown menu ---------- */

export const Menu = {
  Root({ onOpenChangeComplete, ...props }: ComponentProps<typeof BaseMenu.Root>) {
    const { ctx, onComplete } = useDeferredActions(onOpenChangeComplete);
    return (
      <DeferContext value={ctx}>
        <BaseMenu.Root onOpenChangeComplete={onComplete} {...props} />
      </DeferContext>
    );
  },
  Trigger: BaseMenu.Trigger,
  Group: BaseMenu.Group,
  RadioGroup: BaseMenu.RadioGroup,
  Content({
    className,
    side = 'bottom',
    align = 'end',
    sideOffset = 4,
    children,
    ...props
  }: ComponentProps<typeof BaseMenu.Popup> & Pick<ComponentProps<typeof BaseMenu.Positioner>, 'side' | 'align' | 'sideOffset'>) {
    const { finalFocus } = useContext(DeferContext);
    return (
      <BaseMenu.Portal>
        <BaseMenu.Positioner side={side} align={align} sideOffset={sideOffset} collisionPadding={8} className="z-50 outline-hidden">
          <BaseMenu.Popup data-popup="" className={cn(popupClass, className)} finalFocus={finalFocus} {...props}>
            {children}
          </BaseMenu.Popup>
        </BaseMenu.Positioner>
      </BaseMenu.Portal>
    );
  },
  Item({ className, danger, ...props }: ComponentProps<typeof BaseMenu.Item> & { danger?: boolean }) {
    return <BaseMenu.Item className={cn(itemClass, danger && dangerItemClass, className)} {...props} />;
  },
  RadioItem({ className, children, description, ...props }: ComponentProps<typeof BaseMenu.RadioItem> & { description?: ReactNode }) {
    return (
      <BaseMenu.RadioItem className={cn(itemClass, 'items-start pl-7', className)} {...props}>
        <BaseMenu.RadioItemIndicator className="absolute top-2 left-2 flex text-accent [&_svg]:size-3.5 [&_svg]:text-accent">
          <Check strokeWidth={2.5} />
        </BaseMenu.RadioItemIndicator>
        <span className="flex min-w-0 flex-1 flex-col">
          <span>{children}</span>
          {description && <span className="text-caption text-muted">{description}</span>}
        </span>
      </BaseMenu.RadioItem>
    );
  },
  Label({ className, ...props }: ComponentProps<typeof BaseMenu.GroupLabel>) {
    return <BaseMenu.GroupLabel className={cn(labelClass, className)} {...props} />;
  },
  Separator({ className, ...props }: ComponentProps<typeof BaseMenu.Separator>) {
    return <BaseMenu.Separator className={cn(separatorClass, className)} {...props} />;
  },
  Actions({ items }: { items: ActionItem[] }) {
    const { defer } = useContext(DeferContext);
    return <>{renderActions({ Item: BaseMenu.Item, Separator: BaseMenu.Separator }, items, defer)}</>;
  },
};

/* ---------- Context menu ---------- */

export const ContextMenu = {
  Root({ onOpenChangeComplete, ...props }: ComponentProps<typeof BaseContextMenu.Root>) {
    const { ctx, onComplete } = useDeferredActions(onOpenChangeComplete);
    return (
      <DeferContext value={ctx}>
        <BaseContextMenu.Root onOpenChangeComplete={onComplete} {...props} />
      </DeferContext>
    );
  },
  Trigger: BaseContextMenu.Trigger,
  Content({ className, children, ...props }: ComponentProps<typeof BaseContextMenu.Popup>) {
    const { finalFocus } = useContext(DeferContext);
    return (
      <BaseContextMenu.Portal>
        <BaseContextMenu.Positioner collisionPadding={8} className="z-50 outline-hidden">
          <BaseContextMenu.Popup data-popup="" className={cn(popupClass, className)} finalFocus={finalFocus} {...props}>
            {children}
          </BaseContextMenu.Popup>
        </BaseContextMenu.Positioner>
      </BaseContextMenu.Portal>
    );
  },
  Actions({ items }: { items: ActionItem[] }) {
    const { defer } = useContext(DeferContext);
    return <>{renderActions({ Item: BaseContextMenu.Item, Separator: BaseContextMenu.Separator }, items, defer)}</>;
  },
};
