import { BookmarkPlus, MessageSquareText, Pencil, Sparkles, Trash2 } from 'lucide-react';
import { useEffect, useState, type SubmitEvent } from 'react';
import { api, describeError, provider, type Project, type SavedPrompt, type SessionDetail, type SessionSummary } from '../api';
import { promptsFor, suggestionKey } from '../lib/assist';
import { modelChoices } from '../lib/models';
import { Note, useApp } from './common';
import { Button } from './ui/button';
import { AlertDialog, Dialog, useConfirm } from './ui/dialog';
import { Input } from './ui/input';
import { itemClass } from './ui/menu';
import { Popover } from './ui/popover';
import { Segmented } from './ui/segmented';
import { Select } from './ui/select';
import { Tip } from './ui/tooltip';

/** Focuses a field once, when it mounts (a stable callback ref, so a re-render never refocuses). */
const focusOnMount = (el: HTMLElement | null) => el?.focus();

/* ---------- Suggested replies ---------- */

/** The replies fetched for each Task, by the item its transcript ended with, so a remount never asks again. */
const suggested = new Map<string, { key: string; replies: string[] }>();

/**
 * Up to three replies the owner would likely send next, as buttons above the composer's text once a
 * turn has completed. One fills the composer and sends nothing. The service asks the Utility model
 * once per finished turn, only when a composer shows it; Settings → Composer turns them off.
 */
export function SuggestedReplies({ session, hidden, onPick }: Readonly<{ session: SessionDetail; hidden: boolean; onPick: (text: string) => void }>) {
  const { settings } = useApp();
  const key = settings.suggest_replies === false ? '' : suggestionKey(session, session.recent_items ?? session.items);
  const [, setFetched] = useState(0);
  useEffect(() => {
    if (!key || hidden || suggested.get(session.id)?.key === key) return;
    const controller = new AbortController();
    api.suggestions(session.id, controller.signal).then(
      (r) => {
        // Replies the service did not keep (Background AI paused or off) are asked for again on the next look.
        if (!r.item_id) return;
        suggested.set(session.id, { key, replies: r.replies });
        setFetched((n) => n + 1);
      },
      () => {},
    );
    return () => controller.abort();
  }, [session.id, key, hidden]);
  const cached = suggested.get(session.id);
  const replies = key && cached?.key === key ? cached.replies : [];
  if (hidden || replies.length === 0) return null;
  return (
    <div role="group" aria-label="Suggested replies" className="flex min-w-0 flex-wrap items-center gap-1.5 px-3.5 pt-3 animate-fade-in">
      <Sparkles aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
      {replies.map((r) => (
        <Button key={r} size="sm" variant="secondary" className="min-w-0 max-w-full font-normal" title={r} onClick={() => onPick(r)}>
          <span className="truncate">{r}</span>
        </Button>
      ))}
    </div>
  );
}

/* ---------- Saved prompts ---------- */

type Scope = 'project' | 'all';

/**
 * The composer's saved prompts: search them by name and insert one at the caret, or save the
 * composer's text as a new one, for this Project or for every Project. The list follows the
 * owner across browsers (Settings).
 */
export function SavedPrompts({ projectId, text, onInsert }: Readonly<{ projectId: string; text: string; onInsert: (text: string) => void }>) {
  const { settings } = useApp();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [draft, setDraft] = useState<{ name: string; scope: Scope } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const all = promptsFor(settings.saved_prompts, projectId, '');
  const shown = promptsFor(settings.saved_prompts, projectId, query);

  function toggle(next: boolean) {
    setOpen(next);
    if (next) {
      setQuery('');
      setDraft(null);
      setError('');
    }
  }
  async function save(e: SubmitEvent) {
    e.preventDefault();
    // The popover is portaled, but React still bubbles this submit to the composer's form, which would send.
    e.stopPropagation();
    if (!draft) return;
    setBusy(true);
    setError('');
    try {
      await api.addPrompt({ name: draft.name.trim(), text, ...(draft.scope === 'project' && projectId ? { project_id: projectId } : {}) });
      setDraft(null);
    } catch (err) {
      setError(`Could not save the prompt: ${describeError(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Popover.Root open={open} onOpenChange={toggle}>
      <Tip label="Saved prompts">
        <Popover.Trigger render={<Button id="composer-prompts" size="icon" variant="subtle" aria-label="Saved prompts" className="text-muted" />}>
          <MessageSquareText />
        </Popover.Trigger>
      </Tip>
      <Popover.Content className="w-80 max-w-[calc(100vw-16px)] gap-2 py-3">
        <Popover.Title>Saved prompts</Popover.Title>
        {draft ? (
          <form className="flex flex-col gap-2" onSubmit={(e) => void save(e)}>
            <Input size="md" aria-label="Prompt name" placeholder="Name" maxLength={80} ref={focusOnMount} value={draft.name} disabled={busy} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
            {projectId && (
              <Segmented
                aria-label="Offer it in"
                value={draft.scope}
                disabled={busy}
                onValueChange={(v) => setDraft({ ...draft, scope: v as Scope })}
                items={[
                  { value: 'project', label: 'This project' },
                  { value: 'all', label: 'All projects' },
                ]}
              />
            )}
            <p className="line-clamp-3 rounded-sm bg-sunken px-2 py-1.5 text-caption whitespace-pre-wrap text-body">{text}</p>
            {error && <Note tone="error" role="alert">{error}</Note>}
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setDraft(null)}>Back</Button>
              <Button type="submit" size="sm" variant="primary" loading={busy} disabled={!draft.name.trim()}>Save prompt</Button>
            </div>
          </form>
        ) : (
          <>
            {all.length > 0 && <Input size="md" aria-label="Search saved prompts by name" placeholder="Search by name" ref={focusOnMount} value={query} onChange={(e) => setQuery(e.target.value)} />}
            {all.length === 0 && <Popover.Description>No saved prompts yet. Write a message, then save it here to use it again.</Popover.Description>}
            {all.length > 0 && shown.length === 0 && <Popover.Description>No saved prompt is named like that.</Popover.Description>}
            {shown.length > 0 && (
              <ul className="-mx-1 flex max-h-64 flex-col overflow-y-auto">
                {shown.map((p) => (
                  <li key={p.id}>
                    <button
                      type="button"
                      className={`${itemClass} flex-col items-start gap-0 text-left hover:bg-tint-hover focus-visible:bg-tint-hover`}
                      onClick={() => {
                        onInsert(p.text);
                        setOpen(false);
                      }}
                    >
                      <span className="w-full truncate text-ink">{p.name}</span>
                      <span className="w-full truncate text-caption text-muted">{p.text}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <Tip label={text.trim() ? '' : 'Write a message first'}>
              <Button size="sm" variant="secondary" className="self-start" aria-disabled={text.trim() ? undefined : 'true'} onClick={() => text.trim() && setDraft({ name: '', scope: projectId ? 'project' : 'all' })}>
                <BookmarkPlus />
                Save as prompt
              </Button>
            </Tip>
          </>
        )}
      </Popover.Content>
    </Popover.Root>
  );
}

/** Settings → Saved prompts: every saved prompt with where it is offered, to rename or delete. */
export function SavedPromptsSettings({ prompts, projects }: Readonly<{ prompts: SavedPrompt[]; projects: Project[] }>) {
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const removal = useConfirm<SavedPrompt>();
  async function run(id: string, op: () => Promise<unknown>, verb: string) {
    setBusy(id);
    setError('');
    try {
      await op();
      return true;
    } catch (e) {
      setError(`Could not ${verb}: ${describeError(e)}`);
      return false;
    } finally {
      setBusy('');
    }
  }
  const where = (p: SavedPrompt) => (p.project_id ? projects.find((x) => x.id === p.project_id)?.name ?? 'A removed project' : 'All projects');
  return (
    <>
      <Note>Save a message from a composer's Saved prompts button; it is offered there, by name, in every browser.</Note>
      {error && <Note tone="error" role="alert">{error}</Note>}
      {prompts.length === 0 && <p className="text-ui text-muted">No saved prompts yet.</p>}
      {prompts.length > 0 && (
        <ul className="flex flex-col">
          {prompts.map((p) => (
            <li key={p.id} className="flex min-h-12 items-center gap-3 py-2">
              {editing?.id === p.id ? (
                <form
                  className="flex min-w-0 flex-1 items-center gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void run(p.id, () => api.renamePrompt(p.id, editing.name.trim()), 'rename the prompt').then((ok) => ok && setEditing(null));
                  }}
                >
                  <Input size="md" aria-label={`New name for ${p.name}`} maxLength={80} ref={focusOnMount} value={editing.name} disabled={busy === p.id} onChange={(e) => setEditing({ id: p.id, name: e.target.value })} onKeyDown={(e) => e.key === 'Escape' && setEditing(null)} />
                  <Button type="submit" size="sm" variant="secondary" loading={busy === p.id} disabled={!editing.name.trim()}>Save</Button>
                  <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
                </form>
              ) : (
                <>
                  <div className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-ui font-medium text-ink">{p.name}</span>
                    <span className="truncate text-meta text-muted">{where(p)} · {p.text}</span>
                  </div>
                  <Tip label="Rename">
                    <Button size="icon" variant="subtle" aria-label={`Rename ${p.name}`} className="text-muted" disabled={!!busy} onClick={() => setEditing({ id: p.id, name: p.name })}>
                      <Pencil />
                    </Button>
                  </Tip>
                  <Tip label="Delete">
                    <Button size="icon" variant="subtle" aria-label={`Delete ${p.name}`} className="text-muted" disabled={!!busy} onClick={() => removal.ask(p)}>
                      <Trash2 />
                    </Button>
                  </Tip>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      {(removal.target || removal.props.open) && (
        <AlertDialog
          {...removal.props}
          title={`Delete prompt “${removal.target?.name ?? ''}”?`}
          description="It leaves the Saved prompts list in every browser. Its text is not kept."
          confirmLabel="Delete"
          onConfirm={() => {
            const p = removal.target;
            removal.close();
            if (p) void run(p.id, () => api.deletePrompt(p.id), 'delete the prompt');
          }}
        />
      )}
    </>
  );
}

/* ---------- Try with another model ---------- */

/**
 * Picks the model a Task's last message runs again on, in a new Task with its other settings.
 * Open while `session` is set; `onRun` starts the Task, and the dialog closes once it has.
 */
export function TryModelDialog({ session, onClose, onRun }: Readonly<{ session: SessionSummary | null; onClose: () => void; onRun: (model: string) => Promise<void> }>) {
  const { meta, settings } = useApp();
  // The last Task shown stays through the exit transition, so the copy never changes on screen.
  const [shown, setShown] = useState(session);
  if (session && session !== shown) setShown(session);
  const [model, setModel] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const info = shown ? provider(meta, shown.provider) : undefined;
  const choices = shown && info ? modelChoices(info.models, settings.hidden_models?.[info.name], '').filter((c) => c.model.id !== shown.model) : [];
  const chosen = model || choices[0]?.model.id || '';
  return (
    <Dialog
      open={!!session}
      onOpenChange={(o) => !o && onClose()}
      onClosed={() => {
        setModel('');
        setError('');
      }}
      title="Try with another model"
      description="Starts a new task in the same project with this task's last message and settings, on the model you choose. Attachments are not sent again."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!chosen}
            onClick={() => {
              setBusy(true);
              setError('');
              onRun(chosen).then(onClose, (e: unknown) => setError(describeError(e))).finally(() => setBusy(false));
            }}
          >
            Start task
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-2">
        {choices.length === 0 ? (
          <Note>No other model is offered.</Note>
        ) : (
          <Select aria-label="Model" value={chosen} onValueChange={setModel} items={choices.map(({ model: m }) => ({ value: m.id, label: m.name || m.id }))} />
        )}
        {error && <Note tone="error" role="alert">{error}</Note>}
      </div>
    </Dialog>
  );
}
