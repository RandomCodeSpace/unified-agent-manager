// PROTOTYPE (throwaway): Variant D "Messages". Borrow the one interface everyone already knows:
// a chat app. Each Task is a thread; "needs you" is an unread badge; working is "typing…";
// the row's second line is a plain sentence of state, never a transcript peek. Inside, tool
// steps fold into one receipt, questions arrive as an agent bubble with quick replies, and the
// composer says what Send will do instead of naming Steer and Queue.

import { ArrowUp, ChevronRight, Paperclip, Search, SquarePen } from 'lucide-react';
import type { Item } from '../../api';
import { cn } from '../../lib/cn';
import type { MockTask } from '../../mock/data';
import { ago, byRecent, live, nameOf, needsYou, pendingInteraction, projectOf, receipt, sentence, tasks, visibleItems } from './data';
import { ProjectBadge, Prose } from './shared';

export const name = 'Messages';

export function VariantD({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const list = [...tasks].sort((a, b) => Number(needsYou(b)) - Number(needsYou(a)) || byRecent(a, b));
  const current = tasks.find((t) => t.id === openId) ?? null;
  return (
    <div className="flex h-dvh bg-canvas text-body">
      <aside className={cn('flex w-full shrink-0 flex-col bg-raised md:w-[360px] md:border-r md:border-hairline', current && 'hidden md:flex')}>
        <header className="flex items-center gap-2 px-4 pt-5 pb-3">
          <h1 className="text-display-md text-ink">Tasks</h1>
          <button aria-label="New task" className="ml-auto grid size-9 place-items-center rounded-full bg-primary text-on-primary">
            <SquarePen className="size-4" />
          </button>
        </header>
        <div className="mx-4 mb-2 flex items-center gap-2 rounded-full bg-sunken px-3 py-2 text-ui text-muted">
          <Search className="size-4" /> Search tasks
        </div>
        <div className="flex gap-2 px-4 pb-2 text-caption">
          {['All', 'Needs you', 'Working', 'Done'].map((f, n) => (
            <span key={f} className={cn('rounded-full px-3 py-1', n === 0 ? 'bg-primary text-on-primary' : 'bg-tint-well text-body')}>{f}</span>
          ))}
        </div>
        <div className="flex-1 overflow-y-auto">
          {list.map((t) => <Thread key={t.id} task={t} on={t.id === openId} onOpen={() => open(t.id)} />)}
        </div>
      </aside>
      {current ? <Conversation task={current} onBack={() => open(null)} /> : <Empty />}
    </div>
  );
}

function Thread({ task, on, onOpen }: { task: MockTask; on: boolean; onOpen: () => void }) {
  const p = projectOf(task);
  const unread = needsYou(task);
  return (
    <button onClick={onOpen} className={cn('flex w-full items-center gap-3 px-4 py-3 text-left', on ? 'bg-tint-selected' : 'hover:bg-tint-hover')}>
      <span className="relative">
        <span className="grid size-11 place-items-center overflow-hidden rounded-full">
          <ProjectBadge badge={p.badge} size={44} />
        </span>
        {live(task) && <span className="absolute -right-0.5 -bottom-0.5 size-3.5 rounded-full border-2 border-raised bg-success" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className={cn('truncate text-ui', unread ? 'font-semibold text-ink' : 'font-medium text-ink')}>{nameOf(task)}</span>
          <span className={cn('ml-auto shrink-0 text-meta', unread ? 'font-semibold text-accent' : 'text-muted')}>{ago(task.updated_at)}</span>
        </span>
        <span className="mt-0.5 flex items-center gap-2">
          <span className={cn('truncate text-caption', unread ? 'text-ink' : 'text-muted')}>{live(task) ? <Typing /> : sentence(task)}</span>
          {unread && <span className="ml-auto grid size-5 shrink-0 place-items-center rounded-full bg-accent text-meta font-semibold text-on-accent">1</span>}
        </span>
      </span>
    </button>
  );
}

const Typing = () => (
  <span className="inline-flex items-center gap-1.5 text-success">
    <span className="inline-flex gap-0.5">
      {[0, 1, 2].map((n) => <span key={n} className="size-1 animate-pulse rounded-full bg-success motion-reduce:animate-none" style={{ animationDelay: `${n * 150}ms` }} />)}
    </span>
    working
  </span>
);

function Empty() {
  return (
    <main className="hidden flex-1 place-items-center md:grid">
      <div className="text-center">
        <p className="text-display-sm text-ink">Pick a task, or start a new one</p>
        <p className="mt-1 text-ui text-muted">Tasks with a blue badge are waiting for you.</p>
      </div>
    </main>
  );
}

/** Groups consecutive tool calls (and thoughts) into one receipt between messages. */
function groups(items: Item[]): ({ kind: 'msg'; item: Item } | { kind: 'work'; items: Item[] })[] {
  const out: ({ kind: 'msg'; item: Item } | { kind: 'work'; items: Item[] })[] = [];
  for (const it of items) {
    if (it.kind === 'tool' || it.kind === 'reasoning') {
      const last = out.at(-1);
      if (last?.kind === 'work') last.items.push(it);
      else out.push({ kind: 'work', items: [it] });
    } else out.push({ kind: 'msg', item: it });
  }
  return out;
}

function Conversation({ task, onBack }: { task: MockTask; onBack: () => void }) {
  const p = projectOf(task);
  const q = pendingInteraction(task);
  const working = live(task);
  return (
    <main className="flex min-w-0 flex-1 flex-col">
      <header className="flex items-center gap-3 border-b border-hairline bg-raised px-5 py-3">
        <button onClick={onBack} className="shrink-0 whitespace-nowrap text-ui text-accent md:hidden">‹ Back</button>
        <span className="overflow-hidden rounded-full"><ProjectBadge badge={p.badge} size={36} /></span>
        <div className="min-w-0">
          <h2 className="truncate text-title text-ink">{nameOf(task)}</h2>
          <p className="truncate text-caption text-muted">{working ? <Typing /> : sentence(task)} · {p.name}</p>
        </div>
        <button className="ml-auto hidden rounded-full bg-tint-well px-3 py-1.5 text-caption text-body md:block">What changed · 3 files</button>
      </header>

      <div className="flex-1 overflow-y-auto px-4 py-6 md:px-10">
        <div className="space-y-4">
          {groups(visibleItems(task)).map((g, n) =>
            g.kind === 'work' ? (
              <button key={n} className="mx-auto flex items-center gap-2 rounded-full bg-tint-well px-3 py-1 text-caption text-muted">
                {g.items.some((i) => i.tool?.status === 'running') ? 'Working: ' : 'It '}
                {receipt(g.items) || 'thought it over'}
                <ChevronRight className="size-3.5" />
              </button>
            ) : g.item.kind === 'user' ? (
              <div key={n} className="ml-auto w-fit max-w-[70%] rounded-lg rounded-br-xs bg-accent px-4 py-2.5 text-chat text-on-accent">{g.item.text}</div>
            ) : (
              <div key={n} className="w-fit max-w-[75%] rounded-lg rounded-bl-xs bg-raised px-4 py-2.5 text-chat text-ink shadow-raised">
                <Prose text={g.item.text ?? ''} />
              </div>
            ),
          )}
          {q && (
            <div className="w-fit max-w-[85%] rounded-lg rounded-bl-xs bg-raised px-4 py-3 text-chat text-ink shadow-raised">
              <p className="font-medium">{q.title}</p>
              {q.questions?.[0]?.multiple && <p className="text-caption text-muted">Pick any that apply</p>}
            </div>
          )}
          {working && (
            <div className="flex w-fit items-center gap-2 rounded-lg rounded-bl-xs bg-raised px-4 py-3 shadow-raised">
              <Typing />
            </div>
          )}
        </div>
      </div>

      <footer className="bg-canvas px-4 pt-2 pb-4 md:px-10">
        <div>
          {q?.questions?.[0]?.choices && (
            <div className="mb-2 flex flex-wrap justify-end gap-2">
              {q.questions[0].choices.map((c) => (
                <button key={c} className="rounded-full border border-accent px-3 py-1.5 text-caption font-medium text-accent">{c}</button>
              ))}
            </div>
          )}
          <div className="flex items-end gap-2">
            <button aria-label="Attach" className="grid size-10 shrink-0 place-items-center rounded-full text-muted hover:bg-tint-hover">
              <Paperclip className="size-5" />
            </button>
            <div className="flex-1 rounded-lg bg-raised px-4 py-2.5 shadow-raised">
              <p className="text-ui text-muted">{q ? 'Or type your own answer…' : working ? 'Message — it will read this right away' : 'Message'}</p>
            </div>
            <button aria-label="Send" className="grid size-10 shrink-0 place-items-center rounded-full bg-accent text-on-accent">
              <ArrowUp className="size-5" />
            </button>
          </div>
          {working && (
            <p className="mt-2 text-center text-meta text-muted">
              Sending interrupts nothing: it reads your message at its next step. <span className="text-accent">Send after it finishes instead</span>
            </p>
          )}
        </div>
      </footer>
    </main>
  );
}
