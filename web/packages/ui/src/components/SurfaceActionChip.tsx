import { MousePointerClick } from 'lucide-react';
import { Fragment } from 'react';
import { actionLabel, useKnownSurface, type SurfaceActionLine } from '../lib/surface';
import { Popover } from './ui/popover';

const shown = (v: unknown): string => (Array.isArray(v) ? v.map(shown).join(', ') : v && typeof v === 'object' ? JSON.stringify(v) : v === null || v === undefined || v === '' ? '—' : String(v));

/**
 * A user message a card's Button sent (`[uam_surface action] {…}`, the line the model reads):
 * one compact chip, "Pressed <button> · <card>", named from the card when this view loaded it
 * and from the action's name otherwise; the values sent open in a popover on demand.
 */
export function SurfaceActionChip({ action, sessionId }: Readonly<{ action: SurfaceActionLine; sessionId?: string }>) {
  const { button, card } = actionLabel(action, useKnownSurface(sessionId, action.surfaceId));
  const values = Object.entries(action.context);
  return (
    <Popover.Root>
      <Popover.Trigger render={<button type="button" className="inline-flex min-h-9 max-w-full items-center gap-2 rounded-lg bg-bubble px-3.5 text-left text-ui text-ink transition-colors duration-160 hover:bg-tint-hover pointer-coarse:min-h-11" />}>
        <MousePointerClick aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
        <span className="min-w-0 truncate">
          <span className="sr-only">You: </span>Pressed <span className="font-medium">{button}</span>
          {card && <span className="text-muted"> · {card}</span>}
        </span>
      </Popover.Trigger>
      <Popover.Content side="bottom" align="end" className="w-80 gap-2">
        <Popover.Title>Sent to the agent</Popover.Title>
        <Popover.Description>{card ? `${button} on “${card}”` : `The ${action.name} action`}</Popover.Description>
        {values.length ? (
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 tabular-nums">
            {values.map(([key, value]) => (
              <Fragment key={key}>
                <dt className="text-muted">{key}</dt>
                <dd className="[overflow-wrap:anywhere] text-ink">{shown(value)}</dd>
              </Fragment>
            ))}
          </dl>
        ) : (
          <p className="text-caption text-muted">No values sent</p>
        )}
      </Popover.Content>
    </Popover.Root>
  );
}
