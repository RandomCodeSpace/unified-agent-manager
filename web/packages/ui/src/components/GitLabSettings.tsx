import { Check, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type GitLabStatus, type Project } from '../api';
import { dateTime, Note, Skeleton } from './common';
import { Field } from './TaskDefaults';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Select } from './ui/select';

const ROLES: Record<number, string> = { 5: 'Minimal access', 10: 'Guest', 15: 'Planner', 20: 'Reporter', 25: 'Security Manager', 30: 'Developer', 40: 'Maintainer', 50: 'Owner' };

export function GitLabSettings({ projects }: Readonly<{ projects: Project[] }>) {
  const api = useApi();
  const [project, setProject] = useState('');
  const [check, setCheck] = useState(0);
  const [result, setResult] = useState<{ client: typeof api; project: string; check: number; status?: GitLabStatus; error?: string }>();
  const current = result?.client === api && result.project === project ? result : undefined;
  const busy = current?.check !== check;
  const status = current?.status;

  useEffect(() => {
    let active = true;
    void api.gitlabStatus(project).then(
      (status) => { if (active) setResult({ client: api, project, check, status }); },
      (error: unknown) => { if (active) setResult({ client: api, project, check, error: describeError(error) }); },
    );
    return () => { active = false; };
  }, [api, project, check]);

  return <div className="flex min-w-0 flex-col gap-5">
    <div className="flex flex-wrap items-end gap-3">
      <div className="min-w-0 flex-1"><Field id="gitlab-project" label="Check access for">
        <Select id="gitlab-project" value={project} onValueChange={setProject} items={[
          { value: '', label: 'Default GitLab instance' },
          ...projects.map((p) => ({ value: p.id, label: p.name })),
        ]} />
      </Field></div>
      <Button variant="secondary" size="lg" loading={busy} onClick={() => setCheck((n) => n + 1)}><RefreshCw aria-hidden="true" />Check access</Button>
    </div>
    {busy && !status && <Skeleton label="Checking GitLab access…" rows={4} />}
    {!busy && current?.error && <Note tone="error" role="alert">{current.error} Try Check access again.</Note>}
    {status && <div aria-busy={busy} className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2" role="status">
        <span className="break-all text-ui font-medium text-ink">{status.hostname || 'GitLab connection'}</span>
        <Chip tone={busy ? 'muted' : status.authenticated ? 'success' : 'warning'}>
          {!busy && status.authenticated && <Check aria-hidden="true" className="size-3.5" />}
          {busy ? 'Checking…' : status.authenticated ? 'Authenticated' : status.installed ? 'Not verified' : 'glab not installed'}
        </Chip>
      </div>
      {status.message && <Note tone="warn">{status.message}</Note>}
      {status.authenticated && <>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-3 text-ui">
          <dt className="text-muted">Account</dt>
          <dd className="min-w-0 break-words text-ink">{status.name && <span>{status.name} </span>}<span className="text-muted">@{status.username}</span></dd>
          {status.project && <>
            <dt className="text-muted">Project</dt><dd className="break-all text-ink">{status.project.path}</dd>
            <dt className="text-muted">Project access</dt><dd className="text-ink">{status.project.readable ? status.project.access_level ? ROLES[status.project.access_level] ?? `Access level ${status.project.access_level}` : 'Readable · role not reported' : 'Could not verify project access'}</dd>
          </>}
          <dt className="text-muted">Token scopes</dt>
          <dd className="min-w-0 break-words text-ink">{status.token?.scopes ? status.token.scopes.length ? <span className="flex flex-wrap gap-x-3 gap-y-1">{status.token.scopes.map((scope) => <code key={scope} className="break-all font-mono text-caption">{scope}</code>)}</span> : 'No standard scopes reported' : 'Not available'}</dd>
          <dt className="text-muted">Expires</dt><dd className="text-ink">{status.token ? status.token.expires_at ? <time dateTime={status.token.expires_at}>{status.token.expires_at.includes('T') ? dateTime(status.token.expires_at) : status.token.expires_at}</time> : 'No expiry reported' : 'Not available'}</dd>
        </dl>
        {!status.token && <Note>GitLab did not return token details. This instance or credential may not expose them.</Note>}
        <Note>Token scopes and project roles describe your access. Branch rules still apply, and Git push uses your Git credentials.</Note>
      </>}
      <Note>Checked <time dateTime={status.checked_at}>{dateTime(status.checked_at)}</time>{busy && ' · Checking again…'}</Note>
    </div>}
    <div className="flex flex-col gap-1">
      <h3 className="text-ui font-medium text-ink">Automatic in GitLab projects</h3>
      <Note>Once glab is signed in, UAM limits supported lists to 10 results by default, up to 20, and checks changed GitLab CI files before a push. Select a project above to check its instance and access.</Note>
    </div>
  </div>;
}
