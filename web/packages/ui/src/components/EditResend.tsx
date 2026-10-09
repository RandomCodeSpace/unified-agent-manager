import { Pencil, X } from 'lucide-react';
import type { Attachment, RewindMode, RewindPreview } from '../api';
import { Note } from './common';
import { Button } from './ui/button';
import { Segmented } from './ui/segmented';

/** A past owner prompt being edited in the composer. Opening or cancelling changes nothing but the draft. */
export interface Editing {
  sessionId: string;
  userItemId: string;
  /** The whole original prompt, read on demand. */
  text: string;
  attachments: Attachment[];
  time: string;
  onDone: () => void;
}

const clock = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

/** What sending the edit costs: the later prompts, and the files the agent edited since. */
function cost(preview: RewindPreview, mode: RewindMode) {
  const later = preview.turns - 1;
  const files = preview.files.files ?? 0;
  const parts = [later > 0 ? `Removes this prompt and the ${later} after it` : 'Removes this prompt and its reply'];
  if (files && mode === 'conversation-and-files') parts.push(`restores ${files} ${files === 1 ? 'file' : 'files'}`);
  if (files && mode === 'conversation') parts.push('files stay as they are');
  return parts.join(' · ');
}

/** The flat strip above the composer while a past prompt is edited (one strip at a time: the status line hides). */
export function EditStrip({ editing, preview, error, mode, onMode, disabled, note }: Readonly<{
  editing: Editing;
  preview: RewindPreview | null;
  error: string;
  mode: RewindMode;
  onMode: (mode: RewindMode) => void;
  disabled: boolean;
  note?: { tone: 'warn' | 'error' | 'muted'; text: string } | null;
}>) {
  const unsendable = editing.attachments.filter(a => !a.id).length;
  return (
    <div role="group" aria-label="Editing a past prompt" className="flex flex-col gap-1 px-3.5 pt-2 pb-1">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-caption text-muted">
        <Pencil aria-hidden="true" className="size-3.5 shrink-0 text-accent" />
        <span className="font-medium text-ink">Editing your prompt from {clock(editing.time)}</span>
        <span role="status" className="min-w-56 flex-1 max-sm:order-last max-sm:basis-full">{preview ? cost(preview, mode) : error ? '' : 'Reading what sending would rewind…'}</span>
        {preview?.files_available && <Segmented size="sm" aria-label="What to rewind" className="max-sm:order-last max-sm:basis-full" value={mode} onValueChange={v => onMode(v as RewindMode)} disabled={disabled} items={[{ value: 'conversation-and-files', label: 'Conversation and files' }, { value: 'conversation', label: 'Conversation only' }]} />}
        <Button size="icon-sm" aria-label="Stop editing" className="text-muted max-sm:ml-auto" disabled={disabled} onClick={editing.onDone}><X /></Button>
      </div>
      {error && <Note tone="error" role="alert">{error}</Note>}
      {preview && !preview.files_available && !!preview.files.files && <Note>Files stay as they are: this conversation cannot restore them.</Note>}
      {!!unsendable && <Note>{unsendable === 1 ? '1 attachment of the original prompt has no stored copy and' : `${unsendable} attachments of the original prompt have no stored copy and`} cannot be sent again.</Note>}
      {note && <Note tone={note.tone} role="alert">{note.text}</Note>}
      <Note>Nothing changes until you send. Your other draft comes back when you finish editing.</Note>
    </div>
  );
}
