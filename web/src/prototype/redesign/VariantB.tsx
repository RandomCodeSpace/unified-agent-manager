// PROTOTYPE (throwaway): Variant B "Workbench". Projects are top-level tabs, like an editor's
// workspaces. Each tab is a three-pane bench: the Project's Tasks, the conversation, and a
// permanent inspector (run settings, changes, subagents, background work, the Planner card),
// so context never hides behind a tab or a dialog.

import { ArrowUp, Bot, ChevronDown, Cpu, FileDiff, GitBranch, KanbanSquare, Paperclip, Plus, Search, Shield, SquareTerminal, TimerReset } from 'lucide-react';
import { cn } from '../../lib/cn';
import { ago, byRecent, changes, focusTask, live, nameOf, needsYou, projectOf, projects, stateLabel, tasks, visibleItems } from './data';
import { Kbd, ProjectBadge, Prose, StateDot, ToolLine, toneText } from './shared';
import type { MockTask } from '../../mock/data';

export const name = 'Workbench';

export function VariantB({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const current = tasks.find((t) => t.id === openId) ?? focusTask;
  const project = projectOf(current);
  const mine = tasks.filter((t) => t.project_id === project.id).sort(byRecent);
  const active = mine.filter((t) => needsYou(t) || live(t));
  const done = mine.filter((t) => !needsYou(t) && !live(t));
  const attention = tasks.filter(needsYou).length;

  return (
    <div className="flex h-dvh flex-col bg-canvas text-body">
      <header className="flex h-11 shrink-0 items-end gap-1 bg-rail px-3">
        <span className="mr-3 self-center text-title font-bold text-ink">uam</span>
        <div className="flex min-w-0 items-end gap-1 overflow-x-auto">
          {projects.map((p) => {
            const count = tasks.filter((t) => t.project_id === p.id && (needsYou(t) || live(t))).length;
            const on = p.id === project.id;
            return (
              <button key={p.id} onClick={() => open(tasks.find((t) => t.project_id === p.id)?.id ?? null)} className={cn('flex h-9 shrink-0 items-center gap-2 rounded-t-md px-3 text-ui', on ? 'bg-canvas text-ink' : 'text-muted hover:text-ink')}>
                <ProjectBadge badge={p.badge} size={16} />
                {p.name}
                {count > 0 && <span className="rounded-full bg-sunken px-1.5 text-meta text-muted">{count}</span>}
              </button>
            );
          })}
          <button aria-label="Add project" className="mb-1.5 grid size-7 shrink-0 place-items-center rounded-sm text-muted hover:bg-tint-hover">
            <Plus className="size-4" />
          </button>
        </div>
        <div className="ml-auto flex items-center gap-3 self-center">
          <button className="hidden items-center gap-2 rounded-sm bg-raised px-3 py-1 text-caption text-muted shadow-well md:flex">
            <Search className="size-3.5" /> Jump to task or file <Kbd>⌘K</Kbd>
          </button>
          <span className="rounded-full bg-attention-wash px-2 py-0.5 text-caption font-semibold whitespace-nowrap text-attention">{attention}<span className="hidden md:inline"> need you</span></span>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-[248px] shrink-0 flex-col overflow-y-auto px-2 py-3 md:flex">
          <div className="mb-2 flex items-center gap-1.5 px-2 text-caption text-muted">
            <GitBranch className="size-3.5" />
            <span className="truncate">{project.branch ?? 'no branch'}</span>
          </div>
          <button className="mb-4 flex items-center gap-2 rounded-sm px-2 py-1.5 text-ui font-medium text-ink hover:bg-tint-hover">
            <Plus className="size-4" /> New task
          </button>
          <ListTitle>Active</ListTitle>
          {active.map((t) => <TaskRow key={t.id} task={t} on={t.id === current.id} onOpen={() => open(t.id)} />)}
          <ListTitle>Settled</ListTitle>
          {done.map((t) => <TaskRow key={t.id} task={t} on={t.id === current.id} onOpen={() => open(t.id)} />)}
        </aside>

        <main className="flex min-w-0 flex-1 flex-col rounded-tl-lg bg-raised shadow-raised">
          <div className="flex items-center gap-3 border-b border-hairline px-4 py-3 md:px-6">
            <h1 className="min-w-0 truncate text-display-sm text-ink">{nameOf(current)}</h1>
            <span className={cn('flex shrink-0 items-center gap-1.5 text-caption', toneText(current.state))}>
              <StateDot state={current.state} /> {stateLabel[current.state]}
            </span>
            <div className="ml-auto hidden items-center gap-1 rounded-sm bg-sunken p-0.5 text-caption md:flex">
              {['Conversation', 'Changes', 'Files', 'Terminal'].map((l, n) => (
                <button key={l} className={cn('rounded-xs px-2.5 py-1', n === 0 ? 'bg-raised text-ink shadow-raised' : 'text-muted')}>{l}</button>
              ))}
            </div>
          </div>
          <div className="flex-1 overflow-y-auto px-5 py-6 md:px-8">
            <div className="space-y-4 text-chat">
              {visibleItems(current).map((it) =>
                it.kind === 'user' ? (
                  <div key={it.id} className="border-l-2 border-ink pl-4 font-medium text-ink">{it.text}</div>
                ) : it.kind === 'tool' ? (
                  <ToolLine key={it.id} item={it} />
                ) : it.kind === 'reasoning' ? null : (
                  <Prose key={it.id} text={it.text ?? ''} className="text-ink" />
                ),
              )}
            </div>
          </div>
          <div className="border-t border-hairline px-6 py-3">
            <div>
              <div className="flex items-center gap-3 rounded-md bg-tint-well px-4 py-3">
                <Paperclip className="size-4 text-faint" />
                <span className="flex-1 text-ui text-muted">Message the agent — @ for files, / for commands</span>
                <button aria-label="Send" className="grid size-8 place-items-center rounded-full bg-primary text-on-primary">
                  <ArrowUp className="size-4" />
                </button>
              </div>
            </div>
          </div>
        </main>

        <Inspector task={current} />
      </div>
    </div>
  );
}

const ListTitle = ({ children }: { children: string }) => <p className="mt-3 mb-1 px-2 text-eyebrow text-muted uppercase">{children}</p>;

function TaskRow({ task, on, onOpen }: { task: MockTask; on: boolean; onOpen: () => void }) {
  return (
    <button onClick={onOpen} className={cn('flex w-full items-center gap-2.5 rounded-sm px-2 py-1.5 text-left', on ? 'bg-tint-selected text-ink' : 'text-body hover:bg-tint-hover')}>
      <span className="grid w-3.5 place-items-center"><StateDot state={task.state} /></span>
      <span className="min-w-0 flex-1 truncate text-ui">{nameOf(task)}</span>
      <span className="text-meta text-muted">{ago(task.updated_at)}</span>
    </button>
  );
}

function Inspector({ task }: { task: MockTask }) {
  const files = changes[task.id] ?? [];
  return (
    <aside className="hidden w-[300px] shrink-0 overflow-y-auto bg-canvas px-4 py-4 text-ui lg:block">
      <Section icon={Cpu} title="Run">
        <Field label="Model" value="Auto → MAI-Code-1.1-Flash" />
        <Field label="Mode" value={<span className="flex items-center gap-1"><Shield className="size-3.5 text-success" /> Safe · Interactive</span>} />
        <Field label="Context" value={<Meter value={0.42} label="42% of 200k" />} />
        <Field label="Usage" value="3.2 AI units" />
      </Section>
      <Section icon={FileDiff} title={`Changes · ${files.length || 3}`}>
        {(files.length ? files : [{ path: 'internal/vterm/redraw.go', additions: 14, deletions: 2 }, { path: 'internal/vterm/redraw_test.go', additions: 41, deletions: 0 }, { path: 'docs/terminal.md', additions: 6, deletions: 1 }]).map((f) => (
          <div key={f.path} className="flex items-center gap-2 py-1">
            <span className="min-w-0 flex-1 truncate font-mono text-code-sm text-ink">{f.path}</span>
            <span className="text-meta text-success">+{f.additions}</span>
            <span className="text-meta text-error">−{f.deletions}</span>
          </div>
        ))}
      </Section>
      <Section icon={TimerReset} title="Background · 1 running">
        {task.background_tasks?.tasks.map((b) => (
          <div key={b.id} className="flex items-center gap-2 py-1">
            <SquareTerminal className="size-3.5 text-faint" />
            <span className="min-w-0 flex-1 truncate text-caption text-body">{b.description}</span>
            <span className={cn('text-meta', b.status === 'running' ? 'text-accent' : 'text-muted')}>{b.status}</span>
          </div>
        ))}
      </Section>
      <Section icon={Bot} title="Subagents · none">
        <p className="text-caption text-muted">Subagents this Task starts appear here, each openable on its own.</p>
      </Section>
      <Section icon={KanbanSquare} title="Planner">
        <div className="rounded-md bg-raised p-3 shadow-raised">
          <p className="text-meta text-muted">Terminal reliability › Re-attach</p>
          <p className="mt-1 text-ui font-medium text-ink">Replay focus events on re-attach</p>
          <div className="mt-2 flex items-center gap-2 text-meta">
            <span className="rounded-full bg-info-wash px-2 py-0.5 text-info">In progress</span>
            <span className="text-muted">accept: go test ./internal/vterm/...</span>
          </div>
        </div>
      </Section>
      <button className="mt-2 flex w-full items-center justify-between rounded-sm px-2 py-1.5 text-caption text-muted hover:bg-tint-hover">
        Settle, archive, rename… <ChevronDown className="size-3.5" />
      </button>
    </aside>
  );
}

function Section({ icon: Icon, title, children }: { icon: typeof Cpu; title: string; children: React.ReactNode }) {
  return (
    <section className="mb-5">
      <h3 className="mb-2 flex items-center gap-1.5 text-eyebrow text-muted uppercase">
        <Icon className="size-3.5" /> {title}
      </h3>
      {children}
    </section>
  );
}

const Field = ({ label, value }: { label: string; value: React.ReactNode }) => (
  <div className="flex items-center justify-between gap-3 py-1">
    <span className="text-caption text-muted">{label}</span>
    <span className="text-caption text-ink">{value}</span>
  </div>
);

const Meter = ({ value, label }: { value: number; label: string }) => (
  <span className="flex items-center gap-2">
    <span className="h-1.5 w-20 overflow-hidden rounded-full bg-sunken">
      <span className="block h-full origin-left rounded-full bg-accent" style={{ transform: `scaleX(${value})` }} />
    </span>
    {label}
  </span>
);
