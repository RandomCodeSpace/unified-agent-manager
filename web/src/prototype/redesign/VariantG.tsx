// PROTOTYPE (throwaway): Demo G "Command Center". Research items 1, 2, 3, 4, 5 and 8 together:
// a list grouped by state (Needs you first) where questions are answered in place and finished
// work shows its +N −M; the open Task leads with "Since you left" and an evidence-first finish
// card with the git actions; the composer offers "Send now" and "After this turn" as two choices.

import { AlertTriangle, ArrowUp, Check, CheckCircle2, ChevronDown, CircleDashed, GitBranch, GitCommitHorizontal, Keyboard, Paperclip, Plus, RefreshCw, RotateCcw, Sparkles, Upload, X } from 'lucide-react';
import { cn } from '../../lib/cn';
import type { MockTask } from '../../mock/data';
import { ago, byRecent, evidence, live, nameOf, needsYou, pendingInteraction, projectChanges, projectOf, sentence, stateLabel, tasks } from './data';
import { Kbd, ProjectBadge, toneText } from './shared';

export const name = 'Command Center';

const groupsOf = () => {
  const ready = tasks.filter((t) => t.state === 'completed' || t.state === 'failed').sort(byRecent);
  const idle = tasks.filter((t) => !needsYou(t) && !live(t) && !ready.includes(t)).sort(byRecent);
  return [
    { key: 'you', title: 'Needs you', items: tasks.filter(needsYou).sort(byRecent) },
    { key: 'review', title: 'Ready for review', items: ready },
    { key: 'work', title: 'Working', items: tasks.filter(live).sort(byRecent) },
    { key: 'idle', title: 'Idle', items: idle.slice(0, 2) },
  ];
};

export function VariantG({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const current = tasks.find((t) => t.id === openId) ?? tasks.find((t) => t.id === 't3')!;
  return (
    <div className="flex h-dvh bg-canvas text-body">
      <CommandList current={current} open={open} />
      <TaskPane task={current} />
    </div>
  );
}

/** The state-grouped list; demo K reuses it beside a different pane. */
export function CommandList({ current, open }: { current: MockTask; open: (id: string | null) => void }) {
  return (
    <aside className="flex w-[420px] shrink-0 flex-col bg-rail">
      <header className="flex items-center gap-2 px-4 pt-4 pb-2">
        <span className="text-title font-bold text-ink">uam</span>
        <span className="ml-1 flex items-center gap-1 text-meta text-muted"><Kbd>Alt</Kbd>+<Kbd>J</Kbd> next that needs you</span>
        <button className="ml-auto inline-flex items-center gap-1 rounded-sm bg-primary px-2.5 py-1.5 text-caption font-medium text-on-primary"><Plus className="size-3.5" /> New task</button>
      </header>
      <div className="flex-1 space-y-4 overflow-y-auto px-3 pb-6">
        {groupsOf().map((g) => (
          <section key={g.key}>
            <h2 className={cn('mb-1.5 flex items-center gap-2 px-1 text-eyebrow uppercase', g.key === 'you' ? 'text-attention' : 'text-muted')}>
              {g.title} <span className="rounded-full bg-sunken px-1.5 text-meta normal-case">{g.items.length}</span>
            </h2>
            <div className="space-y-1.5">
              {g.items.map((t) => <Row key={t.id} task={t} group={g.key} on={t.id === current.id} onOpen={() => open(t.id)} />)}
            </div>
          </section>
        ))}
        <button className="flex w-full items-center gap-1.5 px-1 text-caption text-muted"><ChevronDown className="size-3.5" /> Settled · 6 (closed automatically after 7 idle days)</button>
      </div>
    </aside>
  );
}

function Row({ task, group, on, onOpen }: { task: MockTask; group: string; on: boolean; onOpen: () => void }) {
  const p = projectOf(task);
  const i = pendingInteraction(task);
  const files = projectChanges(task);
  const add = files.reduce((n, f) => n + f.additions, 0);
  const del = files.reduce((n, f) => n + f.deletions, 0);
  return (
    <div className={cn('rounded-md px-3 py-2', on ? 'bg-raised shadow-raised' : 'hover:bg-tint-hover')}>
      <button onClick={onOpen} className="flex w-full items-center gap-2 text-left">
        <ProjectBadge badge={p.badge} size={16} />
        <span className="min-w-0 flex-1 truncate text-ui font-medium text-ink">{nameOf(task)}</span>
        {group === 'review' && <span className="font-mono text-meta"><span className="text-success">+{add}</span> <span className="text-error">−{del}</span></span>}
        <span className="text-meta text-muted">{group === 'work' ? (task.id === 't7' ? 'quiet 12m' : `${ago(task.created_at)} in`) : ago(task.updated_at)}</span>
      </button>
      <p className={cn('mt-0.5 truncate pl-6 text-caption', toneText(task.state))}>{sentence(task)}</p>
      {group === 'you' && i?.kind === 'question' && i.questions?.[0]?.choices && (
        <div className="mt-2 flex flex-wrap gap-1.5 pl-6">
          {i.questions[0].choices.slice(0, 3).map((c, n) => (
            <button key={c} className={cn('rounded-full px-2.5 py-0.5 text-caption', n === 0 ? 'bg-accent text-on-accent' : 'bg-raised text-body shadow-raised')}>{c}</button>
          ))}
          <button className="rounded-full px-2 py-0.5 text-caption text-muted">Reply…</button>
        </div>
      )}
      {group === 'you' && i?.kind === 'permission' && (
        <div className="mt-2 flex gap-1.5 pl-6">
          <button className="inline-flex shrink-0 items-center gap-1 rounded-sm bg-primary px-2.5 py-1 text-caption text-on-primary"><Check className="size-3.5" /> Allow</button>
          <button className="inline-flex shrink-0 items-center gap-1 rounded-sm px-2.5 py-1 text-caption whitespace-nowrap text-error"><X className="size-3.5" /> Don’t allow</button>
          <code className="truncate self-center font-mono text-code-sm text-muted">{i.detail}</code>
        </div>
      )}
    </div>
  );
}

function TaskPane({ task }: { task: MockTask }) {
  const p = projectOf(task);
  const files = projectChanges(task);
  return (
    <main className="flex min-w-0 flex-1 flex-col">
      <header className="flex items-center gap-3 px-8 py-4">
        <ProjectBadge badge={p.badge} />
        <div className="min-w-0">
          <h1 className="truncate text-display-sm text-ink">{nameOf(task)}</h1>
          <p className="flex items-center gap-1.5 text-caption text-muted">{p.name} <GitBranch className="size-3" /> {p.branch} · <span className={toneText(task.state)}>{stateLabel[task.state]}</span></p>
        </div>
        <div className="ml-auto flex items-center gap-1 rounded-sm bg-sunken p-0.5 text-caption">
          {['This task · 3', 'Last turn · 1', 'All changes · 5'].map((l, n) => <span key={l} className={cn('rounded-xs px-2.5 py-1', n === 0 ? 'bg-raised text-ink shadow-raised' : 'text-muted')}>{l}</span>)}
        </div>
      </header>

      <div className="flex-1 space-y-5 overflow-y-auto px-8 pb-6">
        <div className="flex items-center gap-3 rounded-md bg-info-wash/60 px-4 py-2.5 text-ui text-ink">
          <RotateCcw className="size-4 text-info" />
          <span><b>Since you left</b> (42 min): the agent finished, ran the tests, and changed 3 files.</span>
          <button className="ml-auto text-caption text-info">Jump to where you stopped</button>
        </div>

        <section className="rounded-lg bg-raised p-5 shadow-raised">
          <div className="flex items-center gap-2">
            <h2 className="text-title text-ink">Finished — check the evidence</h2>
            <span className="rounded-full bg-warning-wash px-2 py-0.5 text-meta text-warning">1 claim not verified</span>
          </div>
          <ul className="mt-3 divide-y divide-hairline">
            {evidence.map((e) => (
              <li key={e.claim} className="flex items-start gap-3 py-2.5">
                {e.result === 'pass' ? <CheckCircle2 className="mt-0.5 size-4 text-success" /> : <CircleDashed className="mt-0.5 size-4 text-warning" />}
                <div className="min-w-0 flex-1">
                  <p className="text-ui text-ink">{e.claim}</p>
                  <p className="text-caption text-muted">{e.check ? <code className="font-mono text-code-sm">{e.check}</code> : 'Not verified'} · {e.detail}</p>
                </div>
                {e.check && <button className="text-caption text-accent">Show output</button>}
              </li>
            ))}
          </ul>
          <div className="mt-4 border-t border-hairline pt-4">
            <div className="flex items-center gap-2">
              <h3 className="text-ui font-medium text-ink">Commit message</h3>
              <span className="inline-flex items-center gap-1 rounded-full bg-info-wash px-2 py-0.5 text-meta text-info"><Sparkles className="size-3" /> Generated from this task’s changes</span>
              <button className="ml-auto inline-flex items-center gap-1 text-caption text-accent"><RefreshCw className="size-3.5" /> Regenerate</button>
            </div>
            <div className="mt-2 rounded-md bg-canvas p-3 font-mono text-code-sm leading-6 text-ink shadow-well">
              <p className="font-semibold">fix(vterm): replay focus events on re-attach</p>
              <p className="mt-2 text-body">Redraw now re-emits ?1004 after the private-mode replay, so a</p>
              <p className="text-body">re-attached client receives focus events again.</p>
              <p className="mt-2 text-body">- add TestRedrawReplaysFocusEvents</p>
              <p className="text-body">- document the replay order in docs/terminal.md</p>
            </div>
            <p className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-meta text-muted">
              <span>Matches this repo’s style: Conventional Commits, from its last 20 commits</span>
              <span>· 44/72 characters in the subject</span>
              <span>· No co-author or AI credit</span>
            </p>
            <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-caption">
              {files.map((f) => (
                <label key={f.path} className="inline-flex items-center gap-1.5 font-mono text-code-sm text-ink"><input type="checkbox" readOnly checked className="size-3.5" /> {f.path}</label>
              ))}
              <span className="flex items-center gap-1 text-muted"><AlertTriangle className="size-3.5 text-warning" /> 2 files from other tasks are not included</span>
            </div>
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <button className="inline-flex items-center gap-1.5 rounded-sm bg-primary px-3 py-1.5 text-ui font-medium text-on-primary"><GitCommitHorizontal className="size-4" /> Commit 3 files</button>
              <button className="inline-flex items-center gap-1.5 rounded-sm bg-tint-well px-3 py-1.5 text-ui text-ink"><Upload className="size-4" /> Commit and push</button>
              <button className="rounded-sm bg-tint-well px-3 py-1.5 text-ui text-ink">Ask for changes</button>
              <button className="rounded-sm px-3 py-1.5 text-ui text-error">Discard</button>
            </div>
          </div>
        </section>

        <section>
          <h3 className="mb-2 text-eyebrow text-muted uppercase">This task changed</h3>
          <div className="divide-y divide-hairline rounded-md bg-raised shadow-raised">
            {files.map((f) => (
              <div key={f.path} className="flex items-center gap-3 px-4 py-2 text-ui">
                <span className={cn('w-4 font-mono text-code-sm font-semibold', f.status === 'A' ? 'text-success' : 'text-warning')}>{f.status}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-code-sm text-ink">{f.path}</span>
                <span className="font-mono text-meta"><span className="text-success">+{f.additions}</span> <span className="text-error">−{f.deletions}</span></span>
              </div>
            ))}
          </div>
        </section>
      </div>

      <footer className="px-8 pb-5">
        <div className="rounded-lg bg-raised px-4 pt-3 pb-2 shadow-float">
          <p className="text-ui text-muted">Ask a follow-up…</p>
          <div className="mt-2 flex items-center gap-2">
            <Paperclip className="size-4 text-faint" />
            <span className="text-caption text-muted">Auto · Ask me first</span>
            <span className="ml-auto flex items-center gap-1 text-meta text-muted"><Keyboard className="size-3.5" /> Enter sends now</span>
            <button className="rounded-sm bg-tint-well px-3 py-1.5 text-caption text-ink">After this turn</button>
            <button className="inline-flex items-center gap-1 rounded-sm bg-primary px-3 py-1.5 text-caption font-medium text-on-primary">Send now <ArrowUp className="size-3.5" /></button>
          </div>
        </div>
      </footer>
    </main>
  );
}
