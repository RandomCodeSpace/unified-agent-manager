import { Check } from 'lucide-react';
import { useEffect, useId, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type DrawioCheck, type Settings } from '../api';
import { Note } from './common';
import { Row } from './TaskDefaults';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Segmented } from './ui/segmented';
import { Switch } from './ui/switch';

type Editor = 'excalidraw' | 'drawio';

/**
 * Settings → Diagram editor: Excalidraw (built in, the default) or a draw.io page at an origin
 * the server checks first. Enable waits for a check that passed for the origin in the field;
 * the server's errors and warnings show in their tones. `onProblem` feeds the rail's dot.
 */
export function DrawioSettings({ value, saving, onSave, onProblem }: Readonly<{ value: Settings['drawio']; saving: boolean; onSave: (drawio: NonNullable<Settings['drawio']>) => void; onProblem: (text: string | null) => void }>) {
  const api = useApi();
  const id = useId();
  const [editor, setEditor] = useState<Editor>(value?.enabled ? 'drawio' : 'excalidraw');
  const [origin, setOrigin] = useState(value?.origin ?? '');
  const [check, setCheck] = useState<{ origin: string; busy: boolean; result?: DrawioCheck; error?: string } | null>(null);
  // The server stores the origin lower-cased without a trailing slash: the check and Enable use that spelling.
  const trimmed = origin.trim().replace(/\/+$/, '').toLowerCase();
  const checked = check && !check.busy && check.origin === trimmed ? check : null;
  const ok = !!checked?.result?.ok;

  async function run() {
    setCheck({ origin: trimmed, busy: true });
    try {
      const result = await api.checkDrawio(trimmed);
      setCheck({ origin: trimmed, busy: false, result });
    } catch (err) {
      setCheck({ origin: trimmed, busy: false, error: describeError(err) });
    }
  }
  // An enabled origin is checked when the page opens, so the rail's dot can say when it stopped working.
  const enabled = !!value?.enabled && !!value.origin;
  useEffect(() => {
    if (!enabled) {
      onProblem(null);
      return;
    }
    let on = true;
    api.checkDrawio(value!.origin).then(
      (result) => on && onProblem(result.ok ? null : result.errors[0] ?? 'The draw.io origin failed its check'),
      (err: unknown) => on && onProblem(describeError(err)),
    );
    return () => {
      on = false;
    };
  }, [api, enabled, value, onProblem]);

  const choose = (next: Editor) => {
    setEditor(next);
    if (next === 'excalidraw' && value?.enabled) onSave({ origin: value.origin, enabled: false });
  };
  const detail = checked?.result?.detail;
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <Row id={`${id}-editor`} label="Editor" help="Excalidraw is built in and needs nothing else. draw.io runs a page from the origin you name, checked by the server before it can be turned on.">
        <Segmented aria-labelledby={`${id}-editor-label`} value={editor} onValueChange={(v) => choose(v as Editor)} items={[{ value: 'excalidraw', label: 'Excalidraw' }, { value: 'drawio', label: 'draw.io' }]} disabled={saving} />
      </Row>
      {editor === 'drawio' && (
        <form className="flex min-w-0 flex-col gap-3" onSubmit={(e) => { e.preventDefault(); if (trimmed) void run(); }}>
          <div className="flex flex-wrap items-end gap-2">
            <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1">
              <label htmlFor={`${id}-origin`} className="text-ui font-medium text-ink">Origin</label>
              <Input id={`${id}-origin`} value={origin} placeholder="https://embed.diagrams.net" spellCheck={false} autoComplete="off" inputMode="url" onChange={(e) => setOrigin(e.target.value)} />
            </div>
            <Button type="submit" variant="secondary" size="md" loading={!!check?.busy} disabled={!trimmed}>Check</Button>
          </div>
          {checked?.error && <Note tone="error" role="alert">{checked.error} Try Check again.</Note>}
          {checked?.result?.errors.map((e) => <Note key={e} tone="error" role="alert">{e}</Note>)}
          {checked?.result?.warnings.map((w) => <Note key={w} tone="warn">{w}</Note>)}
          {detail && (
            <p className="flex flex-wrap items-center gap-2 text-caption text-muted" role="status">
              {ok && <Check aria-hidden="true" className="size-3.5 text-success" />}
              {[detail.public ? 'Public embed' : 'Self-hosted', detail.cors ? 'CORS allowed' : 'No CORS', detail.framing_ok ? 'Framing allowed' : 'Framing blocked'].join(' · ')}
            </p>
          )}
          <div className="flex items-start justify-between gap-4">
            <div className="flex min-w-0 flex-col gap-1">
              <span className="text-ui font-medium text-ink">Enable</span>
              <Note id={`${id}-enable-help`}>{ok ? 'Drawings open in draw.io at this origin.' : 'Check the origin first; Enable waits for a check that passed.'}</Note>
            </div>
            <Switch aria-label="Enable draw.io" aria-describedby={`${id}-enable-help`} checked={!!value?.enabled && value.origin === trimmed} disabled={saving || !ok} onCheckedChange={(on) => onSave({ origin: trimmed, enabled: on })} className="mt-0.5" />
          </div>
        </form>
      )}
    </div>
  );
}
