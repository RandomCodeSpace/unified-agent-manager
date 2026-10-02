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
 * Up to three replies the owner would likely send next, as buttons floating above the composer once a
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
  // Floats just above the composer's top edge, over the transcript's fading foot: out of the composer's flow, so
  // showing or hiding it never resizes the composer or moves the conversation. One row; many replies scroll
  // sideways in it. Only the chips take pointer events, so the conversation beneath still scrolls and clicks.
  // The padding holds each chip's shadow and enlarged touch target (sm), which the scroller would otherwise clip.
  return (
    <div role="group" aria-label="Suggested replies" data-suggestions="" className="pointer-events-none absolute inset-x-0 bottom-full flex gap-1.5 overflow-x-auto overscroll-x-contain px-1.5 py-2 animate-fade-in">
      {replies.map((r) => (
        <Button key={r} size="sm" variant="secondary" className="pointer-events-auto max-w-[min(20rem,75vw)] shrink-0 font-normal shadow-raised" title={r} onClick={() => onPick(r)}>
          <span className="truncate">{r}</span>
        </Button>
      ))}
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
