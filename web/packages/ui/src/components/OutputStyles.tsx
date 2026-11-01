import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import { Check, Plus, Trash2 } from 'lucide-react';
import { useRef, useState } from 'react';
import { PROVIDER_STYLE, type OutputStyle, type Settings } from '../api';
import { Note } from './common';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Select, type SelectOption } from './ui/select';

/** Mirrors the service's limits (output_style.go). */
const MAX_TEXT = 2000;
const MAX_NAME = 40;
const MAX_STYLES = 20;
const PROVIDER_LABEL = "Copilot's own";

const firstLine = (text: string) => text.split('\n', 1)[0];

/** The styles on offer: the built-in ones (from meta) first, then the owner's library. */
const offered = (settings: Settings, builtin: readonly OutputStyle[]) => [...builtin, ...(settings.output_styles ?? [])];

/** Select items for a Project's choice: the default (named), the provider's own, then the styles on offer. A name no longer offered stays, marked. */
export function styleItems(settings: Settings, chosen: string, builtin: readonly OutputStyle[] = []): SelectOption[] {
  const lib = offered(settings, builtin);
  return [
    { value: '', label: `Default (${settings.output_style || PROVIDER_LABEL})` },
    { value: PROVIDER_STYLE, label: PROVIDER_LABEL },
    ...(chosen && chosen !== PROVIDER_STYLE && !lib.some((s) => s.name === chosen) ? [{ value: chosen, label: `${chosen} (not in the library)`, disabled: true }] : []),
    ...lib.map((s) => ({ value: s.name, label: s.name, description: firstLine(s.text) })),
  ];
}

/**
 * A Thread's output style, as the Tools panel's Style tab: picked before the first message (its Project's,
 * else the default, unless the owner picks one here), then fixed for the Thread's life.
 */
export function StyleChoices({ settings, builtin = [], chosen, reason, onChange }: Readonly<{ settings: Settings; builtin?: readonly OutputStyle[]; chosen: string; reason?: string; onChange: (style: string) => void }>) {
  const lib = offered(settings, builtin);
  const missing = chosen && chosen !== PROVIDER_STYLE && !lib.some((s) => s.name === chosen);
  const rows = [
    { id: '', name: 'Project or default', description: `The Project's style, else the default (${settings.output_style || PROVIDER_LABEL}).`, disabled: false },
    { id: PROVIDER_STYLE, name: PROVIDER_LABEL, description: '', disabled: false },
    ...(missing ? [{ id: chosen, name: chosen, description: `No longer in the library: ${PROVIDER_LABEL} applies.`, disabled: true }] : []),
    ...lib.map((s) => ({ id: s.name, name: s.name, description: firstLine(s.text), disabled: false })),
  ];
  return (
    <div className="flex flex-col gap-1.5">
      <RadioGroup aria-label="Output style" value={chosen} disabled={!!reason} onValueChange={(v) => v !== chosen && onChange(v as string)} className="-mx-2 flex flex-col">
        {rows.map((r) => (
          <Radio.Root key={r.id} value={r.id} disabled={r.disabled} className="group/style flex min-h-8 w-full cursor-default items-start gap-2 rounded-sm px-2 py-1.5 text-left outline-hidden not-data-disabled:hover:bg-tint-hover focus-visible:bg-tint-hover data-disabled:opacity-45 pointer-coarse:min-h-11">
            <Check aria-hidden="true" strokeWidth={2.5} className="invisible mt-0.5 size-3.5 shrink-0 text-accent group-data-checked/style:visible" />
            <span className="flex min-w-0 flex-col">
              <span className="text-ui text-ink">{r.name}</span>
              {r.description && <span className="line-clamp-2 text-caption text-muted">{r.description}</span>}
            </span>
          </Radio.Root>
        ))}
      </RadioGroup>
      <p className="text-caption text-muted">{reason ?? 'Fixed when the Thread starts. The Project default is in Edit project.'}</p>
    </div>
  );
}

/** Settings: the style library, saved as a whole, and the default style, saved on change. */
export function OutputStyleSettings({ settings, builtin = [], saving, onSave }: Readonly<{ settings: Settings; builtin?: readonly OutputStyle[]; saving: boolean; onSave: (patch: Pick<Settings, 'output_styles' | 'output_style'>) => void }>) {
  const saved = settings.output_styles ?? [];
  // Each row keeps its key while rows are added or removed: saved ones count down from -1, added ones up from 0.
  const added = useRef(0);
  const [draft, setDraft] = useState<(OutputStyle & { key: number })[] | null>(null);
  const rows = draft ?? saved.map((s, i) => ({ ...s, key: -1 - i }));
  const set = (i: number, patch: Partial<OutputStyle>) => setDraft(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const names = rows.map((r) => r.name.trim().toLowerCase());
  const reserved = [PROVIDER_STYLE, ...builtin.map((b) => b.name)].map((n) => n.toLowerCase());
  const invalid = rows.some((r, i) => !r.name.trim() || !r.text.trim() || names.indexOf(names[i]) !== i || reserved.includes(names[i]));
  const trimmed = rows.map((r) => ({ name: r.name.trim(), text: r.text.trim() }));
  const changed = draft !== null && JSON.stringify(trimmed) !== JSON.stringify(saved);
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h3 className="text-ui font-medium text-ink"><label htmlFor="output-style-default">Default style</label></h3>
        <Note id="output-style-default-help">What Threads use unless their Project or the Thread picks another.</Note>
        <Select id="output-style-default" aria-describedby="output-style-default-help" className="mt-1 max-w-sm" disabled={saving} value={settings.output_style ?? ''}
          items={[{ value: '', label: PROVIDER_LABEL }, ...offered(settings, builtin).map((s) => ({ value: s.name, label: s.name }))]}
          onValueChange={(output_style) => output_style !== (settings.output_style ?? '') && onSave({ output_style })} />
      </div>
      {builtin.length > 0 && <div className="flex min-w-0 flex-col gap-2">
        <h3 className="text-ui font-medium text-ink">Built in</h3>
        {builtin.map((b) => (
          <details key={b.name} className="rounded-sm bg-tint-well px-2.5 py-2">
            <summary className="cursor-default text-ui text-ink">{b.name}</summary>
            <p className="mt-2 text-caption whitespace-pre-line text-muted">{b.text}</p>
          </details>
        ))}
      </div>}
      <form className="flex min-w-0 flex-col gap-3" onSubmit={(e) => {
        e.preventDefault();
        if (!saving && changed && !invalid) onSave({ output_styles: trimmed });
      }}>
        <h3 className="text-ui font-medium text-ink">Library</h3>
        {rows.length === 0 && <Note>No styles yet. Add one to pick it here, in a Project or in a Thread's Tools.</Note>}
        {rows.map((r, i) => (
          <fieldset key={r.key} className="flex min-w-0 flex-col gap-2">
            <legend className="sr-only">Style {i + 1}</legend>
            <div className="flex items-center gap-2">
              <Input aria-label="Style name" className="min-w-0 flex-1" maxLength={MAX_NAME} placeholder="Name" value={r.name} readOnly={saving} onChange={(e) => set(i, { name: e.target.value })} />
              <Button size="icon-md" variant="subtle" aria-label={`Remove ${r.name || 'style'}`} disabled={saving} onClick={() => setDraft(rows.filter((_, j) => j !== i))}><Trash2 aria-hidden="true" className="text-muted" /></Button>
            </div>
            <textarea aria-label="Style text" rows={4} maxLength={MAX_TEXT} spellCheck={false} placeholder="Terse. Lead with the answer, then the evidence. No filler." value={r.text} readOnly={saving} onChange={(e) => set(i, { text: e.target.value })} className="w-full min-w-0 resize-y rounded-sm bg-sunken px-2.5 py-2 text-ui text-ink shadow-well placeholder:text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
          </fieldset>
        ))}
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" disabled={saving || rows.length >= MAX_STYLES} onClick={() => setDraft([...rows, { name: '', text: '', key: added.current++ }])}><Plus aria-hidden="true" />Add style</Button>
          <Button type="submit" variant="primary" loading={saving} disabled={!changed || invalid}>Save library</Button>
          {changed && <Button variant="subtle" disabled={saving} onClick={() => setDraft(null)}>Discard</Button>}
        </div>
        {changed && invalid && <Note tone="error">Each style needs a unique name and some text; "{PROVIDER_STYLE}" and built-in names are reserved.</Note>}
      </form>
    </div>
  );
}
