// Streaming markdown in blocks (ADR 0004 renders provider text as markdown while it streams).
// A block that a blank line and an unindented line have closed cannot change as more text
// arrives, so it is parsed once; only the tail block is parsed again per delta. No DOM.

// The fence run is whole (the lookaheads), so the rest cannot take its characters back.
const FENCE = /^ {0,3}((?:`{3,}(?!`))|(?:~{3,}(?!~)))(.*)$/;
const LIST_ITEM = /^([*+-]|\d{1,9}[.)])([ \t]|$)/;
// Constructs that reach across blank lines at the top level (reference and footnote
// definitions, HTML blocks of CommonMark types 1–5): such a text is one block.
const WHOLE = /^ {0,3}(\[[^\]]+\]:|<!--|<\?|<![a-z]|<!\[CDATA\[|<(pre|script|style|textarea)(\s|>|$))/im;

/**
 * The text split into top-level blocks whose markdown renders exactly as the whole text does.
 * A boundary is a blank line outside a code fence followed by a line that is neither
 * indented (a list item's continuation, indented code) nor a list item (a loose list
 * stays one list). The blank lines themselves are dropped.
 */
const LABEL_LINE = /^\*\*[^*\n]{1,60}(?::\*\*|\*\*:)/;

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
      if (m?.[1].startsWith(fence.char) && m[1].length >= fence.size && !m[2].trim()) fence = null;
      continue;
    }
    if (!line.trim()) {
      if (blank < 0) blank = i;
      continue;
    }
    // The last line may still be arriving: bare digits may yet become an ordered list item.
    const undecided = i === lines.length - 1 && /^\d{1,9}$/.test(line);
    // A run of `**Label:** value` paragraphs stays one block: it renders as one facts grid (richMarkdown.ts).
    const labels = LABEL_LINE.test(line) && LABEL_LINE.test(lines[start]);
    if (blank >= 0 && blank > start && !undecided && !labels && !/^[ \t]/.test(line) && !LIST_ITEM.test(line)) {
      blocks.push(lines.slice(start, blank).join('\n'));
      start = i;
    }
    blank = -1;
    const m = FENCE.exec(line);
    if (m && !(m[1].startsWith('`') && m[2].includes('`'))) fence = { char: m[1][0], size: m[1].length };
  }
  blocks.push(lines.slice(start).join('\n'));
  return blocks;
}

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

/**
 * `ref` without a trailing `:line` or `:line:column` (as compilers and agents cite a place in a
 * file: `src/a.go:12`), with the line as a `#L<line>` fragment. Only a file-like remainder (a
 * "/" or a `.ext` ending) loses it, so `tel:123` stays an address; a ref with its own `?` or
 * `#` keeps everything.
 */
function splitLine(ref: string): { ref: string; hash: string } {
  const m = /[?#]/.test(ref) ? null : /:(\d+)(?::\d+)?$/.exec(ref);
  const rest = m ? ref.slice(0, m.index) : ref;
  return m && (rest.includes('/') || /\.[A-Za-z0-9]{1,10}$/.test(rest)) ? { ref: rest, hash: `#L${m[1]}` } : { ref, hash: '' };
}

/** A path percent-decoded, as markdown encodes it; a stray `%` is part of the name. */
function decodePath(p: string): string {
  try {
    return decodeURIComponent(p);
  } catch {
    return p;
  }
}

/**
 * The file path a markdown `src` or `href` names on the host, or null when it is a web
 * address (`https:`, `data:`, `blob:`, `mailto:`, ...). Agents write `![](shot.png)`,
 * `![](./shots/a.png)`, `![](/home/me/x.png)` and `file:///home/me/x.png`; all of those
 * are paths for the service to serve from the Task's directory, percent-decoded. Windows
 * drive letters are not paths here: the service runs on Linux.
 */
export function localPath(ref: string | undefined): string | null {
  if (!ref) return null;
  if (/^file:\/\//i.test(ref)) {
    try {
      return decodeURIComponent(new URL(ref).pathname) || null;
    } catch {
      return null;
    }
  }
  if (SCHEME.test(splitLine(ref).ref)) return null;
  return decodePath(ref);
}

const realWorkdirs = new Map<string, string>();

/** Records the Task folder `workdir`'s real path, so `taskFile` maps absolute paths written through it too. */
export function registerRealWorkdir(workdir: string, real: string | undefined) {
  if (real && real !== workdir) realWorkdirs.set(workdir.replace(/(?<!\/)\/+$/, ''), real.replace(/(?<!\/)\/+$/, ''));
}

/**
 * The file a markdown link names, as a "/"-separated path relative to the Task's folder
 * `workdir` with the link's `#fragment` (a `:line` suffix becomes `#L<line>`), or null when it is a web address or lies outside
 * the folder. A relative path is relative to `base`, a folder of the Task's folder ("" is
 * the folder itself, as for a reply; a Markdown file's own folder when one renders); a path
 * is percent-decoded, as markdown encodes it, and its `?query` dropped.
 */
export function taskFile(ref: string | undefined, workdir: string | undefined, base = ''): { path: string; hash: string } | null {
  if (!ref || !localPath(ref)) return null;
  const line = splitLine(ref);
  ref = line.ref;
  let p = localPath(ref)!;
  const hash = /#.*/s.exec(ref)?.[0] ?? line.hash;
  // Cut before decoding, so an escaped `%23` stays in the name.
  if (!/^file:/i.test(ref)) p = decodePath(ref.replace(/[?#].*/s, ''));
  if (p.startsWith('/')) {
    const dir = workdir?.replace(/(?<!\/)\/+$/, '');
    const real = dir && realWorkdirs.get(dir);
    const root = [dir, real].find(root => root && p.startsWith(`${root}/`));
    if (!root) return null;
    p = p.slice(root.length + 1);
  } else if (base && p) p = `${base}/${p}`;
  const parts: string[] = [];
  for (const part of p.split('/')) {
    if (part === '..') {
      if (!parts.length) return null;
      parts.pop();
    } else if (part && part !== '.') parts.push(part);
  }
  return parts.length ? { path: parts.join('/'), hash } : null;
}

/**
 * Whether an inline code span's text may name a file: no whitespace, no scheme, at most
 * 300 characters, and a "/" or a `.ext` ending (1–10 letters or digits). Calls (`foo()`),
 * globs (`*.ts`), flags (`--force`), versions (`v1.2.3`), home paths (`~/x`) and
 * directories (`out/`) are not; `package.json` and `src/a.go:12` are. The Task's folder decides
 * the rest.
 */
export function looksLikePath(text: string): boolean {
  text = splitLine(text).ref;
  if (!text || text.length > 300 || /[\s()[\]{}*?<>|"'`$\\]/.test(text) || SCHEME.test(text)) return false;
  if (/^[-~]/.test(text) || text.endsWith('/') || /^v?\d+(\.\d+)+$/i.test(text)) return false;
  return text.includes('/') || /\.[A-Za-z0-9]{1,10}$/.test(text);
}

/**
 * The file of the Task folder `workdir` an inline code span names, as `taskFile` maps a
 * link, or null when the text does not look like a path. Code is literal: a `%` is part of
 * the name, never an escape, and so is a `#` unless what precedes it ends in an extension
 * (`docs/README.md#install`), as in `a#b.txt`.
 */
export function codeFile(text: string, workdir: string | undefined): { path: string; hash: string } | null {
  if (!looksLikePath(text)) return null;
  let ref = text.replaceAll('%', '%25');
  const at = ref.indexOf('#');
  if (at >= 0 && !/\.[A-Za-z0-9]{1,10}$/.test(ref.slice(0, at))) ref = ref.replaceAll('#', '%23');
  return taskFile(ref, workdir);
}
