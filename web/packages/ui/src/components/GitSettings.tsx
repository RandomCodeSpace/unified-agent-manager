import { useState } from 'react';
import { DEFAULT_COMMIT_PROMPT, DEFAULT_MR_PROMPT, type Settings } from '../api';
import { Note } from './common';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Switch } from './ui/switch';

type GitPatch = Pick<Settings, 'commit_prompt' | 'gitlab_mr_prompt' | 'coauthor'>;

/** Agents' commit and pull or merge request instructions, and the co-author trailer of their commits. */
export function GitSettings({ settings, saving, onSave }: Readonly<{ settings: Settings; saving: boolean; onSave: (patch: GitPatch) => void }>) {
  const coauthor = settings.coauthor ?? { enabled: false, name: '' };
  return <div className="flex min-w-0 flex-col gap-5">
    <Instructions id="git-commit-prompt" label="Commit messages" help="Empty uses the default, Conventional Commits." placeholder={DEFAULT_COMMIT_PROMPT} saved={settings.commit_prompt ?? ''} saving={saving} onSave={(commit_prompt) => onSave({ commit_prompt })} />
    <Instructions id="git-mr-prompt" label="Pull and merge requests" help="Also what the Utility model writes a description with when an agent runs glab mr create without one. Empty uses the default." placeholder={DEFAULT_MR_PROMPT} saved={settings.gitlab_mr_prompt ?? ''} saving={saving} onSave={(gitlab_mr_prompt) => onSave({ gitlab_mr_prompt })} />
    <div className="flex items-start justify-between gap-4">
      <div className="flex min-w-0 flex-col gap-1">
        <h3 className="text-ui font-medium text-ink">Co-author</h3>
        <Note id="git-coauthor-description">Add a Co-authored-by trailer to agents' commits. Off: commits carry no AI credit.</Note>
      </div>
      <Switch aria-label="Co-author" aria-describedby="git-coauthor-description" checked={coauthor.enabled} disabled={saving} onCheckedChange={(enabled) => onSave({ coauthor: { ...coauthor, enabled } })} className="mt-0.5" />
    </div>
    {coauthor.enabled && <CoAuthorName saved={coauthor.name ?? ''} saving={saving} onSave={(name) => onSave({ coauthor: { enabled: true, name } })} />}
  </div>;
}

/** Instructions saved as typed; empty means the default, shown as the placeholder. */
function Instructions({ id, label, help, placeholder, saved, saving, onSave }: Readonly<{ id: string; label: string; help: string; placeholder: string; saved: string; saving: boolean; onSave: (text: string) => void }>) {
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? saved;
  return <form className="flex min-w-0 flex-col gap-2" onSubmit={(e) => {
    e.preventDefault();
    if (!saving && value.trim() !== saved) onSave(value.trim());
  }}>
    <div className="flex min-w-0 flex-col gap-1">
      <h3 className="text-ui font-medium text-ink"><label htmlFor={id}>{label}</label></h3>
      <Note id={`${id}-help`}>{help}</Note>
    </div>
    <textarea id={id} rows={6} maxLength={4000} spellCheck={false} aria-describedby={`${id}-help`} placeholder={placeholder} value={value} readOnly={saving} onChange={(e) => setDraft(e.target.value)} className="w-full min-w-0 resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well placeholder:text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
    <div className="flex flex-wrap gap-2">
      <Button type="submit" variant="secondary" disabled={saving || value.trim() === saved}>Save</Button>
      <Button type="button" disabled={saving || (!saved && !value)} onClick={() => { setDraft(''); if (saved) onSave(''); }}>Reset to default</Button>
    </div>
  </form>;
}

/** The trailer's value; empty lets Copilot name the co-author. */
function CoAuthorName({ saved, saving, onSave }: Readonly<{ saved: string; saving: boolean; onSave: (name: string) => void }>) {
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? saved;
  return <form className="flex min-w-0 flex-col gap-2" onSubmit={(e) => {
    e.preventDefault();
    if (!saving && value.trim() !== saved) onSave(value.trim());
  }}>
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor="git-coauthor-name" className="text-ui font-medium text-ink">Co-author name</label>
      <Note id="git-coauthor-name-help">Name and email, as git writes them. Empty lets Copilot name the co-author.</Note>
    </div>
    <div className="flex min-w-0 flex-wrap gap-2">
      <Input id="git-coauthor-name" maxLength={200} spellCheck={false} autoComplete="off" aria-describedby="git-coauthor-name-help" placeholder="Name <name@example.com>" value={value} readOnly={saving} onChange={(e) => setDraft(e.target.value)} className="max-w-md flex-1" />
      <Button type="submit" variant="secondary" size="lg" disabled={saving || value.trim() === saved}>Save</Button>
    </div>
  </form>;
}
