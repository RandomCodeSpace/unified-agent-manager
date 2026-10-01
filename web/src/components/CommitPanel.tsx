import { ArrowDownToLine, Check, ChevronDown, FolderGit2, GitBranch, GitCommitHorizontal, RefreshCw, Sparkles, TriangleAlert, Upload } from 'lucide-react';
import { useEffect, useEffectEvent, useId, useRef, useState } from 'react';
import { api, describeError, type GitFile, type GitState, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { Note } from './common';
import { Button } from './ui/button';
import { Collapse } from './ui/collapse';

/** The subject length git tooling and most hosts show whole. */
export const SUBJECT_LIMIT = 72;

/** A drafted or typed message per Task, kept while the panel is closed. */
interface Draft {
  message: string;
  /** The message the Utility model returned, while the text still is it. */
  generated?: string;
  conventional?: boolean;
}
const drafts = new Map<string, Draft>();

/**
 * The files checked at first: `defaults` when the host names them, else this Task's files (none
 * once they are committed), else, when the Task's own edits are unknown, every changed file no
 * other Task touched.
 */
export function defaultSelection(files: GitFile[], known: boolean, defaults?: string[]): string[] {
  if (defaults) return files.filter((f) => defaults.includes(f.path)).map((f) => f.path);
  return files.filter((f) => (known ? f.mine : !f.other_task)).map((f) => f.path);
}

/** What the note under the files says about the changed files left out, or null. */
export function leftOutNote(files: GitFile[], selected: readonly string[]): string | null {
  const out = files.filter((f) => !selected.includes(f.path));
  if (out.length === 0) return null;
  const n = out.length;
  const files_ = n === 1 ? 'file' : 'files';
  return out.every((f) => f.other_task) ? `${n} ${files_} from other tasks ${n === 1 ? 'is' : 'are'} not included` : `${n} other changed ${files_} ${n === 1 ? 'is' : 'are'} not included`;
}

/** The first line's length, in characters as people count them. */
export function subjectLength(message: string): number {
  return [...(message.split('\n')[0] ?? '')].length;
}

type Action = 'draft' | 'commit' | 'push' | 'pull' | 'init';

/**
 * Commit, push and pull a Task's repository with buttons (contract C3). The message is drafted
 * on the Utility model from the chosen files and edited freely; it is committed as written,
 * with no co-author or AI credit. The files default to this Task's own. While any Task in the
 * repository is mid-turn the service refuses every write and the panel says why. Hosts: the
 * Changes panel (collapsed until asked), and the task pane's finish card (`defaultOpen`).
 */
export function CommitPanel({
  session,
  defaultFiles,
  defaultOpen = false,
  onChanged,
  className,
}: Readonly<{
  session: SessionSummary;
  /** Paths to check at first; this Task's changed files when absent. */
  defaultFiles?: string[];
  defaultOpen?: boolean;
  /** After a commit, push, pull or set-up, so the host can refresh what it shows. */
  onChanged?: () => void;
  className?: string;
}>) {
  const [git, setGit] = useState<GitState | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [open, setOpen] = useState(defaultOpen);
  const [draft, setDraftState] = useState<Draft>(() => drafts.get(session.id) ?? { message: '' });
  // Null until the owner changes the checks: until then they follow the defaults as files change.
  const [picked, setPicked] = useState<string[] | null>(null);
  const [running, setRunning] = useState<Action | null>(null);
  const [result, setResult] = useState<{ tone: 'info' | 'error'; text: string } | null>(null);
  const request = useRef<AbortController | null>(null);
  const formId = useId();

  const setDraft = (next: Draft) => {
    drafts.set(session.id, next);
    setDraftState(next);
  };

  const load = useEffectEvent(() => {
    if (document.visibilityState !== 'visible' || request.current) return;
    const controller = new AbortController();
    request.current = controller;
    api.git(session.id, controller.signal).then((next) => {
      if (controller.signal.aborted) return;
      setGit(next);
      setLoadError(null);
    }).catch((e: unknown) => {
      if (!controller.signal.aborted) setLoadError(describeError(e));
    }).finally(() => {
      if (request.current === controller) request.current = null;
    });
  });
  useEffect(() => {
    load();
    const timer = window.setInterval(load, 5000);
    const visible = () => { if (document.visibilityState === 'visible') load(); };
    document.addEventListener('visibilitychange', visible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', visible);
      request.current?.abort();
      request.current = null;
    };
  }, [session.id]);
  // A turn starting or ending changes whether writes may run; an action changes the rest.
  const [reload, setReload] = useState(0);
  useEffect(() => { load(); }, [session.state, reload]);

  const files = git?.files ?? [];
  const paths = files.map((f) => f.path);
  const selected = (picked ?? defaultSelection(files, !!git?.task_files_known, defaultFiles)).filter((p) => paths.includes(p));
  const leftOut = leftOutNote(files, selected);
  const subject = subjectLength(draft.message);
  const generated = !!draft.generated && draft.generated === draft.message;
  const mineOnly = selected.length > 0 && selected.every((p) => files.find((f) => f.path === p)?.mine);
  const busy = git?.busy;
  const blocked = !!busy || running !== null;
  const canCommit = !blocked && selected.length > 0 && draft.message.trim() !== '';

  const run = async (action: Action, work: () => Promise<string | null>) => {
    setRunning(action);
    setResult(null);
    try {
      const summary = await work();
      if (summary) setResult({ tone: 'info', text: summary });
    } catch (e) {
      setResult({ tone: 'error', text: describeError(e) });
    } finally {
      setRunning(null);
      request.current?.abort();
      request.current = null;
      setReload((n) => n + 1);
      if (action !== 'draft') onChanged?.();
    }
  };

  const generate = () => void run('draft', async () => {
    const d = await api.gitMessage(session.id, selected);
    setDraft({ message: d.message, generated: d.message, conventional: d.conventional });
    return null;
  });
  const commit = (push: boolean) => void run('commit', async () => {
    const done = await api.gitCommit(session.id, selected, draft.message);
    setDraft({ message: '' });
    setPicked(null);
    if (!push) return done.summary;
    try {
      const pushed = await api.gitPush(session.id);
      return `${done.summary} ${pushed.summary}`;
    } catch (e) {
      throw new Error(`${done.summary} The push failed: ${describeError(e)}`, { cause: e });
    }
  });
  const push = () => void run('push', async () => (await api.gitPush(session.id)).summary);
  const pull = () => void run('pull', async () => (await api.gitPull(session.id)).summary);

  if (!git) {
    return loadError ? <Note tone="error" role="alert" className={cn('px-3 py-2', className)}>Could not read the repository: {loadError}</Note> : null;
  }
  if (!git.repo) {
    return (
      <section aria-label="Git" className={cn('flex flex-col gap-2 px-3 py-2', className)}>
        <Note>{git.reason ? `${git.reason[0].toUpperCase()}${git.reason.slice(1)}.` : 'This folder is not in a Git repository.'}</Note>
        {git.can_init && <SetUpGitButton session={session} busy={busy} onDone={() => { setReload((n) => n + 1); onChanged?.(); }} />}
      </section>
    );
  }

  const pushTitle = !git.remote ? 'No remote to push to' : !git.has_commits ? 'Nothing to push yet' : git.upstream ? `Push to ${git.upstream}` : 'Push to origin';
  return (
    <section aria-label="Commit" className={cn('flex shrink-0 flex-col gap-2 px-3 py-2', className)}>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          size="sm"
          variant={open ? 'ghost' : 'secondary'}
          aria-expanded={open}
          aria-controls={formId}
          disabled={files.length === 0 && !open}
          onClick={() => setOpen(!open)}
        >
          <GitCommitHorizontal />
          {files.length === 0 ? 'Nothing to commit' : 'Commit'}
          {files.length > 0 && <ChevronDown className={cn('transition-transform duration-160', open && 'rotate-180')} />}
        </Button>
        <span className="flex min-w-0 flex-1 items-center gap-1 text-meta text-muted" title={git.upstream ? `${git.branch ?? 'HEAD'} tracks ${git.upstream}` : undefined}>
          <GitBranch aria-hidden="true" className="size-3 shrink-0" />
          <span className="truncate">{git.branch ?? 'detached HEAD'}</span>
          {git.ahead > 0 && <span className="tabular-nums" aria-label={`${git.ahead} to push`}>↑{git.ahead}</span>}
          {git.behind > 0 && <span className="tabular-nums" aria-label={`${git.behind} to pull`}>↓{git.behind}</span>}
        </span>
        <Button size="sm" variant="secondary" title={pushTitle} disabled={blocked || !git.remote || !git.has_commits} loading={running === 'push'} onClick={push}>
          <Upload />
          Push
        </Button>
        <Button size="sm" variant="secondary" title={git.upstream ? `Fast-forward from ${git.upstream}` : 'No upstream to pull from'} disabled={blocked || !git.upstream} loading={running === 'pull'} onClick={pull}>
          <ArrowDownToLine />
          Pull
        </Button>
      </div>

      <Collapse open={open && files.length > 0} inner="flex flex-col gap-2">
        <div id={formId} className="flex flex-col gap-2 pt-0.5">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <label htmlFor={`${formId}-message`} className="text-ui font-medium text-ink">Commit message</label>
            {generated && (
              <span className="inline-flex items-center gap-1 rounded-full bg-info-wash px-2 py-0.5 text-meta text-info">
                <Sparkles aria-hidden="true" className="size-3" />
                {mineOnly ? 'Generated from this task’s changes' : 'Generated from the chosen files'}
              </span>
            )}
            <Button size="sm" variant="subtle" className="ml-auto text-accent" disabled={running !== null || selected.length === 0} loading={running === 'draft'} onClick={generate}>
              {draft.generated ? <RefreshCw /> : <Sparkles />}
              {draft.generated ? 'Regenerate' : 'Generate'}
            </Button>
          </div>
          <textarea
            id={`${formId}-message`}
            rows={4}
            spellCheck
            placeholder="Summarize the change in one line, then add details after a blank line"
            className="min-h-24 w-full resize-y rounded-sm bg-sunken px-2.5 py-2 font-mono text-code-sm text-ink shadow-well transition-[background-color] placeholder:font-sans placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none"
            value={draft.message}
            onChange={(e) => setDraft({ ...draft, message: e.target.value })}
          />
          <p className="flex flex-wrap gap-x-2 gap-y-0.5 text-meta text-muted">
            {generated && draft.conventional && <span>Matches this repo’s style: Conventional Commits, from its last 20 commits</span>}
            <span className={cn('tabular-nums', subject > SUBJECT_LIMIT && 'text-warning')}>{generated && draft.conventional && '· '}{subject}/{SUBJECT_LIMIT} characters in the subject</span>
            <span>· No co-author or AI credit</span>
          </p>
          <fieldset className="flex max-h-44 flex-col overflow-y-auto">
            <legend className="sr-only">Files to commit</legend>
            {files.map((f) => (
              <label key={f.path} className="flex min-h-7 items-center gap-2 text-ui text-ink pointer-coarse:min-h-11">
                <input
                  type="checkbox"
                  className="size-3.5 shrink-0 accent-accent"
                  checked={selected.includes(f.path)}
                  onChange={(e) => setPicked(e.target.checked ? [...selected, f.path] : selected.filter((p) => p !== f.path))}
                />
                <span className="min-w-0 truncate font-mono text-code-sm [direction:rtl] text-left [unicode-bidi:plaintext]">{f.path}</span>
                {f.other_task && <span className="shrink-0 text-meta text-muted">another task</span>}
                {f.status === 'deleted' && <span className="shrink-0 text-meta text-error">deleted</span>}
              </label>
            ))}
          </fieldset>
          {leftOut && (
            <p className="flex items-center gap-1 text-caption text-muted">
              <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
              {leftOut}
            </p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" disabled={!canCommit} loading={running === 'commit'} onClick={() => commit(false)}>
              <GitCommitHorizontal />
              Commit {selected.length} {selected.length === 1 ? 'file' : 'files'}
            </Button>
            <Button variant="secondary" disabled={!canCommit || !git.remote} title={git.remote ? undefined : 'No remote to push to'} onClick={() => commit(true)}>
              <Upload />
              Commit and push
            </Button>
          </div>
        </div>
      </Collapse>

      {busy ? (
        <p role="status" className="flex items-start gap-1 text-meta text-warning">
          <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
          {busy}
        </p>
      ) : (
        open && <p className="flex items-center gap-1 text-meta text-muted"><Check aria-hidden="true" className="size-3 text-success" /> No task is running in this repository</p>
      )}
      {result && (
        <Note tone={result.tone} role={result.tone === 'error' ? 'alert' : 'status'} className="max-h-48 overflow-y-auto break-words whitespace-pre-wrap">
          {result.text}
        </Note>
      )}
    </section>
  );
}

/**
 * "Set up git here": `git init` in the Project folder, offered only where git says the folder
 * is in no repository. The Project's frame then drops `no_git`, so Changes and Files appear.
 */
export function SetUpGitButton({ session, busy, onDone }: Readonly<{ session: SessionSummary; busy?: string; onDone?: () => void }>) {
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="flex flex-col gap-1.5">
      <Button
        variant="secondary"
        className="self-start"
        disabled={!!busy}
        loading={running}
        onClick={() => {
          setRunning(true);
          setError(null);
          api.gitInit(session.id).then(() => onDone?.(), (e: unknown) => setError(describeError(e))).finally(() => setRunning(false));
        }}
      >
        <FolderGit2 />
        Set up git here
      </Button>
      {busy && <Note tone="warn" role="status">{busy}</Note>}
      {error && <Note tone="error" role="alert">{error}</Note>}
    </div>
  );
}
