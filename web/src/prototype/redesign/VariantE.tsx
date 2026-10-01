// PROTOTYPE (throwaway): Variant E "Board". Everyone knows a Trello board: work is a card, and
// where the card sits says its state. Cards move between columns on their own as agents work.
// Planned work (the Planner) is just the first column, so Tasks and the Planner become one
// surface. Each card carries a plain sentence and at most one button: the obvious next step.
// Opening a card slides a drawer over the board that leads with "Your move".

import { ArrowUp, Check, CircleSlash, GitBranch, Lightbulb, Paperclip, Plus, X } from 'lucide-react';
import { cn } from '../../lib/cn';
import type { MockTask } from '../../mock/data';
import { ago, byRecent, live, nameOf, needsYou, nextAction, pendingInteraction, projectChanges, projectOf, projects, receipt, sentence, tasks, visibleItems } from './data';
import { ProjectBadge, Prose, StateDot, ToolLine } from './shared';

export const name = 'Board';

const PLANNED = [
  { title: 'Send subagent previews only for recent Tasks', project: 'p1', note: 'Suggested by an agent · confirm or dismiss' },
  { title: 'Document the release checklist', project: 'p1', note: 'Planned' },
  { title: 'Try the new Go toolchain on CI', project: 'p1', note: 'Planned' },
  { title: 'Dark-mode screenshots for the docs', project: 'p3', note: 'Suggested by an agent · confirm or dismiss' },
];

export function VariantE({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const current = tasks.find((t) => t.id === openId) ?? null;
  const columns = [
    { key: 'you', title: 'Waiting for you', hint: 'Answer to keep them going', items: tasks.filter(needsYou).sort(byRecent) },
    { key: 'work', title: 'Agents working', hint: 'Nothing to do here', items: tasks.filter(live).sort(byRecent) },
    { key: 'review', title: 'Done — check it', hint: 'Look over the changes, then close', items: tasks.filter((t) => !needsYou(t) && !live(t)).sort(byRecent) },
  ];
  return (
    <div className="flex h-dvh flex-col bg-sunken text-body">
      <header className="flex flex-wrap items-center gap-3 bg-canvas px-5 py-3">
        <span className="text-title font-bold text-ink">uam</span>
        <div className="flex items-center gap-1 overflow-x-auto text-caption">
          <span className="shrink-0 whitespace-nowrap rounded-full bg-primary px-3 py-1 text-on-primary">All projects</span>
          {projects.map((p) => (
            <span key={p.id} className="flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-body hover:bg-tint-hover">
              <ProjectBadge badge={p.badge} size={14} /> {p.name}
            </span>
          ))}
        </div>
        <button className="ml-auto inline-flex items-center gap-1.5 rounded-sm bg-primary px-3 py-1.5 text-ui font-medium text-on-primary">
          <Plus className="size-4" /> New task
        </button>
      </header>

      <div className="flex min-h-0 flex-1 gap-4 overflow-x-auto p-4">
        <Column title="Planned" hint="Start one when you are ready" count={PLANNED.length}>
          {PLANNED.map((c) => {
            const suggested = c.note.startsWith('Suggested');
            return (
              <div key={c.title} className={cn('rounded-md p-3', suggested ? 'border border-dashed border-hairline-strong bg-canvas' : 'bg-raised shadow-raised')}>
                <p className="text-ui font-medium text-ink">{c.title}</p>
                <p className="mt-1 flex items-center gap-1.5 text-caption text-muted">
                  {suggested && <Lightbulb className="size-3.5 text-warning" />}
                  {c.note}
                </p>
                <div className="mt-2 flex gap-2">
                  {suggested ? (
                    <>
                      <button className="rounded-sm bg-raised px-2.5 py-1 text-caption font-medium text-ink shadow-raised">Keep</button>
                      <button className="rounded-sm px-2.5 py-1 text-caption text-muted">Dismiss</button>
                    </>
                  ) : (
                    <button className="rounded-sm bg-raised px-2.5 py-1 text-caption font-medium text-ink shadow-raised">▶ Start</button>
                  )}
                </div>
              </div>
            );
          })}
        </Column>
        {columns.map((c) => (
          <Column key={c.key} title={c.title} hint={c.hint} count={c.items.length} loud={c.key === 'you'}>
            {c.items.map((t) => <Card key={t.id} task={t} on={t.id === openId} onOpen={() => open(t.id)} />)}
          </Column>
        ))}
      </div>

      {current && <Drawer task={current} onClose={() => open(null)} />}
    </div>
  );
}

function Column({ title, hint, count, loud, children }: { title: string; hint: string; count: number; loud?: boolean; children: React.ReactNode }) {
  return (
    <section className="flex min-w-[280px] flex-1 flex-col">
      <header className="mb-2 px-1">
        <h2 className={cn('flex items-center gap-2 text-title', loud ? 'text-attention' : 'text-ink')}>
          {title} <span className="text-caption font-normal text-muted">{count}</span>
        </h2>
        <p className="text-caption text-muted">{hint}</p>
      </header>
      <div className="flex-1 space-y-2 overflow-y-auto pb-4">{children}</div>
    </section>
  );
}

function Card({ task, on, onOpen }: { task: MockTask; on: boolean; onOpen: () => void }) {
  const p = projectOf(task);
  const action = nextAction(task);
  return (
    <article onClick={onOpen} className={cn('cursor-pointer rounded-md bg-raised p-3 shadow-raised', on && 'ring-2 ring-accent')}>
      <p className="flex items-center gap-1.5 text-meta text-muted">
        <ProjectBadge badge={p.badge} size={14} /> {p.name}
        <span className="ml-auto">{ago(task.updated_at)}</span>
      </p>
      <p className="mt-1.5 text-ui font-medium text-ink">{nameOf(task)}</p>
      <p className={cn('mt-1 flex items-start gap-1.5 text-caption', needsYou(task) ? 'text-attention' : 'text-muted')}>
        <StateDot state={task.state} className="mt-1" />
        <span className="line-clamp-2">{sentence(task)}</span>
      </p>
      {action && (
        <button className={cn('mt-2.5 w-full rounded-sm py-1.5 text-caption font-medium', needsYou(task) ? 'bg-primary text-on-primary' : 'bg-tint-well text-ink')}>{action}</button>
      )}
    </article>
  );
}

function Drawer({ task, onClose }: { task: MockTask; onClose: () => void }) {
  const p = projectOf(task);
  const i = pendingInteraction(task);
  const files = projectChanges(task);
  return (
    <div className="fixed inset-0 z-30 flex justify-end bg-backdrop">
      <section className="flex h-full w-full flex-col bg-canvas shadow-modal lg:w-[58%]">
        <header className="flex items-start gap-3 px-6 pt-5 pb-3">
          <div className="min-w-0">
            <p className="flex items-center gap-1.5 text-caption text-muted">
              <ProjectBadge badge={p.badge} size={14} /> {p.name} <GitBranch className="size-3" /> {p.branch ?? 'no branch'}
            </p>
            <h2 className="mt-1 text-display-md text-ink">{nameOf(task)}</h2>
          </div>
          <button aria-label="Close" onClick={onClose} className="ml-auto rounded-sm p-1.5 text-muted hover:bg-tint-hover">
            <X className="size-5" />
          </button>
        </header>

        <div className="mx-6 rounded-lg bg-raised p-4 shadow-raised">
          <p className="text-eyebrow text-muted uppercase">Your move</p>
          {i ? (
            <>
              <p className="mt-1 text-title text-ink">{i.title}</p>
              {i.detail && <pre className="mt-2 rounded-sm bg-code-bg px-3 py-2 font-mono text-code-sm">{i.detail}</pre>}
              <div className="mt-3 flex gap-2">
                <button className="inline-flex items-center gap-1.5 rounded-sm bg-primary px-3 py-1.5 text-ui text-on-primary"><Check className="size-4" /> Allow</button>
                <button className="inline-flex items-center gap-1.5 rounded-sm px-3 py-1.5 text-ui text-error"><CircleSlash className="size-4" /> Don’t allow</button>
              </div>
            </>
          ) : live(task) ? (
            <p className="mt-1 text-ui text-body">
              Nothing yet — it is working ({receipt(visibleItems(task))} so far). You can still add a message below; it reads it at its next step.
            </p>
          ) : (
            <>
              <p className="mt-1 text-ui text-body">It changed {files.length} files. Look them over, then close the task.</p>
              <div className="mt-3 flex gap-2">
                <button className="rounded-sm bg-primary px-3 py-1.5 text-ui text-on-primary">Review changes</button>
                <button className="rounded-sm bg-tint-well px-3 py-1.5 text-ui text-ink">Looks good — close</button>
              </div>
            </>
          )}
        </div>

        <div className="flex-1 overflow-y-auto px-6 py-5">
          <p className="mb-3 text-eyebrow text-muted uppercase">Conversation</p>
          <div className="space-y-4 text-chat">
            {visibleItems(task).map((it) =>
              it.kind === 'user' ? (
                <div key={it.id} className="rounded-md bg-bubble px-4 py-3 text-ink">{it.text}</div>
              ) : it.kind === 'tool' ? (
                <ToolLine key={it.id} item={it} />
              ) : it.kind === 'reasoning' ? null : (
                <Prose key={it.id} text={it.text ?? ''} className="text-ink" />
              ),
            )}
          </div>
        </div>
        <div className="px-6 pb-5">
          <div className="flex items-center gap-3 rounded-lg bg-raised px-4 py-3 shadow-float">
            <Paperclip className="size-4 text-faint" />
            <span className="flex-1 text-ui text-muted">Add a message…</span>
            <button aria-label="Send" className="grid size-8 place-items-center rounded-full bg-primary text-on-primary"><ArrowUp className="size-4" /></button>
          </div>
        </div>
      </section>
    </div>
  );
}
