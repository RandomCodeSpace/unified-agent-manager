// PROTOTYPE (throwaway): Variant C "Launchpad". No sidebar at all. The home is a launcher: one
// big prompt to start work, then Tasks as tiles grouped by Project, built from stable data only.
// Opening a Task is a focus mode: a centred reading column, and a dock along the bottom edge
// that holds every live Task for one-click switching (like a taskbar).

import { ArrowLeft, ArrowUp, ChevronDown, Cpu, GitBranch, LayoutGrid, Paperclip, Shield } from 'lucide-react';
import { cn } from '../../lib/cn';
import { ago, byRecent, live, nameOf, needsYou, projectOf, projects, stateLabel, tasks, visibleItems } from './data';
import { Kbd, ProjectBadge, Prose, StateDot, ToolLine, toneText } from './shared';
import type { MockTask } from '../../mock/data';

export const name = 'Launchpad';

export function VariantC({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const current = tasks.find((t) => t.id === openId) ?? null;
  return current ? <Focus task={current} open={open} /> : <Home open={open} />;
}

function Home({ open }: { open: (id: string | null) => void }) {
  const attention = tasks.filter(needsYou).sort(byRecent);
  return (
    <div className="min-h-dvh overflow-y-auto bg-canvas text-body">
      <header className="flex items-center px-6 py-4 md:px-10">
        <span className="text-title font-bold text-ink">uam</span>
        <nav className="ml-8 hidden gap-5 text-ui text-muted md:flex">
          <span className="text-ink">Tasks</span>
          <span>Planner</span>
          <span>Projects</span>
        </nav>
        <span className="ml-auto flex items-center gap-2 text-caption text-muted">
          <Kbd>⌘K</Kbd> anywhere
        </span>
      </header>

      <section className="px-6 pt-10 pb-12 md:px-10 md:pt-14">
        <h1 className="text-center text-[28px] leading-tight font-semibold tracking-tight text-ink md:text-[34px]">What should an agent do next?</h1>
        <div className="mt-6 rounded-lg bg-raised p-4 shadow-float">
          <p className="min-h-16 text-chat-lg text-muted">Describe the work. @ a file, / a command, drop a screenshot.</p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Chip><ProjectBadge badge={projects[0].badge} size={14} /> {projects[0].name} <ChevronDown className="size-3" /></Chip>
            <Chip><Cpu className="size-3.5" /> Auto <ChevronDown className="size-3" /></Chip>
            <Chip><Shield className="size-3.5 text-success" /> Safe <ChevronDown className="size-3" /></Chip>
            <Paperclip className="ml-1 size-4 text-faint" />
            <button className="ml-auto inline-flex items-center gap-1.5 rounded-full bg-primary px-4 py-2 text-ui font-medium text-on-primary">
              Start task <ArrowUp className="size-4" />
            </button>
          </div>
        </div>
      </section>

      <div className="space-y-10 px-6 pb-20 md:px-10">
        <section>
          <h2 className="mb-3 text-eyebrow text-attention uppercase">Waiting on you · {attention.length}</h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {attention.map((t) => <Tile key={t.id} task={t} loud onOpen={() => open(t.id)} />)}
          </div>
        </section>
        {projects.map((p) => {
          const mine = tasks.filter((t) => t.project_id === p.id && !needsYou(t)).sort(byRecent);
          if (!mine.length) return null;
          return (
            <section key={p.id}>
              <h2 className="mb-3 flex items-center gap-2 text-title text-ink">
                <ProjectBadge badge={p.badge} /> {p.name}
                {p.branch && <span className="flex items-center gap-1 text-caption font-normal text-muted"><GitBranch className="size-3" />{p.branch}</span>}
              </h2>
              <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5">
                {mine.map((t) => <Tile key={t.id} task={t} onOpen={() => open(t.id)} />)}
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}

const Chip = ({ children }: { children: React.ReactNode }) => (
  <span className="inline-flex items-center gap-1.5 rounded-full bg-tint-well px-3 py-1 text-caption text-body">{children}</span>
);

function Tile({ task, loud, onOpen }: { task: MockTask; loud?: boolean; onOpen: () => void }) {
  const p = projectOf(task);
  return (
    <button
      onClick={onOpen}
      className={cn('flex h-[116px] flex-col rounded-lg p-4 text-left transition-transform hover:-translate-y-0.5 motion-reduce:transition-none', loud ? 'bg-attention-wash/50 shadow-raised' : 'bg-raised shadow-raised')}
    >
      <span className="flex items-center gap-2 text-caption">
        <StateDot state={task.state} />
        <span className={toneText(task.state)}>{stateLabel[task.state]}</span>
        <span className="ml-auto text-muted">{ago(task.updated_at)}</span>
      </span>
      <span className="mt-2 line-clamp-2 text-title text-ink">{nameOf(task)}</span>
      {loud && <span className="mt-auto flex items-center gap-1.5 text-caption text-muted"><ProjectBadge badge={p.badge} size={14} /> {p.name}</span>}
    </button>
  );
}

function Focus({ task, open }: { task: MockTask; open: (id: string | null) => void }) {
  const p = projectOf(task);
  const dock = tasks.filter((t) => live(t) || needsYou(t)).sort(byRecent);
  return (
    <div className="flex h-dvh flex-col bg-canvas text-body">
      <header className="flex items-center gap-3 px-6 py-3">
        <button onClick={() => open(null)} className="inline-flex items-center gap-1.5 rounded-sm px-2 py-1 text-ui text-muted hover:bg-tint-hover">
          <ArrowLeft className="size-4" /> <LayoutGrid className="size-4" />
        </button>
        <span className="flex items-center gap-1.5 text-caption text-muted"><ProjectBadge badge={p.badge} size={14} /> {p.name} /</span>
        <h1 className="truncate text-title text-ink">{nameOf(task)}</h1>
        <span className={cn('flex items-center gap-1.5 text-caption', toneText(task.state))}><StateDot state={task.state} /> {stateLabel[task.state]}</span>
        <div className="ml-auto flex items-center gap-2 text-caption">
          {['Changes · 3', 'Files', 'Terminal'].map((l) => (
            <button key={l} className="rounded-full bg-raised px-3 py-1 text-body shadow-raised">{l}</button>
          ))}
        </div>
      </header>

      <div className="flex-1 overflow-y-auto">
        <article className="space-y-6 px-6 pt-6 pb-40 text-chat-lg leading-relaxed md:px-12">
          {visibleItems(task).map((it) =>
            it.kind === 'user' ? (
              <p key={it.id} className="text-display-sm text-ink">{it.text}</p>
            ) : it.kind === 'tool' ? (
              <ToolLine key={it.id} item={it} className="border-l-2 border-hairline pl-3" />
            ) : it.kind === 'reasoning' ? null : (
              <Prose key={it.id} text={it.text ?? ''} className="text-ink" />
            ),
          )}
        </article>
      </div>

      <div className="pointer-events-none fixed inset-x-0 bottom-0 flex flex-col items-center gap-3 pb-3">
        <div className="pointer-events-auto flex w-[calc(100%-2rem)] md:w-[calc(100%-6rem)] items-center gap-3 rounded-full bg-raised py-2 pr-2 pl-5 shadow-float">
          <span className="flex-1 text-ui text-muted">Steer, or queue a follow-up…</span>
          <span className="hidden text-caption text-muted sm:inline">Auto · Safe</span>
          <button aria-label="Send" className="grid size-9 place-items-center rounded-full bg-primary text-on-primary">
            <ArrowUp className="size-4" />
          </button>
        </div>
        <nav className="pointer-events-auto flex max-w-[calc(100%-2rem)] items-center gap-1 overflow-x-auto rounded-md bg-rail p-1 shadow-raised">
          {dock.map((t) => (
            <button key={t.id} onClick={() => open(t.id)} className={cn('flex shrink-0 items-center gap-2 rounded-sm px-3 py-1.5 text-caption', t.id === task.id ? 'bg-raised text-ink shadow-raised' : 'text-muted hover:text-ink')}>
              <StateDot state={t.state} />
              <span className="max-w-[140px] truncate">{nameOf(t)}</span>
            </button>
          ))}
        </nav>
      </div>
    </div>
  );
}
