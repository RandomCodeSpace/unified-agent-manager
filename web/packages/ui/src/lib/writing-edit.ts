/** Textarea offsets are UTF-16 code units, like string.slice and the checker's ranges. */
export interface WritingSelection {
  start: number;
  end: number;
  direction: 'forward' | 'backward' | 'none';
}

export function writingSelection(input: HTMLTextAreaElement): WritingSelection {
  return { start: input.selectionStart, end: input.selectionEnd, direction: input.selectionDirection };
}

/** Keep the typing position, shifting offsets after the replacement by its length change. */
export function selectionAfterWritingEdit(selection: WritingSelection, start: number, end: number, length: number): WritingSelection {
  const offset = (position: number) => position <= start ? position : position >= end ? position + length - (end - start) : start + length;
  return { start: offset(selection.start), end: offset(selection.end), direction: selection.direction };
}

/**
 * insertText preserves native undo in browsers that support it. setRangeText is the
 * fallback; callers offer a guarded Undo for that case. Never replace a changed draft.
 */
export function replaceWritingRange(
  input: HTMLTextAreaElement,
  expected: string,
  start: number,
  end: number,
  replacement: string,
  selection = writingSelection(input),
): { text: string; selection: WritingSelection; native: boolean } | null {
  if (input.disabled || input.readOnly || input.value !== expected || !Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start || end > expected.length) return null;
  const next = expected.slice(0, start) + replacement + expected.slice(end);
  const mapped = selectionAfterWritingEdit(selection, start, end, replacement.length);
  const { scrollTop, scrollLeft } = input;
  input.focus({ preventScroll: true });
  input.setSelectionRange(start, end);
  try { input.ownerDocument.execCommand?.('insertText', false, replacement); }
  catch { /* Unsupported editing command: the explicit Undo covers the fallback. */ }
  const native = input.value === next;
  if (!native) {
    if (input.value !== expected) return null;
    input.setRangeText(replacement, start, end, 'end');
  }
  input.setSelectionRange(mapped.start, mapped.end, mapped.direction);
  input.scrollTop = scrollTop;
  input.scrollLeft = scrollLeft;
  return { text: input.value, selection: mapped, native };
}
