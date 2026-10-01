// PROTOTYPE (throwaway): Demo H "Review Desk". Research items 3, 5, 6 and 7 plus the git actions:
// a full-viewport review of one finished Task. Files are ordered by risk, each with a Viewed tick
// and "changed since viewed"; a size warning sits above; line comments collect into one batch
// that goes back to the Task as its next message; the commit panel commits this Task's files.

import { AlertTriangle, ArrowDownToLine, ArrowLeft, Check, CheckCircle2, CircleDashed, GitCommitHorizontal, MessageSquarePlus, ShieldAlert, Sparkles, Upload } from 'lucide-react';
import { cn } from '../../lib/cn';
import { evidence, nameOf, projectChanges, projectOf, riskyChange, tasks } from './data';
import { ProjectBadge } from './shared';

export const name = 'Review Desk';

const DIFF = [
  { n: 88, kind: ' ', text: '	}' },
  { n: 89, kind: ' ', text: '}' },
  { n: 90, kind: '+', text: '' },
  { n: 91, kind: '+', text: 'func TestRedrawReplaysFocusEvents(t *testing.T) {' },
  { n: 92, kind: '+', text: '	v := New(80, 24)' },
  { n: 93, kind: '+', text: '	v.Write([]byte("\\x1b[?1004h"))' },
  { n: 94, kind: '+', text: '	var out bytes.Buffer' },
  { n: 95, kind: '+', text: '	if err := v.Redraw(&out); err != nil {' },
  { n: 96, kind: '+', text: '		t.Fatal(err)' },
  { n: 97, kind: '+', text: '	}' },
  { n: 98, kind: '+', text: '	if !bytes.Contains(out.Bytes(), []byte("\\x1b[?1004h")) {' },
  { n: 99, kind: '+', text: '		t.Fatalf("focus events not replayed: %q", out.String())' },
  { n: 100, kind: '+', text: '	}' },
  { n: 101, kind: '+', text: '}' },
];

export function VariantH({ openId }: { openId: string | null; open: (id: string | null) => void }) {
  const task = tasks.find((t) => t.id === openId) ?? tasks.find((t) => t.id === 't3')!;
  const p = projectOf(task);
  const files = [{ ...riskyChange, risk: 'CI workflow' }, ...projectChanges(task).map((f) => ({ ...f, risk: '' }))];
  return (
    <div className="flex h-dvh flex-col bg-canvas text-body">
      <header className="flex items-center gap-3 px-6 py-3">
        <button className="rounded-sm p-1 text-muted hover:bg-tint-hover"><ArrowLeft className="size-4" /></button>
        <ProjectBadge badge={p.badge} />
        <h1 className="text-title text-ink">Review: {nameOf(task)}</h1>
        <span className="text-caption text-muted">4 files · +20 −1 · 2 of 4 viewed</span>
        <div className="ml-3 h-1.5 w-40 overflow-hidden rounded-full bg-sunken"><span className="block h-full w-1/2 bg-success" /></div>
        <div className="ml-auto flex items-center gap-1 rounded-sm bg-sunken p-0.5 text-caption">
          {['This task', 'Last turn', 'All changes'].map((l, n) => <span key={l} className={cn('rounded-xs px-2.5 py-1', n === 0 ? 'bg-raised text-ink shadow-raised' : 'text-muted')}>{l}</span>)}
        </div>
      </header>

      <div className="flex min-h-0 flex-1 gap-4 px-4 pb-4">
        <aside className="flex w-[320px] shrink-0 flex-col gap-3 overflow-y-auto">
          <section className="rounded-lg bg-raised p-4 shadow-raised">
            <h2 className="text-eyebrow text-muted uppercase">Evidence</h2>
            <ul className="mt-2 space-y-2">
              {evidence.map((e) => (
                <li key={e.claim} className="flex gap-2 text-caption">
                  {e.result === 'pass' ? <CheckCircle2 className="size-4 shrink-0 text-success" /> : <CircleDashed className="size-4 shrink-0 text-warning" />}
                  <span><span className="text-ink">{e.claim}</span><br /><span className="text-muted">{e.detail}</span></span>
                </li>
              ))}
            </ul>
          </section>
          <section className="rounded-lg bg-raised p-2 shadow-raised">
            <h2 className="px-2 pt-2 text-eyebrow text-muted uppercase">Files · riskiest first</h2>
            {files.map((f, n) => (
              <div key={f.path} className={cn('mt-1 flex items-center gap-2 rounded-sm px-2 py-2', n === 2 && 'bg-tint-selected')}>
                <input type="checkbox" readOnly checked={n === 0 || n === 3} aria-label={`Viewed ${f.path}`} className="size-3.5 accent-[#196a41]" />
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-code-sm text-ink">{f.path}</p>
                  <p className="flex items-center gap-1.5 text-meta">
                    {f.risk && <span className="inline-flex items-center gap-1 text-error"><ShieldAlert className="size-3" /> {f.risk}</span>}
                    {n === 0 && <span className="text-warning">changed since you viewed</span>}
                    <span className="text-success">+{f.additions}</span><span className="text-error">−{f.deletions}</span>
                  </p>
                </div>
              </div>
            ))}
          </section>
          <p className="flex items-start gap-1.5 px-1 text-caption text-muted"><AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" /> Small change. Reviews catch the most under ~400 lines.</p>
        </aside>

        <main className="min-w-0 flex-1 overflow-y-auto rounded-lg bg-raised shadow-raised">
          <div className="flex items-center gap-2 border-b border-hairline px-4 py-2.5">
            <span className="font-mono text-code-sm text-ink">internal/vterm/redraw_test.go</span>
            <span className="text-meta text-success">+12</span>
            <label className="ml-auto flex items-center gap-1.5 text-caption text-muted"><input type="checkbox" readOnly className="size-3.5" /> Viewed</label>
          </div>
          <pre className="font-mono text-code-sm leading-6">
            {DIFF.map((l) => (
              <div key={l.n}>
                <div className={cn('group flex', l.kind === '+' && 'bg-diff-add-bg/60')}>
                  <span className="w-12 shrink-0 pr-3 text-right text-faint select-none">{l.n}</span>
                  <span className={cn('w-4 shrink-0', l.kind === '+' ? 'text-diff-add-text' : 'text-faint')}>{l.kind}</span>
                  <span className="text-ink">{l.text}</span>
                  {l.n === 93 && <MessageSquarePlus className="ml-2 size-4 self-center text-accent" />}
                </div>
                {l.n === 93 && (
                  <div className="my-1 ml-16 mr-6 rounded-md bg-canvas p-3 font-sans shadow-raised">
                    <p className="text-caption text-ink">Also cover focus-out (<code className="font-mono">?1004l</code>) so a reset is replayed, not only the set.</p>
                    <p className="mt-1 text-meta text-muted">Your comment · goes out with the batch</p>
                  </div>
                )}
              </div>
            ))}
          </pre>
        </main>

        <aside className="flex w-[300px] shrink-0 flex-col gap-3">
          <section className="rounded-lg bg-raised p-4 shadow-raised">
            <h2 className="text-eyebrow text-muted uppercase">Comments · 2</h2>
            <p className="mt-2 text-caption text-body">Sent together as one message to this Task, which then fixes them in one turn.</p>
            <button className="mt-3 w-full rounded-sm bg-primary py-2 text-ui font-medium text-on-primary">Send 2 comments to the task</button>
          </section>
          <section className="rounded-lg bg-raised p-4 shadow-raised">
            <h2 className="text-eyebrow text-muted uppercase">Commit</h2>
            <div className="mt-2 rounded-sm bg-tint-well p-2.5 text-caption text-ink">fix(vterm): replay focus events on re-attach</div>
            <button className="mt-1.5 inline-flex items-center gap-1 text-meta text-accent"><Sparkles className="size-3" /> Draft from the diff</button>
            <p className="mt-3 text-caption text-muted">Files: <b className="text-ink">this task’s 3</b> · 1 from another task left out</p>
            <button className="mt-3 inline-flex w-full items-center justify-center gap-1.5 rounded-sm bg-primary py-2 text-ui font-medium text-on-primary"><GitCommitHorizontal className="size-4" /> Commit</button>
            <div className="mt-2 grid grid-cols-2 gap-2">
              <button className="inline-flex items-center justify-center gap-1 rounded-sm bg-tint-well py-1.5 text-caption text-ink"><Upload className="size-3.5" /> Push</button>
              <button className="inline-flex items-center justify-center gap-1 rounded-sm bg-tint-well py-1.5 text-caption text-ink"><ArrowDownToLine className="size-3.5" /> Pull</button>
            </div>
            <p className="mt-2 flex items-center gap-1 text-meta text-muted"><Check className="size-3 text-success" /> No task is running in this folder</p>
          </section>
        </aside>
      </div>
    </div>
  );
}
