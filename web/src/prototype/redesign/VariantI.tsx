// PROTOTYPE (throwaway): Demo I "Pocket", the iPhone flow, as three phone screens side by side.
// 1) Needs-you inbox with a bottom tab bar (badge on Needs you only). 2) A push opened the app on
// a half-height answer sheet: options as full-width 44pt rows plus free text (iOS web push has no
// action buttons, so answering happens here). 3) Result first: evidence, then files, then Commit.

import { ArrowUp, CheckCircle2, ChevronRight, CircleDashed, GitCommitHorizontal, Inbox, LayoutList, ListChecks, Settings } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';
import { evidence, live, nameOf, needsYou, pendingInteraction, projectChanges, projectOf, sentence, tasks } from './data';
import { ProjectBadge } from './shared';

export const name = 'Pocket (iPhone)';

export function VariantI(_: { openId: string | null; open: (id: string | null) => void }) {
  const waiting = tasks.filter(needsYou);
  const q = tasks.find((t) => t.id === 't2')!;
  const done = tasks.find((t) => t.id === 't3')!;
  return (
    <div className="flex min-h-dvh items-start justify-center gap-10 overflow-x-auto bg-sunken px-10 py-8">
      <Phone caption="1 · Needs you">
        <PhoneHeader title="Needs you" sub={`${waiting.length} waiting · ${tasks.filter(live).length} working`} />
        <div className="flex-1 space-y-2 overflow-hidden px-3">
          {waiting.slice(0, 5).map((t) => (
            <div key={t.id} className="flex items-center gap-3 rounded-lg bg-raised px-3 py-3 shadow-raised">
              <ProjectBadge badge={projectOf(t).badge} size={28} />
              <div className="min-w-0 flex-1">
                <p className="truncate text-ui font-medium text-ink">{nameOf(t)}</p>
                <p className="truncate text-caption text-attention">{sentence(t)}</p>
              </div>
              <ChevronRight className="size-4 text-faint" />
            </div>
          ))}
        </div>
        <TabBar active={0} badge={waiting.length} />
      </Phone>

      <Phone caption="2 · Answer sheet (opened from a push)">
        <div className="flex-1 bg-backdrop/40 px-3 pt-4">
          <div className="rounded-lg bg-raised/70 p-3 text-caption text-muted">{nameOf(q)}</div>
        </div>
        <div className="-mt-4 rounded-t-lg bg-raised px-4 pt-2 pb-6 shadow-modal">
          <div className="mx-auto mb-3 h-1 w-9 rounded-full bg-hairline-strong" />
          <p className="text-caption text-muted">{projectOf(q).name} · asked 14m ago</p>
          <p className="mt-1 text-[17px] font-semibold text-ink">{pendingInteraction(q)?.title}</p>
          <p className="text-caption text-muted">Pick any that apply</p>
          <div className="mt-3 space-y-2">
            {pendingInteraction(q)?.questions?.[0]?.choices?.map((c, n) => (
              <button key={c} className={cn('flex min-h-11 w-full items-center gap-3 rounded-md px-3 text-left text-ui', n < 2 ? 'bg-accent-wash text-ink' : 'bg-tint-well text-body')}>
                <span className={cn('grid size-5 place-items-center rounded-xs border', n < 2 ? 'border-accent bg-accent text-on-accent' : 'border-hairline-strong')}>{n < 2 ? '✓' : ''}</span>{c}
              </button>
            ))}
          </div>
          <div className="mt-3 flex items-center gap-2 rounded-full bg-tint-well py-1.5 pr-1.5 pl-4">
            <span className="flex-1 text-ui text-muted">Add a note…</span>
            <button aria-label="Send answer" className="grid size-9 place-items-center rounded-full bg-primary text-on-primary"><ArrowUp className="size-4" /></button>
          </div>
        </div>
      </Phone>

      <Phone caption="3 · Result first">
        <PhoneHeader title={nameOf(done)} sub="Finished 42m ago · ready for review" />
        <div className="flex-1 space-y-3 overflow-hidden px-3">
          <div className="rounded-lg bg-raised p-3 shadow-raised">
            {evidence.map((e) => (
              <p key={e.claim} className="flex gap-2 py-1 text-caption">
                {e.result === 'pass' ? <CheckCircle2 className="size-4 shrink-0 text-success" /> : <CircleDashed className="size-4 shrink-0 text-warning" />}
                <span className="text-ink">{e.claim}</span>
              </p>
            ))}
          </div>
          <div className="divide-y divide-hairline rounded-lg bg-raised shadow-raised">
            {projectChanges(done).map((f) => (
              <div key={f.path} className="flex min-h-11 items-center gap-2 px-3">
                <span className="min-w-0 flex-1 truncate font-mono text-code-sm text-ink">{f.path.split('/').pop()}</span>
                <span className="font-mono text-meta text-success">+{f.additions}</span>
                <ChevronRight className="size-4 text-faint" />
              </div>
            ))}
          </div>
          <button className="flex min-h-11 w-full items-center justify-center gap-1.5 rounded-md bg-primary text-ui font-medium text-on-primary"><GitCommitHorizontal className="size-4" /> Commit 3 files</button>
          <button className="min-h-11 w-full rounded-md bg-tint-well text-ui text-ink">Ask for changes</button>
        </div>
        <TabBar active={2} badge={waiting.length} />
      </Phone>
    </div>
  );
}

function Phone({ caption, children }: { caption: string; children: ReactNode }) {
  return (
    <figure className="flex shrink-0 flex-col items-center gap-3">
      <div className="flex h-[780px] w-[360px] flex-col overflow-hidden rounded-[44px] border-[10px] border-[#1c1d21] bg-canvas shadow-modal">
        <div className="flex h-9 shrink-0 items-center justify-between px-6 text-meta font-semibold text-ink"><span>9:41</span><span className="h-5 w-24 rounded-full bg-[#1c1d21]" /><span>100%</span></div>
        {children}
      </div>
      <figcaption className="text-ui font-medium text-body">{caption}</figcaption>
    </figure>
  );
}

const PhoneHeader = ({ title, sub }: { title: string; sub: string }) => (
  <header className="px-4 pt-2 pb-3">
    <h2 className="truncate text-display-md text-ink">{title}</h2>
    <p className="text-caption text-muted">{sub}</p>
  </header>
);

function TabBar({ active, badge }: { active: number; badge: number }) {
  const tabs = [{ icon: Inbox, label: 'Needs you' }, { icon: LayoutList, label: 'Tasks' }, { icon: ListChecks, label: 'Review' }, { icon: Settings, label: 'Settings' }];
  return (
    <nav className="grid shrink-0 grid-cols-4 border-t border-hairline bg-raised pt-1.5 pb-5">
      {tabs.map(({ icon: Icon, label }, n) => (
        <span key={label} className={cn('relative flex flex-col items-center gap-0.5 text-meta', n === active ? 'text-accent' : 'text-muted')}>
          <Icon className="size-5" />
          {label}
          {n === 0 && <span className="absolute -top-1 left-1/2 ml-1 rounded-full bg-attention px-1.5 text-meta font-semibold text-on-accent">{badge}</span>}
        </span>
      ))}
    </nav>
  );
}
