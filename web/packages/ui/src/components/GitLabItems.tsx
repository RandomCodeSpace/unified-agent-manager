import { ExternalLink, Search } from 'lucide-react';
import { useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type GitLabItemSummary, type GitLabPipelineDetail, type GitLabReview as Review, type GitLabWorkItem, type GitLabWorkItemLink, type SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { parseRefKey, refKey, refText, sameRef, type GitLabRef } from '../lib/gitlabRefs';
import { dateTime, Note, Skeleton } from './common';
import { GitLabItemsContext, KIND_WORD, KindGlyph, itemLabel, statusLabel, statusTone, toneText, useSummaries, useSummaryRevision, type GitLabItems } from './GitLabStrip';
import { GitLabMarkdown, GitLabReview } from './GitLabReview';
import { useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Clamp, PanelSection } from './ui/panel';
// The GitLab side panel's views of one item opened from the conversation (GitLabStrip): a merge
// request with its discussions, a work item, or a pipeline with its jobs and a job's log.

/* ---------- the side panel's item views ---------- */

/** The open item: a switcher between the turn's items, its head and its view. */
export function GitLabItemView({ session, refs, current, onOpen }: Readonly<{ session?: SessionDetail; refs: GitLabRef[]; current: GitLabRef; onOpen: (ref: GitLabRef) => void }>) {
  const gl = useContext(GitLabItemsContext);
  const sessionId = gl?.sessionId ?? '';
  const items = useSummaries(sessionId, gl ? (refs.some((r) => sameRef(r, current)) ? refs : [...refs, current]) : []);
  const item = items.get(refKey(current));
  if (!gl) return null;
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {refs.length > 1 && (
        <div role="group" aria-label="This turn's GitLab items" className="flex flex-wrap gap-1">
          {refs.map((r) => (
            <button key={refKey(r)} type="button" aria-pressed={sameRef(r, current)} onClick={() => onOpen(r)} className="inline-flex h-6 items-center gap-1 rounded-sm px-1.5 text-caption text-muted hover:bg-tint-hover hover:text-ink aria-pressed:bg-tint-selected aria-pressed:text-ink">
              <KindGlyph r={r} item={items.get(refKey(r))} className="size-3" />
              <span className={cn(r.kind === 'job' && 'font-mono')}>{r.kind === 'pipeline' ? `#${r.id}` : itemLabel(r, items.get(refKey(r)))}</span>
            </button>
          ))}
        </div>
      )}
      {current.kind === 'mr' ? <MergeRequestView key={refKey(current)} gl={gl} session={session} r={current} item={item} onOpen={onOpen} />
        : current.kind === 'pipeline' || current.kind === 'job' ? <PipelineView key={refKey(current)} gl={gl} r={current} item={item} />
        : <WorkItemView key={refKey(current)} gl={gl} r={current} item={item} onOpen={onOpen} />}
    </div>
  );
}

function Head({ title, state, facts }: Readonly<{ title: ReactNode; state?: ReactNode; facts?: ReactNode }>) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex min-w-0 items-start gap-2">
        <h2 className="min-w-0 flex-1 text-title text-ink [overflow-wrap:anywhere]">{title}</h2>
        {state && <span className="mt-0.5 flex shrink-0 items-center gap-1">{state}</span>}
      </div>
      {facts && <p className="text-caption leading-6 text-muted [overflow-wrap:anywhere]">{facts}</p>}
    </div>
  );
}

function StateChip({ state, draft }: Readonly<{ state?: string; draft?: boolean }>) {
  if (!state) return null;
  return <>{draft && <Chip fill="outline">Draft</Chip>}<Chip tone={statusTone(state)} className="px-0 font-medium">{statusLabel(state)}</Chip></>;
}

/** A linked item as one row: glyph, ref, title, state word; it opens when it is one of this Thread's project's. */
function LinkRow({ link, onOpen }: Readonly<{ link: GitLabWorkItemLink; onOpen: (ref: GitLabRef) => void }>) {
  const r = link.ref ? parseRefKey(link.ref) : undefined;
  const summary: GitLabItemSummary = { ref: link.ref ?? '', kind: link.kind, title: link.title, state: link.state };
  const body = <>
    <KindGlyph r={r ?? { kind: 'issue', id: link.iid }} item={summary} />
    <span className="shrink-0 font-medium text-ink">{link.kind === 'epic' ? '&' : '#'}{link.iid}</span>
    <span className="min-w-0 flex-1 truncate text-body">{link.title}</span>
    <span className={cn('shrink-0 text-caption', toneText(link.state))}>{statusLabel(link.state)}</span>
  </>;
  const className = '-mx-2 flex min-h-8 min-w-0 items-center gap-2 rounded-sm px-2 text-left text-ui';
  if (r) return <button type="button" className={cn(className, 'hover:bg-tint-hover')} onClick={() => onOpen(r)}>{body}</button>;
  return link.web_url ? <a href={link.web_url} target="_blank" rel="noreferrer" className={cn(className, 'hover:bg-tint-hover')}>{body}<ExternalLink aria-hidden="true" className="size-3 shrink-0 text-muted" /></a> : <div className={className}>{body}</div>;
}

function useRead<T>(read: (signal: AbortSignal) => Promise<T>, deps: unknown[]): { value?: T; error?: string } {
  const [result, setResult] = useState<{ value?: T; error?: string }>({});
  useEffect(() => {
    const controller = new AbortController();
    // A read again that fails keeps what was shown.
    read(controller.signal).then((value) => { if (!controller.signal.aborted) setResult({ value }); }, (e: unknown) => { if (!controller.signal.aborted) setResult((old) => ({ value: old.value, error: describeError(e) })); });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the caller names what the read depends on.
  }, deps);
  return result;
}

function MergeRequestView({ gl, session, r, item, onOpen }: Readonly<{ gl: GitLabItems; session?: SessionDetail; r: GitLabRef; item?: GitLabItemSummary; onOpen: (ref: GitLabRef) => void }>) {
  const [review, setReview] = useState<Review>();
  // Writes (reply, resolve, Fix, Draft reply) stay with the merge requests of the Thread's own commit.
  const own = gl.state.commit && gl.state.merge_requests?.some((mr) => mr.iid === r.id) ? gl.state.commit.sha : undefined;
  const mr = review?.merge_request;
  const pipeline = mr?.head_pipeline;
  const revision = useSummaryRevision(gl.sessionId, r);
  return (
    <>
      <Head title={(mr?.title ?? item?.title ?? `Merge request ${refText(r)}`).replace(/^Draft:\s*/, '')} state={<StateChip state={mr?.state ?? item?.state} draft={mr?.draft ?? item?.draft} />}
        facts={mr && <>{refText(r)} · <span className="font-mono text-code-sm">{mr.source_branch}</span> → <span className="font-mono text-code-sm">{mr.target_branch}</span>{mr.author && <> · {mr.author.name || mr.author.username}</>}</>} />
      {pipeline && (
        <PanelSection label="Pipeline" meta={pipeline.sha ? pipeline.sha.slice(0, 8) : undefined}>
          <button type="button" className="-mx-2 flex min-h-8 min-w-0 items-center gap-2 rounded-sm px-2 text-left text-ui hover:bg-tint-hover" onClick={() => onOpen({ kind: 'pipeline', id: pipeline.id })}>
            <KindGlyph r={{ kind: 'pipeline', id: pipeline.id }} item={{ ref: '', kind: 'pipeline', state: pipeline.status }} />
            <span className="shrink-0 font-medium text-ink">#{pipeline.id}</span>
            <span className="min-w-0 flex-1 truncate font-mono text-code-sm text-body">{pipeline.ref}</span>
            <span className={cn('shrink-0 text-caption', toneText(pipeline.status))}>{statusLabel(pipeline.status)}</span>
          </button>
        </PanelSection>
      )}
      <PanelSection label="Discussions">
        <GitLabReview key={own ?? 'read'} sessionId={gl.sessionId} session={session} iid={r.id} sha={own} onLoad={setReview} refresh={revision} />
      </PanelSection>
    </>
  );
}

function WorkItemView({ gl, r, item, onOpen }: Readonly<{ gl: GitLabItems; r: GitLabRef; item?: GitLabItemSummary; onOpen: (ref: GitLabRef) => void }>) {
  const api = useApi();
  const revision = useSummaryRevision(gl.sessionId, r);
  const { value: w, error } = useRead((signal) => api.gitlabWorkItem(gl.sessionId, refKey(r), signal), [api, gl.sessionId, refKey(r), revision]);
  const parent = w?.parent?.ref ? parseRefKey(w.parent.ref) : undefined;
  return (
    <>
      <Head title={w?.title ?? item?.title ?? itemLabel(r, item, true)} state={<StateChip state={w?.state ?? item?.state} />}
        facts={w && <>{KIND_WORD[w.kind] ?? 'Work item'} {refText(r)}{w.created_at && <> · opened {dateTime(w.created_at)}</>}{w.author && <> by {w.author.name || w.author.username}</>}{w.parent && <> · in {parent
          ? <button type="button" className="inline-flex h-5 items-center gap-1 rounded-xs bg-tint-well px-1.5 align-middle text-caption text-ink hover:bg-sunken" onClick={() => onOpen(parent)}>{refText(parent)} {w.parent.title}</button>
          : <span className="text-body">{w.parent.title}</span>}</>}</>} />
      {error && <Note tone="error" role="alert">{error}</Note>}
      {!w && !error && <Skeleton label="Loading the work item…" rows={3} />}
      {w && <WorkItemBody w={w} onOpen={onOpen} />}
    </>
  );
}

function WorkItemBody({ w, onOpen }: Readonly<{ w: GitLabWorkItem; onOpen: (ref: GitLabRef) => void }>) {
  return <>
    {(w.labels.length > 0 || w.assignees.length > 0 || w.milestone) && (
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-ui">
        {w.labels.length > 0 && <><dt className="text-caption text-muted">Labels</dt><dd className="flex flex-wrap gap-1">{w.labels.map((label) => <Chip key={label} fill="well" className="text-body">{label}</Chip>)}</dd></>}
        {w.assignees.length > 0 && <><dt className="text-caption text-muted">Assignees</dt><dd className="flex flex-wrap gap-3 text-ink">{w.assignees.map((user) => <span key={user.username}>{user.name || user.username}</span>)}</dd></>}
        {w.milestone && <><dt className="text-caption text-muted">Milestone</dt><dd className="text-ink">{w.milestone}</dd></>}
      </dl>
    )}
    {w.description && (
      <PanelSection label="Description">
        <Clamp noun="description" height={160}><GitLabMarkdown text={w.description} /></Clamp>
      </PanelSection>
    )}
    {w.child_count > 0 && (
      <PanelSection label="Child items" meta={w.child_count > w.children.length ? `${w.children.length} of ${w.child_count}` : `${w.children.filter((c) => c.state === 'opened').length} open of ${w.child_count}`}>
        <div className="flex flex-col">{w.children.map((child) => <LinkRow key={`${child.ref ?? child.web_url ?? child.iid}`} link={child} onOpen={onOpen} />)}</div>
      </PanelSection>
    )}
  </>;
}

/** Error lines of a job log, picked out without colour alone: they also read as errors. */
const ERROR_LINE = /\berror\b|\bfailed\b|^FAIL|\.go:\d+:\d+:|\bpanic:/i;
const LOG_LINES = 60;

function PipelineView({ gl, r, item }: Readonly<{ gl: GitLabItems; r: GitLabRef; item?: GitLabItemSummary }>) {
  const api = useApi();
  const tasks = useTaskActions();
  const pipelineId = r.kind === 'pipeline' ? r.id : item?.pipeline_id;
  // The open item's state is read on the strip's cadence; each read again reads the jobs again.
  const revision = useSummaryRevision(gl.sessionId, r);
  const { value: detail, error } = useRead<GitLabPipelineDetail | undefined>((signal) => pipelineId ? api.gitlabPipelineDetail(gl.sessionId, pipelineId, signal) : Promise.resolve(undefined), [api, gl.sessionId, pipelineId, revision]);
  const jobs = detail?.jobs ?? [];
  const [picked, setPicked] = useState<number | undefined>(r.kind === 'job' ? r.id : undefined);
  const selected = jobs.find((job) => job.id === picked) ?? jobs.find((job) => job.status === 'failed' && !job.allow_failure);
  const stages = [...new Set(jobs.map((job) => job.stage))];
  const p = detail?.pipeline;
  const failed = jobs.filter((job) => job.status === 'failed' && !job.allow_failure).length;
  // Investigate stays with the Thread's own commit's pipeline, as in the pipeline view.
  const own = gl.state.commit && gl.state.pipeline?.id === pipelineId && gl.state.pipeline?.sha === gl.state.commit.sha ? gl.state : undefined;
  const [starting, setStarting] = useState(false);
  const [investigateError, setInvestigateError] = useState<string>();
  const investigate = async () => {
    if (!own?.commit || !pipelineId || starting) return;
    setStarting(true);
    setInvestigateError(undefined);
    try {
      tasks.select((await api.investigateGitlabPipeline(gl.sessionId, own.commit.sha, pipelineId)).task_id);
    } catch (e) {
      setInvestigateError(describeError(e));
    } finally {
      setStarting(false);
    }
  };
  if (!pipelineId) return item?.error ? <Note tone="error" role="alert">{item.error}</Note> : <Skeleton label="Loading the job…" rows={2} />;
  return (
    <>
      <Head title={`Pipeline #${pipelineId}`} state={p && <Chip tone={statusTone(p.status)} className="px-0 font-medium">{statusLabel(p.status)}</Chip>}
        facts={p && <><span className="font-mono text-code-sm">{p.ref}</span> · <span className="font-mono text-code-sm">{p.sha.slice(0, 8)}</span>{p.duration != null && <> · {Math.round(p.duration)}s</>}{p.created_at && <> · {dateTime(p.created_at)}</>}</>} />
      {own && (own.investigation_task_id && own.investigation_task_id !== gl.sessionId
        ? <div><Button size="sm" variant="secondary" onClick={() => tasks.select(own.investigation_task_id!)}>Open investigation</Button></div>
        : own.can_investigate && !own.auto_investigate && p?.status === 'failed' && <div><Button size="sm" variant="primary" loading={starting} onClick={() => void investigate()}><Search />Investigate</Button></div>)}
      {investigateError && <Note tone="error" role="alert">{investigateError}</Note>}
      {error && <Note tone="error" role="alert">{error}</Note>}
      {!detail && !error && <Skeleton label="Loading the pipeline…" rows={3} />}
      {detail && (
        <PanelSection label="Jobs" meta={`${jobs.length} ${jobs.length === 1 ? 'job' : 'jobs'} · ${failed} failed`}>
          {jobs.length ? (
            <div className="grid grid-cols-[repeat(auto-fit,minmax(8rem,1fr))] gap-2">
              {stages.map((stage) => (
                <div key={stage} role="group" aria-label={`Stage ${stage}`} className="flex min-w-0 flex-col gap-1">
                  <span className="truncate text-caption text-muted">{stage}</span>
                  {jobs.filter((job) => job.stage === stage).map((job) => (
                    <button key={job.id} type="button" aria-pressed={job.id === selected?.id} onClick={() => setPicked(job.id)} className="flex h-8 min-w-0 items-center gap-1.5 rounded-sm bg-sunken px-2 text-left text-ui text-ink transition-colors duration-100 hover:bg-tint-hover aria-pressed:bg-tint-selected">
                      <KindGlyph r={{ kind: 'job', id: job.id }} item={{ ref: '', kind: 'job', state: job.status }} />
                      <span className="min-w-0 flex-1 truncate font-mono text-code-sm">{job.name}</span>
                      <span className={cn('shrink-0 text-caption', toneText(job.status))}>{statusLabel(job.status).toLowerCase()}{job.allow_failure && job.status === 'failed' && ' · allowed'}</span>
                    </button>
                  ))}
                </div>
              ))}
            </div>
          ) : <Note>No jobs were returned for this pipeline.</Note>}
        </PanelSection>
      )}
      {selected && <JobLog key={selected.id} sessionId={gl.sessionId} pipelineId={pipelineId} job={selected} revision={revision} />}
    </>
  );
}

function JobLog({ sessionId, pipelineId, job, revision }: Readonly<{ sessionId: string; pipelineId: number; job: GitLabPipelineDetail['jobs'][number]; revision: number }>) {
  const api = useApi();
  // A running job's log grows: it is read again with the pipeline; a finished one once more when it ends.
  const growing = job.status === 'running' ? revision : 0;
  const { value: trace, error } = useRead((signal) => api.gitlabJobTrace(sessionId, job.id, undefined, pipelineId, signal), [api, sessionId, job.id, pipelineId, job.status, growing]);
  const lines = (trace?.text ?? '').replace(/\n$/, '').split('\n').slice(-LOG_LINES);
  // The failure is at the end: the log opens there and stays there as it grows, unless the owner scrolled up.
  const box = useRef<HTMLPreElement>(null);
  const pinned = useRef(true);
  useLayoutEffect(() => {
    const el = box.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [trace?.text]);
  return (
    <PanelSection label={`${job.name} log`} meta={trace && `last ${lines.length} lines`}
      action={job.web_url && <a href={job.web_url} target="_blank" rel="noreferrer" className="inline-flex h-6 items-center gap-1 rounded-xs px-1 text-caption text-muted hover:text-ink">Full log<ExternalLink aria-hidden="true" className="size-3" /></a>}>
      {error ? <Note tone="error" role="alert">{error}</Note> : !trace ? <Skeleton label={`Loading the ${job.name} log…`} rows={3} /> : !trace.text ? <Note>No log is available for this job yet.</Note> : (
        <pre ref={box} aria-label={`${job.name} log`} onScroll={(e) => { const el = e.currentTarget; pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24; }} className="max-h-96 overflow-auto rounded-md bg-sunken px-3 py-2 font-mono text-code-sm leading-5 text-body">
          {lines.map((line, i) => <div key={i} className={cn('whitespace-pre-wrap [overflow-wrap:anywhere]', ERROR_LINE.test(line) && 'text-error')}>{line || ' '}</div>)}
        </pre>
      )}
    </PanelSection>
  );
}
