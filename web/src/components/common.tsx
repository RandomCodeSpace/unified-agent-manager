import { createContext, useContext, useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { LIVE, describeError, taskName, type Meta, type SessionState, type SessionSummary } from '../api';
import type { Action } from '../state';

export const STATE_LABELS: Record<SessionState, string> = {
  idle: 'Idle',
  starting: 'Starting',
  working: 'Working',
  awaiting_permission: 'Needs permission',
  awaiting_answer: 'Needs answer',
  completed: 'Completed',
  cancelled: 'Cancelled',
  failed: 'Failed',
  interrupted: 'Interrupted',
  closed: 'Closed',
};

/** States drawn as a glyph rather than a dot (DESIGN.md, State marks). */
const STATE_GLYPH: Partial<Record<SessionState, string>> = {
  completed: '✓',
  failed: '✕',
  cancelled: '–',
  interrupted: '‖',
};

/** Chip tone per state; only the attention chip has a fill. */
const STATE_TONE: Partial<Record<SessionState, string>> = {
  awaiting_permission: 'attention',
  awaiting_answer: 'attention',
  completed: 'success',
  failed: 'error',
  interrupted: 'warning',
};

export const INTERRUPTED_TEXT = 'UAM stopped while this turn was running; it was not resumed or resent.';

/** Values shared by most of the tree; avoids threading meta/dispatch through every layer. */
export interface AppContextValue {
  meta: Meta | null;
  dispatch: (a: Action) => void;
  narrow: boolean;
  /** Tasks with activity the user has not looked at yet (UI-local). */
  hasNews: (s: SessionSummary) => boolean;
}

export const AppContext = createContext<AppContextValue>({
  meta: null,
  dispatch: () => {},
  narrow: false,
  hasNews: () => false,
});

export const useApp = () => useContext(AppContext);

/** True while the media query matches; follows changes. */
export function useMedia(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const m = window.matchMedia(query);
    const on = () => setMatches(m.matches);
    on();
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, [query]);
  return matches;
}

/**
 * State mark: an 8px dot or a glyph per state, always with a text label. With `label` the
 * mark is a chip carrying the word; without it the word is only for assistive tech.
 */
export function StateMark({ state, label = true, title }: { state: SessionState; label?: boolean; title?: string }) {
  const text = STATE_LABELS[state] ?? state;
  const glyph = STATE_GLYPH[state];
  const mark = glyph ? (
    <span className="mark-glyph" aria-hidden="true">
      {glyph}
    </span>
  ) : (
    <span className="mark-dot" aria-hidden="true" />
  );
  if (!label) {
    return (
      <span className={`mark mark-${state}`} title={title}>
        {mark}
        <span className="sr-only">{text}</span>
      </span>
    );
  }
  const tone = STATE_TONE[state];
  return (
    <span className={`chip mark mark-${state}${tone ? ` chip-${tone}` : ''}`} title={title}>
      {mark}
      <span className="mark-text">{text}</span>
    </span>
  );
}

export function Spinner() {
  return <span className="spinner" aria-hidden="true" />;
}

/** Display name: name, else provider title, else a placeholder that says whether a title is on its way. */
export function TaskTitle({ session, className }: { session: SessionSummary; className?: string }) {
  const name = taskName(session);
  if (name) return <span className={className}>{name}</span>;
  const waiting = LIVE.includes(session.state);
  return <span className={`title-pending ${className ?? ''}`}>{waiting ? 'Waiting for a title…' : 'New task'}</span>;
}

/** Visually hidden separator so adjacent labels do not run together in accessible names. */
export function Sep() {
  return <span className="sr-only">, </span>;
}

export function relTime(iso: string, now = Date.now()): string {
  const ms = now - new Date(iso).getTime();
  const m = Math.round(ms / 60000);
  if (m < 1) return 'now';
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

/** Native modal dialog: Esc closes, focus is trapped, focus returns on close. */
export function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  // No cleanup on purpose: closing here would fire `close` → onClose during StrictMode's
  // simulated unmount and drop the dialog. Removing the element closes it anyway.
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal();
  }, []);
  return (
    <dialog ref={ref} className="dialog" aria-labelledby={titleId} onClose={onClose}>
      <h2 id={titleId} className="display-sm dialog-title">
        {title}
      </h2>
      {children}
    </dialog>
  );
}

/** Confirmation with the safe action focused first. `onConfirm` may throw; the message is shown in place. */
export function ConfirmDialog({
  title,
  confirmLabel,
  danger = true,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => Promise<void>;
  onClose: () => void;
  children: ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await onConfirm();
      onClose();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog title={title} onClose={onClose}>
      {children}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="actions">
        <button type="button" className="btn btn-secondary" onClick={onClose} autoFocus>
          Cancel
        </button>
        <button type="button" className={danger ? 'btn btn-danger' : 'btn btn-primary'} disabled={busy} onClick={() => void confirm()}>
          {confirmLabel}
        </button>
      </div>
    </Dialog>
  );
}

/** One text field. Submitting an empty value is allowed when `allowEmpty` (rename to the provider title). */
export function NameDialog({
  title,
  label,
  initial,
  hint,
  allowEmpty = false,
  submitLabel = 'Save',
  onSubmit,
  onClose,
}: {
  title: string;
  label: string;
  initial: string;
  hint?: string;
  allowEmpty?: boolean;
  submitLabel?: string;
  onSubmit: (value: string) => Promise<void>;
  onClose: () => void;
}) {
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await onSubmit(value.trim());
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog title={title} onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <label className="field">
          <span className="control-label">{label}</span>
          <input className="input" type="text" autoFocus value={value} onChange={(e) => setValue(e.target.value)} />
        </label>
        {hint && <p className="caption">{hint}</p>}
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <button type="button" className="btn btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || (!allowEmpty && !value.trim())}>
            {submitLabel}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

/** An icon button that opens a small menu; closes on a choice, Esc, or a click outside. */
export function Menu({ label, children }: { label: string; children: ReactNode }) {
  const ref = useRef<HTMLDetailsElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const close = () => ref.current?.removeAttribute('open');
    const onPointer = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        close();
        ref.current?.querySelector('summary')?.focus();
      }
    };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey, true);
    };
  }, [open]);
  return (
    <details ref={ref} className="menu" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="btn btn-icon" aria-label={label} aria-haspopup="menu">
        …
      </summary>
      <div className="menu-list" role="menu" onClick={() => ref.current?.removeAttribute('open')}>
        {children}
      </div>
    </details>
  );
}

const remarkPlugins = [remarkGfm];

const mdComponents: Components = {
  a({ href, children }) {
    const safe = typeof href === 'string' && /^(https?:|mailto:)/i.test(href);
    return safe ? (
      <a href={href} rel="noopener noreferrer" target="_blank">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    );
  },
  pre({ children }) {
    return <pre translate="no">{children}</pre>;
  },
};

/** Markdown for untrusted provider text: no raw HTML, no images, safe links only. */
export function Markdown({ text }: { text: string }) {
  return (
    <div className="md">
      <ReactMarkdown remarkPlugins={remarkPlugins} components={mdComponents} disallowedElements={['img']} unwrapDisallowed>
        {text}
      </ReactMarkdown>
    </div>
  );
}
