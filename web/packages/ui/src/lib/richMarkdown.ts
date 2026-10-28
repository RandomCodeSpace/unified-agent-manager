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
const FIGURE = /^[~≈<>]?[$€£]?[-+]?\d[\d,.]*(?:\s?(?:%|[a-zA-Z]{1,5}))?$/;

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

/** A paragraph that opens `**Label:** value` (the colon inside or right after the bold), split into its label and value. */
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
  } else return null;
  if (value[0]?.type === 'text') value[0] = { ...value[0], value: value[0].value!.trimStart() };
  return plain(label).trim() ? { label, value: value.filter((n) => n.type !== 'text' || n.value) } : null;
}

/** A figure split into its number and unit: "14.6 MB" → 14.6, "MB"; null when it is not one. */
function figureOf(text: string): { n: number; unit: string; decimals: number } | null {
  const m = /^([-+]?\d[\d,]*(?:\.(\d+))?)\s?(%|[a-zA-Z]{1,5})?$/.exec(text.trim());
  return m ? { n: Number(m[1].replaceAll(',', '')), unit: m[3] ?? '', decimals: m[2]?.length ?? 0 } : null;
}

/**
 * A fact's value that reads "old → new" (or "->"), as text alone: the old value struck, an arrow,
 * the new in bold, and the signed change when both are figures in one unit. Else the value as written.
 */
function change(value: MdNode[]): MdNode[] {
  if (value.length !== 1 || value[0].type !== 'text') return value;
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

/** The remark plugin; run it only on text that has finished arriving, so a block changes shape once. */
export function remarkRich() {
  const visit = (node: MdNode) => {
    node.children?.forEach(visit);
    if (node.type === 'blockquote') alert(node);
    if (node.type === 'list' && !statusList(node) && !severityList(node) && !fileList(node) && !factList(node)) {
      steps(node);
      tasks(node);
    }
  };
  return (tree: MdNode) => {
    visit(tree);
    const paragraphs = (node: MdNode) => {
      // A facts grid's own rows are done; a value that opens in bold is not another fact.
      if (node.data?.hName === 'dl' || !node.children) return;
      factParagraphs(node);
      node.children.forEach(paragraphs);
    };
    paragraphs(tree);
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
