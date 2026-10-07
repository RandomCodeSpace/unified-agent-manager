import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { modelCatalog, provider, type TaskDefaults } from '../api';
import { effortLabel, modelChoices } from '../lib/models';
import { MODE_TEXT, contextReason, effortReason, sizeLabel } from './Composer';
import { Note, useApp } from './common';
import { Select } from './ui/select';
import { HelpTip } from './ui/tooltip';

/** A model in a menu: its name, with the note ("hidden in Settings", "not offered now") in parentheses. */
export const choiceLabel = (name: string, note?: string): string => (note ? `${name} (${note.toLowerCase()})` : name);

/**
 * The one column system of Settings cards: a label column capped at 28rem, the control right after it (not at the
 * card's far edge), stacked on a phone. A card header with a control (`Section`'s `control`) uses it too.
 */
export const ROW_GRID = 'grid items-start gap-x-6 gap-y-2 sm:grid-cols-[minmax(0,28rem)_auto] sm:justify-start';

/** A Settings row: the label and its help in the label column, the control beside it, both centred on a 32px line. */
export function Row({ id, label, htmlFor, help, helpVisible = false, children }: Readonly<{ id: string; label: string; htmlFor?: string; help: ReactNode; helpVisible?: boolean; children: ReactNode }>) {
  const Label = htmlFor ? 'label' : 'span';
  return (
    <div className={ROW_GRID}>
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex min-h-8 items-center gap-1">
          <Label id={`${id}-label`} htmlFor={htmlFor} className="text-ui font-medium text-ink">
            {label}
          </Label>
          {!helpVisible && <HelpTip label={label} id={`${id}-help`}>{help}</HelpTip>}
        </div>
        {helpVisible && <Note id={`${id}-help`}>{help}</Note>}
      </div>
      <div className="flex min-h-8 min-w-0 flex-wrap items-center gap-2">{children}</div>
    </div>
  );
}

/** The header slot of the enclosing Settings card, where its Add button sits (DESIGN.md Settings view). */
export const SectionActionSlot = createContext<HTMLElement | null | undefined>(undefined);

/** Renders its children at the right of the enclosing Settings card's header (null until it mounts); in place outside a card. */
export function SectionAction({ children }: Readonly<{ children: ReactNode }>) {
  const slot = useContext(SectionActionSlot);
  if (slot === undefined) return children;
  return slot ? createPortal(children, slot) : null;
}

const FIELD = 'input, textarea, select, [role="combobox"]';

// The button clicked last (a keyboard press clicks too). Chrome blurs a focused button the moment it turns disabled,
// as a row's Edit does when its form opens, so focus alone cannot say what opened the form.
let lastClicked: HTMLElement | null = null;
document.addEventListener('click', (e) => { lastClicked = e.target instanceof Element ? e.target.closest('button') : null; }, true);

/**
 * An inline Add or Edit form in Settings: its first field takes focus when it opens, Escape cancels it (not while a
 * menu or tooltip of its own is open), and when it closes focus goes back to the button that opened it, or to the
 * card's Add button when that one was replaced. `open` is for a parent that keeps the hook while its form comes and goes.
 */
export function useInlineForm<T extends HTMLElement>(onCancel: () => void, open = true) {
  const ref = useRef<T>(null);
  const opener = useRef<HTMLElement | null>(null);
  const opened = useRef<HTMLElement | null>(null);
  const cancel = useRef(onCancel);
  useEffect(() => { cancel.current = onCancel; });
  useEffect(() => {
    const form = ref.current;
    if (!form) return;
    // Taken once per form element: the second StrictMode run must not take the first field for the opener.
    if (opened.current !== form) {
      opened.current = form;
      const active = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : lastClicked;
      opener.current = active?.isConnected && !form.contains(active) ? active : null;
    }
    const first = [...form.querySelectorAll<HTMLElement & { disabled?: boolean; type?: string }>(FIELD)].find((el) => !el.disabled && el.type !== 'hidden' && el.tabIndex >= 0 && el.getAttribute('aria-hidden') !== 'true');
    first?.focus();
    const card = form.closest('section');
    // A native listener: a menu's popup is portalled out of the form, so its own Escape never reaches here.
    const onKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if (e.key !== 'Escape' || e.defaultPrevented || target.getAttribute('aria-expanded') === 'true' || document.querySelector('[data-popup="tooltip"]')) return;
      e.preventDefault();
      cancel.current();
    };
    form.addEventListener('keydown', onKeyDown);
    return () => {
      form.removeEventListener('keydown', onKeyDown);
      window.setTimeout(() => {
        const back = opener.current;
        const target = back?.isConnected && !back.matches(':disabled') ? back : card?.querySelector<HTMLElement>('[data-section-add]:not(:disabled)');
        const active = document.activeElement;
        if (target && (!active || active === document.body || !active.isConnected)) target.focus();
      });
    };
  }, [open]);
  return ref;
}

const FieldHelpContext = createContext(false);

/** Settings use compact help; fields in project and task forms retain their inline guidance. */
export function FieldHelpProvider({ children }: Readonly<{ children: ReactNode }>) {
  return <FieldHelpContext value>{children}</FieldHelpContext>;
}

export function Field({ id, label, hint, hintVisible = false, hintId = `${id}-hint`, children }: Readonly<{ id: string; label: string; hint?: string; hintVisible?: boolean; hintId?: string; children: ReactNode }>) {
  const compactHelp = useContext(FieldHelpContext) && !hintVisible;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      {hint && compactHelp ? (
        <div className="flex min-w-0 items-center gap-1">
          <label htmlFor={id} className="text-caption text-muted">{label}</label>
          <HelpTip label={label} id={hintId}>{hint}</HelpTip>
        </div>
      ) : (
        <label htmlFor={id} className="text-caption text-muted">{label}</label>
      )}
      {children}
      {hint && !compactHelp && <Note id={hintId}>{hint}</Note>}
    </div>
  );
}

/** The settings a new Task starts with: model, effort, context size and mode (the New tasks section of Settings). */
export function TaskDefaultsFields({ prefix, value, disabled, onChange }: Readonly<{ prefix: string; value: TaskDefaults; disabled: boolean; onChange: (next: TaskDefaults) => void }>) {
  const { meta, settings } = useApp();
  const chosen = provider(meta, value.provider);
  const catalog = modelCatalog(meta, value.provider);
  const choices = modelChoices(catalog, settings.hidden_models?.[value.provider], value.model);
  const model = catalog.find((m) => m.id === value.model);
  const noEffort = effortReason(model);
  const noContext = contextReason(model, !!chosen?.capabilities.context_size);
  const sizes = model?.context_sizes ?? [];
  return (
    <div className="grid grid-cols-2 gap-x-3 gap-y-3 max-sm:grid-cols-1">
      {catalog.length > 0 && (
        <div className="col-span-full">
          <Field id={`${prefix}-model`} label="Model">
            <Select
              id={`${prefix}-model`}
              value={value.model}
              disabled={disabled}
              // No model yet (an approval's run, ADR 0006 §8): the trigger asks for one. Always listed, hidden, so
              // the select never loses the item it starts on.
              items={[{ value: '', label: 'Pick a model', hidden: true }, ...choices.map(({ model: m, note }) => ({ value: m.id, label: choiceLabel(m.name, note), hidden: !!note }))]}
              onValueChange={(id) => {
                const next = catalog.find((m) => m.id === id);
                onChange({
                  ...value,
                  model: id,
                  effort: next?.efforts?.includes(value.effort) ? value.effort : '',
                  context_size: next?.context_sizes?.some((s) => s.id === value.context_size) ? value.context_size : 'default',
                });
              }}
            />
          </Field>
        </div>
      )}
      <Field id={`${prefix}-effort`} label="Effort" hint={noEffort || undefined} hintVisible={!!noEffort}>
        <Select
          id={`${prefix}-effort`}
          value={value.effort}
          disabled={disabled || !!noEffort}
          aria-describedby={noEffort ? `${prefix}-effort-hint` : undefined}
          items={[{ value: '', label: 'Default' }, ...(model?.efforts ?? []).map((e) => ({ value: e, label: effortLabel(e) }))]}
          // Base UI resets the value to null when a new model's levels drop the one it held: that is the default effort.
          onValueChange={(effort) => onChange({ ...value, effort: effort ?? '' })}
        />
      </Field>
      <Field id={`${prefix}-context-size`} label="Context size" hintVisible={!!noContext} hint={noContext || (value.context_size === 'long_context' ? 'Long context may cost more.' : undefined)}>
        <Select
          id={`${prefix}-context-size`}
          value={value.context_size || 'default'}
          disabled={disabled || !!noContext}
          aria-describedby={noContext ? `${prefix}-context-size-hint` : undefined}
          items={[
            ...(sizes.some((s) => s.id === 'default') ? [] : [{ value: 'default', label: 'Default' }]),
            ...sizes.map((s) => ({ value: s.id, label: `${sizeLabel(s.id)} · ${s.tokens.toLocaleString()}` })),
          ]}
          onValueChange={(context_size) => onChange({ ...value, context_size })}
        />
      </Field>
      <div className="col-span-full">
        <Field id={`${prefix}-mode`} label="Mode" hint={MODE_TEXT[value.mode]}>
          <Select
            id={`${prefix}-mode`}
            value={value.mode}
            disabled={disabled}
            items={[
              { value: 'safe', label: 'Safe' },
              { value: 'yolo', label: 'Yolo' },
            ]}
            onValueChange={(mode) => onChange({ ...value, mode: mode as 'safe' | 'yolo' })}
          />
        </Field>
      </div>
    </div>
  );
}
