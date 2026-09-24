// Streaming markdown in blocks (ADR 0004 renders provider text as markdown while it streams).
// A block that a blank line and an unindented line have closed cannot change as more text
// arrives, so it is parsed once; only the tail block is parsed again per delta. No DOM.

const FENCE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
const LIST_ITEM = /^([*+-]|\d{1,9}[.)])([ \t]|$)/;
// Constructs that reach across blank lines at the top level (reference and footnote
// definitions, HTML blocks of CommonMark types 1–5): such a text is one block.
const WHOLE = /^ {0,3}(\[[^\]]+\]:|<!--|<\?|<![A-Za-z]|<!\[CDATA\[|<(pre|script|style|textarea)(\s|>|$))/im;

/**
 * The text split into top-level blocks whose markdown renders exactly as the whole text does.
 * A boundary is a blank line outside a code fence followed by a line that is neither
 * indented (a list item's continuation, indented code) nor a list item (a loose list
 * stays one list). The blank lines themselves are dropped.
 */
export function splitBlocks(text: string): string[] {
  if (WHOLE.test(text)) return [text];
  const lines = text.split('\n');
  const blocks: string[] = [];
  let start = 0;
  let fence: { char: string; size: number } | null = null;
  let blank = -1;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (fence) {
      const m = FENCE.exec(line);
      if (m && m[1][0] === fence.char && m[1].length >= fence.size && !m[2].trim()) fence = null;
      continue;
    }
    if (!line.trim()) {
      if (blank < 0) blank = i;
      continue;
    }
    // The last line may still be arriving: bare digits may yet become an ordered list item.
    const undecided = i === lines.length - 1 && /^\d{1,9}$/.test(line);
    if (blank >= 0 && blank > start && !undecided && !/^[ \t]/.test(line) && !LIST_ITEM.test(line)) {
      blocks.push(lines.slice(start, blank).join('\n'));
      start = i;
    }
    blank = -1;
    const m = FENCE.exec(line);
    if (m && !(m[1][0] === '`' && m[2].includes('`'))) fence = { char: m[1][0], size: m[1].length };
  }
  blocks.push(lines.slice(start).join('\n'));
  return blocks;
}
