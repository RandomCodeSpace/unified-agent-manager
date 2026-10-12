import { ChevronLeft, ChevronRight, SpellCheck, Undo2 } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import { checkWriting, MAX_WRITING_CHECK_LENGTH, type WritingIssue } from '../lib/writing-checker';
import { replaceWritingRange, writingSelection, type WritingSelection } from '../lib/writing-edit';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

interface Props {
  text: string;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  scope: string;
  disabled: boolean;
  suspended: boolean;
  toolbarTarget: HTMLElement | null;
  onChange: (text: string, caret: number) => void;
}

const issueKey = (issue: WritingIssue) => JSON.stringify([issue.kind, issue.original, issue.message]);
const NO_ISSUES: WritingIssue[] = [];
const MIRROR_STYLES = ['font-family', 'font-size', 'font-weight', 'font-style', 'line-height', 'letter-spacing', 'text-indent', 'text-transform', 'text-align', 'direction', 'tab-size', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left', 'word-spacing', 'word-break', 'overflow-wrap'] as const;
type Check = { revision: number; issues: WritingIssue[]; failed: boolean };
type Open = { revision: number; index: number; anchor: HTMLElement | { getBoundingClientRect: () => DOMRect }; selection: WritingSelection };
type Undo = { revision: number; before: string; after: string; selection: WritingSelection };

/** A new answer, edit or Task owns a fresh set of suggestions and ignored phrases. */
export function WritingAssistance(props: Readonly<Props>) {
  return <WritingBuffer key={props.scope} {...props} />;
}

function WritingBuffer({ text, textareaRef, disabled, suspended, toolbarTarget, onChange }: Readonly<Props>) {
  const [draft, setDraft] = useState({ text, revision: 0 });
  const [check, setCheck] = useState<Check | null>(null);
  const [ignored, setIgnored] = useState<Set<string>>(() => new Set());
  const [composing, setComposing] = useState(false);
  const [opened, setOpened] = useState<Open | null>(null);
  const [undo, setUndo] = useState<Undo | null>(null);
  const [announcement, setAnnouncement] = useState('');
  const mirror = useRef<HTMLDivElement>(null);
  const reviewButton = useRef<HTMLButtonElement>(null);
  const pointer = useRef<{ x: number; y: number; selection: WritingSelection } | null>(null);
  const blocked = disabled || suspended || composing;
  if (draft.text !== text) {
    setDraft({ text, revision: draft.revision + 1 });
    setOpened(null);
    if (!text && ignored.size) setIgnored(new Set());
    if (undo && (undo.after !== text || undo.revision !== draft.revision + 1)) setUndo(null);
  }
  if (blocked && opened) setOpened(null);
  if (disabled && undo) setUndo(null);
  const current = check?.revision === draft.revision ? check : null;
  const tooLong = text.length > MAX_WRITING_CHECK_LENGTH;
  const ready = !blocked && !tooLong && !!current && !current.failed;
  const issues = ready ? current.issues.filter(issue => !ignored.has(issueKey(issue))) : NO_ISSUES;
  const open = !!opened && !blocked && opened.revision === draft.revision;
  const index = Math.min(opened?.index ?? 0, Math.max(0, issues.length - 1));
  const issue = issues[index];
  const fallbackUndo = undo?.revision === draft.revision && undo.after === text ? undo : null;

  useEffect(() => {
    if (blocked || tooLong || !text.trim()) return;
    let active = true;
    const timer = window.setTimeout(() => {
      checkWriting(text).then(
        issues => { if (active) setCheck({ revision: draft.revision, issues, failed: false }); },
        () => { if (active) setCheck({ revision: draft.revision, issues: [], failed: true }); },
      );
    }, 700);
    return () => { active = false; window.clearTimeout(timer); };
  }, [text, draft.revision, blocked, tooLong]);

  // Once loaded, keep native squiggles off across pauses between checks. Restore on failure.
  const customChecking = !!check && !check.failed && !tooLong;
  useEffect(() => {
    const input = textareaRef.current;
    if (!input || !customChecking) return;
    const original = input.getAttribute('spellcheck');
    input.setAttribute('spellcheck', 'false');
    return () => { if (original === null) input.removeAttribute('spellcheck'); else input.setAttribute('spellcheck', original); };
  }, [textareaRef, customChecking]);

  useLayoutEffect(() => {
    const input = textareaRef.current;
    const layer = mirror.current;
    if (!input || !layer) return;
    const align = () => {
      const style = getComputedStyle(input);
      for (const name of MIRROR_STYLES) layer.style.setProperty(name, style.getPropertyValue(name));
      layer.style.width = `${input.clientWidth}px`;
      layer.style.height = `${input.clientHeight}px`;
      layer.style.top = `${input.offsetTop + input.clientTop}px`;
      layer.style.left = `${input.offsetLeft + input.clientLeft}px`;
      layer.scrollTop = input.scrollTop;
      layer.scrollLeft = input.scrollLeft;
    };
    align();
    const scroll = () => { align(); setOpened(null); };
    input.addEventListener('scroll', scroll, { passive: true });
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(align);
    observer?.observe(input);
    let mounted = true;
    void document.fonts?.ready?.then(() => { if (mounted) align(); });
    return () => { mounted = false; observer?.disconnect(); input.removeEventListener('scroll', scroll); };
  }, [textareaRef, text, issues.length]);

  useEffect(() => {
    const input = textareaRef.current;
    if (!input) return;
    const start = () => { setComposing(true); setOpened(null); pointer.current = null; };
    const end = () => setComposing(false);
    input.addEventListener('compositionstart', start);
    input.addEventListener('compositionend', end);
    return () => { input.removeEventListener('compositionstart', start); input.removeEventListener('compositionend', end); };
  }, [textareaRef]);

  useEffect(() => {
    const input = textareaRef.current;
    if (!input || !opened) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      setOpened(null);
      if (input.value === text) input.setSelectionRange(opened.selection.start, opened.selection.end, opened.selection.direction);
    };
    // A pointer-opened popup leaves focus in the textarea. Escape must close it before
    // the composer's history/edit handlers interpret Escape as discarding a buffer.
    input.addEventListener('keydown', escape, true);
    return () => input.removeEventListener('keydown', escape, true);
  }, [textareaRef, opened, text]);

  useEffect(() => {
    const input = textareaRef.current;
    if (!input || blocked) return;
    const down = (event: PointerEvent) => {
      pointer.current = event.button === 0 && !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey
        ? { x: event.clientX, y: event.clientY, selection: writingSelection(input) } : null;
    };
    const click = (event: MouseEvent) => {
      const before = pointer.current;
      pointer.current = null;
      if (!before || event.detail !== 1 || input.selectionStart !== input.selectionEnd || Math.hypot(event.clientX - before.x, event.clientY - before.y) > 4 || input.value !== text) {
        setOpened(null);
        return;
      }
      const spans = mirror.current?.querySelectorAll<HTMLElement>('[data-writing-issue]');
      for (const span of spans ?? []) {
        const range = document.createRange();
        range.selectNodeContents(span);
        const rect = Array.from(range.getClientRects()).find(rect => event.clientX >= rect.left && event.clientX <= rect.right && event.clientY >= rect.top && event.clientY <= rect.bottom + 3);
        if (!rect) continue;
        const index = Number(span.dataset.writingIssue);
        // A click that opens correction review keeps the owner's typing position.
        // Drag and multi-click selections take the ordinary textarea path above.
        input.setSelectionRange(before.selection.start, before.selection.end, before.selection.direction);
        setOpened({ revision: draft.revision, index, anchor: { getBoundingClientRect: () => rect }, selection: before.selection });
        break;
      }
    };
    input.addEventListener('pointerdown', down);
    input.addEventListener('click', click);
    return () => { input.removeEventListener('pointerdown', down); input.removeEventListener('click', click); };
  }, [textareaRef, blocked, text, draft.revision, issues]);

  function restoreSelection(selection: WritingSelection) {
    const input = textareaRef.current;
    if (!input || input.disabled || input.value !== text) return;
    input.focus({ preventScroll: true });
    input.setSelectionRange(selection.start, selection.end, selection.direction);
  }

  function apply() {
    const input = textareaRef.current;
    if (!input || !opened || !issue || blocked || input.value !== text || text.slice(issue.start, issue.end) !== issue.original) return;
    const changed = replaceWritingRange(input, text, issue.start, issue.end, issue.replacements[0], opened.selection);
    if (!changed) return;
    setUndo(changed.native ? null : { revision: draft.revision + 1, before: text, after: changed.text, selection: opened.selection });
    setOpened(null);
    setAnnouncement(changed.native ? 'Correction applied. Use your usual Undo shortcut to undo.' : 'Correction applied. Undo is available in Spelling and grammar.');
    onChange(changed.text, changed.selection.start);
  }

  function undoFallback() {
    const input = textareaRef.current;
    if (!input || !fallbackUndo || blocked || input.disabled || input.value !== fallbackUndo.after) return;
    input.focus({ preventScroll: true });
    input.setRangeText(fallbackUndo.before, 0, input.value.length, 'end');
    input.setSelectionRange(fallbackUndo.selection.start, fallbackUndo.selection.end, fallbackUndo.selection.direction);
    setUndo(null);
    setOpened(null);
    setAnnouncement('Correction undone.');
    onChange(input.value, fallbackUndo.selection.start);
  }

  const segments: ReactNode[] = [];
  let end = 0;
  issues.forEach((issue, index) => {
    if (issue.start < end || issue.end <= issue.start || text.slice(issue.start, issue.end) !== issue.original) return;
    segments.push(text.slice(end, issue.start));
    segments.push(<span key={`${issue.start}:${issue.end}`} data-writing-issue={index} className={issue.kind === 'spelling' ? 'underline decoration-error decoration-wavy underline-offset-[3px]' : 'underline decoration-info decoration-wavy underline-offset-[3px]'}>{text.slice(issue.start, issue.end)}</span>);
    end = issue.end;
  });
  if (issues.length) segments.push(text.slice(end), '\n');
  const message = tooLong ? `Spelling and grammar checks support drafts up to ${MAX_WRITING_CHECK_LENGTH.toLocaleString()} characters.`
    : current?.failed ? 'Spelling and grammar checks are unavailable. You can keep typing and sending.'
      : !text.trim() ? 'Start typing to check spelling and grammar.'
        : !current ? 'Checking spelling and grammar…' : 'No spelling or grammar suggestions.';

  return <>
    <div ref={mirror} aria-hidden="true" data-writing-mirror="" className="pointer-events-none absolute overflow-hidden whitespace-pre-wrap break-words text-transparent select-none">{segments}</div>
    <span className="sr-only" role="status">{announcement}</span>
    <Popover.Root open={open} onOpenChange={value => {
      const input = textareaRef.current;
      if (!value) setOpened(null);
      else if (input && reviewButton.current && !blocked) setOpened({ revision: draft.revision, index: 0, anchor: reviewButton.current, selection: writingSelection(input) });
    }}>
      {toolbarTarget && createPortal(<Tip label={<>Spelling and grammar<span className="block text-on-primary/70">English · Checked on this device</span></>}>
        <Popover.Trigger render={<Button ref={reviewButton} size="icon" variant="subtle" aria-label={`Spelling and grammar${issues.length ? `: ${issues.length} suggestions` : ''}${fallbackUndo ? '. Undo correction available' : ''}`} disabled={blocked} className="relative text-muted" />}>
          <SpellCheck />
          {issues.length > 0 && <span aria-hidden="true" className="absolute right-1 top-1 size-1 rounded-full bg-info" />}
        </Popover.Trigger>
      </Tip>, toolbarTarget)}
      {opened && <Popover.Content anchor={opened.anchor} initialFocus={type => type === 'keyboard'} finalFocus={false} className="w-72 gap-2 p-3" onKeyDown={event => {
        if (event.key === 'Escape') { event.stopPropagation(); setOpened(null); restoreSelection(opened.selection); }
      }}>
        <Popover.Title>{issue ? issue.kind === 'grammar' ? 'Grammar' : 'Spelling' : 'Spelling and grammar'}</Popover.Title>
        {issue ? <>
          {/* "Teh → The" says what the suggestion is; the checker's own message would repeat it. */}
          <Popover.Description className="break-words text-ui text-body"><span className="text-muted line-through">{issue.original}</span><span aria-hidden="true"> → </span><span className="sr-only"> becomes </span><span className="font-medium text-ink">{issue.replacements[0] || 'Remove'}</span></Popover.Description>
          <div className="flex items-center gap-1">
            <Button size="sm" variant="primary" onClick={apply}>Apply</Button>
            <Button size="sm" variant="subtle" onClick={() => {
              setIgnored(previous => new Set([...previous, issueKey(issue)]));
              setOpened(null);
              restoreSelection(opened.selection);
            }}>Keep original</Button>
            {issues.length > 1 && <div className="ml-auto flex items-center gap-0.5">
              <Button size="icon-sm" aria-label="Previous suggestion" onClick={() => setOpened({ ...opened, index: (index + issues.length - 1) % issues.length })}><ChevronLeft /></Button>
              <span className="text-caption text-muted">{index + 1}/{issues.length}</span>
              <Button size="icon-sm" aria-label="Next suggestion" onClick={() => setOpened({ ...opened, index: (index + 1) % issues.length })}><ChevronRight /></Button>
            </div>}
          </div>
        </> : <Popover.Description>{message}</Popover.Description>}
        {fallbackUndo && <Button size="sm" variant="subtle" className="self-start" onClick={undoFallback}><Undo2 />Undo correction</Button>}
      </Popover.Content>}
    </Popover.Root>
  </>;
}
