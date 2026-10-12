import type { Item } from '../api';
import { mainArgument, patchPaths, toolKind } from './transcript.ts';

// Receipts (DESIGN.md Receipts): what a turn's last reply claims, checked against what the
// turn's record shows. The reply names paths and commands in code spans and says whether tests
// pass; the main agent's tool calls say what was changed, viewed and run, and how each run
// ended. Deterministic: text and record in, stamps out. Never a model.

/** `pending`: the evidence is on its way (a call's whole output is being read); never a final verdict. */
export type Verdict = 'verified' | 'unseen' | 'contradicted' | 'pending';

export interface Stamp {
  /** What the reply said, as written: a path, a command, or the words that claimed the tests pass. */
  claim: string;
  kind: 'path' | 'command' | 'tests';
  verdict: Verdict;
  /** What the record says, in a few words. */
  note: string;
  /** The tool call that is the evidence, when one is. */
  itemId?: string;
  /** A pending stamp's call, whose whole body `settle` judges once read. */
  awaits?: Item;
}

/** Stamps shown at first; the rest wait behind "Show N more" rather than crowd the reply. */
export const MAX_STAMPS = 12;

const EXTENSIONS = /\.(go|ts|tsx|js|mjs|cjs|jsx|py|rs|rb|php|java|kt|kts|swift|c|h|cc|cpp|hpp|cs|md|mdx|json|jsonc|yaml|yml|toml|ini|cfg|css|scss|html|htm|sh|bash|zsh|sql|txt|csv|env|lock|xml|proto|graphql|tf|dockerfile|mod|sum|work)$/i;
const COMMAND_HEADS = new Set(['go', 'npm', 'npx', 'pnpm', 'yarn', 'bun', 'deno', 'node', 'make', 'pytest', 'python', 'python3', 'pip', 'uv', 'cargo', 'rustc', 'git', 'gh', 'rtk', 'vitest', 'jest', 'eslint', 'tsc', 'prettier', 'ruff', 'black', 'mypy', 'golangci-lint', 'gofmt', 'goimports', 'staticcheck', 'mvn', 'gradle', './gradlew', 'dotnet', 'docker', 'kubectl', 'helm', 'terraform', 'bash', 'sh', 'ls', 'cat', 'rg', 'grep', 'find', 'curl', 'wget', 'sed', 'awk', 'head', 'tail', 'wc', 'diff', 'uname', 'date', 'df', 'du', 'ps', 'sleep', 'echo', 'cd', 'cp', 'mv', 'rm', 'mkdir', 'touch', 'chmod', 'tar', 'zip', 'unzip', 'ssh', 'scp', 'rsync', 'sudo', 'env', 'export', 'which', 'tree']);
/** A command that runs tests, anywhere in a shell line: its head after the shell's own joiners. */
const TEST_COMMAND = /(^|[;&|(]\s*|\bthen\s+|\bdo\s+)(?:rtk\s+)?(?:go\s+test|npm\s+(?:run\s+)?test|pnpm\s+(?:run\s+)?test|yarn\s+(?:run\s+)?test|bun\s+test|npx\s+(?:vitest|jest|playwright\s+test|mocha)|vitest|jest|pytest|python3?\s+-m\s+pytest|cargo\s+test|make\s+test|node\s+--test|dotnet\s+test|mvn\s+(?:test|verify)|gradle\s+test|\.\/gradlew\s+test|rspec|phpunit|ctest)\b/;
/** The words that claim the tests pass, in prose outside code spans. */
const TESTS_PASS = /\b(?:all\s+)?(?:\d+\s+)?(?:unit\s+|integration\s+|e2e\s+)?tests?\s+(?:(?:are|is|now|still|all)\s+)*(?:pass(?:es|ed|ing)?|green|succeed(?:s|ed)?)\b|\btest\s+suites?\s+(?:(?:is|are|now)\s+)*(?:pass(?:es|ed|ing)?|green)\b|\b(?:\d+)\s+passed\b(?!\s*,?\s*\d+\s+failed)|\b(?:everything|they)\s+pass(?:es|ed)\b/i;
/** A single pipe: the shell reports the last command's exit, not the test runner's. */
const PIPED = /[^|]\|[^|]/;

/** Test output that says some test failed, whatever the exit code. */
const FAILED_OUTPUT = /\b([1-9]\d*)\s+failed\b|^(?:FAIL|--- FAIL|FAILED)\b/m;

const CODE_SPAN = /`([^`\n]{1,200})`/g;

/** A path as the record would write it: no leading "./", no line suffix, no trailing punctuation. */
function cleanPath(s: string): string {
  return s.replace(/^\.\//, '').replace(/:\d+(?:[-–:]\d+)?$/, '').replace(/[.,;:)\]]+$/, '');
}

/** Files named without an extension that still read as files. */
const BARE_FILES = /(^|\/)(Makefile|Dockerfile|Caddyfile|LICENSE|CHANGELOG|README|Gemfile|Rakefile|Procfile|Justfile)$/;

/** A file path: its last segment has a file extension (or is a known bare file name), so a model id, a directory or a package path is not one. */
function looksLikePath(s: string): boolean {
  if (/\s/.test(s) || /^(https?:|[a-z]+:\/\/)/i.test(s) || s.startsWith('-') || s.length > 200) return false;
  if (/[<>|*?"']/.test(s)) return false;
  const p = cleanPath(s);
  const last = p.slice(p.lastIndexOf('/') + 1);
  return p.length > 1 && last.length > 0 && (EXTENSIONS.test(last) || BARE_FILES.test(p));
}

function looksLikeCommand(s: string): boolean {
  const head = s.trim().split(/\s+/)[0]?.toLowerCase() ?? '';
  return COMMAND_HEADS.has(head) && !looksLikePath(s);
}

/** A command claim's `ran`: the reply says it ran the command, rather than naming or suggesting it. */
interface Claim { kind: Stamp['kind']; claim: string; ran?: boolean }

/** Past-tense verbs that say a file was changed, or a command run. */
const EDIT_VERBS = new Set(['edited', 'updated', 'changed', 'modified', 'wrote', 'rewrote', 'written', 'rewritten', 'created', 'added', 'fixed', 'patched', 'renamed', 'deleted', 'removed', 'refactored']);
const RUN_VERBS = new Set(['ran', 'reran', 're-ran', 'executed', 'invoked']);
/** Verbs that say something else was done with what follows; they end the search for an edit or a run verb. */
const OTHER_VERBS = new Set(['read', 'reads', 'viewed', 'looked', 'checked', 'opened', 'inspected', 'reviewed', 'saw', 'see', 'found', 'searched', 'grepped', 'compared', 'kept', 'left', 'consulted', 'examined', 'verified', 'confirmed', 'used', 'using', 'based', 'calls', 'imports']);
/** How near before a span its verb must stand, in words (other spans, as in a list, do not count). */
const VERB_REACH = 8;
const NEGATION = /\b(?:not|no|never|none|nothing|neither|nor|without)\b|n['’]t\b/i;
/** Positive phrases built from negative words ("no errors"). */
const NO_PROBLEM = /\b(?:no|without|zero)\s+(?:\w+\s+)?(?:issues|warnings|errors|problems|findings|failures|regressions)\b/gi;
/** After a path, an edit verb that takes it as its subject: "`a.go`: added …", "`a.go` was updated". */
const EDIT_AFTER = /^[\s:—–-]*(?:(?:is|was|were|are|has|have|been|now|also|got)\s+)*([a-z-]+)/i;

/** The verb nearest before a span in its sentence that says what was done with it, if any. */
function verbBefore(before: string): string | undefined {
  const words = before.toLowerCase().match(/[a-z][a-z'’-]*/g) ?? [];
  for (const w of words.slice(-VERB_REACH).reverse()) if (EDIT_VERBS.has(w) || RUN_VERBS.has(w) || OTHER_VERBS.has(w)) return w;
  return undefined;
}

/** The parts of a span's sentence before and after it, in `masked` (the prose with code spans blanked, so their dots end nothing). */
function sentenceAround(masked: string, start: number, end: number): [string, string] {
  let from = 0;
  for (const m of masked.slice(0, start).matchAll(/[.!?](?=\s)|[;\n]/g)) from = m.index + 1;
  const rest = masked.slice(end);
  const stop = rest.search(/[.!?](?=\s|$)|[;\n]/);
  return [masked.slice(from, start), stop < 0 ? rest : rest.slice(0, stop)];
}

/**
 * The claims a reply makes: a path in a code span that an edit verb next to it says was changed
 * ("updated `a.go`", "`a.go`: fixed …"), a command in a code span (`ran` when a run verb before it
 * says it was run), and the words that say the tests pass. A negated sentence ("No files were
 * edited") claims no edit and no run.
 */
export function claimsOf(text: string): Claim[] {
  const out: Claim[] = [];
  const seen = new Map<string, Claim>();
  const add = (kind: Stamp['kind'], claim: string, ran?: boolean) => {
    const key = `${kind}:${claim}`;
    const held = seen.get(key);
    if (held) {
      if (ran) held.ran = true;
      return;
    }
    const c: Claim = kind === 'command' ? { kind, claim, ran: !!ran } : { kind, claim };
    seen.set(key, c);
    out.push(c);
  };
  // Fenced code is the reply's own content, not a claim about the work.
  const prose = text.replace(/```[\s\S]*?```/g, ' ');
  const masked = prose.replace(CODE_SPAN, (m) => `\`${'_'.repeat(m.length - 2)}\``);
  for (const m of prose.matchAll(CODE_SPAN)) {
    const s = m[1].trim();
    const command = looksLikeCommand(s);
    if (!command && !looksLikePath(s)) continue;
    const [before, after] = sentenceAround(masked, m.index, m.index + m[0].length);
    const negated = NEGATION.test(`${before} ${after}`.replace(NO_PROBLEM, ' '));
    const verb = verbBefore(before);
    if (command) add('command', s.replace(/\s+/g, ' '), !negated && !!verb && RUN_VERBS.has(verb));
    else if (!negated && ((verb && EDIT_VERBS.has(verb)) || EDIT_VERBS.has(EDIT_AFTER.exec(after)?.[1]?.toLowerCase() ?? ''))) add('path', cleanPath(s));
  }
  const words = prose.replace(CODE_SPAN, ' ');
  const tests = TESTS_PASS.exec(words);
  if (tests) add('tests', tests[0].trim());
  return out;
}

/** `whole`: the output is all there (not held back by the compact view, not clipped), so it can decide. */
interface Run { itemId: string; item: Item; command: string; exit?: number; failed: boolean; output: string; whole: boolean; tests?: { passed: number; failed: number } }
interface Record { changed: Map<string, string>; viewed: Map<string, string>; runs: Run[]; subagents: number }

const pathTokens = (command: string) => command.split(/\s+/).filter((t) => !t.startsWith('-') && looksLikePath(t)).map(cleanPath);

/** A call's main argument; a folded row may hold no input yet, and the service's own argument for the row stands in. */
function argOf(tool: NonNullable<Item['tool']>): string {
  return (tool.input ? mainArgument(tool.name.toLowerCase(), tool.input) : '') || tool.display_arg || '';
}

function runOf(item: Item, command: string): Run {
  const tool = item.tool!;
  const exit = tool.exit_code;
  const failed = tool.status === 'failed' || (exit !== undefined && exit !== 0);
  return { itemId: item.id, item, command, exit, failed, output: tool.output ?? '', whole: !item.clipped && !(tool.has_output && !tool.output), tests: tool.tests };
}

/** What the turn's record shows, from the main agent's tool calls: paths changed and viewed (by the call that did it), the shell runs, and how many subagents it started. */
function recordOf(items: readonly Item[]): Record {
  const record: Record = { changed: new Map(), viewed: new Map(), runs: [], subagents: 0 };
  for (const item of items) {
    if (item.kind !== 'tool' || !item.tool || item.agent_id) continue;
    const { tool } = item;
    const name = tool.name.toLowerCase();
    const kind = toolKind(name);
    const arg = argOf(tool);
    for (const edit of tool.file_edits ?? []) record.changed.set(cleanPath(edit.path), item.id);
    if (name === 'apply_patch') {
      for (const p of tool.input ? patchPaths(tool.input) : arg.split(', ')) if (p) record.changed.set(cleanPath(p), item.id);
    } else if (['edit', 'write', 'create', 'str_replace_editor', 'str_replace_based_edit_tool'].includes(name)) {
      if (arg) record.changed.set(cleanPath(arg), item.id);
    } else if (kind === 'file') {
      if (arg && !record.viewed.has(cleanPath(arg))) record.viewed.set(cleanPath(arg), item.id);
    } else if (kind === 'command') {
      const command = arg.replace(/\s+/g, ' ').trim();
      if (!command) continue;
      record.runs.push(runOf(item, command));
      // What a shell read (cat, sed, head) counts as viewed.
      if (/^(?:rtk\s+)?(cat|sed|head|tail|less|bat)\b/.test(command)) for (const p of pathTokens(command)) if (!record.viewed.has(p)) record.viewed.set(p, item.id);
    } else if (kind === 'subagent') record.subagents++;
  }
  return record;
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Whether a path the reply names is the one the record holds: the same, or one the other ends with, at a boundary. */
function samePath(claim: string, recorded: string): boolean {
  if (claim === recorded) return true;
  return recorded.endsWith(`/${claim}`) || claim.endsWith(`/${recorded}`);
}

function findPath(paths: Map<string, string>, claim: string): string | undefined {
  for (const [p, id] of paths) if (samePath(claim, p)) return id;
  return undefined;
}

/** The run of a command the reply names: the last one whose line holds it whole, at a boundary, so a passing rerun stands for the command. */
function findRun(runs: Run[], claim: string): Run | undefined {
  const re = new RegExp(`(^|[\\s;&|(])${escape(claim)}($|[\\s;&|)])`);
  for (let i = runs.length - 1; i >= 0; i--) if (re.test(runs[i].command)) return runs[i];
  return undefined;
}

const exitNote = (run: Run) => (run.exit !== undefined ? `exit ${run.exit}` : run.failed ? 'failed' : 'ran');

/** A test claim against the turn's last test run. Only the service's counts, a failed exit or a whole output decide; while the output is held back the stamp waits for it, so what the page has loaded never changes the verdict. */
function testStamp(claim: string, last: Run): Stamp {
  const kind = 'tests';
  // A part of the output that already shows a failure is enough: the whole one shows it too.
  if (last.failed || (last.tests ? last.tests.failed > 0 : FAILED_OUTPUT.test(last.output))) return { claim, kind, verdict: 'contradicted', note: `last test run ${last.failed ? exitNote(last) : 'reported failures'}`, itemId: last.itemId };
  if (!last.tests && !last.whole) return { claim, kind, verdict: 'pending', note: 'reading the test output', itemId: last.itemId, awaits: last.item };
  // A piped run exits with the pipe's last command, so without output a green exit proves nothing.
  if (PIPED.test(last.command) && !last.output && !last.tests) return { claim, kind, verdict: 'unseen', note: 'piped test run, no output', itemId: last.itemId };
  return { claim, kind, verdict: 'verified', note: `${last.command.length > 40 ? `${last.command.slice(0, 40)}…` : last.command} · ${exitNote(last)}`, itemId: last.itemId };
}

/** A pending stamp judged against its call's whole body (the item route's copy): the final verdict. */
export function settle(stamp: Stamp, body: Item): Stamp {
  if (!body.tool) return { claim: stamp.claim, kind: stamp.kind, verdict: 'unseen', note: 'test output unavailable', itemId: stamp.itemId };
  // The route's copy is as whole as the record holds.
  return testStamp(stamp.claim, { ...runOf(body, argOf(body.tool).replace(/\s+/g, ' ').trim()), whole: true });
}

/**
 * The reply's claims against the turn's record, every one of them. A claim the main agent's calls
 * do not bear out is contradicted: a changed path the turn did not change, a command the reply
 * says it ran that did not run, a test claim with no passing test run. When the turn started
 * subagents, whose work is not read here, such a claim is only unseen, and the note says so. A
 * command the reply only names (it may suggest it) is unseen when the record lacks it; any
 * command is contradicted when the run it names failed.
 */
export function receipts(text: string, items: readonly Item[]): Stamp[] {
  const claims = claimsOf(text);
  if (!claims.length) return [];
  const record = recordOf(items);
  const aside = record.subagents ? ` · ${record.subagents} ${record.subagents === 1 ? 'subagent' : 'subagents'} ran` : '';
  const missing: Verdict = record.subagents ? 'unseen' : 'contradicted';
  const where = record.subagents ? ' by the main agent' : ' this turn';
  const stamps: Stamp[] = [];
  for (const { kind, claim, ran } of claims) {
    if (kind === 'path') {
      const changed = findPath(record.changed, claim);
      if (changed) stamps.push({ claim, kind, verdict: 'verified', note: 'changed', itemId: changed });
      else {
        const viewed = findPath(record.viewed, claim);
        if (viewed) stamps.push({ claim, kind, verdict: missing, note: `viewed, not changed${aside}`, itemId: viewed });
        else stamps.push({ claim, kind, verdict: missing, note: `not changed${where}${aside}` });
      }
    } else if (kind === 'command') {
      const run = findRun(record.runs, claim);
      if (run) stamps.push({ claim, kind, verdict: run.failed ? 'contradicted' : 'verified', note: exitNote(run), itemId: run.itemId });
      else stamps.push({ claim, kind, verdict: ran ? missing : 'unseen', note: `not run${where}${aside}` });
    } else {
      const last = record.runs.filter((r) => TEST_COMMAND.test(r.command)).at(-1);
      if (!last) stamps.push({ claim, kind, verdict: missing, note: `no test command ran${record.subagents ? ' in the main agent’s work' : ' this turn'}${aside}` });
      else stamps.push(testStamp(claim, last));
    }
  }
  return stamps;
}

export const allVerified = (stamps: readonly Stamp[]) => stamps.length > 0 && stamps.every((s) => s.verdict === 'verified');

/** Every claim is unseen with no evidence either way (a turn whose work subagents did): one line says so instead of a stamp each. */
export const allUnseen = (stamps: readonly Stamp[]) => stamps.length > 0 && stamps.every((s) => s.verdict === 'unseen' && !s.itemId);
