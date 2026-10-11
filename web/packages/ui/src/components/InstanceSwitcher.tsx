import { Popover as BasePopover } from '@base-ui/react/popover';
import { Check, ChevronsUpDown, Network, Plus, X } from 'lucide-react';
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import type { ConnectedStatus, SessionSummary } from '../api';
import { scopedMachine, useFederation, type Machine } from '../FederationContext';
import { cn } from '../lib/cn';
import { groupTasks, needsYouCount, type Unread } from '../lib/tasks';
import { shortVersion } from '../lib/update';
import { Dot, useApp, type Tone } from './common';
import { Key } from './InlinePicker';
import { BottomSheet } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { Tip } from './ui/tooltip';
import { InstanceBadge } from './InstanceBadge';
import { PanelFoot, PanelHead } from './ui/panel';

/**
 * The instance switcher (DESIGN.md Connected instances): which instance the Task list shows and which one is on screen.
 * In the sidebar it opens from the header toolbar, with All instances first; in the Settings header it picks the
 * instance whose Settings show. Its list is an anchored reader, a bottom sheet on a phone.
 */

const STATUS: Record<ConnectedStatus['status'], [string, Tone]> = {
  online: ['Online', 'success'],
  connecting: ['Connecting…', 'muted'],
  offline: ['Offline, retrying', 'error'],
  'auth-required': ['Needs a new access key', 'warning'],
  unsupported: ['Needs an update', 'warning'],
  'account-mismatch': ['Different Copilot account', 'warning'],
};

/** An instance as the switcher shows it: its status, version and Task counts. */
interface Instance {
  id: string;
  label: string;
  home: boolean;
  status: ConnectedStatus['status'];
  version?: string;
  open: number;
  working: number;
  needs: number;
}

/** The instance on screen counts from `live` (App's own state); the others from what they last streamed. */
function describe(machine: Machine, live: { sessions: SessionSummary[]; hasNews: Unread } | undefined, version: string | undefined): Instance {
  const sessions = machine.active && live ? live.sessions : machine.state.sessions;
  return {
    id: machine.id,
    label: machine.label,
    home: !machine.connection,
    status: machine.status.status,
    version: shortVersion(machine.connection?.version ?? version ?? '') || undefined,
    open: groupTasks(sessions).active.length,
    working: sessions.filter((s) => s.state === 'working').length,
    needs: needsYouCount(sessions, machine.active && live ? live.hasNews : machine.hasNews),
  };
}

function facts(instance: Instance): string {
  const parts = [STATUS[instance.status][0], instance.version];
  if (instance.status === 'online') parts.push(`${instance.open} open${instance.working ? `, ${instance.working} working` : ''}`);
  return parts.filter(Boolean).join(' · ');
}

/** The same coloured text initials used on Task cards, with a status dot; All instances uses a network glyph. */
function InstanceMark({ instance, size = 'md' }: Readonly<{ instance: Instance | null; size?: 'sm' | 'md' | 'lg' }>) {
  const box = { sm: 'size-5 rounded-xs text-stamp', md: 'size-7 rounded-sm text-meta', lg: 'size-9 rounded-md text-caption' }[size];
  return (
    <span aria-hidden="true" className={cn('relative inline-flex shrink-0 items-center justify-center text-muted', box, !instance && 'bg-sunken')}>
      {instance ? <InstanceBadge id={instance.id} label={instance.label} className={size === 'sm' ? 'text-stamp' : size === 'lg' ? 'text-caption' : undefined} /> : <Network className={size === 'sm' ? 'size-3' : 'size-4'} />}
      {instance && size !== 'sm' && <Dot tone={STATUS[instance.status][1]} className="absolute -right-0.5 -bottom-0.5 ring-2 ring-(--mark-ring)" />}
    </span>
  );
}

function NeedsCount({ count }: Readonly<{ count: number }>) {
  if (!count) return null;
  return <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-attention px-1 text-meta font-semibold text-on-primary tabular-nums">{count}</span>;
}

/** The reader's rows; arrow keys move between them. `current` is the row picked (null: All instances). */
function InstanceList({ all, instances, unavailable, current, currentRow, onPick, onConnect }: Readonly<{
  all: boolean;
  instances: Instance[];
  /** Connections not shown (disabled), with why. */
  unavailable: { id: string; label: string; reason: string }[];
  current: string | null;
  currentRow: RefObject<HTMLButtonElement | null>;
  onPick: (id: string | null) => void;
  onConnect: () => void;
}>) {
  const row = (id: string | null, label: string, sub: string, mark: ReactNode, needs: number) => {
    const on = current === id;
    return (
      <button
        key={id ?? 'all'}
        ref={on ? currentRow : undefined}
        type="button"
        data-instance=""
        aria-current={on ? 'true' : undefined}
        aria-label={`${label}, ${sub}${needs ? `, ${needs} need you` : ''}`}
        onClick={() => onPick(id)}
        className={cn('flex min-h-12 w-full items-center gap-3 rounded-md px-2 text-left transition-colors focus-visible:-outline-offset-2 pointer-coarse:min-h-14', on ? 'bg-tint-selected' : 'hover:bg-tint-hover')}
      >
        {mark}
        <span className="flex min-w-0 flex-1 flex-col">
          <span className={cn('truncate text-ui', on ? 'font-medium text-ink' : 'text-body')}>{label}</span>
          <span className="truncate text-caption text-muted">{sub}</span>
        </span>
        <NeedsCount count={needs} />
        <Check aria-hidden="true" className={cn('size-4 shrink-0 text-ink', !on && 'invisible')} />
      </button>
    );
  };
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const rows = [...e.currentTarget.querySelectorAll<HTMLElement>('[data-instance]')];
    const at = rows.indexOf(document.activeElement as HTMLElement);
    rows[(at + (e.key === 'ArrowDown' ? 1 : -1) + rows.length) % rows.length]?.focus();
    e.preventDefault();
  };
  const section = (label: string, items: Instance[], extra?: ReactNode) => (items.length > 0 || extra) && (
    <section aria-label={label} className="flex flex-col gap-0.5">
      <h3 className="px-2 pb-1 text-eyebrow uppercase text-muted">{label}</h3>
      {items.map((i) => row(i.id, i.label, facts(i), <InstanceMark instance={i} size="lg" />, i.needs))}
      {extra}
    </section>
  );
  return (
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions -- the rows are buttons; this only relays arrow keys between them.
    <div className="flex min-h-0 flex-col gap-3 overflow-y-auto overscroll-contain px-2 pt-1 pb-3 [--mark-ring:var(--color-raised)]" onKeyDown={onKeyDown}>
      {all && row(null, 'All instances', 'Every task in one list', <InstanceMark instance={null} size="lg" />, instances.reduce((n, i) => n + i.needs, 0))}
      {section('Main Server', instances.filter((i) => i.home))}
      {section(
        'Connected',
        instances.filter((i) => !i.home),
        unavailable.map((u) => (
          <div key={u.id} className="flex min-h-12 items-center gap-3 px-2 text-muted">
            <InstanceBadge id={u.id} label={u.label} className="w-9 text-center text-caption opacity-60" />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate text-ui">{u.label}</span>
              <span className="truncate text-caption">{u.reason}</span>
            </span>
          </div>
        )),
      )}
      <button type="button" data-instance="" onClick={onConnect} className="flex h-9 items-center gap-3 rounded-md px-2 text-left text-ui text-muted transition-colors hover:bg-tint-hover hover:text-body focus-visible:-outline-offset-2 pointer-coarse:h-11">
        <span className="flex size-9 shrink-0 items-center justify-center"><Plus aria-hidden="true" className="size-4" /></span>
        Connect another instance
      </button>
    </div>
  );
}

/**
 * `place`: the sidebar's switcher scopes the Task list (All instances, or one) and puts a picked instance on screen,
 * keeping Settings open when it is; Settings' picks the instance on screen, and the scope follows unless it is All.
 * `live`: the instance on screen's own Tasks, fresher than its streamed copy.
 * `onConnect`: shows Settings → Connected instances while this server is on screen; from a connection, its Settings open there.
 */
export function InstanceSwitcher({ place, live, settingsOpen = false, onConnect }: Readonly<{ place: 'sidebar' | 'settings'; live?: { sessions: SessionSummary[]; hasNews: Unread }; settingsOpen?: boolean; onConnect: () => void }>) {
  const federation = useFederation();
  const { meta } = useApp();
  const [view, setView] = useState({ open: false, phone: false });
  const trigger = useRef<HTMLButtonElement>(null);
  const currentRow = useRef<HTMLButtonElement>(null);
  const sidebar = place === 'sidebar';
  const machines = federation?.machines;
  const shortcut = sidebar && !!machines;
  // Alt+I opens the sidebar's switcher from anywhere but a menu, a dialog or the terminal, as Alt+N opens New task.
  useEffect(() => {
    if (!shortcut) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.code !== 'KeyI' || e.defaultPrevented) return;
      if (document.querySelector('[data-popup]:not([data-popup="tooltip"])') || (e.target instanceof Element && e.target.closest('.xterm'))) return;
      e.preventDefault();
      setView({ open: true, phone: window.matchMedia(PHONE).matches });
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [shortcut]);
  if (!federation || !machines) return null;
  const onScreen = machines.find((m) => m.active);
  // The hosting instance's version: its own meta while on screen, else the one Federation reads.
  const homeVersion = onScreen && !onScreen.connection ? meta?.version : federation.homeVersion;
  const instances = machines.map((m) => describe(m, live, m.connection ? undefined : homeVersion));
  const unavailable = (federation.choices ?? []).filter((c) => !machines.some((m) => m.id === c.id)).map((c) => ({ id: c.id, label: c.label, reason: c.reason ?? 'Unavailable' }));
  const scope = scopedMachine(federation)?.id ?? null;
  const current = sidebar ? scope : onScreen?.id ?? '';
  const chosen = current === null ? null : instances.find((i) => i.id === current) ?? null;
  const total = instances.reduce((n, i) => n + i.needs, 0);
  const offline = instances.filter((i) => i.status === 'offline').length;
  const close = () => setView((v) => ({ ...v, open: false }));
  const show = (id: string, path: string) => {
    if (id === (onScreen?.id ?? '')) return;
    if (path === '#settings') federation.carry?.write({ nextSection: federation.carry.read().section });
    federation.go?.(id, path);
  };
  const pick = (id: string | null) => {
    close();
    if (sidebar) {
      federation.onFilter?.(id === null ? null : { machine: id, project: '' });
      if (id !== null) show(id, settingsOpen ? '#settings' : '');
      return;
    }
    if (id === null) return;
    if (scope !== null) federation.onFilter?.({ machine: id, project: '' });
    show(id, '#settings');
  };
  // Connections are added in this server's Settings → Connected instances; switching there opens its Settings on that page.
  const connect = () => {
    close();
    if (!onScreen?.connection) return onConnect();
    federation.carry?.write({ nextSection: 'connections' });
    federation.go?.('', '#settings');
  };
  const label = chosen ? chosen.label : 'All instances';
  const summary = `${instances.length} ${instances.length === 1 ? 'instance' : 'instances'}${offline ? `, ${offline} offline` : ''}`;
  const head = (closeButton?: ReactNode) => (
    <PanelHead className="gap-0.5 pt-2 pr-2 pb-2 pl-4">
      <div className="flex min-h-7 items-center gap-2">
        <h2 className="flex-1 text-title text-ink">{sidebar ? 'Instances' : 'Settings of which instance?'}</h2>
        {closeButton}
      </div>
      <p className="text-caption text-muted">{sidebar ? `${summary}${total ? ` · ${total} need you` : ''}` : 'Each instance keeps its own settings.'}</p>
    </PanelHead>
  );
  const list = <InstanceList all={sidebar} instances={instances} unavailable={unavailable} current={current} currentRow={currentRow} onPick={pick} onConnect={connect} />;
  const name = `Instance: ${label}${sidebar && (chosen ? chosen.needs : total) ? `, ${chosen ? chosen.needs : total} need you` : ''}`;
  const open = () => setView({ open: true, phone: window.matchMedia(PHONE).matches });
  const button = sidebar ? (
    <Tip label={<>{label}<span className="block text-on-primary/70">{chosen ? STATUS[chosen.status][0] : summary}{(chosen ? chosen.needs : total) > 0 && ` · ${chosen ? chosen.needs : total} need you`}</span><span className="block text-on-primary/70">Alt+I</span></>} disabled={view.open}>
      <Button
        ref={trigger}
        size="icon"
        aria-label={name}
        aria-haspopup="dialog"
        aria-expanded={view.open}
        aria-keyshortcuts="Alt+I"
        onClick={open}
        className={cn('text-muted [--mark-ring:var(--color-rail)]', (view.open || chosen) && 'bg-raised shadow-raised [--mark-ring:var(--color-raised)]')}
      >
        {chosen ? <InstanceMark instance={chosen} size="sm" /> : <Network />}
        {(chosen ? chosen.needs : total) > 0 && <span aria-hidden="true" className="absolute top-0.5 right-0.5 size-1.5 rounded-full bg-attention ring-2 ring-rail" />}
      </Button>
    </Tip>
  ) : (
    <Button ref={trigger} size="md" aria-label={name} aria-haspopup="dialog" aria-expanded={view.open} onClick={open} className={cn('min-w-0 max-w-64 gap-2 px-2', view.open && 'bg-raised shadow-raised')}>
      <InstanceMark instance={chosen} size="sm" />
      <span className="min-w-0 truncate">{label}</span>
      <ChevronsUpDown className="text-muted" />
    </Button>
  );
  if (view.phone) {
    return (
      <>
        {button}
        <BottomSheet open={view.open} onClose={close} onClosed={() => trigger.current?.isConnected && trigger.current.focus()} label="Instances" initialFocus={currentRow}>
          {head(
            <Button size="icon-sm" aria-label="Close" className="text-muted" onClick={close}>
              <X />
            </Button>,
          )}
          {list}
        </BottomSheet>
      </>
    );
  }
  return (
    <>
      {button}
      <BasePopover.Root open={view.open} onOpenChange={(o) => !o && close()}>
        <BasePopover.Portal>
          <BasePopover.Positioner anchor={trigger} side="bottom" align="start" sideOffset={6} collisionPadding={12} className="z-50 outline-hidden">
            <BasePopover.Popup
              data-popup=""
              aria-label="Instances"
              initialFocus={currentRow}
              finalFocus={trigger}
              className="flex max-h-[min(80vh,var(--available-height))] w-88 max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0"
            >
              {head()}
              {list}
              <PanelFoot>
                <span className="flex items-center gap-1.5"><Key>↑</Key><Key>↓</Key>move</span>
                <span className="flex items-center gap-1.5"><Key>Enter</Key>switch</span>
                <span className="flex-1" />
                {sidebar && <span className="flex items-center gap-1.5"><Key>Alt</Key><Key>I</Key>open</span>}
              </PanelFoot>
            </BasePopover.Popup>
          </BasePopover.Positioner>
        </BasePopover.Portal>
      </BasePopover.Root>
    </>
  );
}
