// Routine wording (docs/web.md, Routines). Pure, so the unit tests run in node.

import type { Routine, RoutineInput, RoutineOutcome, RoutineRun, RoutineSchedule, SessionState } from '../api';

export const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'] as const;

export type ScheduleKind = RoutineSchedule['kind'];

export const SCHEDULE_KINDS: { value: ScheduleKind; label: string }[] = [
  { value: 'daily', label: 'Every day' },
  { value: 'weekdays', label: 'Every weekday (Monday to Friday)' },
  { value: 'hours', label: 'Every few hours' },
  { value: 'weekly', label: 'Once a week' },
];

/** The schedule in words: "Every weekday at 09:00", "Every 6 hours", "Every Monday at 08:30". */
export function describeSchedule(s: RoutineSchedule): string {
  switch (s.kind) {
    case 'daily':
      return `Every day at ${s.time}`;
    case 'weekdays':
      return `Every weekday at ${s.time}`;
    case 'weekly':
      return `Every ${WEEKDAYS[s.weekday] ?? 'week'} at ${s.time}`;
    case 'hours':
      return s.hours === 1 ? 'Every hour' : `Every ${s.hours} hours`;
  }
}

/** A schedule for the form's choice of kind, keeping the time, day and hours already entered. */
export function scheduleOf(kind: ScheduleKind, time: string, weekday: number, hours: number): RoutineSchedule {
  if (kind === 'hours') return { kind, hours };
  if (kind === 'weekly') return { kind, time, weekday };
  return { kind, time };
}

/** The form's choice of how a run works: its permission mode, and autopilot. */
export type RoutineMode = 'autopilot' | 'yolo' | 'safe';

/** The modes the form offers, Yolo with autopilot first (a new routine's), each with a line on how a run then goes. */
export const ROUTINE_MODES: { value: RoutineMode; label: string; hint: string }[] = [
  { value: 'autopilot', label: 'Yolo with autopilot', hint: 'Allows every permission request and keeps working until the task is done or the time limit stops it.' },
  { value: 'yolo', label: 'Yolo', hint: 'Allows every permission request for one turn: the run ends when the agent first stops.' },
  { value: 'safe', label: 'Safe', hint: 'Each permission request waits for you, and the run shows as Needs you until you answer.' },
];

/** The form's mode for a routine; Safe with autopilot, which only the API sets, reads as Safe. */
export function routineMode(r: Pick<Routine, 'mode' | 'autopilot'>): RoutineMode {
  if (r.mode === 'safe') return 'safe';
  return r.autopilot ? 'autopilot' : 'yolo';
}

/** A routine's mode in words, as its card shows it. */
export function routineModeLabel(r: Pick<Routine, 'mode' | 'autopilot'>): string {
  if (r.mode === 'safe') return r.autopilot ? 'Safe with autopilot' : 'Safe';
  return r.autopilot ? 'Yolo with autopilot' : 'Yolo';
}

/** The fields the form sends for its mode. */
export function routineModeInput(mode: RoutineMode): Pick<RoutineInput, 'mode' | 'autopilot'> {
  return { mode: mode === 'safe' ? 'safe' : 'yolo', autopilot: mode === 'autopilot' };
}

/** How a run ended, in a word or two; a running run whose Task waits for the owner needs you. */
export function outcomeLabel(run: Pick<RoutineRun, 'outcome'>, taskState?: SessionState): string {
  if (run.outcome === 'running' && (taskState === 'awaiting_permission' || taskState === 'awaiting_answer')) return 'Needs you';
  const labels: Record<RoutineOutcome, string> = {
    running: 'Running',
    finished: 'Finished',
    failed: 'Failed',
    cancelled: 'Cancelled',
    time_limit: 'Stopped at the time limit',
    skipped: 'Skipped',
  };
  return labels[run.outcome] ?? run.outcome;
}

export type OutcomeTone = 'accent' | 'attention' | 'error' | 'warning' | 'muted' | 'ink';

export function outcomeTone(run: Pick<RoutineRun, 'outcome'>, taskState?: SessionState): OutcomeTone {
  if (run.outcome === 'running') return taskState === 'awaiting_permission' || taskState === 'awaiting_answer' ? 'attention' : 'accent';
  if (run.outcome === 'failed') return 'error';
  if (run.outcome === 'time_limit') return 'warning';
  if (run.outcome === 'finished') return 'ink';
  return 'muted';
}

export const TRIGGER_LABEL: Record<RoutineRun['trigger'], string> = {
  schedule: 'On schedule',
  missed: 'Missed while uam was stopped',
  manual: 'Run now',
};

/** A run's moment: "Thu 1 Oct, 09:00" in this browser's locale and zone. */
export function runTime(iso: string, locale?: string): string {
  return new Date(iso).toLocaleString(locale, { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}

/** How far ahead a time is: "in 5 min", "in 3 h", "in 2 days"; "now" once due. */
export function untilText(iso: string, now = Date.now()): string {
  const m = Math.round((new Date(iso).getTime() - now) / 60000);
  if (m < 1) return 'now';
  if (m < 60) return `in ${m} min`;
  const h = Math.round(m / 60);
  if (h < 48) return `in ${h} h`;
  return `in ${Math.round(h / 24)} days`;
}
