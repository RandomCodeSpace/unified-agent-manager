// PROTOTYPE (throwaway): Variant A "Triage". The home is an inbox ordered by what needs the
// owner: questions and permissions are answered in place, without opening the Task. Opening a
// Task splits the screen; the inbox stays on the left so triage never loses its place.

import { ArrowUp, Check, ChevronRight, CircleSlash, FolderGit2, GitBranch, Inbox, KanbanSquare, ListTodo, Paperclip, Plus, Settings, X } from 'lucide-react';
import type { Interaction } from '../../api';
import { cn } from '../../lib/cn';
import { ago, byRecent, live, nameOf, needsYou, pendingInteraction, projectOf, stateLabel, tasks, visibleItems } from './data';
import { ProjectBadge, Prose, StateDot, ToolLine, toneText } from './shared';
import type { MockTask } from '../../mock/data';

export const name = 'Triage';

export function VariantA({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const attention = tasks.filter(needsYou).sort(byRecent);
  const working = tasks.filter(live).sort(byRecent);
  const rest = tasks.filter((t) => !needsYou(t) && !live(t)).sort(byRecent);
  const current = tasks.find((t) => t.id === openId) ?? null;

  return (
    <div className="flex h-dvh bg-canvas text-body">
      <Rail attention={attention.length} />
      <main className={cn('flex min-w-0 flex-col overflow-y-auto', current ? 'hidden w-[440px] shrink-0 border-r border-hairline md:flex' : 'flex-1')}>
        <header className="flex w-full items-center gap-3 px-6 pt-6 pb-4">
          <h1 className="text-display-md text-ink">Inbox</h1>
          <span className="rounded-full bg-attention-wash px-2 py-0.5 text-caption font-semibold text-attention">{attention.length} need you</span>
          <button className="ml-auto inline-flex items-center gap-1.5 rounded-sm bg-primary px-3 py-1.5 text-ui font-medium text-on-primary">
            <Plus className="size-4" /> New task
          </button>
        </header>
        <div className="w-full space-y-8 px-6 pb-16">
          <section>
            <SectionTitle>Needs you</SectionTitle>
            <div className={cn('grid items-start gap-3', !current && 'lg:grid-cols-2 2xl:grid-cols-3')}>
              {attention.map((t) => (
                <AttentionCard key={t.id} task={t} compact={!!current} onOpen={() => open(t.id)} />
              ))}
            </div>
          </section>
          <section>
            <SectionTitle>Working</SectionTitle>
            <div className="divide-y divide-hairline rounded-md bg-raised shadow-raised">
              {working.map((t) => (
                <Row key={t.id} task={t} selected={t.id === openId} onOpen={() => open(t.id)} />
              ))}
            </div>
          </section>
          <section>
            <SectionTitle>Finished and idle</SectionTitle>
            <div className="divide-y divide-hairline">
              {rest.map((t) => (
                <Row key={t.id} task={t} quiet selected={t.id === openId} onOpen={() => open(t.id)} />
              ))}
            </div>
          </section>
        </div>
      </main>
      {current && <TaskPane task={current} onClose={() => open(null)} />}
    </div>
  );
}

function Rail({ attention }: { attention: number }) {
  const items = [
    { icon: Inbox, label: 'Inbox', active: true, count: attention },
    { icon: ListTodo, label: 'All tasks' },
    { icon: KanbanSquare, label: 'Planner' },
    { icon: FolderGit2, label: 'Projects' },
  ];
  return (
    <nav className="hidden w-[68px] shrink-0 flex-col items-center gap-1 bg-rail py-4 md:flex">
      <div className="mb-4 grid size-9 place-items-center rounded-md bg-primary text-ui font-bold text-on-primary">u</div>
      {items.map(({ icon: Icon, label, active, count }) => (
        <button key={label} className={cn('relative flex w-14 flex-col items-center gap-1 rounded-md py-2 text-meta', active ? 'bg-tint-selected text-ink' : 'text-muted')}>
          <Icon className="size-5" />
          {label}
          {count ? <span className="absolute top-1 right-2 rounded-full bg-attention px-1.5 text-meta font-semibold text-on-accent">{count}</span> : null}
        </button>
      ))}
      <button className="mt-auto flex w-14 flex-col items-center gap-1 py-2 text-meta text-muted">
        <Settings className="size-5" />
        Settings
      </button>
    </nav>
  );
}

const SectionTitle = ({ children }: { children: string }) => <h2 className="mb-3 text-eyebrow text-muted uppercase">{children}</h2>;

function AttentionCard({ task, compact, onOpen }: { task: MockTask; compact: boolean; onOpen: () => void }) {
  const p = projectOf(task);
  const i = pendingInteraction(task);
  return (
    <article className="rounded-lg bg-raised p-4 shadow-raised">
      <button onClick={onOpen} className="flex w-full items-center gap-3 text-left">
        <ProjectBadge badge={p.badge} size={28} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-title text-ink">{nameOf(task)}</span>
          <span className="block truncate text-caption text-muted">{p.name}</span>
        </span>
        <span className="shrink-0 text-caption text-muted">{ago(task.updated_at)}</span>
        <ChevronRight className="size-4 shrink-0 text-faint" />
      </button>
      {i ? <InlineAnswer interaction={i} compact={compact} /> : <p className="mt-2 text-ui text-attention">{stateLabel[task.state]}</p>}
    </article>
  );
}

function InlineAnswer({ interaction: i, compact }: { interaction: Interaction; compact: boolean }) {
  if (i.kind === 'permission') {
    return (
      <div className="mt-3 rounded-md bg-attention-wash/40 p-3">
        <p className="text-ui font-medium text-ink">{i.title}</p>
        {i.detail && <pre className="mt-2 overflow-x-auto rounded-sm bg-code-bg px-3 py-2 font-mono text-code-sm text-ink">{i.detail}</pre>}
        <div className="mt-3 flex flex-wrap gap-2">
          {(i.options ?? []).map((o) => (
            <button key={o.id} className={cn('inline-flex items-center gap-1.5 rounded-sm px-3 py-1.5 text-ui font-medium', o.reject ? 'text-error hover:bg-error-wash' : o.id === 'once' ? 'bg-primary text-on-primary' : 'bg-raised text-ink shadow-raised')}>
              {o.reject ? <CircleSlash className="size-3.5" /> : <Check className="size-3.5" />}
              {o.label}
            </button>
          ))}
        </div>
      </div>
    );
  }
  const q = i.questions?.[0];
  return (
    <div className="mt-3 rounded-md bg-tint-well p-3">
      <p className="text-ui font-medium text-ink">{i.title}</p>
      {q?.choices && (
        <div className="mt-2 flex flex-wrap gap-2">
          {q.choices.slice(0, compact ? 3 : 6).map((c, n) => (
            <button key={c} className={cn('rounded-full px-3 py-1 text-caption', n === 0 ? 'bg-accent text-on-accent' : 'bg-raised text-body shadow-raised')}>
              {c}
            </button>
          ))}
        </div>
      )}
      <div className="mt-3 flex items-center gap-2 rounded-sm bg-raised px-3 py-2 shadow-well">
        <span className="flex-1 text-ui text-muted">Add a note, or just send the choice…</span>
        <button aria-label="Send" className="grid size-7 place-items-center rounded-full bg-primary text-on-primary">
          <ArrowUp className="size-4" />
        </button>
      </div>
    </div>
  );
}

function Row({ task, quiet, selected, onOpen }: { task: MockTask; quiet?: boolean; selected?: boolean; onOpen: () => void }) {
  const p = projectOf(task);
  return (
    <button onClick={onOpen} className={cn('flex w-full items-center gap-3 px-4 py-3 text-left', selected && 'bg-tint-selected')}>
      <StateDot state={task.state} />
      <span className={cn('min-w-0 flex-1 truncate text-ui', quiet ? 'text-body' : 'font-medium text-ink')}>{nameOf(task)}</span>
      <span className="hidden items-center gap-1.5 text-caption text-muted sm:flex">
        <ProjectBadge badge={p.badge} size={14} />
        {p.name}
      </span>
      {task.subagents_running > 0 && <span className="rounded-full bg-info-wash px-2 text-meta text-info">{task.subagents_running} subagents</span>}
      <span className={cn('w-24 text-right text-caption', toneText(task.state))}>{quiet ? ago(task.updated_at) : stateLabel[task.state]}</span>
    </button>
  );
}

function TaskPane({ task, onClose }: { task: MockTask; onClose: () => void }) {
  const p = projectOf(task);
  return (
    <section className="flex min-w-0 flex-1 flex-col bg-raised">
      <header className="flex items-center gap-3 border-b border-hairline px-6 py-3">
        <ProjectBadge badge={p.badge} />
        <div className="min-w-0">
          <h2 className="truncate text-title text-ink">{nameOf(task)}</h2>
          <p className="flex items-center gap-1.5 text-caption text-muted">
            {p.name} <GitBranch className="size-3" /> {p.branch ?? 'no branch'} · <span className={toneText(task.state)}>{stateLabel[task.state]}</span>
          </p>
        </div>
        <div className="ml-auto flex items-center gap-1 text-ui text-body">
          {['Changes 3', 'Files', 'Terminal'].map((l) => (
            <button key={l} className="rounded-sm px-2.5 py-1.5 hover:bg-tint-hover">{l}</button>
          ))}
          <button aria-label="Close" onClick={onClose} className="ml-2 rounded-sm p-1.5 text-muted hover:bg-tint-hover">
            <X className="size-4" />
          </button>
        </div>
      </header>
      <div className="flex-1 overflow-y-auto px-8 py-6">
        <div className="space-y-4 text-chat">
          {visibleItems(task).map((it) =>
            it.kind === 'user' ? (
              <div key={it.id} className="ml-auto max-w-[85%] rounded-lg bg-bubble px-4 py-3 text-ink">
                {it.text}
              </div>
            ) : it.kind === 'tool' ? (
              <ToolLine key={it.id} item={it} className="pl-1" />
            ) : it.kind === 'reasoning' ? (
              <p key={it.id} className="text-caption text-muted italic">Thought for a moment</p>
            ) : (
              <Prose key={it.id} text={it.text ?? ''} className="text-ink" />
            ),
          )}
        </div>
      </div>
      <div className="px-8 pb-6">
        <div className="flex items-center gap-3 rounded-lg bg-raised px-4 py-3 shadow-float">
          <Paperclip className="size-4 text-faint" />
          <span className="flex-1 text-ui text-muted">Steer this turn, or queue a follow-up…</span>
          <span className="text-caption text-muted">Auto · Safe</span>
          <button aria-label="Send" className="grid size-8 place-items-center rounded-full bg-primary text-on-primary">
            <ArrowUp className="size-4" />
          </button>
        </div>
      </div>
    </section>
  );
}
