import { useEffect, useRef, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, errorCode, provider, type SessionSummary } from '../api';
import { modelChoices } from '../lib/models';
import { Note, useApp } from './common';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Select } from './ui/select';

/** A conversation prefix in the same folder. Confirmation starts no turn. */
export function ForkPicker({ session, userItemId, anchor, onClose, onForked }: Readonly<{
  session: SessionSummary;
  userItemId: string;
  anchor: HTMLElement | null;
  onClose: () => void;
  onForked: (session: SessionSummary) => void;
}>) {
  const api = useApi();
  const { meta, settings } = useApp();
  const info = provider(meta, session.provider);
  const choices = info ? modelChoices(info.models, settings.hidden_models?.[info.name], session.model) : [];
  const [model, setModel] = useState(session.model);
  const [requestId] = useState(() => crypto.randomUUID());
  const [attempted, setAttempted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [uncertain, setUncertain] = useState(false);
  const offered = !model || !!info?.models.some(m => m.id === model);
  const alive = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);
  const confirm = () => {
    if (busy || uncertain) return;
    setAttempted(true);
    setBusy(true);
    setError('');
    api.fork(session.id, { user_item_id: userItemId, model, request_id: requestId }).then(
      result => { if (alive.current) onForked(result); },
      (e: unknown) => {
        if (!alive.current) return;
        setError(describeError(e));
        setUncertain(errorCode(e) === 'fork_uncertain');
      },
    ).finally(() => { if (alive.current) setBusy(false); });
  };
  return (
    <Popover.Root open onOpenChange={open => { if (!open && !busy) onClose(); }} modal={false}>
      <Popover.Content anchor={anchor} className="w-80 max-w-[calc(100vw-16px)] max-h-(--available-height) gap-3 overflow-y-auto" finalFocus={() => anchor?.isConnected ? anchor : false}>
        <Popover.Title>Branch from here</Popover.Title>
        <Popover.Description>
          Copies the conversation through this reply into a new task. Both tasks use the same project and current files. No message is sent until you continue.
        </Popover.Description>
        <Select aria-label="Branch model" value={model} onValueChange={setModel} disabled={busy || attempted} items={[
          { value: '', label: 'Default' },
          ...choices.map(({ model: m, note }) => ({ value: m.id, label: m.name || m.id, disabled: note === 'Not offered now' })),
        ]} />
        {error && <Note tone="error" role="alert">{error}</Note>}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" onClick={confirm} loading={busy} disabled={uncertain || !offered}>Branch task</Button>
        </div>
      </Popover.Content>
    </Popover.Root>
  );
}
