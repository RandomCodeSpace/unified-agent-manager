import { ChevronRight, ExternalLink, GitBranch, GitCommitHorizontal, GitPullRequest, RefreshCw, Search } from 'lucide-react';
import { useEffect, useEffectEvent, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type GitLabPipelineState, type SessionDetail } from '../api';
import { dateTime, Note, Skeleton } from './common';
import { GitLabReview } from './GitLabReview';
import { useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { statusLabel, statusTone } from './GitLabStrip';

export function externalURL(value: string): string | undefined {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.href : undefined;
  } catch { return undefined; }
}

function GitLabLink({ url, children, label, fallback }: Readonly<{ url: string; children: ReactNode; label?: string; fallback?: string }>) {
  const href = externalURL(url);
  return href ? <a href={href} aria-label={label} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-start gap-1.5 text-ui text-accent hover:underline"><span className="min-w-0 break-words">{children}</span><ExternalLink aria-hidden="true" className="mt-0.5 size-3 shrink-0" /></a>
    : <span className="min-w-0 break-words text-ui text-ink">{fallback ?? children}</span>;
}

/** Content of the Task's GitLab side panel. Its API never lists unrelated pipelines. */
export function GitLabPipelinePanel({ sessionId, session, refreshKey }: Readonly<{ sessionId: string; session?: SessionDetail; refreshKey: number }>) {
  const api = useApi();
  return api.supports('gitlab-pipelines-v1') ? <Pipeline key={`${api.cacheKey(sessionId)}:${api.owner?.generation ?? 0}`} sessionId={sessionId} session={session} refreshKey={refreshKey} /> : <Note>This UAM instance does not support GitLab pipeline monitoring.</Note>;
}

function Pipeline({ sessionId, session, refreshKey }: Readonly<{ sessionId: string; session?: SessionDetail; refreshKey: number }>) {
  const api = useApi();
  const tasks = useTaskActions();
  const [state, setState] = useState<GitLabPipelineState>();
  const [error, setError] = useState<string>();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshCount, setRefreshCount] = useState(0);
  const [starting, setStarting] = useState(false);
  const [selectedMR, setSelectedMR] = useState<number>();
  const request = useRef<AbortController | null>(null);
  const live = useRef(true);

  const load = useEffectEvent((refresh = false) => {
    if (document.visibilityState !== 'visible') return;
    if (request.current && !refresh) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const read = refresh ? api.refreshGitlabPipeline : api.gitlabPipeline;
    void read(sessionId, controller.signal).then((next) => {
      if (controller.signal.aborted) return;
      setState(next);
      setError(undefined);
    }).catch((e: unknown) => {
      if (!controller.signal.aborted) setError(describeError(e));
    }).finally(() => {
      if (request.current === controller) {
        request.current = null;
        setRefreshing(false);
      }
    });
  });
  useEffect(() => {
    live.current = true;
    load(refreshCount > 0);
    const timer = window.setInterval(() => load(), 5000);
    const visible = () => { if (document.visibilityState === 'visible') load(); };
    document.addEventListener('visibilitychange', visible);
    return () => {
      live.current = false;
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', visible);
      request.current?.abort();
      request.current = null;
    };
  }, [api, sessionId, refreshKey, refreshCount]);

  const investigate = async () => {
    if (!state?.commit || !state.pipeline || starting) return;
    const commit = state.commit.sha;
    const pipeline = state.pipeline.id;
    setStarting(true);
    setError(undefined);
    try {
      const created = await api.investigateGitlabPipeline(sessionId, commit, pipeline);
      if (!live.current) return;
      setState((before) => before?.commit?.sha === commit && before.pipeline?.id === pipeline ? { ...before, investigation_task_id: created.task_id, can_investigate: false } : before);
      tasks.select(created.task_id);
    } catch (e) {
      if (live.current) setError(describeError(e));
    } finally {
      if (live.current) setStarting(false);
    }
  };

  const pipeline = state?.pipeline;
  const commit = state?.commit;
  const eligible = !!state?.supported && !!commit && !!pipeline && pipeline.sha === commit.sha && pipeline.ref === commit.branch && pipeline.status === 'failed';
  const failure = error ?? state?.error;
  const jobs = state?.jobs ?? [];
  return <div className="flex min-w-0 flex-col gap-5">
    <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
      <p className="text-caption text-muted">{state?.supported ? state.auto_investigate ? 'Automatic investigation' : 'Manual investigation' : 'Current branch'}</p>
      <Button size="sm" variant="subtle" loading={refreshing} disabled={starting} onClick={() => { setRefreshing(true); setRefreshCount((n) => n + 1); }}><RefreshCw />Refresh</Button>
    </div>
    {!state && !failure && <Skeleton label="Loading pipeline…" rows={2} />}
    {failure && <Note tone="error" role="alert">{failure} Refresh to check again.</Note>}
    {state && <>
      <section aria-label="Pipeline" className="flex shrink-0 flex-col gap-3">
        <h3 className="text-ui font-medium text-ink">Pipeline</h3>
        {pipeline && <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <Chip tone={statusTone(pipeline.status)} className="h-auto px-0 text-title">{statusLabel(pipeline.status)}</Chip>
          <GitLabLink url={pipeline.web_url} label={`Open pipeline #${pipeline.id} in GitLab`}>#{pipeline.id}</GitLabLink>
          {pipeline.duration != null && <span className="text-caption tabular-nums text-muted">{Math.round(pipeline.duration)}s</span>}
        </div>}
        {commit && <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-2 text-ui">
          <dt className="inline-flex items-center gap-1.5 text-muted"><GitCommitHorizontal aria-hidden="true" className="size-3.5" />Commit</dt>
          <dd><code className="font-mono text-code-sm text-ink" title={commit.sha}>{commit.sha.slice(0, 8)}</code></dd>
          <dt className="inline-flex items-center gap-1.5 text-muted"><GitBranch aria-hidden="true" className="size-3.5" />Branch</dt>
          <dd className="break-all text-ink">{commit.branch}</dd>
          <dt className="text-muted">UAM task</dt><dd className="min-w-0 break-words text-body">{commit.task_id === sessionId ? commit.task_name || 'Untitled task' : <Button size="sm" variant="subtle" className="h-auto max-w-full whitespace-normal px-0 py-0 text-left text-ui text-accent" onClick={() => tasks.select(commit.task_id)}>{commit.task_name || 'Open source task'}</Button>}</dd>
        </dl>}
        {!state.supported ? <Note>{state.message || 'This task’s repository does not use a supported GitLab remote.'}</Note>
          : !commit ? <Note>{state.message || 'No commit made through UAM is tracked on this branch yet.'}</Note>
            : !pipeline && <Note>{state.message || 'Waiting for a pipeline for this commit. Push the commit, then refresh.'}</Note>}
        {state.reason && !state.investigation_task_id && <Note>{state.reason}</Note>}
        {state.investigation_task_id === sessionId ? <Note>This is the investigation task.</Note>
          : state.investigation_task_id ? <div><Button size="sm" variant="secondary" onClick={() => tasks.select(state.investigation_task_id!)}>Open investigation</Button></div>
          : eligible && state.can_investigate && !state.auto_investigate && <div><Button size="sm" variant="primary" loading={starting} disabled={!!failure} onClick={() => void investigate()}><Search />Investigate</Button></div>}
        {commit && !state.investigation_task_id && <Note>{state.auto_investigate ? 'Utility collects failed pipeline data in a new task automatically.' : 'Investigate starts a task with Utility. You can switch models in the task.'}</Note>}
      </section>
      <section aria-label="Jobs" className="flex shrink-0 flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h3 className="text-ui font-medium text-ink">Jobs</h3>
          {!!jobs.length && <span className="text-caption text-muted">{jobs.length} {jobs.length === 1 ? 'job' : 'jobs'} · {jobs.filter((job) => job.status === 'failed' && !job.allow_failure).length} failed</span>}
        </div>
        {pipeline && jobs.length ? <div className="flex flex-col gap-1">
          {jobs.map((job) => <Job key={`${pipeline.id}:${job.id}:${pipeline.sha}`} sessionId={sessionId} sha={pipeline.sha} pipelineId={pipeline.id} job={job} />)}
        </div> : <Note>{pipeline ? 'No jobs were returned for this pipeline.' : 'Jobs will appear when a pipeline is available for this commit.'}</Note>}
      </section>
      <section aria-label="Merge requests" className="flex shrink-0 flex-col gap-3">
        <h3 className="text-ui font-medium text-ink">Merge requests</h3>
        {state.merge_requests?.length ? state.merge_requests.map((mr) => <div key={mr.iid} className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-2 text-caption">
            <span className="inline-flex items-center gap-1.5 text-muted"><GitPullRequest aria-hidden="true" className="size-3.5" />!{mr.iid}</span>
            <Chip tone={statusTone(mr.state)}>{statusLabel(mr.state)}</Chip>
            {mr.draft && <Chip fill="outline">Draft</Chip>}
          </div>
          <GitLabLink url={mr.web_url}>{mr.title}</GitLabLink>
          {commit && api.supports('gitlab-reviews-v1') && <Button size="sm" variant="subtle" className="self-start" aria-pressed={(selectedMR ?? state.merge_requests?.[0]?.iid) === mr.iid} onClick={() => setSelectedMR(mr.iid)}>View discussions for MR !{mr.iid}</Button>}
        </div>) : <Note>{failure ? 'Merge request status could not be checked.' : !commit ? 'Merge requests will appear after a UAM commit is tracked on this branch.' : !state.checked_at ? 'Merge requests have not been checked yet. Refresh to check.' : 'No merge request is associated with this commit and branch.'}</Note>}
        {!!state.merge_requests?.length && !api.supports('gitlab-reviews-v1') && <Note>This UAM instance does not support GitLab discussions. Open the merge request in GitLab.</Note>}
        {commit && state.merge_requests?.filter((mr) => mr.iid === (selectedMR ?? state.merge_requests?.[0]?.iid)).map((mr) => api.supports('gitlab-reviews-v1') && <GitLabReview key={`${commit.sha}:${mr.iid}`} sessionId={sessionId} session={session} iid={mr.iid} sha={commit.sha} />)}
      </section>
      {state.checked_at && <p className="mt-auto shrink-0 pt-2 text-meta text-muted">Checked <time dateTime={state.checked_at}>{dateTime(state.checked_at)}</time></p>}
    </>}
  </div>;
}

function Job({ sessionId, sha, pipelineId, job }: Readonly<{ sessionId: string; sha: string; pipelineId: number; job: NonNullable<GitLabPipelineState['jobs']>[number] }>) {
  const api = useApi();
  const [open, setOpen] = useState(false);
  const [trace, setTrace] = useState<{ text: string; truncated: boolean }>();
  const [error, setError] = useState<string>();
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void api.gitlabJobTrace(sessionId, job.id, sha, pipelineId, controller.signal).then((result) => {
      if (!controller.signal.aborted) { setTrace(result); setError(undefined); }
    }).catch((e: unknown) => { if (!controller.signal.aborted) setError(describeError(e)); });
    return () => controller.abort();
  }, [api, sessionId, job.id, sha, pipelineId, open, retry]);
  return <details className="group rounded-sm open:bg-sunken" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary className="flex cursor-pointer list-none items-center gap-2 rounded-sm px-2 py-2 text-ui text-body hover:bg-sunken [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-muted transition-transform group-open:rotate-90" />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5"><span className="break-all text-ink">{job.name}</span><span className="text-caption text-muted">{job.stage}{job.duration != null && ` · ${Math.round(job.duration)}s`}</span></span>
      <Chip tone={statusTone(job.status)}>{statusLabel(job.status)}{job.allow_failure && ' · allowed'}</Chip>
    </summary>
    <div className="flex flex-col gap-2 px-2 pb-2">
      {error ? <Note tone="error" role="alert">{error} <Button size="sm" variant="subtle" onClick={() => setRetry((n) => n + 1)}>Retry log</Button></Note> : !trace ? <Skeleton label={`Loading ${job.name} log…`} rows={2} /> : <>
        <pre aria-label={`${job.name} log`} className="max-h-48 overflow-auto rounded-sm bg-sunken p-2 font-mono text-code-sm text-body">{trace.text || 'No log is available for this job yet.'}</pre>
        {trace.truncated && <Note>Showing part of the log. Open the job in GitLab for the full log.</Note>}
      </>}
      <GitLabLink url={job.web_url} fallback="GitLab job link unavailable">Open job in GitLab</GitLabLink>
    </div>
  </details>;
}
