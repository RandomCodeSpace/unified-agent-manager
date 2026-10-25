// Lens: a file's changes as marks on the file itself. Pure, so the unit tests run in node.

/** A parsed hunk, as `diff`'s parsePatch gives it. */
export interface Hunk { oldStart: number; oldLines: number; newStart: number; newLines: number; lines: string[] }

/** One run of changed lines: `added` lines from new line `line`, with the lines it `removed` just before them. */
export interface LensChange { line: number; added: number; removed: string[]; hunk: number; run: number }

/** The runs of every hunk, in file order. A pure deletion has no added lines and sits before `line`. */
export function lensChanges(hunks: readonly Hunk[]): LensChange[] {
  const out: LensChange[] = [];
  hunks.forEach((h, hunk) => {
    let line = h.newStart;
    let run = 0;
    let current: LensChange | null = null;
    for (const text of h.lines) {
      const sign = text[0];
      if (sign === '+' || sign === '-') {
        current ??= { line, added: 0, removed: [], hunk, run: run++ };
        if (sign === '+') { current.added++; line++; } else current.removed.push(text.slice(1));
      } else if (sign !== '\\') {
        if (current) out.push(current);
        current = null;
        line++;
      }
    }
    if (current) out.push(current);
  });
  return out;
}

/** Hunk `h` as patch text, its header first. */
export function hunkText(h: Hunk): string {
  return `@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@\n${h.lines.join('\n')}\n`;
}

/**
 * Hunk `h` narrowed to its run `keep`: the other runs read as the file has them now (their added lines become
 * context, their removed lines go), so reversing the result undoes that one change and leaves the rest.
 */
export function runText(h: Hunk, keep: number): string {
  const lines: string[] = [];
  let run = -1;
  let inRun = false;
  let dropped = false;
  for (const text of h.lines) {
    const sign = text[0];
    // "\ No newline at end of file" belongs to the line before it, and goes where that line went.
    if (sign === '\\') {
      if (!dropped) lines.push(text);
      continue;
    }
    const changed = sign === '+' || sign === '-';
    if (changed && !inRun) run++;
    inRun = changed;
    dropped = false;
    if (!changed || run === keep) lines.push(text);
    else if (sign === '+') lines.push(' ' + text.slice(1));
    // A removed line of another run is not in the file now: it leaves the narrowed hunk.
    else dropped = true;
  }
  const old = lines.filter((l) => l[0] === ' ' || l[0] === '-').length;
  const next = lines.filter((l) => l[0] === ' ' || l[0] === '+').length;
  return hunkText({ ...h, oldLines: old, newLines: next, lines });
}
