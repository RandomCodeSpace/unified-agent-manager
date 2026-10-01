// PROTOTYPE (throwaway): Demo J "Workspace". The file manager feature with the Task beside it:
// the tree marks changed files (M/A/U/D) and folders holding them, with a "Changed only" filter;
// a Markdown file renders (Preview | Source) through the app's real Markdown renderer; the Task
// pane uses plain-language status and the two visible send choices.

import { ArrowUp, ChevronDown, ChevronRight, FolderTree, GitBranch, Paperclip } from 'lucide-react';
import { cn } from '../../lib/cn';
import { Markdown } from '../../components/common';
import { nameOf, projectOf, sentence, tasks, visibleItems } from './data';
import { ProjectBadge, Prose, ToolLine } from './shared';

export const name = 'Workspace (files)';

const TREE: { path: string; depth: number; folder?: boolean; open?: boolean; status?: string }[] = [
  { path: '.github', depth: 0, folder: true, status: 'changed' },
  { path: 'cmd', depth: 0, folder: true },
  { path: 'docs', depth: 0, folder: true, open: true, status: 'changed' },
  { path: 'terminal.md', depth: 1, status: 'A' },
  { path: 'web.md', depth: 1 },
  { path: 'internal', depth: 0, folder: true, open: true, status: 'changed' },
  { path: 'vterm', depth: 1, folder: true, open: true, status: 'changed' },
  { path: 'modes.go', depth: 2 },
  { path: 'redraw.go', depth: 2, status: 'M' },
  { path: 'redraw_test.go', depth: 2, status: 'M' },
  { path: 'replay_old.go', depth: 2, status: 'D' },
  { path: 'web', depth: 1, folder: true },
  { path: 'notes.txt', depth: 0, status: 'U' },
  { path: 'go.mod', depth: 0 },
  { path: 'Makefile', depth: 0 },
];

const TONE: Record<string, string> = { M: 'text-warning', A: 'text-success', U: 'text-success', D: 'text-error' };

const DOC = `# Terminal adaptation

Re-attaching a Task's terminal replays **every reset the attach client sends on detach**, in the same order:

1. colours and the pen
2. private modes (\`?1049\`, \`?2004\`)
3. focus events (\`?1004\`), new in this change

| Mode | Replayed | Since |
|---|---|---|
| Alternate screen | yes | #28 |
| Bracketed paste | yes | #28 |
| Focus events | yes | this change |

\`\`\`go
func (v *VT) Redraw(w io.Writer) error {
	v.replayPrivateModes(w)
	v.replayFocusEvents(w)
	return v.replayScreen(w)
}
\`\`\`

See [web.md](web.md) for how the web terminal attaches.`;

export function VariantJ({ openId }: { openId: string | null; open: (id: string | null) => void }) {
  const task = tasks.find((t) => t.id === openId) ?? tasks.find((t) => t.id === 't1')!;
  const p = projectOf(task);
  return (
    <div className="flex h-dvh bg-canvas text-body">
      <aside className="flex w-[300px] shrink-0 flex-col bg-rail">
        <header className="flex items-center gap-2 px-4 pt-4 pb-2">
          <FolderTree className="size-4 text-muted" />
          <span className="text-title text-ink">Files</span>
          <span className="ml-auto flex items-center gap-1 text-caption text-muted"><GitBranch className="size-3" /> feat/web-project…</span>
        </header>
        <div className="mx-3 mb-2 flex items-center gap-1 rounded-sm bg-sunken p-0.5 text-caption">
          <span className="flex-1 rounded-xs px-2 py-1 text-center text-muted">All files</span>
          <span className="flex-1 rounded-xs bg-raised px-2 py-1 text-center text-ink shadow-raised">Changed · 5</span>
        </div>
        <ul className="flex-1 overflow-y-auto px-2 pb-4">
          {TREE.map((e) => (
            <li key={`${e.depth}:${e.path}`}>
              <div style={{ paddingLeft: `${0.25 + e.depth * 0.875}rem` }} className={cn('flex min-h-7 items-center gap-1 rounded-sm py-1 pr-2 text-caption', e.path === 'terminal.md' ? 'bg-raised text-ink shadow-raised' : 'text-body')}>
                {e.folder ? (e.open ? <ChevronDown className="size-3.5 text-muted" /> : <ChevronRight className="size-3.5 text-muted" />) : <span className="size-3.5" />}
                <span className={cn('min-w-0 flex-1 truncate', e.folder && 'font-medium', e.status && !e.folder && e.status !== 'D' && 'text-ink', e.status === 'D' && 'text-muted line-through')}>{e.path}</span>
                {e.folder && e.status && <span className="size-1.5 rounded-full bg-warning" />}
                {!e.folder && e.status && <span className={cn('w-3 text-center font-mono text-code-sm font-semibold', TONE[e.status])}>{e.status}</span>}
              </div>
            </li>
          ))}
        </ul>
        <p className="px-4 pb-4 text-meta text-muted">M modified · A added · U new · D deleted · ● folder holds changes</p>
      </aside>

      <section className="flex min-w-0 flex-1 flex-col border-x border-hairline bg-raised">
        <div className="flex items-center gap-3 border-b border-hairline px-6 py-3">
          <span className="font-mono text-code-sm text-ink">docs/terminal.md</span>
          <span className="text-meta text-success">A · new in this task</span>
          <div className="ml-auto flex items-center gap-1 rounded-sm bg-sunken p-0.5 text-caption">
            <span className="rounded-xs bg-raised px-2.5 py-1 text-ink shadow-raised">Preview</span>
            <span className="rounded-xs px-2.5 py-1 text-muted">Source</span>
            <span className="rounded-xs px-2.5 py-1 text-muted">Diff</span>
          </div>
        </div>
        <div className="flex-1 overflow-y-auto px-10 py-8">
          <Markdown text={DOC} className="text-chat text-ink" />
        </div>
      </section>

      <section className="flex w-[480px] shrink-0 flex-col">
        <header className="flex items-center gap-2 px-5 py-3">
          <ProjectBadge badge={p.badge} />
          <div className="min-w-0">
            <h2 className="truncate text-title text-ink">{nameOf(task)}</h2>
            <p className="truncate text-caption text-accent">{sentence(task)}</p>
          </div>
        </header>
        <div className="flex-1 space-y-3 overflow-y-auto px-5 text-ui">
          {visibleItems(task).slice(-6).map((it) =>
            it.kind === 'tool' ? <ToolLine key={it.id} item={it} /> : it.kind === 'reasoning' ? null : it.kind === 'user' ? (
              <div key={it.id} className="rounded-md bg-bubble px-3 py-2 text-ink">{it.text}</div>
            ) : <Prose key={it.id} text={it.text ?? ''} className="text-ink" />,
          )}
        </div>
        <footer className="p-4">
          <div className="rounded-lg bg-raised px-3 pt-2.5 pb-2 shadow-float">
            <p className="text-ui text-muted">Message the agent…</p>
            <div className="mt-2 flex items-center gap-2">
              <Paperclip className="size-4 text-faint" />
              <span className="flex-1 text-meta text-muted">It is working: choose when it reads this</span>
              <button className="rounded-sm bg-tint-well px-2.5 py-1.5 text-caption text-ink">After this turn</button>
              <button className="inline-flex items-center gap-1 rounded-sm bg-primary px-2.5 py-1.5 text-caption font-medium text-on-primary">Send now <ArrowUp className="size-3.5" /></button>
            </div>
          </div>
        </footer>
      </section>
    </div>
  );
}
