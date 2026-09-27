import { Terminal } from 'lucide-react';
import { useState } from 'react';
import { api, describeError, type BackgroundTasks as Snapshot } from '../api';
import { WorkingMark } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/**
 * The Task's background shells in the composer toolbar (DESIGN.md Background tasks): the
 * working mark and how many run (else the terminal glyph and how many there were), and on a
 * click a popover listing each with its status, command and Stop.
 */
/** Why Stop is disabled, or what it does. */
function stopTip(locked: boolean, known: boolean): string {
  if (locked) return 'This task is read-only.';
  if (!known) return 'Refresh the connection to check this task before stopping it.';
  return 'Stop this background shell';
}

export function BackgroundTasks({ sessionId, snapshot, locked }: Readonly<{ sessionId: string; snapshot: Snapshot | undefined; locked: boolean }>) {
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
            {active ? <WorkingMark /> : <Terminal aria-hidden="true" className="text-faint" />}
            {active ? running : shown.tasks.length}
          </Popover.Trigger>
        </Tip>
        <Popover.Content className="w-96 max-w-[calc(100vw-16px)] gap-2">
          <Popover.Title>Background tasks</Popover.Title>
          <Popover.Description>{shown.known ? status : 'Last reported tasks. Their current status is unavailable.'}</Popover.Description>
          <ul className="max-h-64 space-y-2 overflow-y-auto overscroll-contain text-caption text-muted">
            {shown.tasks.map((task) => (
              <li key={task.id} className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-body" title={task.description || task.command}>{task.description || 'Shell task'}</span>
                  {shown.known && task.status === 'running' ? (
                    <Chip tone="accent">
                      <WorkingMark />
                      Running
                    </Chip>
                  ) : (
                    <span className="shrink-0 capitalize">{shown.known ? task.status : 'Unknown'}</span>
                  )}
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
