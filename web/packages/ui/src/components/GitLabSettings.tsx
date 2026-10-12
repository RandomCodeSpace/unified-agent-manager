import { Check, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { DEFAULT_MR_PROMPT, describeError, type GitLabStatus } from '../api';
import { dateTime, Note, Skeleton } from './common';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Switch } from './ui/switch';

const ROLES: Record<number, string> = { 5: 'Minimal access', 10: 'Guest', 15: 'Planner', 20: 'Reporter', 25: 'Security Manager', 30: 'Developer', 40: 'Maintainer', 50: 'Owner' };

/** What the merge request description prompt needs: the saved prompt ('' for the default) and how to save one. */
export interface MrPromptSetting { saved: string; saving: boolean; onSave: (prompt: string) => void }

export function GitLabSettings({ automatic, mrPrompt }: Readonly<{ automatic?: { on: boolean; saving: boolean; onChange: (on: boolean) => void }; mrPrompt?: MrPromptSetting }>) {
  const api = useApi();
  const [check, setCheck] = useState(0);
  const [result, setResult] = useState<{ client: typeof api; check: number; status?: GitLabStatus; error?: string }>();
  const current = result?.client === api ? result : undefined;
  const busy = current?.check !== check;
  const status = current?.status;

  useEffect(() => {
    let active = true;
    void api.gitlabStatus().then(
      (status) => { if (active) setResult({ client: api, check, status }); },
      (error: unknown) => { if (active) setResult({ client: api, check, error: describeError(error) }); },
    );
    return () => { active = false; };
  }, [api, check]);

  return <div className="flex min-w-0 flex-col gap-5">
    {automatic && <div className="flex items-start justify-between gap-4">
      <div className="flex min-w-0 flex-col gap-1">
        <h3 className="text-ui font-medium text-ink">Automatic investigation</h3>
        <Note id="gitlab-auto-description">Start a Utility-model task when a pipeline fails for a UAM commit. Off: choose Investigate in the task's GitLab panel.</Note>
      </div>
      <Switch aria-label="Automatic investigation" aria-describedby="gitlab-auto-description" checked={automatic.on} disabled={automatic.saving} onCheckedChange={automatic.onChange} className="mt-0.5" />
    </div>}
    {mrPrompt && <MrPrompt {...mrPrompt} />}
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h3 className="text-ui font-medium text-ink">Connection</h3>
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
      </>}
      <Note>Checked <time dateTime={status.checked_at}>{dateTime(status.checked_at)}</time>{busy && ' · Checking again…'}</Note>
    </div>}
  </div>;
}

/** The prompt the Utility model writes merge request descriptions with; empty means the default, shown as the placeholder. */
function MrPrompt({ saved, saving, onSave }: Readonly<MrPromptSetting>) {
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? saved;
  return <form className="flex min-w-0 flex-col gap-2" onSubmit={(e) => {
    e.preventDefault();
    if (!saving && value.trim() !== saved) onSave(value.trim());
  }}>
    <div className="flex min-w-0 flex-col gap-1">
      <h3 className="text-ui font-medium text-ink"><label htmlFor="gitlab-mr-prompt">Merge request description</label></h3>
      <Note id="gitlab-mr-prompt-help">The Utility model writes it when an agent runs glab mr create without one. Empty uses the default.</Note>
    </div>
    <textarea id="gitlab-mr-prompt" rows={8} maxLength={4000} spellCheck={false} aria-describedby="gitlab-mr-prompt-help" placeholder={DEFAULT_MR_PROMPT} value={value} readOnly={saving} onChange={(e) => setDraft(e.target.value)} className="w-full min-w-0 resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well placeholder:text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
    <div className="flex flex-wrap gap-2">
      <Button type="submit" variant="secondary" disabled={saving || value.trim() === saved}>Save</Button>
      <Button type="button" disabled={saving || (!saved && !value)} onClick={() => { setDraft(''); if (saved) onSave(''); }}>Reset to default</Button>
    </div>
  </form>;
}
