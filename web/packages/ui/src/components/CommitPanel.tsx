import { useApi } from '../ApiContext';
import { ArrowDownToLine, ChevronDown, FolderGit2, GitBranch, GitCommitHorizontal, RefreshCw, Sparkles, TriangleAlert, Upload } from 'lucide-react';
import { useEffect, useEffectEvent, useId, useRef, useState, useSyncExternalStore } from 'react';
import { ApiError, describeError, type GitFile, type GitState, type SessionSummary } from '../api';
import { cn } from '../lib/cn';
import { Note } from './common';
import { Button } from './ui/button';
import { Collapse } from './ui/collapse';
import { Tip } from './ui/tooltip';

/** The subject length git tooling and most hosts show whole. */
export const SUBJECT_LIMIT = 72;

/**
 * A Task's commit draft, kept while Changes is closed: the message, the checked files, and a count that moves
 * after each action so every panel re-reads the repository.
 */
interface Draft {
  message: string;
  /** The message the Utility model returned, while the text still is it. */
  generated?: string;
  conventional?: boolean;
  /** Null until the owner changes the checks: until then they follow the defaults as files change. */
  picked: string[] | null;
  acted: number;
}
const EMPTY: Draft = { message: '', picked: null, acted: 0 };
const drafts = new Map<string, Draft>();
const listeners = new Set<() => void>();
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
function saveDraft(id: string, next: Draft) {
  drafts.set(id, next);
  for (const listener of listeners) listener();
}

/**
 * The files checked at first: this Task's own (none once they are committed). A file whose
 * author is unknown is never checked for the owner: after a restart neither this Task's earlier
 * edits nor other Tasks' may be known yet.
 */
export function defaultSelection(files: GitFile[]): string[] {
  return files.filter((f) => f.mine).map((f) => f.path);
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
 * repository could still be writing the service refuses every write and the panel says why.
 * Hosted in the Changes panel, collapsed until asked.
 */
export function CommitPanel({
  session,
  defaultOpen = false,
  quiet = false,
  onChanged,
  className,
}: Readonly<{
  session: SessionSummary;
  defaultOpen?: boolean;
  /** Shows nothing while none of the changed files is this Task's and there is no outcome to show; polls only while shown. */
  quiet?: boolean;
  /** After a commit, push, pull or set-up, so the host can refresh what it shows. */
  onChanged?: () => void;
  className?: string;
}>) {
  const api = useApi();
  const [git, setGit] = useState<GitState | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [open, setOpen] = useState(defaultOpen);
  const draft = useSyncExternalStore(subscribe, () => drafts.get(session.id) ?? EMPTY);
  const [running, setRunning] = useState<Action | null>(null);
  const [result, setResult] = useState<{ tone: 'info' | 'warn' | 'error'; text: string } | null>(null);
  const request = useRef<AbortController | null>(null);
  const formId = useId();

  // Read at call time: an action's continuation must not overwrite edits made while it ran.
  const setDraft = (change: Partial<Draft>) => saveDraft(session.id, { ...(drafts.get(session.id) ?? EMPTY), ...change });
  const setPicked = (picked: string[] | null) => setDraft({ picked });

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
  const files = git?.files ?? [];
  const hidden = quiet && running === null && !result && !files.some((f) => f.mine);
  // A quiet panel showing nothing re-reads only when a turn starts or ends or an action ran.
  const poll = useEffectEvent(() => { if (!hidden) load(); });
  useEffect(() => {
    load();
    const timer = window.setInterval(poll, 5000);
    const visible = () => { if (document.visibilityState === 'visible') load(); };
    document.addEventListener('visibilitychange', visible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', visible);
      request.current?.abort();
      request.current = null;
    };
  }, [session.id]);
  // A turn starting or ending changes whether writes may run; an action, from either panel, changes the rest.
  useEffect(() => { load(); }, [session.state, draft.acted]);

  const paths = files.map((f) => f.path);
  const selected = (draft.picked ?? defaultSelection(files)).filter((p) => paths.includes(p));
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
      // Background AI paused (its daily limit) is no failure: the message can still be written by hand.
      if (e instanceof ApiError && e.body.code === 'utility_paused') setResult({ tone: 'warn', text: `${e.message.replace(/\.$/, '')}. You can still write the message yourself.` });
      else setResult({ tone: 'error', text: describeError(e) });
    } finally {
      setRunning(null);
      request.current?.abort();
      request.current = null;
      setDraft({ acted: (drafts.get(session.id) ?? EMPTY).acted + 1 });
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
    setDraft({ message: '', generated: undefined, conventional: undefined, picked: null });
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

  if (!git || hidden) {
    return !git && !quiet && loadError ? <Note tone="error" role="alert" className={cn('px-3 py-2', className)}>Could not read the repository: {loadError}</Note> : null;
  }
  if (!git.repo) {
    return (
      <section aria-label="Git" className={cn('flex flex-col gap-2 px-3 py-2', className)}>
        <Note>{git.reason ? `${git.reason[0].toUpperCase()}${git.reason.slice(1)}.` : 'This folder is not in a Git repository.'}</Note>
        {git.can_init && <SetUpGitButton session={session} busy={busy} onDone={() => { setDraft({ acted: draft.acted + 1 }); onChanged?.(); }} />}
      </section>
    );
  }

  // A reason the repository gives stays hoverable (aria-disabled with a tip); a running action or a busy Task disables outright and says why below.
  const pushOff = !git.remote ? 'No remote to push to' : !git.has_commits ? 'Nothing to push yet' : null;
  const pushTitle = pushOff ?? (git.upstream ? `Push to ${git.upstream}` : 'Push to origin');
  const pullTitle = git.upstream ? `Fast-forward from ${git.upstream}` : 'No upstream to pull from';
  const formOpen = open && files.length > 0;
  let commitOff: string | null = null;
  if (!blocked && selected.length === 0) commitOff = 'Check a file to commit.';
  else if (!blocked && draft.message.trim() === '') commitOff = 'Write or generate a message to commit.';
  return (
    <section aria-label="Commit" className={cn('flex shrink-0 flex-col gap-2 px-3 py-2', className)}>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          size="sm"
          variant={open ? 'ghost' : 'secondary'}
          aria-expanded={open}
          aria-controls={`${formId} ${formId}-actions`}
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
        <Tip label={pushTitle}>
          <Button size="sm" variant="secondary" aria-disabled={pushOff ? true : undefined} disabled={blocked} loading={running === 'push'} onClick={() => { if (!pushOff) push(); }}>
            <Upload />
            Push
          </Button>
        </Tip>
        <Tip label={pullTitle}>
          <Button size="sm" variant="secondary" aria-disabled={git.upstream ? undefined : true} disabled={blocked} loading={running === 'pull'} onClick={() => { if (git.upstream) pull(); }}>
            <ArrowDownToLine />
            Pull
          </Button>
        </Tip>
      </div>

      {/* The form is the one scroll area: it shrinks into the space the host leaves, and the actions below it stay in view. */}
      <Collapse open={formOpen} className="min-h-0" inner="flex flex-col gap-2 overflow-y-auto overflow-x-hidden overscroll-contain">
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
            onChange={(e) => setDraft({ message: e.target.value })}
          />
          <p className="flex flex-wrap gap-x-2 gap-y-0.5 text-meta text-muted">
            {generated && draft.conventional && <span>Matches this repo’s style: Conventional Commits, from its last 20 commits</span>}
            <span className={cn('tabular-nums', subject > SUBJECT_LIMIT && 'text-warning')}>{generated && draft.conventional && '· '}{subject}/{SUBJECT_LIMIT} characters in the subject</span>
            <span>· No co-author or AI credit</span>
          </p>
          <fieldset>
            <legend className="sr-only">Files to commit</legend>
            <div className="flex flex-col">
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
            </div>
          </fieldset>
          {!git.task_files_known && <p className="text-caption text-muted">This task’s earlier edits are not all known, so check the files to include yourself.</p>}
          {leftOut && (
            <p className="flex items-center gap-1 text-caption text-muted">
              <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
              {leftOut}
            </p>
          )}
        </div>
      </Collapse>
      <Collapse open={formOpen} className="shrink-0">
        <div id={`${formId}-actions`} className="flex flex-wrap items-center gap-2 pt-0.5">
          <Button variant="primary" disabled={!canCommit} loading={running === 'commit'} onClick={() => commit(false)}>
            <GitCommitHorizontal />
            Commit {selected.length} {selected.length === 1 ? 'file' : 'files'}
          </Button>
          <Tip label={git.remote ? undefined : 'No remote to push to'}>
            <Button variant="secondary" aria-disabled={git.remote ? undefined : true} disabled={!canCommit} onClick={() => { if (git.remote) commit(true); }}>
              <Upload />
              Commit and push
            </Button>
          </Tip>
        </div>
      </Collapse>

      {busy ? (
        <p role="status" className="flex items-start gap-1 text-meta text-warning">
          <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
          {busy}
        </p>
      ) : (
        formOpen && commitOff && <p className="text-meta text-muted">{commitOff}</p>
      )}
      {result && (
        <Note tone={result.tone} role={result.tone === 'error' ? 'alert' : 'status'} className="max-h-48 overflow-y-auto overflow-x-hidden break-words whitespace-pre-wrap">
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
  const api = useApi();
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
