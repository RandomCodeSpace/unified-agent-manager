// Rich blocks from plain Markdown (DESIGN.md Rich replies): fixed rules that match exact
// Markdown shapes and mark them for the renderer. The text itself is never changed, so the
// stored reply, Copy and the model's history keep the Markdown as written; only the view differs.
// A shape that does not match exactly renders as ordinary Markdown.

interface MdNode {
  type: string;
  value?: string;
  ordered?: boolean | null;
  checked?: boolean | null;
  children?: MdNode[];
  data?: { hName?: string; hProperties?: Record<string, unknown> };
}

export const CALLOUTS = ['note', 'tip', 'important', 'warning', 'caution'] as const;
export type CalloutKind = (typeof CALLOUTS)[number];

const ALERT = /^\[!(note|tip|important|warning|caution)\][ \t]*(?:\r?\n|$)/i;
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

/**
 * Every item `**Label:** value` (colon inside or right after the bold): a list of facts, as a
 * label/value grid; two to four facts that are each one short figure: stat tiles.
 */
function facts(list: MdNode) {
  const items = list.children ?? [];
  if (list.ordered || items.length < 2 || items.some((i) => i.checked != null)) return false;
  const rows: { label: MdNode[]; value: MdNode[]; rest: MdNode[] }[] = [];
  for (const item of items) {
    const lead = boldLead(item);
    if (!lead) return false;
    const after = item.children![0].children!.slice(1);
    const label = [...(lead.children ?? [])];
    const last = label.at(-1);
    let value: MdNode[];
    if (last?.type === 'text' && last.value?.trimEnd().endsWith(':')) {
      label[label.length - 1] = { ...last, value: last.value.trimEnd().slice(0, -1) };
      value = after;
    } else if (after[0]?.type === 'text' && after[0].value?.startsWith(':')) {
      value = [{ ...after[0], value: after[0].value.slice(1) }, ...after.slice(1)];
    } else return false;
    if (value[0]?.type === 'text') value[0] = { ...value[0], value: value[0].value!.trimStart() };
    value = value.filter((n) => n.type !== 'text' || n.value);
    const rest = item.children!.slice(1);
    if (!plain(label).trim() || (!value.length && !rest.length)) return false;
    rows.push({ label, value, rest });
  }
  const tiles = rows.length <= 4 && rows.every((r) => !r.rest.length && r.value.length === 1 && r.value[0].type === 'text' && FIGURE.test(r.value[0].value!.trim()));
  markProps(list, { className: tiles ? ['md-facts', 'md-tiles'] : ['md-facts'] }, 'dl');
  rows.forEach((row, i) => {
    const item = items[i];
    item.data = { ...item.data, hName: 'div' };
    item.children = [
      { type: 'paragraph', data: { hName: 'dt' }, children: row.label },
      { type: 'paragraph', data: { hName: 'dd' }, children: [...row.value, ...row.rest] },
    ];
  });
  return true;
}

/** A numbered list whose every item opens with bold text: steps. */
function steps(list: MdNode) {
  const items = list.children ?? [];
  if (list.ordered && items.length >= 2 && items.every(boldLead)) markProps(list, { className: ['md-steps'] });
}

/** A task list: how many of its items are done, for the progress line above it. */
function tasks(list: MdNode) {
  const items = list.children ?? [];
  if (!items.length || items.some((i) => i.checked == null)) return;
  markProps(list, { dataDone: items.filter((i) => i.checked).length, dataTotal: items.length });
}

/** The remark plugin; run it only on text that has finished arriving, so a block changes shape once. */
export function remarkRich() {
  const visit = (node: MdNode) => {
    node.children?.forEach(visit);
    if (node.type === 'blockquote') alert(node);
    else if (node.type === 'list' && !facts(node)) {
      steps(node);
      tasks(node);
    }
  };
  return (tree: MdNode) => visit(tree);
}
