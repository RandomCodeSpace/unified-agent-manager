// PROTOTYPE (throwaway): Variant F "Brief". The home reads like a note from a colleague: three
// sentences (who is waiting on you, who is working, what finished) each with its list under it.
// A Task opens on a summary card (goal, what it did, your move); the full conversation sits
// right below, never hidden. Outcome first, transcript second.

import { ArrowRight, ArrowUp, Check, ChevronDown, FileDiff, Paperclip, Sparkles } from 'lucide-react';
import { cn } from '../../lib/cn';
import type { MockTask } from '../../mock/data';
import { ago, byRecent, live, nameOf, needsYou, nextAction, pendingInteraction, projectChanges, projectOf, receipt, sentence, tasks, visibleItems } from './data';
import { ProjectBadge, Prose, StateDot, ToolLine } from './shared';

export const name = 'Brief';

export function VariantF({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const current = tasks.find((t) => t.id === openId) ?? null;
  return current ? <TaskSummary task={current} onBack={() => open(null)} /> : <Home open={open} />;
}

function Home({ open }: { open: (id: string | null) => void }) {
  const waiting = tasks.filter(needsYou).sort(byRecent);
  const working = tasks.filter(live).sort(byRecent);
  const finished = tasks.filter((t) => t.state === 'completed' || t.state === 'failed' || t.state === 'interrupted').sort(byRecent);
  return (
    <div className="min-h-dvh overflow-y-auto bg-canvas text-body">
      <div className="px-6 pt-10 pb-24 md:px-10">
        <p className="text-ui text-muted">{new Date().toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' })}</p>
        <h1 className="mt-1 text-[30px] leading-tight font-semibold tracking-tight text-ink">
          <span className="text-attention">{waiting.length} tasks are waiting for you</span>, {working.length} are working, and {finished.length} finished.
        </h1>

        <div className="mt-6 flex items-center gap-3 rounded-lg bg-raised px-4 py-3 shadow-float">
          <Sparkles className="size-4 text-accent" />
          <span className="flex-1 text-ui text-muted">Start something new — describe it in a sentence</span>
          <button aria-label="Start" className="grid size-8 place-items-center rounded-full bg-primary text-on-primary"><ArrowUp className="size-4" /></button>
        </div>

        <div className="mt-4 grid items-start gap-x-6 lg:grid-cols-3">
        <Block title="Waiting for you" lead="Each one is stuck until you reply." tone="attention">
          {waiting.map((t) => <Line key={t.id} task={t} onOpen={() => open(t.id)} />)}
        </Block>
        <Block title="Working" lead="No action needed. Open one to watch or add a message.">
          {working.map((t) => <Line key={t.id} task={t} onOpen={() => open(t.id)} />)}
        </Block>
        <Block title="Finished" lead="Check the result, then close it so this list stays short.">
          {finished.map((t) => <Line key={t.id} task={t} onOpen={() => open(t.id)} />)}
        </Block>
        </div>
      </div>
    </div>
  );
}

function Block({ title, lead, tone, children }: { title: string; lead: string; tone?: 'attention'; children: React.ReactNode }) {
  return (
    <section className="mt-8">
      <h2 className={cn('text-display-sm', tone === 'attention' ? 'text-attention' : 'text-ink')}>{title}</h2>
      <p className="mt-0.5 mb-3 text-ui text-muted">{lead}</p>
      <div className="divide-y divide-hairline rounded-lg bg-raised shadow-raised">{children}</div>
    </section>
  );
}

function Line({ task, onOpen }: { task: MockTask; onOpen: () => void }) {
  const p = projectOf(task);
  const action = nextAction(task);
  return (
    <button onClick={onOpen} className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-tint-hover">
      <ProjectBadge badge={p.badge} size={22} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-ui font-medium text-ink">{nameOf(task)}</span>
        <span className="block truncate text-caption text-muted">{sentence(task)}</span>
      </span>
      <span className="text-meta text-muted">{ago(task.updated_at)}</span>
      {action ? (
        <span className={cn('shrink-0 rounded-sm px-2.5 py-1 text-caption font-medium', needsYou(task) ? 'bg-primary text-on-primary' : 'bg-tint-well text-ink')}>{action}</span>
      ) : (
        <ArrowRight className="size-4 shrink-0 text-faint" />
      )}
    </button>
  );
}

function TaskSummary({ task, onBack }: { task: MockTask; onBack: () => void }) {
  const p = projectOf(task);
  const items = visibleItems(task);
  const goal = items.find((i) => i.kind === 'user')?.text ?? '';
  const i = pendingInteraction(task);
  const files = projectChanges(task);
  return (
    <div className="flex h-dvh flex-col bg-canvas text-body lg:flex-row">
      <aside className="shrink-0 overflow-y-auto px-6 pt-6 pb-6 md:px-10 lg:w-[44%] lg:border-r lg:border-hairline">
        <button onClick={onBack} className="text-ui text-muted hover:text-ink">← Back to the brief</button>
        <p className="mt-6 flex items-center gap-1.5 text-caption text-muted"><ProjectBadge badge={p.badge} size={14} /> {p.name}</p>
        <h1 className="mt-1 text-[26px] leading-tight font-semibold tracking-tight text-ink">{nameOf(task)}</h1>

        <div className="mt-5 grid gap-px overflow-hidden rounded-lg bg-hairline shadow-raised sm:grid-cols-[120px_1fr]">
          <Cell label="You asked">{goal}</Cell>
          <Cell label="Right now">
            <span className="flex items-center gap-2"><StateDot state={task.state} /> {sentence(task)}</span>
          </Cell>
          <Cell label="So far it">{receipt(items) || 'has not started yet'}</Cell>
          <Cell label="Changed">
            <span className="flex flex-wrap gap-x-4 gap-y-1">
              {files.map((f) => (
                <span key={f.path} className="inline-flex items-center gap-1.5 font-mono text-code-sm text-ink">
                  <FileDiff className="size-3.5 text-faint" /> {f.path}
                  <span className="text-success">+{f.additions}</span>
                </span>
              ))}
            </span>
          </Cell>
          <Cell label="Your move" strong>
            {i ? (
              <span className="flex flex-wrap items-center gap-2">
                {i.title}
                <button className="ml-auto inline-flex items-center gap-1 rounded-sm bg-primary px-3 py-1.5 text-caption text-on-primary"><Check className="size-3.5" /> Allow</button>
              </span>
            ) : live(task) ? (
              'Nothing — it will tell you when it needs you or when it is done.'
            ) : (
              <span className="flex gap-2">
                <button className="rounded-sm bg-primary px-3 py-1.5 text-caption text-on-primary">Review changes</button>
                <button className="rounded-sm bg-tint-well px-3 py-1.5 text-caption text-ink">Close task</button>
              </span>
            )}
          </Cell>
        </div>

      </aside>
      <main className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex-1 overflow-y-auto px-6 pt-6 pb-6 md:px-10">
        <h2 className="mb-4 flex items-center gap-2 text-title text-ink">
          Full conversation <span className="text-caption font-normal text-muted">{items.length} entries</span> <ChevronDown className="size-4 text-faint" />
        </h2>
        <div className="space-y-4 text-chat">
          {items.map((it) =>
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
      <div className="px-6 pt-2 pb-5 md:px-10">
        <div className="flex items-center gap-3 rounded-lg bg-raised px-4 py-3 shadow-float">
          <Paperclip className="size-4 text-faint" />
          <span className="flex-1 text-ui text-muted">Tell it something — it reads this at its next step</span>
          <button aria-label="Send" className="grid size-8 place-items-center rounded-full bg-primary text-on-primary"><ArrowUp className="size-4" /></button>
        </div>
      </div>
      </main>
    </div>
  );
}

const Cell = ({ label, strong, children }: { label: string; strong?: boolean; children: React.ReactNode }) => (
  <>
    <div className={cn('px-4 pt-3 text-caption text-muted sm:py-3', strong ? 'bg-tint-selected' : 'bg-raised')}>{label}</div>
    <div className={cn('px-4 pb-3 text-ui text-ink sm:py-3', strong ? 'bg-tint-selected font-medium' : 'bg-raised')}>{children}</div>
  </>
);
