import type { Reference } from '../api';

// The numbered items a reply cites (DESIGN.md references ledger): the service resolves each to
// where the Thread defined it; this file decides when to ask, words the sign-off's summary and
// finds the cited numbers in the reply's prose. Text in, words out.

const NOUNS = 'issue|bug|finding|item|task|step|ticket|pr|mr|problem|question|risk|todo';

/** Whether a reply's text cites a numbered item: only then is the service asked. */
export const citesReferences = (text: string) => new RegExp(`(?:^|[^\\w])[#!]\\d|\\b(?:${NOUNS})s?\\s+[#!]?\\d`, 'i').test(text);

/** A state the recorded state does not answer for: none recorded, or the number is not resolved to one item. */
const unknown = (r: Reference) => r.status !== 'resolved';

/** The reply's state word for an item differs from the state the Thread recorded. */
export const mismatched = (r: Reference) => !!r.claimed && !!r.state && r.claimed.toLowerCase() !== r.state.toLowerCase();

/** What a cited item is, in words: its title, or why there is none. */
export function referenceWords(r: Reference): string {
  if (r.status === 'unresolved') return 'no definition in this Thread';
  if (r.status === 'ambiguous') return `${r.candidates?.length ?? 0} matches`;
  return r.title || '';
}

/** How a row names the item: "#4" for an issue, "!7" for a merge request, else the reply's own label ("finding 3"). */
export function referenceName(r: Reference): string {
  if (/^[#!]\d/.test(r.label)) return r.label;
  const noun = r.noun.toLowerCase();
  if (noun === 'mr') return `!${r.number}`;
  if (noun === 'issue' || noun === 'bug' || noun === 'ticket' || noun === 'pr') return `#${r.number}`;
  return r.label || `#${r.number}`;
}

/** "6 issues · 4 open · 2 closed · 1 unknown": the noun is the references' common one, else "references"; states in their first appearance's order, unknown last. */
export function referenceSummary(refs: readonly Reference[]): string {
  const nouns = new Set(refs.map((r) => r.noun.toLowerCase()));
  const noun = nouns.size === 1 && !nouns.has('') ? [...nouns][0] : 'reference';
  const word = noun === 'pr' || noun === 'mr' ? noun.toUpperCase() : noun;
  const states = new Map<string, number>();
  let unknowns = 0;
  for (const r of refs) {
    if (unknown(r)) unknowns++;
    else if (r.state) states.set(r.state.toLowerCase(), (states.get(r.state.toLowerCase()) ?? 0) + 1);
  }
  return [
    `${refs.length} ${word}${refs.length === 1 ? '' : 's'}`,
    ...[...states].map(([state, n]) => `${n} ${state}`),
    ...(unknowns ? [`${unknowns} unknown`] : []),
  ].join(' · ');
}

interface MdNode { type: string; value?: string; children?: MdNode[]; data?: Record<string, unknown> }

// A number in prose: "#4", "!7" or a bare "4", never part of a word, a version, a time or a path.
const NUMBER = /(?<![\w#!&./:-])([#!]?)(\d+)(?![\w%]|[.:/-]\d)/g;
// Bounded, and tested on a short tail only: a whole-prefix `\s+$` backtracks on every number.
const AFTER_NOUN = new RegExp(`\\b(?:${NOUNS})s?\\s{1,3}$`, 'i');
const LIST_GAP = /^\s*(?:,|,?\s*(?:and|or|&))\s*$/i;

/**
 * Where the cited numbers stand in one text: "#4" and "!7", a number after an item noun, and the
 * numbers listed with one ("issues 4, 8 and 17"); a run of two or more cited numbers stands alone too
 * ("5 and 6 are closed"). A lone bare number ("took 5 minutes") stays text.
 */
export function citedSpans(text: string, cited: ReadonlyMap<string, number>): { start: number; end: number; index: number }[] {
  const found = [...text.matchAll(NUMBER)].flatMap((m) => {
    const index = cited.get(m[2]);
    if (index === undefined) return [];
    return [{ start: m.index, end: m.index + m[0].length, index, sure: !!m[1] || AFTER_NOUN.test(text.slice(Math.max(0, m.index - 16), m.index)) }];
  });
  // A cited number listed with another one stands with it.
  for (let i = 1; i < found.length; i++) {
    if (!LIST_GAP.test(text.slice(found[i - 1].end, found[i].start))) continue;
    found[i - 1].sure = found[i].sure = true;
  }
  for (let i = found.length - 2; i >= 0; i--) if (found[i + 1].sure && LIST_GAP.test(text.slice(found[i].end, found[i + 1].start))) found[i].sure = true;
  return found.filter((f) => f.sure).map(({ start, end, index }) => ({ start, end, index }));
}

/**
 * A remark plugin: each cited number in the reply's prose becomes a button (`data-reference`, the
 * row's index) that opens the reply's references on that row. Text inside links and code stays as written.
 */
export function remarkReferences(cited: ReadonlyMap<string, number>) {
  const visit = (node: MdNode) => {
    if (!node.children || node.type === 'link' || node.type === 'linkReference') return;
    node.children = node.children.flatMap((child): MdNode[] => {
      if (child.type !== 'text' || !child.value) { visit(child); return [child]; }
      const value = child.value;
      const spans = citedSpans(value, cited);
      if (!spans.length) return [child];
      const out: MdNode[] = [];
      let at = 0;
      for (const s of spans) {
        out.push({ type: 'text', value: value.slice(at, s.start) }, { type: 'referenceCite', data: { hName: 'button', hProperties: { type: 'button', dataReference: String(s.index) } }, children: [{ type: 'text', value: value.slice(s.start, s.end) }] });
        at = s.end;
      }
      out.push({ type: 'text', value: value.slice(at) });
      return out.filter((n) => n.type !== 'text' || n.value);
    });
  };
  return () => (tree: MdNode) => visit(tree);
}

/** Each cited number's first row. */
export function citedNumbers(refs: readonly Reference[]): Map<string, number> {
  const out = new Map<string, number>();
  refs.forEach((r, i) => { if (!out.has(r.number)) out.set(r.number, i); });
  return out;
}

/** The GitLab kind a cited number names: "#4" or an issue noun is an issue, "!7" or "MR" a merge request; anything else is not GitLab's. */
export function gitLabKind(r: Reference): 'issue' | 'mr' | undefined {
  const noun = r.noun.toLowerCase();
  if (r.label.startsWith('!') || noun === 'mr') return 'mr';
  if (r.label.startsWith('#') || noun === 'issue' || noun === 'bug' || noun === 'ticket') return 'issue';
  return undefined;
}
