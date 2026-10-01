// Rules for the finish card and the "Since you left" strip: what a finished turn can show as
// evidence (the checks it ran, their exit status, the files it edited) and which sentences of
// its final message claim something that evidence should back. Deterministic text rules only,
// never a model. They lean towards under-claiming: anything unclear reads "Not verified".
// No DOM, so the unit tests run in node.
import type { Interaction, Item, TurnTiming } from '../api';
import { CHANGE_TOOLS, formatMs, toolKind, toolLabel } from './transcript.ts';

export type CheckKind = 'test' | 'build' | 'lint' | 'vet' | 'typecheck';

/** Plain words for a check row: what the agent did. */
export const CHECK_TEXT: Record<CheckKind, string> = {
  test: 'Ran the tests',
  build: 'Ran the build',
  lint: 'Ran the linter',
  vet: 'Ran go vet',
  typecheck: 'Ran the type check',
};

/** The noun a "no … in this turn" reason uses. */
const CHECK_NOUN: Record<CheckKind, string> = { test: 'test run', build: 'build', lint: 'lint run', vet: 'vet run', typecheck: 'type check' };

const PM = '(?:npm|pnpm|yarn|bun)(?:\\s+run)?';
/** A command segment that starts with one of these is that kind of check; the first match wins. */
const CHECKS: [CheckKind, RegExp][] = [
  ['vet', /^go\s+vet\b/],
  ['typecheck', new RegExp(`^(?:tsc\\b|npx\\s+tsc\\b|mypy\\b|pyright\\b|cargo\\s+check\\b|${PM}\\s+(?:typecheck|type-check|tsc|check-types)\\b)`)],
  ['lint', new RegExp(`^(?:eslint\\b|npx\\s+eslint\\b|golangci-lint\\b|staticcheck\\b|ruff\\b|flake8\\b|pylint\\b|shellcheck\\b|cargo\\s+clippy\\b|prettier\\s+.*--check\\b|npx\\s+prettier\\s+.*--check\\b|gofmt\\s+-l\\b|make\\s+lint\\b|${PM}\\s+lint\\b)`)],
  ['test', new RegExp(`^(?:go\\s+test\\b|cargo\\s+test\\b|(?:python3?\\s+-m\\s+)?pytest\\b|npx\\s+(?:jest|vitest|playwright\\s+test)\\b|jest\\b|vitest\\b|node\\s+(?:--\\S+\\s+)*--test\\b|deno\\s+test\\b|bun\\s+test\\b|dotnet\\s+test\\b|mvn\\s+(?:\\S+\\s+)*test\\b|(?:\\.\\/)?gradlew?\\s+test\\b|rspec\\b|phpunit\\b|tox\\b|ctest\\b|make\\s+test\\b|${PM}\\s+test(?::\\S+)?\\b)`)],
  ['build', new RegExp(`^(?:go\\s+build\\b|cargo\\s+build\\b|vite\\s+build\\b|npx\\s+vite\\s+build\\b|dotnet\\s+build\\b|mvn\\s+(?:\\S+\\s+)*(?:package|install|verify)\\b|(?:\\.\\/)?gradlew?\\s+build\\b|make(?:\\s+build)?\\s*$|${PM}\\s+build\\b)`)],
];

/** A shell command split at `&&`, `||`, `;` and line breaks; each part keeps its pipes. */
function segments(command: string): string[] {
  return command.split(/&&|\|\||;|\n/).map((s) => s.trim()).filter(Boolean);
}

/** A segment without its leading `VAR=value` assignments and `cd`, `time`, `timeout N` or `sudo` wrappers. */
function bare(segment: string): string {
  let s = segment.replace(/^\(+/, '').trim();
  for (;;) {
    const next = s.replace(/^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+|time\s+|sudo\s+|timeout\s+\S+\s+|env\s+)/, '');
    if (next === s) return s;
    s = next;
  }
}

/** What a check is and whether its exit status is its own: a pipe hands the status to the pipe's last command. */
export function checkOf(command: string): { kind: CheckKind; piped: boolean } | null {
  const pipefail = /pipefail/.test(command);
  for (const segment of segments(command)) {
    const [head, ...rest] = segment.split('|');
    const s = bare(head);
    for (const [kind, re] of CHECKS) if (re.test(s)) return { kind, piped: rest.length > 0 && !pipefail };
  }
  return null;
}

/** The exit code Copilot's shell tool appends to its output (`<shellId: 0 completed with exit code 1>`); null when absent. */
export function exitCode(output: string | undefined): number | null {
  const m = /(?:exited|completed) with exit code (\d+)>?\s*$/.exec(output?.trimEnd() ?? '');
  return m ? Number(m[1]) : null;
}

/** What a test or build output says it counted; null when nothing recognisable. */
export interface Counts {
  passed: number;
  failed: number;
  /** "2 packages ok", "14 passed, 1 failed". */
  label: string;
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

/** Counts from Go, Jest, Vitest, pytest, cargo and TAP (node --test) summaries. */
export function countsOf(output: string | undefined): Counts | null {
  if (!output) return null;
  const sum = (re: RegExp) => [...output.matchAll(re)].reduce((n, m) => n + Number(m[1]), 0);
  // cargo: one "test result:" line per test binary.
  if (/test result: (?:ok|FAILED)\./.test(output)) {
    const passed = sum(/test result: \w+\. (\d+) passed/g), failed = sum(/test result: \w+\. \d+ passed; (\d+) failed/g);
    return { passed, failed, label: `${passed} passed${failed ? `, ${failed} failed` : ''}` };
  }
  // Jest ("Tests: 1 failed, 12 passed, 13 total") and Vitest ("Tests  1 failed | 12 passed (13)").
  const js = /^\s*Tests:?\s+(.*)$/m.exec(output);
  if (js && /\d+ (?:passed|failed)/.test(js[1])) {
    const passed = Number(/(\d+) passed/.exec(js[1])?.[1] ?? 0), failed = Number(/(\d+) failed/.exec(js[1])?.[1] ?? 0);
    return { passed, failed, label: `${passed} passed${failed ? `, ${failed} failed` : ''}` };
  }
  // pytest: "===== 3 passed, 1 failed in 0.12s =====".
  const py = /^=+ (.*\b(?:passed|failed)\b.*) in [\d.]+s(?: \([^)]*\))? =+$/m.exec(output);
  if (py) {
    const passed = Number(/(\d+) passed/.exec(py[1])?.[1] ?? 0), failed = Number(/(\d+) failed/.exec(py[1])?.[1] ?? 0);
    return { passed, failed, label: `${passed} passed${failed ? `, ${failed} failed` : ''}` };
  }
  // TAP summary (node --test).
  if (/^# pass \d+/m.test(output)) {
    const passed = sum(/^# pass (\d+)/gm), failed = sum(/^# fail (\d+)/gm);
    return { passed, failed, label: `${passed} passed${failed ? `, ${failed} failed` : ''}` };
  }
  // go test: "ok  <pkg>" and "FAIL <pkg>" per package; "--- FAIL:" per test.
  const ok = output.match(/^ok[ \t]+\S+/gm)?.length ?? 0;
  const failedPkgs = output.match(/^FAIL[ \t]+\S+[ \t]/gm)?.length ?? 0;
  if (ok || failedPkgs) {
    const failedTests = output.match(/^\s*--- FAIL:/gm)?.length ?? 0;
    const parts = [ok && `${plural(ok, 'package')} ok`, failedPkgs && `${plural(failedPkgs, 'package')} failed`, failedTests && `${plural(failedTests, 'test')} failed`].filter(Boolean);
    return { passed: ok, failed: failedPkgs + failedTests, label: parts.join(', ') };
  }
  return null;
}

/** How a check came out: `pass` and `fail` only on a clear exit status (or, through a pipe, clear counts). */
export type Outcome = 'pass' | 'fail' | 'unclear' | 'running';

export interface Check {
  item: Item;
  kind: CheckKind;
  command: string;
  outcome: Outcome;
  exit: number | null;
  counts: Counts | null;
  /** "1.2s"; null when the call's end is not recorded. */
  took: string | null;
  /** Why the outcome is unclear, in plain words; empty otherwise. */
  note: string;
}

/** A check from its tool item and, when the item is compact, its whole body (`body`); null for anything that is no check. */
export function checkFrom(item: Item, body?: Item): Check | null {
  const t = item.tool;
  if (item.kind !== 'tool' || !t || toolKind(t.name) !== 'command') return null;
  const command = toolLabel(body?.tool ?? t).arg;
  const found = checkOf(command);
  if (!found) return null;
  const output = (body ?? item).tool?.output;
  const exit = exitCode(output);
  const counts = countsOf(output);
  const took = item.ended_at ? formatMs(Date.parse(item.ended_at) - Date.parse(item.time)) : null;
  const base = { item, kind: found.kind, command, exit, counts, took };
  if (t.status === 'pending' || t.status === 'running') return { ...base, outcome: 'running', note: 'Still running' };
  if (found.piped) {
    // The status is the pipe's last command's; only the output's own counts can say more.
    if (counts?.failed) return { ...base, outcome: 'fail', note: '' };
    if (counts && counts.passed > 0) return { ...base, outcome: 'pass', note: 'passed by its output' };
    return { ...base, outcome: 'unclear', note: output === undefined ? 'output not loaded' : 'piped, so the exit status is the last command’s' };
  }
  if (exit !== null) return { ...base, outcome: exit === 0 ? 'pass' : 'fail', note: '' };
  if (t.status === 'failed') return { ...base, outcome: 'fail', note: '' };
  return { ...base, outcome: 'unclear', note: output === undefined ? 'output not loaded' : 'no exit status recorded' };
}

/** A path relative to the Task's folder when it is inside it. */
export function relative(path: string, workdir: string): string {
  const root = workdir.replace(/\/+$/, '');
  return root && path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path;
}

/** The files a patch's headers name (`*** Update File: path`); the input may be the patch as a JSON string. */
function patchPaths(input: string | undefined): string[] {
  let patch = input ?? '';
  if (patch.trimStart().startsWith('"')) {
    try { patch = JSON.parse(patch) as string; } catch { return []; }
  }
  return [...patch.matchAll(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/gm)].map((m) => m[1].trim());
}

/**
 * The distinct paths the items' completed edit, write, create and apply_patch calls named,
 * relative to the Task's folder, in order. A compact patch call carries its paths in
 * `file_paths`; a whole one names them in its patch.
 */
export function editedPaths(items: readonly Item[], workdir: string): string[] {
  const out: string[] = [];
  for (const item of items) {
    const t = item.tool;
    const name = t?.name.toLowerCase() ?? '';
    if (item.kind !== 'tool' || t?.status !== 'completed' || !(CHANGE_TOOLS.includes(name) || name === 'apply_patch')) continue;
    const paths = name === 'apply_patch' ? (t.file_paths ?? patchPaths(t.input)) : [t.path || toolLabel(t).arg];
    for (const path of paths) {
      const rel = path && relative(path, workdir);
      if (rel && !out.includes(rel)) out.push(rel);
    }
  }
  return out;
}

const DOC_PATH = /(?:^|\/)(?:README|CHANGELOG|CONTRIBUTING)[^/]*$|\.(?:md|mdx|rst|adoc)$|(?:^|\/)docs?\//i;

export type ClaimTopic = CheckKind | 'docs';

export interface Claim {
  /** The sentence as the agent wrote it, without Markdown marks. */
  text: string;
  topics: ClaimTopic[];
  verified: boolean;
  /** What backs it ("go test ./... · exit 0", "edited README.md") or why it is not verified. */
  detail: string;
}

/**
 * Which topics a sentence claims went well. A claim is a positive statement about tests,
 * the build, linting, vetting, type checks or docs; a sentence with a negation or a failure
 * word is no claim (the evidence rows already show failures), so nothing is read into it.
 */
export function claimTopics(sentence: string): ClaimTopic[] {
  const s = sentence.toLowerCase();
  if (/\b(?:not|no|never|didn['’]t|don['’]t|couldn['’]t|can['’]t|cannot|unable|fail(?:s|ed|ing|ure|ures)?|error(?:s)?|broken|skipp(?:ed|ing))\b/.test(s)
    && !/\b(?:no|without) (?:\w+ )?(?:issues|warnings|errors|problems|findings)\b/.test(s)) return [];
  const good = /\b(?:pass(?:es|ed|ing)?|green|succeed(?:s|ed)?|successful(?:ly)?|clean(?:ly)?|ok|(?:no|without) (?:\w+ )?(?:issues|warnings|errors|problems|findings))\b/.test(s);
  const out: ClaimTopic[] = [];
  if (good && /\b(?:tests?|test suite|specs?)\b/.test(s)) out.push('test');
  if ((good && /\b(?:build|builds|built)\b/.test(s)) || /\bcompiles?\b|\bcompiled (?:successfully|cleanly)\b/.test(s)) out.push('build');
  if (good && /\b(?:go vet|vet)\b/.test(s)) out.push('vet');
  else if (good && /\b(?:lint|linter|linters|linting|eslint|golangci-lint|clippy|ruff)\b/.test(s)) out.push('lint');
  if (good && /\b(?:type[- ]?check(?:s|ed|ing)?|types? check|tsc|mypy|pyright)\b/.test(s)) out.push('typecheck');
  if (/\b(?:readme|changelog|docs?|documentation|[\w-]+\.(?:md|mdx|rst))\b/.test(s)
    && /\b(?:updat(?:e|es|ed)|add(?:s|ed)?|document(?:s|ed)?|describ(?:e|es|ed)|mention(?:s|ed)?|not(?:e|es|ed)|wrote|written|rewr(?:ote|itten)|fix(?:es|ed)?|chang(?:e|es|ed)|explain(?:s|ed)?|cover(?:s|ed)?)\b/.test(s)) out.push('docs');
  return out;
}

/** The final message's sentences: prose only (code blocks and inline code marks out), list marks and headings stripped. */
export function sentences(text: string): string[] {
  const prose = text.replace(/```[\s\S]*?(?:```|$)/g, '\n').replace(/`([^`\n]*)`/g, '$1');
  return prose
    .split(/\n+/)
    .map((line) => line.replace(/^\s*(?:#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)/, '').replace(/\*\*|__/g, '').trim())
    .flatMap((line) => line.split(/(?<=[.!?])(?<!\b(?:e\.g|i\.e|etc|vs)\.|\.\.\.)\s+(?=[\w`"'(])/))
    .map((s) => s.trim())
    .filter((s) => s.length > 3 && s.length <= 400);
}

/** The checks that back a claim topic. */
const BACKERS: Record<CheckKind, CheckKind[]> = { test: ['test'], build: ['build', 'typecheck'], lint: ['lint', 'vet'], vet: ['vet'], typecheck: ['typecheck', 'build'] };

/**
 * Each claim in the final message against the turn's evidence. A check topic is verified by
 * the last check of a backing kind passing with no file other than a doc edited after it; a docs topic by an
 * edit to a doc file (README, CHANGELOG, *.md, docs/…), the named one when the sentence names
 * one. A sentence with several topics is verified only when every topic is.
 */
export function judgeClaims(text: string, checks: readonly Check[], turn: readonly Item[], workdir: string): Claim[] {
  const index = new Map(turn.map((item, i) => [item.id, i]));
  // A doc edit after a run leaves the run's result standing; any other edit makes it stale.
  const lastEdit = turn.reduce((at, item, i) => (editedPaths([item], workdir).some((p) => !DOC_PATH.test(p)) ? i : at), -1);
  const docs = editedPaths(turn, workdir).filter((p) => DOC_PATH.test(p));
  const out: Claim[] = [];
  for (const sentence of sentences(text)) {
    const topics = claimTopics(sentence);
    if (!topics.length) continue;
    const backs: string[] = [];
    let reason = '';
    for (const topic of topics) {
      if (topic === 'docs') {
        const named = [...sentence.matchAll(/\b(README|CHANGELOG|CONTRIBUTING|[\w./-]+\.(?:md|mdx|rst))\b/gi)].map((m) => m[1]);
        const stems = named.map((n) => (n.split('/').pop() ?? n).toLowerCase().replace(/\.(?:md|mdx|rst)$/, ''));
        const hit = docs.find((p) => !stems.length || stems.some((n) => (p.split('/').pop() ?? '').toLowerCase().startsWith(n)));
        if (hit) backs.push(`edited ${hit}`);
        else reason ||= named.length ? `no edit to ${named[0]} in this turn` : 'no doc file edited in this turn';
        continue;
      }
      const last = checks.filter((c) => BACKERS[topic].includes(c.kind)).at(-1);
      if (!last) reason ||= `no ${CHECK_NOUN[topic]} in this turn`;
      else if (last.outcome === 'fail') reason ||= `the last run failed${last.exit !== null ? ` (exit ${last.exit})` : ''}`;
      else if (last.outcome !== 'pass') reason ||= `the last run’s result is unclear`;
      else if ((index.get(last.item.id) ?? -1) < lastEdit) reason ||= 'files changed after the last run';
      else backs.push(`${last.command} · ${last.exit !== null ? `exit ${last.exit}` : last.note}`);
    }
    out.push(reason ? { text: sentence, topics, verified: false, detail: `Not verified · ${reason}` } : { text: sentence, topics, verified: true, detail: backs.join(' · ') });
  }
  return out;
}

/** The finished turn's items: from its prompt (the last message that was not a steer) to the end. */
export function lastTurn(items: readonly Item[]): Item[] {
  for (let i = items.length - 1; i >= 0; i--) if (items[i].kind === 'user' && !items[i].delivery && !items[i].agent_id) return items.slice(i);
  return [...items];
}

/** The turn's final message: its last main-agent reply with text; empty when there is none. */
export function finalMessage(turn: readonly Item[]): string {
  for (let i = turn.length - 1; i >= 0; i--) if (turn[i].kind === 'assistant' && !turn[i].agent_id && turn[i].text?.trim()) return turn[i].text!;
  return '';
}

export interface Since {
  /** "the agent finished, ran the tests, and changed 3 files". */
  text: string;
  /** The main-agent items after the mark, oldest first: the jump goes to the first one on screen. */
  ids: string[];
}

/** "a", "a and b", "a, b, and c". */
function series(parts: string[]): string {
  if (parts.length < 3) return parts.join(' and ');
  return `${parts.slice(0, -1).join(', ')}, and ${parts.at(-1)}`;
}

/**
 * What changed in the Task after `mark` (the last update the owner saw) and up to `until`
 * (when they came back; what happens while they watch is seen), from its items and turns:
 * turns that finished or failed, commands run (the tests named), files edited and questions
 * asked. Null when nothing countable happened.
 */
export function sinceYouLeft(items: readonly Item[], timings: readonly TurnTiming[], interactions: readonly Interaction[], mark: string, until: string, workdir: string, working: boolean): Since | null {
  const since = Date.parse(mark), end = Date.parse(until);
  const after = (at: string | undefined) => { const t = at ? Date.parse(at) : NaN; return t > since && t <= end; };
  const fresh = items.filter((item) => !item.agent_id && after(item.time));
  const ended = timings.filter((t) => after(t.ended_at));
  const parts: string[] = [];
  const last = ended.at(-1);
  if (working) parts.push('the agent is still working');
  else if (last?.state === 'completed') parts.push(ended.filter((t) => t.state === 'completed').length > 1 ? `the agent finished ${ended.filter((t) => t.state === 'completed').length} turns` : 'the agent finished');
  else if (last?.state === 'failed') parts.push('the turn failed');
  else if (last?.state === 'cancelled') parts.push('the turn was stopped');
  const commands = fresh.filter((item) => item.kind === 'tool' && toolKind(item.tool?.name ?? '') === 'command');
  const tests = commands.filter((item) => checkOf(toolLabel(item.tool).arg)?.kind === 'test').length;
  const other = commands.length - tests;
  if (tests) parts.push(other ? `ran the tests plus ${plural(other, 'other command')}` : 'ran the tests');
  else if (other) parts.push(`ran ${plural(other, 'command')}`);
  const files = editedPaths(fresh, workdir).length;
  if (files) parts.push(`changed ${plural(files, 'file')}`);
  const asked = new Set([
    ...fresh.filter((item) => item.kind === 'tool' && toolKind(item.tool?.name ?? '') === 'question').map((item) => item.id),
    ...interactions.filter((ix) => ix.kind === 'question' && after(ix.time)).map((ix) => ix.tool_call_id || ix.id),
  ]).size;
  if (asked) parts.push(asked === 1 ? 'asked you a question' : `asked you ${asked} questions`);
  // A turn end alone is no news: its record can land a moment after the last update the owner saw.
  if (!parts.length || !fresh.length) return null;
  return { text: series(parts), ids: fresh.map((item) => item.id) };
}

/** "<1 min", "42 min", "3 h", "2 days" since an ISO time. */
export function away(mark: string, now: number): string {
  const min = Math.floor((now - Date.parse(mark)) / 60000);
  if (!Number.isFinite(min) || min < 1) return '<1 min';
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h} h`;
  return `${Math.floor(h / 24)} days`;
}
