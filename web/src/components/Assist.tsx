import { ArrowRight } from 'lucide-react';
import { useEffect, useState } from 'react';
import { api, describeError, provider, type SessionDetail, type SessionSummary } from '../api';
import { suggestionKey } from '../lib/assist';
import { modelChoices } from '../lib/models';
import { Note, useApp } from './common';
import { Button } from './ui/button';
import { Dialog } from './ui/dialog';
import { Select } from './ui/select';

/* ---------- Suggested replies ---------- */

/** The replies fetched for each Task, by the item its transcript ended with, so a remount never asks again. */
const suggested = new Map<string, { key: string; replies: string[] }>();

/**
 * The reply the owner would likely send next, once a turn has completed with an answer: the first of the
 * replies the service suggests, or '' while there is none. The service asks the Utility model once per
 * finished turn, only when a composer would show it (`hidden` false); Settings → Composer turns it off.
 */
export function useSuggestion(session: SessionDetail, hidden: boolean): string {
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
  return !hidden && key && cached?.key === key ? (cached.replies[0] ?? '') : '';
}

/** The suggestion's accessible description, which the composer's textarea points at. */
export const SUGGESTION_ID = 'composer-suggestion';

/**
 * The suggestion as ghost text in the empty composer, where its placeholder would be: muted, in the
 * textarea's font and padding, over the textarea and out of its flow, so it never changes the composer's
 * height (two lines, then an ellipsis). Right Arrow or End in the empty composer uses it (Composer.tsx);
 * on a touch screen, which has neither key, the arrow button at its end does. Typed text replaces it.
 */
export function SuggestionGhost({ text, onUse }: Readonly<{ text: string; onUse: () => void }>) {
  return (
    <div className="pointer-events-none absolute inset-x-0 top-0 flex items-start gap-1 px-3.5 pt-3 animate-fade-in pointer-coarse:pr-1.5">
      <span aria-hidden="true" className="line-clamp-2 min-w-0 flex-1 text-chat text-muted max-sm:text-chat-lg">
        {text}
      </span>
      <span id={SUGGESTION_ID} className="sr-only">
        Suggestion: {text}, press Right Arrow to use it
      </span>
      <Button size="icon" variant="subtle" aria-label="Use suggestion" className="pointer-events-auto -mt-0.5 hidden shrink-0 text-muted pointer-coarse:inline-flex" onClick={onUse}>
        <ArrowRight />
      </Button>
    </div>
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
