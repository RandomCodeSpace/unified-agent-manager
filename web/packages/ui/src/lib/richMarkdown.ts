// Rich blocks from plain Markdown (DESIGN.md Rich replies): fixed rules that match exact
// Markdown shapes and mark them for the renderer. The text itself is never changed, so the
// stored reply, Copy and the model's history keep the Markdown as written; only the view differs.
// A shape that does not match exactly renders as ordinary Markdown.

import { looksLikePath } from './markdown';

interface MdNode {
  type: string;
  value?: string;
  ordered?: boolean | null;
  checked?: boolean | null;
  children?: MdNode[];
  data?: { hName?: string; hProperties?: Record<string, unknown> };
}

export const CALLOUTS = ['note', 'tip', 'important', 'warning', 'caution', 'question', 'blocker'] as const;
export type CalloutKind = (typeof CALLOUTS)[number];

const ALERT = /^\[!(note|tip|important|warning|caution|question|blocker)\][ \t]*(?:\r?\n|$)/i;
/** A plan item's or todo's title as a match key: lower case, letters and digits only, single spaces. */
export const planKey = (title: string) => title.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ').trim();
// A short figure: "12", "98%", "1.2 s", "$40k", "~3 min", "1,024 KiB".
const FIGURE = /^[~≈<>]?[$€£]?[-+−]?\d[\d,.]*(?:\s?(?:%|[a-zA-Z]{1,5}))?$/;

const markProps = (node: MdNode, props: Record<string, unknown>, hName?: string) => {
  node.data = { ...node.data, ...(hName ? { hName } : {}), hProperties: { ...node.data?.hProperties, ...props } };
};

/** `> [!WARNING]` (GitHub alerts): the marker goes, the quote is marked with its kind. */
function alert(node: MdNode) {
  const para = node.children?.[0];
  const first = para?.type === 'paragraph' ? para.children?.[0] : undefined;
  const m = first?.type === 'text' ? ALERT.exec(first.value ?? '') : null;
  if (!m || !para?.children || !first) return;
  first.value = (first.value ?? '').slice(m[0].length);
  if (!first.value) para.children.shift();
  if (para.children[0]?.type === 'break') para.children.shift();
  if (!para.children.length) node.children!.shift();
  markProps(node, { dataCallout: m[1].toLowerCase() });
}

/** The item's lead when its first paragraph opens with bold text, else null. */
function boldLead(item: MdNode): MdNode | null {
  const para = item.children?.[0];
  const lead = para?.type === 'paragraph' ? para.children?.[0] : undefined;
  return lead?.type === 'strong' ? lead : null;
}

const plain = (nodes: MdNode[] = []): string => nodes.map((n) => n.value ?? plain(n.children)).join('');

interface Fact { label: MdNode[]; value: MdNode[]; rest: MdNode[] }

// "**Label** — value": an em or en dash between spaces (a hyphen stays prose).
const DASH = /^\s+[—–]\s+/;

/** A paragraph that opens `**Label:** value` (the colon inside or right after the bold) or `**Label** — value`, split into its label and value. */
function labelled(para: MdNode | undefined): Omit<Fact, 'rest'> | null {
  const lead = para?.type === 'paragraph' ? para.children?.[0] : undefined;
  if (lead?.type !== 'strong') return null;
  const after = para!.children!.slice(1);
  const label = [...(lead.children ?? [])];
  const last = label.at(-1);
  let value: MdNode[];
  if (last?.type === 'text' && last.value?.trimEnd().endsWith(':')) {
    label[label.length - 1] = { ...last, value: last.value.trimEnd().slice(0, -1) };
    value = after;
  } else if (after[0]?.type === 'text' && after[0].value?.startsWith(':')) {
    value = [{ ...after[0], value: after[0].value.slice(1) }, ...after.slice(1)];
  } else if (after[0]?.type === 'text' && DASH.test(after[0].value ?? '')) {
    value = [{ ...after[0], value: after[0].value!.replace(DASH, '') }, ...after.slice(1)];
  } else return null;
  if (value[0]?.type === 'text') value[0] = { ...value[0], value: value[0].value!.trimStart() };
  return plain(label).trim() ? { label, value: value.filter((n) => n.type !== 'text' || n.value) } : null;
}

/** A figure split into its number and unit: "14.6 MB" → 14.6, "MB"; null when it is not one. */
function figureOf(text: string): { n: number; unit: string; decimals: number } | null {
  const m = /^([-+−]?\d[\d,]*(?:\.(\d+))?)\s?(%|[a-zA-Z]{1,5})?$/.exec(text.trim());
  return m ? { n: Number(m[1].replaceAll(',', '').replace('−', '-')), unit: m[3] ?? '', decimals: m[2]?.length ?? 0 } : null;
}

/**
 * A fact's value that reads "old → new" (or "->"), as text alone with one arrow: the old value struck, an arrow,
 * the new in bold, and the signed change when both are figures in one unit. Else the value as written.
 */
function change(value: MdNode[]): MdNode[] {
  if (value.length !== 1 || value[0].type !== 'text') return value;
  // One change only: a value listing several ("1 → 2, size 3 → 4") stays as written.
  if (value[0].value!.split(/→|->/).length !== 2) return value;
  const m = /^(.+?)\s*(?:→|->)\s*(.+)$/.exec(value[0].value!.trim());
  if (!m) return value;
  const out: MdNode[] = [
    { type: 'delete', children: [{ type: 'text', value: m[1] }] },
    { type: 'arrow', data: { hName: 'span', hProperties: { className: ['md-arrow'], ariaLabel: 'to' } }, children: [{ type: 'text', value: '→' }] },
    { type: 'strong', children: [{ type: 'text', value: m[2] }] },
  ];
  const [a, b] = [figureOf(m[1]), figureOf(m[2])];
  if (a && b && a.unit === b.unit && a.n !== b.n) {
    const d = b.n - a.n;
    const words = `${d > 0 ? '+' : '−'}${Math.abs(d).toFixed(Math.max(a.decimals, b.decimals))}${b.unit ? (b.unit === '%' ? '%' : ` ${b.unit}`) : ''}`;
    out.push({ type: 'delta', data: { hName: 'span', hProperties: { className: ['md-delta'] } }, children: [{ type: 'text', value: words }] });
  }
  return out;
}

/** Facts as a label/value grid; two to four that are each one short figure: stat tiles. */
function factsNode(node: MdNode, rows: Fact[]) {
  const tiles = rows.length <= 4 && rows.every((r) => !r.rest.length && r.value.length === 1 && r.value[0].type === 'text' && FIGURE.test(r.value[0].value!.trim()));
  markProps(node, { className: tiles ? ['md-facts', 'md-tiles'] : ['md-facts'] }, 'dl');
  node.children = rows.map((row) => ({
    type: 'fact',
    data: { hName: 'div' },
    children: [
      { type: 'paragraph', data: { hName: 'dt' }, children: row.label },
      { type: 'paragraph', data: { hName: 'dd' }, children: [...(tiles ? row.value : change(row.value)), ...row.rest] },
    ],
  }));
}

/** A bullet list whose every item opens `**Label:** value`: facts. */
function factList(list: MdNode) {
  const items = list.children ?? [];
  if (list.ordered || items.length < 2 || items.some((i) => i.checked != null)) return false;
  const rows: Fact[] = [];
  for (const item of items) {
    const row = labelled(item.children?.[0]);
    const rest = item.children?.slice(1) ?? [];
    if (!row || (!row.value.length && !rest.length)) return false;
    rows.push({ ...row, rest });
  }
  factsNode(list, rows);
  return true;
}

/** A paragraph's phrasing split at its line breaks (soft newlines in text, and hard breaks). */
function lines(nodes: MdNode[]): MdNode[][] {
  const out: MdNode[][] = [[]];
  for (const n of nodes) {
    if (n.type === 'break') out.push([]);
    else if (n.type !== 'text' || !n.value?.includes('\n')) out.at(-1)!.push(n);
    else n.value.split('\n').forEach((part, i) => {
      if (i) out.push([]);
      if (part) out.at(-1)!.push({ ...n, value: part });
    });
  }
  return out;
}

/**
 * Facts in their place: two or more paragraphs in a row that each open `**Label:** value`,
 * or one paragraph whose every line (two or more) does.
 */
function factParagraphs(parent: MdNode) {
  const out: MdNode[] = [];
  let run: Fact[] = [];
  const flush = () => {
    if (run.length >= 2) {
      const node: MdNode = { type: 'facts' };
      factsNode(node, run);
      out.splice(out.length - run.length, run.length, node);
    }
    run = [];
  };
  for (const child of parent.children ?? []) {
    const rows = child.type === 'paragraph' ? lines(child.children ?? []).map((line) => labelled({ type: 'paragraph', children: line })) : [];
    if (rows.length >= 2 && rows.every((r) => r?.value.length)) {
      flush();
      const node: MdNode = { type: 'facts' };
      factsNode(node, rows.map((r) => ({ ...r!, rest: [] })));
      out.push(node);
      continue;
    }
    const row = labelled(child);
    if (row?.value.length) run.push({ ...row, rest: [] });
    else flush();
    out.push(child);
  }
  flush();
  parent.children = out;
}

/** An item's first paragraph and its first text when that opens the paragraph. */
function leadText(item: MdNode): MdNode | undefined {
  const para = item.children?.[0];
  const first = para?.type === 'paragraph' ? para.children?.[0] : undefined;
  return first?.type === 'text' ? first : undefined;
}

const STATUS_LEAD = /^(✅|✔|☑|❌|✖|✗|❎|⚠|⏭)\uFE0F?\s*/u;
const STATUS_OF: Record<string, string> = { '✅': 'ok', '✔': 'ok', '☑': 'ok', '❌': 'fail', '✖': 'fail', '✗': 'fail', '❎': 'fail', '⚠': 'warn', '⏭': 'skip' };
/** The order a status list's items take: what failed first, what passed last. */
export const STATUS_ORDER = ['fail', 'warn', 'skip', 'ok'] as const;

/** Stable order by `rank`, lowest first. */
const ranked = (items: MdNode[], rank: (item: MdNode) => number) => items.map((item, i) => ({ item, i })).sort((a, b) => rank(a.item) - rank(b.item) || a.i - b.i).map(({ item }) => item);

/** A list whose every item opens with ✅ ❌ ⚠️ (or ✔ ✗ ⏭): each item's mark gives way to its state, failures first. */
function statusList(list: MdNode): boolean {
  const items = list.children ?? [];
  if (items.length < 2 || items.some((i) => i.checked != null)) return false;
  const leads = items.map((i) => {
    const text = leadText(i);
    const m = text ? STATUS_LEAD.exec(text.value ?? '') : null;
    return m && text ? { text, m } : null;
  });
  if (leads.some((l) => !l)) return false;
  items.forEach((item, i) => {
    const { text, m } = leads[i]!;
    text.value = text.value!.slice(m[0].length);
    markProps(item, { dataStatus: STATUS_OF[m[1]] });
  });
  list.children = ranked(items, (i) => STATUS_ORDER.indexOf(i.data!.hProperties!.dataStatus as (typeof STATUS_ORDER)[number]));
  markProps(list, { className: ['md-status'] });
  return true;
}

const SEVERITY: Record<string, string> = { critical: 'critical', high: 'high', medium: 'medium', low: 'low', p0: 'critical', p1: 'high', p2: 'medium', p3: 'low' };
/** The order findings take, most severe first. */
export const SEVERITY_ORDER = ['critical', 'high', 'medium', 'low'] as const;

/** A list whose every item opens `**High:**` (Critical, High, Medium, Low or P0–P3): the label leaves the text for the row's own, most severe first. */
function severityList(list: MdNode): boolean {
  const items = list.children ?? [];
  if (items.length < 2 || items.some((i) => i.checked != null)) return false;
  const rows = items.map((i) => {
    const row = labelled(i.children?.[0]);
    const level = row && SEVERITY[plain(row.label).trim().toLowerCase()];
    return row && level && row.value.length ? { row, level } : null;
  });
  if (rows.some((r) => !r)) return false;
  items.forEach((item, i) => {
    const { row, level } = rows[i]!;
    item.children![0].children = row.value;
    markProps(item, { dataSeverity: level, dataLabel: plain(row.label).trim() });
  });
  list.children = ranked(items, (i) => SEVERITY_ORDER.indexOf(i.data!.hProperties!.dataSeverity as (typeof SEVERITY_ORDER)[number]));
  markProps(list, { className: ['md-severity'] });
  return true;
}

const COUNTS = /^\s*\(\+(\d+)\s*[−-](\d+)\)/;

/** A list whose every item opens with inline code naming a file, then optionally "(+12 −3)": file rows with the counts drawn. */
function fileList(list: MdNode): boolean {
  const items = list.children ?? [];
  if (items.length < 2 || items.some((i) => i.checked != null)) return false;
  const paths = items.map((i) => {
    const para = i.children?.[0];
    const code = para?.type === 'paragraph' ? para.children?.[0] : undefined;
    return code?.type === 'inlineCode' && looksLikePath(code.value ?? '') ? { para: para!, path: code.value! } : null;
  });
  if (paths.some((p) => !p)) return false;
  items.forEach((item, i) => {
    const { para, path } = paths[i]!;
    const after = para.children![1];
    const m = after?.type === 'text' ? COUNTS.exec(after.value ?? '') : null;
    if (m && after) {
      after.value = after.value!.slice(m[0].length);
      if (!after.value) para.children!.splice(1, 1);
    }
    markProps(item, { dataPath: path, ...(m ? { dataAdd: Number(m[1]), dataDel: Number(m[2]) } : {}) });
  });
  markProps(list, { className: ['md-files'] });
  return true;
}

// A clock time ("06:58", "14:02:10", "9:05 pm") or a date ("2026-10-28"), optionally with a time.
const WHEN = /^(\d{4}-[01]\d-[0-3]\d(?:[ T](?:[01]?\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)?|(?:[01]?\d|2[0-3]):[0-5]\d(?::[0-5]\d)?(?:\s?[ap]m)?)(?=$|[\s,:—–-])/i;
// What may sit between the time and the event: "06:58 — merged", "06:58: merged", "06:58 - merged".
const WHEN_GAP = /^\s*(?:[—–:-]\s*)?/;

/** A time's minutes, to measure the gap to the next: a clock time within its day, a date-time since the epoch; null for a date alone. */
function minutesOf(when: string): { n: number; clock: boolean } | null {
  const date = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{1,2}):(\d{2})/.exec(when);
  if (date) return { n: Date.UTC(+date[1], +date[2] - 1, +date[3], +date[4], +date[5]) / 60000, clock: false };
  const clock = /^(\d{1,2}):(\d{2})(?::\d{2})?\s?([ap]m)?$/i.exec(when);
  if (!clock) return null;
  const pm = clock[3]?.toLowerCase() === 'pm', am = clock[3]?.toLowerCase() === 'am';
  const h = (+clock[1] % (am || pm ? 12 : 24)) + (pm ? 12 : 0);
  return { n: h * 60 + +clock[2], clock: true };
}

/** An item's time when its first paragraph opens with one (in bold or not); the rest of the item is the event. */
function whenOf(item: MdNode): { when: string; strip: () => void } | null {
  const para = item.children?.[0];
  const first = para?.type === 'paragraph' ? para.children?.[0] : undefined;
  if (!first || !para?.children) return null;
  if (first.type === 'strong') {
    const text = plain(first.children).trim();
    const m = WHEN.exec(text);
    if (!m || m[0] !== text) return null;
    return { when: text, strip: () => { para.children!.shift(); const next = para.children![0]; if (next?.type === 'text') next.value = next.value!.replace(WHEN_GAP, ''); } };
  }
  const m = first.type === 'text' ? WHEN.exec(first.value ?? '') : null;
  if (!m) return null;
  return { when: m[1], strip: () => { first.value = first.value!.slice(m[0].length).replace(WHEN_GAP, ''); if (!first.value) para.children!.shift(); } };
}

/** A list whose every item opens with a time or date: a timeline, each item's time on the rail, a gap of more than an hour to the next marked. */
function timeline(list: MdNode): boolean {
  const items = list.children ?? [];
  if (items.length < 2 || items.some((i) => i.checked != null)) return false;
  const whens = items.map(whenOf);
  if (whens.some((w) => !w)) return false;
  items.forEach((item, i) => {
    whens[i]!.strip();
    markProps(item, { dataWhen: whens[i]!.when });
    const [a, b] = [minutesOf(whens[i]!.when), i + 1 < items.length ? minutesOf(whens[i + 1]!.when) : null];
    if (!a || !b || a.clock !== b.clock) return;
    // Clock times may run either way and past midnight: the shorter way round the day.
    const d = Math.abs(b.n - a.n);
    if ((a.clock ? Math.min(d, 1440 - d) : d) > 60) markProps(item, { dataGap: true });
  });
  markProps(list, { className: ['md-timeline'] });
  return true;
}

// An option's letter at the start of its bold lead: "A", "A.", "A)", "A:", "A ·", "A —", "Option A".
const LETTER = /^(?:Option\s+([A-H])\b|([A-H])(?:[.):]|\s*·|\s+[—–]|$))\s*/;
const PICK = /\b(?:pick|choose) one\b/i;

/**
 * Choices, not steps: a list of 2–8 items whose bold leads run A, B, C… (the letter moves to the gutter),
 * or a numbered list of bold leads right after a line that says "pick one" or "choose one". Drawn like
 * steps with the letter, but never tied to the todo list: nothing to tick off, nothing to click.
 */
function choices(list: MdNode, prev: MdNode | undefined): boolean {
  const items = list.children ?? [];
  if (items.length < 2 || items.length > 8 || !items.every(boldLead)) return false;
  const letters = items.map((item) => LETTER.exec(plain(boldLead(item)!.children).trim()));
  const lettered = letters.every((m, i) => m && (m[1] ?? m[2]) === 'ABCDEFGH'[i]);
  if (!lettered && !(list.ordered && prev?.type === 'paragraph' && PICK.test(plain(prev.children)))) return false;
  items.forEach((item, i) => {
    const lead = boldLead(item)!;
    const first = lead.children?.[0];
    const m = letters[i];
    // The letter is drawn in the gutter: take it off the lead when the lead's text starts with it.
    if (lettered && m && first?.type === 'text' && first.value?.trimStart().startsWith(m[0].trim())) {
      first.value = first.value.trimStart().slice(m[0].length);
      if (!first.value) lead.children!.shift();
      if (!lead.children!.length) item.children![0].children!.shift();
    }
    markProps(item, { dataOption: 'ABCDEFGH'[i] });
  });
  markProps(list, { className: ['md-steps', 'md-options'] });
  return true;
}

const TALLY = /\b(\d[\d,]*)\s+(passed|failed|skipped|errors?)\b/gi;
const TALLY_ORDER = ['failed', 'errors', 'skipped', 'passed'];

/** A paragraph with counts of two or more kinds ("979 passed, 3 skipped, 1 failed"): the tally for a count line above it, failures first. */
function tally(para: MdNode) {
  const counts = new Map<string, number>();
  for (const m of plain(para.children).matchAll(TALLY)) {
    const kind = m[2].toLowerCase().replace(/^error$/, 'errors');
    counts.set(kind, (counts.get(kind) ?? 0) + Number(m[1].replaceAll(',', '')));
  }
  if (counts.size < 2) return;
  markProps(para, { dataTally: TALLY_ORDER.filter((k) => counts.has(k)).map((k) => `${counts.get(k)} ${k}`).join(',') });
}

// Section heads that say what was not proven, in warning; only these two vouch, in success.
const HEAD_WARN = new Set(['unverified', 'not verified', 'not run', 'not tested', 'untested', 'still open', 'blocked']);
const HEAD_OK = new Set(['verified', 'tested']);

/**
 * A line of bold text alone directly followed by a list is the list's head: an eyebrow, toned when it
 * says what was or was not verified. Other heads only when short (four words, 32 characters, no code).
 */
function heads(parent: MdNode) {
  const kids = parent.children ?? [];
  kids.forEach((kid, i) => {
    const label = boldLine(kid);
    if (!label || kids[i + 1]?.type !== 'list') return;
    const tone = HEAD_WARN.has(label) ? 'warn' : HEAD_OK.has(label) ? 'ok' : undefined;
    const code = kid.children![0].children?.some((c) => c.type !== 'text');
    if (!tone && (code || label.length > 32 || label.split(/\s+/).length > 4)) return;
    markProps(kid, { className: ['md-head'], ...(tone ? { dataTone: tone } : {}) });
  });
}

/** A paragraph that opens with bold text starting "N. ": its number and the length of that prefix. */
function stepNumber(node: MdNode | undefined): { n: number; prefix: number } | null {
  const lead = node?.type === 'paragraph' ? node.children?.[0] : undefined;
  const first = lead?.type === 'strong' ? lead.children?.[0] : undefined;
  const m = first?.type === 'text' ? /^(\d{1,2})\.\s+/.exec(first.value ?? '') : null;
  return m ? { n: Number(m[1]), prefix: m[0].length } : null;
}

/**
 * Paragraphs that open `**1. Title**`, `**2. …**` (numbered from 1, no gap) are steps: each takes the
 * blocks up to the next numbered one; nothing after the last. A heading or rule ends the run.
 */
function numberedSteps(parent: MdNode) {
  const kids = parent.children ?? [];
  for (let i = 0; i < kids.length; i++) {
    if (stepNumber(kids[i])?.n !== 1) continue;
    const starts = [i];
    for (let j = i + 1; j < kids.length; j++) {
      const step = stepNumber(kids[j]);
      if (step) {
        if (step.n !== starts.length + 1) break;
        starts.push(j);
      } else if (kids[j].type === 'heading' || kids[j].type === 'thematicBreak') break;
    }
    if (starts.length < 2) continue;
    const items: MdNode[] = starts.map((start, k) => {
      const group = kids.slice(start, k + 1 < starts.length ? starts[k + 1] : start + 1);
      const lead = group[0].children![0];
      const first = lead.children![0];
      first.value = first.value!.slice(stepNumber(group[0])!.prefix);
      if (!first.value) lead.children!.shift();
      // The text on the lines under the title stays under it, not run on after it.
      const para = group[0].children!;
      const after = para[1];
      if (after?.type === 'text' && after.value?.startsWith('\n')) para.splice(1, 1, { type: 'break' }, { ...after, value: after.value.slice(1) });
      return { type: 'listItem', checked: null, children: group };
    });
    const list: MdNode = { type: 'list', ordered: true, children: items };
    Object.assign(list, { start: 1, spread: false });
    for (const item of items) Object.assign(item, { spread: false });
    steps(list);
    kids.splice(i, starts.at(-1)! + 1 - i, list);
  }
}

/** A numbered list whose every item opens with bold text: steps, each keyed by its lead to track against the todo list. */
function steps(list: MdNode) {
  const items = list.children ?? [];
  if (!list.ordered || items.length < 2 || !items.every(boldLead)) return;
  markProps(list, { className: ['md-steps'] });
  for (const item of items) markProps(item, { dataPlan: planKey(plain(boldLead(item)!.children)) });
}

/** A task list: how many of its items are done, for the progress line above it; each item keyed by its text to track against the todo list. */
function tasks(list: MdNode) {
  const items = list.children ?? [];
  if (!items.length || items.some((i) => i.checked == null)) return;
  markProps(list, { dataDone: items.filter((i) => i.checked).length, dataTotal: items.length });
  for (const item of items) {
    const para = item.children?.[0];
    if (para?.type === 'paragraph') markProps(item, { dataPlan: planKey(plain(para.children)) });
  }
}

// Two lists side by side: the first label of a pair opens the left column, its partner the right.
const PAIRS: Record<string, string> = { pros: 'cons', keep: 'drop', do: "don't", advantages: 'disadvantages', for: 'against' };

/** A paragraph that is bold text alone ("**Pros**", "**Pros:**", "**Pros**:"): its label in lower case, else null. */
function boldLine(node: MdNode | undefined): string | null {
  if (node?.type !== 'paragraph') return null;
  const [lead, after] = node.children ?? [];
  if (lead?.type !== 'strong' || node.children!.length > 2 || (after && (after.type !== 'text' || after.value!.trim() !== ':'))) return null;
  return plain(lead.children).trim().replace(/:$/, '').replace('’', "'").toLowerCase();
}

/** `**Pros**` and a list, then `**Cons**` and a list (Keep/Drop, Do/Don't, …): the two side by side. */
function pairs(parent: MdNode) {
  const kids = parent.children ?? [];
  for (let i = 0; i + 3 < kids.length; i++) {
    const left = boldLine(kids[i]);
    const right = boldLine(kids[i + 2]);
    if (!left || !right || PAIRS[left] !== right || kids[i + 1].type !== 'list' || kids[i + 3].type !== 'list') continue;
    const column = (label: MdNode, list: MdNode): MdNode => ({ type: 'side', data: { hName: 'div' }, children: [{ ...label, data: { hProperties: { className: ['md-pair-label'] } } }, list] });
    kids.splice(i, 4, { type: 'pair', data: { hName: 'div', hProperties: { className: ['md-pair'] } }, children: [column(kids[i], kids[i + 1]), column(kids[i + 2], kids[i + 3])] });
  }
}

const LEADS = new Set(['tl;dr', 'tldr', 'summary', 'result', 'in short', 'bottom line']);

/** A reply that opens `**TL;DR:** …` (Summary, Result, …) in one line: the gist set apart above the detail, its label as a caption. */
function summaryLead(tree: MdNode) {
  const para = tree.children?.[0];
  const row = labelled(para);
  if (!row?.value.length || !LEADS.has(plain(row.label).trim().toLowerCase())) return;
  // A paragraph of `**Label:** value` lines is facts, not a lead.
  if (lines(para!.children ?? []).length > 1) return;
  tree.children![0] = {
    type: 'lead',
    data: { hName: 'div', hProperties: { className: ['md-lead'] } },
    children: [
      { type: 'paragraph', data: { hProperties: { className: ['md-lead-label'] } }, children: row.label },
      { type: 'paragraph', children: row.value },
    ],
  };
}

/**
 * The remark plugin; run it only on text that has finished arriving, so a block changes shape once.
 * `first`: whether this text opens the reply (a streamed reply renders in blocks, and only the first may hold its lead).
 */
export function remarkRich(options: { first?: boolean } = {}) {
  const visit = (node: MdNode) => {
    node.children?.forEach(visit);
    if (node.type === 'blockquote') alert(node);
    if (node.type === 'paragraph') tally(node);
    // Lists from their parent, so a list sees the line before it ("pick one:").
    node.children?.forEach((list, i) => {
      if (list.type !== 'list' || statusList(list) || severityList(list) || fileList(list) || timeline(list) || choices(list, node.children![i - 1]) || factList(list)) return;
      steps(list);
      tasks(list);
    });
  };
  return (tree: MdNode) => {
    visit(tree);
    if (options.first !== false) summaryLead(tree);
    numberedSteps(tree);
    const paragraphs = (node: MdNode) => {
      // A facts grid's own rows are done; a value that opens in bold is not another fact.
      if (node.data?.hName === 'dl' || !node.children) return;
      pairs(node);
      factParagraphs(node);
      node.children.forEach(paragraphs);
    };
    paragraphs(tree);
    heads(tree);
  };
}

/** One command of a shell fence and the output lines under it. */
export interface Command { command: string; output: string[] }

/**
 * A shell fence written as a session: its first line and at least one more start with `$ `, each
 * command followed by its output lines (blank lines dropped). Null for any other fence.
 */
export function commandsOf(text: string): Command[] | null {
  const lines = text.replace(/\n$/, '').split('\n');
  if (!lines[0]?.startsWith('$ ')) return null;
  const out: Command[] = [];
  for (const line of lines) {
    if (line.startsWith('$ ')) out.push({ command: line.slice(2), output: [] });
    else if (line.trim()) out.at(-1)!.output.push(line);
  }
  return out.length >= 2 ? out : null;
}

/** A long reply's title and its sections, for an index above them. */
export interface Outline { title: string; sections: string[] }

/** Heading text as it reads: emphasis, code marks and link targets dropped. */
const headingText = (raw: string) => raw.replace(/\s+#+\s*$/, '').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1').replace(/[*_`]/g, '').trim();

/** A reply whose first heading is a `# Title` followed by three or more `## Section` headings (outside code fences): its outline; else null. */
export function outlineOf(text: string): Outline | null {
  let title: string | null = null;
  const sections: string[] = [];
  let fence: string | null = null;
  for (const line of text.split('\n')) {
    const f = /^ {0,3}(`{3,}|~{3,})/.exec(line);
    if (f && (!fence || f[1].startsWith(fence))) fence = fence ? null : f[1];
    if (fence || f) continue;
    const h = /^(#{1,6}) (.+)$/.exec(line);
    if (!h || !headingText(h[2])) continue;
    if (title === null) {
      if (h[1] !== '#') return null;
      title = headingText(h[2]);
    } else if (h[1] === '##') sections.push(headingText(h[2]));
  }
  return title !== null && sections.length >= 3 ? { title, sections } : null;
}
