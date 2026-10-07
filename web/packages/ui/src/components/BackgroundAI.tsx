import { useApi } from '../ApiContext';
import { ChevronRight } from 'lucide-react';
import { useCallback, useContext, useEffect, useRef, useState, type SubmitEvent } from 'react';
import { describeError, taskName, type UtilityCall, type UtilityLog } from '../api';
import { byDay, clockText, dayLabel, dayText, durationText, mergeCalls, modelText, outcomeLabel, purposeLabel, sizeText, tokenText } from '../lib/utility';
import { cn } from '../lib/cn';
import { formatCredits } from '../lib/cost';
import { Note, Skeleton } from './common';
import { Row } from './TaskDefaults';
import { TaskList } from './taskActions';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Collapse } from './ui/collapse';
import { Input } from './ui/input';
import { HelpTip } from './ui/tooltip';

/** How often the open section reads today's count and the newest calls again. */
const REFRESH_MS = 15000;
const MAX_LIMIT = 1000;

function CallRow({ call }: Readonly<{ call: UtilityCall }>) {
  const { sessions, projects, openTask } = useContext(TaskList);
  const task = call.task_id ? sessions.find((s) => s.id === call.task_id) : undefined;
  const project = call.project_id ? projects.find((p) => p.id === call.project_id) : undefined;
  const outcome = outcomeLabel(call);
  const ran = call.outcome !== 'skipped';
  const meta = [modelText(call), ran && sizeText(call), ran && tokenText(call), ran && durationText(call.duration_ms), call.credits ? `${formatCredits(call.credits)} credits` : ''].filter(Boolean).join(' · ');
  return (
    <li className="flex min-w-0 flex-col gap-0.5 py-2">
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="shrink-0 text-caption text-muted tabular-nums">{clockText(call.at)}</span>
        <span className="shrink-0 text-ui font-medium text-ink">{purposeLabel(call.purpose)}</span>
        <span className="flex min-w-0 flex-1">
          {task ? (
            <Button size="sm" variant="subtle" className="h-auto max-w-full min-w-0 px-1 py-0 text-ui text-body pointer-coarse:after:-inset-y-3.5" title="Open the task" onClick={() => openTask(task.id)}>
              <span className="truncate">{taskName(task)}</span>
            </Button>
          ) : (
            <span className="min-w-0 truncate text-ui text-muted">{project?.name ?? (call.task_id ? 'A removed task' : '')}</span>
          )}
        </span>
        {outcome && (
          <Chip tone={call.outcome === 'error' ? 'error' : 'warning'}>
            {outcome}
          </Chip>
        )}
      </div>
      {meta && <span className="min-w-0 text-caption text-muted tabular-nums [overflow-wrap:anywhere]">{meta}</span>}
      {call.outcome === 'error' && call.reason && <span className="min-w-0 text-caption text-error [overflow-wrap:anywhere]">{call.reason}</span>}
    </li>
  );
}

/**
 * Settings → Background AI: UAM's own calls on the Utility model (titles, suggested replies), today's
 * count against the daily limit, the limit, and the log grouped by server-local day with each day's totals. The log
 * starts collapsed and is read only while open (closed, a one-call page keeps today's count current). Older calls
 * load page by page, so every call kept stays reachable.
 */
export function BackgroundAI({ limitSetting, saving, onSaveLimit }: Readonly<{ limitSetting?: number; saving: boolean; onSaveLimit: (limit: number) => Promise<void> }>) {
  const api = useApi();
  const [log, setLog] = useState<UtilityLog | null>(null);
  const [calls, setCalls] = useState<UtilityCall[]>([]);
  const [next, setNext] = useState<number | undefined>();
  const [error, setError] = useState<string | null>(null);
  const [older, setOlder] = useState(false);
  const [draft, setDraft] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [logRead, setLogRead] = useState(false);
  const shown = useRef<{ calls: UtilityCall[]; next: number | undefined }>({ calls: [], next: undefined });

  // Bumped to read again at once: Retry, a saved limit.
  const [reads, setReads] = useState(0);
  const refresh = () => setReads((n) => n + 1);
  const apply = useCallback((page: UtilityLog, withCalls: boolean) => {
    if (withCalls) {
      shown.current = mergeCalls(shown.current.calls, shown.current.next, page.calls, page.next);
      setCalls(shown.current.calls);
      setNext(shown.current.next);
      setLogRead(true);
    }
    setLog(page);
    setError(null);
  }, []);

  useEffect(() => {
    let current = true;
    const read = () => (open ? api.utility() : api.utility(undefined, 1)).then((page) => { if (current) apply(page, open); }).catch((e: unknown) => { if (current) setError(describeError(e)); });
    void read();
    const timer = window.setInterval(() => void read(), REFRESH_MS);
    return () => {
      current = false;
      window.clearInterval(timer);
    };
  }, [apply, limitSetting, reads, open, api]);

  async function loadOlder() {
    if (next === undefined) return;
    setOlder(true);
    try {
      const page = await api.utility(next);
      shown.current = { calls: [...shown.current.calls, ...page.calls.filter((c) => c.id < next)], next: page.next };
      setCalls(shown.current.calls);
      setNext(page.next);
      setError(null);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setOlder(false);
    }
  }

  const limit = log?.today.limit ?? limitSetting;
  const value = draft ?? (limit === undefined ? '' : String(limit));
  const parsed = Number(value);
  const valid = value.trim() !== '' && Number.isInteger(parsed) && parsed >= 0 && parsed <= MAX_LIMIT;
  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!valid) return;
    await onSaveLimit(parsed);
    setDraft(null);
    refresh();
  }

  if (!log) {
    return error ? (
      <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not load Background AI: {error}</span>
        <Button size="sm" variant="secondary" onClick={refresh}>
          Retry
        </Button>
      </Note>
    ) : (
      <Skeleton label="Loading Background AI…" rows={3} />
    );
  }

  const today = log.today;
  const days = new Map(log.days.map((d) => [d.day, d]));
  const fraction = today.limit > 0 ? Math.min(1, today.calls / today.limit) : 1;
  return (
    <>
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="text-title text-ink tabular-nums">
            {today.calls.toLocaleString('en-US')} of {today.limit.toLocaleString('en-US')}
          </span>
          <span className="text-caption text-muted">calls today</span>
        </div>
        <div
          role="meter"
          aria-label="Background AI calls today"
          aria-valuemin={0}
          aria-valuemax={today.limit}
          aria-valuenow={Math.min(today.calls, today.limit)}
          className="h-1.5 w-full max-w-md overflow-hidden rounded-xs bg-sunken shadow-well"
        >
          <div className={today.paused ? 'h-full origin-left bg-warning' : 'h-full origin-left bg-accent'} style={{ transform: `scaleX(${fraction})` }} />
        </div>
        {today.paused && (
          <Note tone="warn" role="status" className="max-w-3xl">
            {today.limit === 0
              ? 'Background AI is off. New tasks keep their first message as the title, and subagent results keep their own report. Set a daily limit above 0 to turn it on.'
              : `Background AI is paused until tomorrow (midnight on the server, ${new Date(today.resets_at).toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit' })} here). Until then new tasks keep their first message as the title, and subagent results keep their own report. Raise the limit to resume now.`}
          </Note>
        )}
      </div>
      {/* A Settings row like the others: the label in the label column, the field and its Save together beside it. */}
      <form aria-label="Daily limit" onSubmit={(e) => void save(e)}>
        <Row id="utility-limit" label="Daily limit (calls)" htmlFor="utility-limit-input" help={`At most ${MAX_LIMIT.toLocaleString('en-US')}; 0 turns Background AI off. The count starts again at midnight on the server.`}>
          <Input
            id="utility-limit-input"
            className="w-28"
            type="number"
            inputMode="numeric"
            min={0}
            max={MAX_LIMIT}
            step={1}
            aria-describedby="utility-limit-help"
            aria-invalid={!valid || undefined}
            disabled={saving}
            value={value}
            onChange={(e) => setDraft(e.target.value)}
          />
          <Button type="submit" variant="secondary" size="lg" disabled={saving || !valid || parsed === limit}>
            Save
          </Button>
        </Row>
      </form>
      <div className="flex flex-col gap-1">
        {error && (
          <Note tone="error" role="alert">
            Could not refresh Background AI: {error}
          </Note>
        )}
        <div className="flex items-center gap-1">
          <Button variant="subtle" className="-ml-3 self-start text-muted" aria-expanded={open} aria-controls="utility-log" onClick={() => setOpen((o) => !o)}>
            <ChevronRight className={cn('transition-transform duration-160', open && 'rotate-90')} />
            {open ? 'Hide log' : `Show log · ${today.calls.toLocaleString('en-US')} ${today.calls === 1 ? 'call' : 'calls'} today`}
          </Button>
          <HelpTip label="Background AI log">Newest first. Tokens marked ≈ are estimated from the characters; the others are what the provider reported.</HelpTip>
        </div>
        <Collapse open={open} soft>
          <div id="utility-log" className="flex flex-col gap-1">
            {!logRead && <Skeleton label="Loading the log…" rows={3} className="pt-2" />}
            {logRead && calls.length === 0 && <Note>No Background AI calls in the last 30 days.</Note>}
            {byDay(calls).map((group) => {
              const totals = days.get(group.day);
              const label = dayLabel(group.day, today.day);
              return (
                <section key={group.day} aria-label={label} className="flex flex-col pt-2">
                  <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
                    <h3 className="text-ui font-medium text-ink">{label}</h3>
                    {totals && <span className="text-caption text-muted tabular-nums">{dayText(totals)}</span>}
                  </div>
                  <ul className="flex flex-col pl-3">
                    {group.calls.map((c) => (
                      <CallRow key={c.id} call={c} />
                    ))}
                  </ul>
                </section>
              );
            })}
            {next !== undefined && (
              <Button size="lg" variant="secondary" className="self-start" loading={older} onClick={() => void loadOlder()}>
                Show older calls
              </Button>
            )}
          </div>
        </Collapse>
      </div>
    </>
  );
}
