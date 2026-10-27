import { useApi } from '../ApiContext';
import { Terminal } from 'lucide-react';
import { useState } from 'react';
import { describeError, type BackgroundTasks as Snapshot } from '../api';
import { Button } from './ui/button';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/**
 * The Task's background shells in the composer toolbar (DESIGN.md Background tasks): the
 * terminal glyph, `accent` and how many run while any runs (else the glyph alone), and on a
 * click a popover listing each with its status in words, command and Stop.
 */
/** The provider's task statuses in words; another reported word shows as it came. */
const STATUS: Record<string, string> = { running: 'Running', idle: 'Idle', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled' };

/** Why Stop is disabled, or what it does. */
function stopTip(locked: boolean, known: boolean): string {
  if (locked) return 'This thread is read-only.';
  if (!known) return 'Refresh the connection to check this task before stopping it.';
  return 'Stop this background shell';
}

export function BackgroundTasks({ sessionId, snapshot, locked }: Readonly<{ sessionId: string; snapshot: Snapshot | undefined; locked: boolean }>) {
  const api = useApi();
  const [response, setResponse] = useState<{ source: Snapshot | undefined; snapshot: Snapshot } | null>(null);
  const [requests, setRequests] = useState<Record<string, { pending?: boolean; requested?: boolean; error?: string }>>({});
  // A newer SSE observation wins over a cancellation response started from an older snapshot.
  const shown = response && response.source === snapshot ? response.snapshot : snapshot;
  const running = shown?.tasks.filter((task) => task.status === 'running').length ?? 0;
  // A stop kills the shell, so it is confirmed first (DESIGN.md Confirmations).
  const stopConfirm = useConfirm<{ id: string; description: string; command: string }>();
  if (!shown?.tasks.length) return null;
  async function stop(id: string) {
    if (requests[id]?.pending || locked || !shown?.known) return;
    setRequests((r) => ({ ...r, [id]: { pending: true } }));
    try {
      const result = await api.cancelBackgroundTask(sessionId, id);
      setResponse({ source: snapshot, snapshot: result.background_tasks });
      setRequests((r) => ({ ...r, [id]: { requested: result.accepted } }));
    } catch (e) {
      setRequests((r) => ({ ...r, [id]: { error: describeError(e) } }));
    }
  }
  const active = shown.known && running > 0;
  const status = shown.known ? `${running} running` : 'Status unavailable';
  const stopTarget = stopConfirm.target ? `“${stopConfirm.target.description}”` : 'this background task';
  return (
    <>
      <Popover.Root>
        <Tip label={`Background tasks · ${status}`}>
          <Popover.Trigger render={<Button id="composer-background-tasks" size="sm" variant="subtle" aria-label={`Background tasks: ${status}`} className="px-1.5 text-caption tabular-nums text-muted pointer-coarse:min-w-11" />}>
            {/* The glyph turns `accent` while any runs; the list says which, on demand. */}
            <Terminal aria-hidden="true" className={active ? 'text-accent' : 'text-faint'} />
            {/* The number is how many run, as the name says; with none running (or none known), the glyph alone. */}
            {active && running}
          </Popover.Trigger>
        </Tip>
        <Popover.Content className="w-96 gap-2">
          <Popover.Title>Background tasks</Popover.Title>
          <Popover.Description>{shown.known ? status : 'Last reported tasks. Their current status is unavailable.'}</Popover.Description>
          <ul className="max-h-64 space-y-2 overflow-y-auto overflow-x-hidden overscroll-contain text-caption text-muted">
            {shown.tasks.map((task) => (
              <li key={task.id} className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-body" title={task.description || task.command}>{task.description || 'Shell task'}</span>
                  <span className="shrink-0">{shown.known ? STATUS[task.status] ?? task.status : 'Unknown'}</span>
                  {task.status === 'running' && <Tip label={stopTip(locked, shown.known)}>
                    <Button size="sm" variant="subtle" aria-label={`Stop background task: ${task.description || task.command}`} loading={!!requests[task.id]?.pending} disabled={locked || !shown.known || requests[task.id]?.requested} onClick={() => stopConfirm.ask({ id: task.id, description: task.description || 'Shell task', command: task.command })}>
                      {requests[task.id]?.requested ? 'Stop requested' : 'Stop'}
                    </Button>
                  </Tip>}
                </div>
                <code className="block truncate font-mono text-code-sm" title={task.command}>{task.command}</code>
                {requests[task.id]?.error && <p role="alert" className="pt-1 text-error">{requests[task.id].error}</p>}
              </li>
            ))}
          </ul>
        </Popover.Content>
      </Popover.Root>
      <AlertDialog
        {...stopConfirm.props}
        title={`Stop ${stopTarget}?`}
        description="This kills the process. Output it has not written yet is lost, and the agent is not told."
        confirmLabel="Stop task"
        onConfirm={() => {
          const id = stopConfirm.target?.id;
          stopConfirm.close();
          if (id) void stop(id);
        }}
      >
        {stopConfirm.target && (
          <code className="mt-3 block truncate rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm text-ink" title={stopConfirm.target.command}>
            {stopConfirm.target.command}
          </code>
        )}
      </AlertDialog>
    </>
  );
}
