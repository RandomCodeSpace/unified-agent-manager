// The notebook layout's live card (lib/layout): the one home of what the running turn does, drawn
// in its cell's "did" track. Top to bottom, each a section that folds: Now (the progress line and
// its peek), Plan (the todo list, each row's stage in a word), Subagents (the rows at work) and
// Changed so far (the Thread's files, with Review). The status strip owns the state and hands the
// parts in (StatusLine), so nothing is kept twice: the card only says where they stand. A section
// with nothing in it is absent, and so is the card with no section. It never says that the Thread
// waits on you: the question in the cell does.

import { ChevronRight } from 'lucide-react';
import { useId, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import type { ChangeFile } from '../../api';
import { cn } from '../../lib/cn';
import { useLayout } from '../../lib/layout';
import { LineCounts } from '../common';
import { LiveSubagents, useScope } from '../Subagents';
import { plural } from '../TimeSplit';
import { Button } from '../ui/button';
import { Collapse } from '../ui/collapse';
import { useNotebookSlot, type NotebookSlot } from './slots';

/** `children` in the running cell's slot while the notebook layout has one on screen, else where they were written. */
export function InNotebookSlot({ name, children }: Readonly<{ name: NotebookSlot; children: ReactNode }>) {
  const slot = useNotebookSlot(name);
  return useLayout() === 'notebook' && slot ? createPortal(children, slot) : children;
}

/** How many changed files the card lists before "and n more"; Review shows them all. */
const FILE_ROWS = 6;

const PAD = 'px-2';

/** One section: its name and count on a button that folds it, word links at its end. One with no body is its head alone. Rows that light up to their edge (the progress line, a subagent) take the section's whole width; plain ones pad themselves (`PAD`). */
function Section({ label, meta, action, children }: Readonly<{ label: string; meta?: string; action?: ReactNode; children?: ReactNode }>) {
  const [open, setOpen] = useState(true);
  const id = useId();
  const head = (
    <>
      <span className="shrink-0 text-eyebrow uppercase text-muted">{label}</span>
      {meta && <span className="shrink-0 text-caption tabular-nums text-body">{meta}</span>}
    </>
  );
  return (
    <section aria-label={label} className="flex min-w-0 flex-col border-t border-hairline px-1 py-1.5 first:border-t-0">
      <div className={cn('flex min-h-7 min-w-0 flex-wrap items-center gap-x-3', PAD)}>
        {children ? (
          <button type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpen((o) => !o)} className="-ml-1.5 flex h-7 min-w-0 items-center gap-1.5 rounded-sm pr-1.5 pl-1 text-left transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11">
            <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-100', open && 'rotate-90')} />
            {head}
          </button>
        ) : (
          <span className="flex h-7 min-w-0 items-center gap-1.5">{head}</span>
        )}
        <span className="flex-1" />
        {action}
      </div>
      {children && (
        <Collapse open={open}>
          <div id={id} className="flex min-w-0 flex-col pb-1">{children}</div>
        </Collapse>
      )}
    </section>
  );
}

export function LiveCard({ now, plan, changed }: Readonly<{
  /** The progress line, absent while the Thread waits on you. */
  now?: ReactNode;
  /** The todo list: its count, its rows (absent when only earlier turns kept one) and the word links to its readers. */
  plan?: { done: number; total: number; rows?: ReactNode; links: ReactNode };
  /** The Thread's changed files and what opens Changes. */
  changed?: { files: readonly ChangeFile[]; onReview: () => void };
}>) {
  const live = useScope((d) => d.live);
  const subagents = live?.filter((x) => !x.ref).length ?? 0;
  const files = changed?.files ?? [];
  if (!now && !plan && !live?.length && !files.length) return null;
  return (
    <section aria-label="This turn so far" data-live-card="" className="flex min-w-0 flex-col rounded-md bg-raised shadow-raised">
      {now && (
        <Section label="Now">
          <div className="flex min-w-0">{now}</div>
        </Section>
      )}
      {plan && (
        <Section label="Plan" meta={plan.rows ? `${plan.done} of ${plan.total}` : undefined} action={plan.links}>
          {plan.rows && <div className={PAD}>{plan.rows}</div>}
        </Section>
      )}
      {!!live?.length && (
        <Section label="Subagents" meta={subagents ? String(subagents) : undefined}>
          <LiveSubagents />
        </Section>
      )}
      {files.length > 0 && (
        <Section label="Changed so far" meta={`· ${plural(files.length, 'file')}`} action={<Button variant="link" onClick={changed!.onReview}>Review</Button>}>
          <ul className={cn('flex min-w-0 flex-col', PAD)}>
            {files.slice(0, FILE_ROWS).map((f) => (
              <li key={f.path} className="flex min-h-6 min-w-0 items-center gap-2">
                <span translate="no" title={f.path} className="min-w-0 truncate font-mono text-code-sm text-body">{f.path}</span>
                {!f.counts_unknown && !f.binary && <span className="shrink-0 font-mono text-meta"><LineCounts additions={f.additions} deletions={f.deletions} /></span>}
              </li>
            ))}
          </ul>
          {files.length > FILE_ROWS && <p className={cn('text-caption text-muted', PAD)}>and {files.length - FILE_ROWS} more</p>}
        </Section>
      )}
    </section>
  );
}
