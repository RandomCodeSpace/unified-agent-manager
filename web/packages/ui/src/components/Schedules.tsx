import { CalendarClock } from 'lucide-react';
import { useState } from 'react';
import type { ScheduleSnapshot } from '../api';
import { runTime } from '../lib/routines';
import { formatMs } from '../lib/transcript';
import { Button } from './ui/button';
import { Popover } from './ui/popover';

type Entry = ScheduleSnapshot['entries'][number];

/** How often a schedule runs, as the provider keeps it. */
function cadence(entry: Entry): string {
  if (entry.self_paced) return 'Self-paced';
  if (entry.cron) return `Cron ${entry.cron}`;
  if (entry.interval_ms) return `${entry.recurring ? 'Every' : 'Once, after'} ${formatMs(entry.interval_ms) ?? `${entry.interval_ms} ms`}`;
  return entry.recurring ? 'Recurring' : 'Once';
}

/** The next run in the schedule's own timezone, which it names; in local time without a known one. */
function nextRun(entry: Entry, at: string): string {
  if (entry.timezone) {
    try {
      return `${new Date(at).toLocaleString(undefined, { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', timeZone: entry.timezone })} · ${entry.timezone}`;
    } catch {
      // A zone this browser does not know: local time, not labelled with it.
    }
  }
  return runTime(at);
}

/**
 * The open Task's native schedules in the status strip: "Scheduled in this Task · N" ("N+" when the
 * list is partial), and on a click a read-only list of each one's cadence, timezone and next run.
 * Display only: no prompts, no actions, no reads or timers of its own. The provider runs them, and
 * only while the conversation is open; an unknown list, an empty one or a closed Task shows nothing.
 */
export function Schedules({ snapshot, open }: Readonly<{ snapshot: ScheduleSnapshot | null | undefined; open: boolean }>) {
  const [popover, setPopover] = useState(false);
  const entries = snapshot?.entries ?? [];
  const shown = open && !!snapshot && entries.length > 0 && (snapshot.known || !!snapshot.truncated);
  if (popover && !shown) setPopover(false);
  if (!shown) return null;
  const count = snapshot.known ? String(entries.length) : `${entries.length}+`;
  return (
    <Popover.Root open={popover} onOpenChange={setPopover}>
      <Popover.Trigger render={<Button size="sm" variant="subtle" aria-label={`Scheduled in this Task: ${count}`} className="shrink-0 bg-canvas px-1.5 text-caption tabular-nums text-muted pointer-coarse:min-w-11" />}>
        <CalendarClock aria-hidden="true" className="text-faint" />
        <span className="max-sm:hidden">Scheduled in this Task ·</span> {count}
      </Popover.Trigger>
      <Popover.Content className="w-80 max-w-[calc(100vw-16px)] gap-2">
        <Popover.Title>Scheduled in this Task</Popover.Title>
        <Popover.Description>
          {snapshot.known ? 'The provider runs these while this conversation is open.' : `Showing the first ${entries.length}; this list is incomplete.`}
        </Popover.Description>
        <ul className="max-h-64 space-y-2 overflow-y-auto overflow-x-hidden overscroll-contain text-caption text-muted">
          {entries.map((entry) => (
            <li key={entry.id} className="min-w-0">
              <span className="block truncate text-body" title={cadence(entry)}>{cadence(entry)}</span>
              <span className="block truncate">
                {entry.next_run_at ? `Next run ${nextRun(entry, entry.next_run_at)}` : `Next run not set${entry.timezone ? ` · ${entry.timezone}` : ''}`}
              </span>
            </li>
          ))}
        </ul>
      </Popover.Content>
    </Popover.Root>
  );
}
