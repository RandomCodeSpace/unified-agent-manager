import type { Item } from '../api';
import { mainArgument, patchPaths, toolKind } from './transcript.ts';

// Receipts (DESIGN.md Receipts): what a turn's last reply claims, checked against what the
// turn's record shows. The reply names paths and commands in code spans and says whether tests
// pass; the main agent's tool calls say what was changed, viewed and run, and how each run
// ended. Deterministic: text and record in, stamps out. Never a model.

export type Verdict = 'verified' | 'unseen' | 'contradicted';

export interface Stamp {
  /** What the reply said, as written: a path, a command, or the words that claimed the tests pass. */
  claim: string;
  kind: 'path' | 'command' | 'tests';
  verdict: Verdict;
  /** What the record says, in a few words. */
  note: string;
  /** The tool call that is the evidence, when one is. */
  itemId?: string;
}

/** Stamps a reply gets at most; the rest of its claims are left unchecked rather than crowd the reply. */
export const MAX_STAMPS = 12;

const EXTENSIONS = /\.(go|ts|tsx|js|mjs|cjs|jsx|py|rs|rb|php|java|kt|kts|swift|c|h|cc|cpp|hpp|cs|md|mdx|json|jsonc|yaml|yml|toml|ini|cfg|css|scss|html|htm|sh|bash|zsh|sql|txt|csv|env|lock|xml|proto|graphql|tf|dockerfile|mod|sum|work)$/i;
const COMMAND_HEADS = new Set(['go', 'npm', 'npx', 'pnpm', 'yarn', 'bun', 'deno', 'node', 'make', 'pytest', 'python', 'python3', 'pip', 'uv', 'cargo', 'rustc', 'git', 'gh', 'rtk', 'vitest', 'jest', 'eslint', 'tsc', 'prettier', 'ruff', 'black', 'mypy', 'golangci-lint', 'gofmt', 'goimports', 'staticcheck', 'mvn', 'gradle', './gradlew', 'dotnet', 'docker', 'kubectl', 'helm', 'terraform', 'bash', 'sh', 'ls', 'cat', 'rg', 'grep', 'find', 'curl', 'wget', 'sed', 'awk', 'head', 'tail', 'wc', 'diff', 'uname', 'date', 'df', 'du', 'ps', 'sleep', 'echo', 'cd', 'cp', 'mv', 'rm', 'mkdir', 'touch', 'chmod', 'tar', 'zip', 'unzip', 'ssh', 'scp', 'rsync', 'sudo', 'env', 'export', 'which', 'tree']);
/** A command that runs tests, anywhere in a shell line: its head after the shell's own joiners. */
const TEST_COMMAND = /(^|[;&|(]\s*|\bthen\s+|\bdo\s+)(?:rtk\s+)?(?:go\s+test|npm\s+(?:run\s+)?test|pnpm\s+(?:run\s+)?test|yarn\s+(?:run\s+)?test|bun\s+test|npx\s+(?:vitest|jest|playwright\s+test|mocha)|vitest|jest|pytest|python3?\s+-m\s+pytest|cargo\s+test|make\s+test|node\s+--test|dotnet\s+test|mvn\s+(?:test|verify)|gradle\s+test|\.\/gradlew\s+test|rspec|phpunit|ctest)\b/;
/** The words that claim the tests pass, in prose outside code spans. */
const TESTS_PASS = /\b(?:all\s+)?(?:\d+\s+)?(?:unit\s+|integration\s+|e2e\s+)?tests?\s+(?:(?:are|is|now|still|all)\s+)*(?:pass(?:es|ed|ing)?|green|succeed(?:s|ed)?)\b|\btest\s+suites?\s+(?:(?:is|are|now)\s+)*(?:pass(?:es|ed|ing)?|green)\b|\b(?:\d+)\s+passed\b(?!\s*,?\s*\d+\s+failed)|\b(?:everything|they)\s+pass(?:es|ed)\b/i;
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

interface Claim { kind: Stamp['kind']; claim: string }

/** The claims a reply makes: code spans that read as a command or a path, and the words that say the tests pass. */
export function claimsOf(text: string): Claim[] {
  const out: Claim[] = [];
  const seen = new Set<string>();
  const add = (kind: Stamp['kind'], claim: string) => {
    const key = `${kind}:${claim}`;
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ kind, claim });
  };
  // Fenced code is the reply's own content, not a claim about the work.
  const prose = text.replace(/```[\s\S]*?```/g, ' ');
  for (const m of prose.matchAll(CODE_SPAN)) {
    const s = m[1].trim();
    if (looksLikeCommand(s)) add('command', s.replace(/\s+/g, ' '));
    else if (looksLikePath(s)) add('path', cleanPath(s));
  }
  const words = prose.replace(CODE_SPAN, ' ');
  const tests = TESTS_PASS.exec(words);
  if (tests) add('tests', tests[0].trim());
  return out;
}

interface Run { itemId: string; command: string; exit?: number; failed: boolean; output: string }
interface Record { changed: Map<string, string>; viewed: Map<string, string>; runs: Run[]; subagents: number }

const pathTokens = (command: string) => command.split(/\s+/).filter((t) => !t.startsWith('-') && looksLikePath(t)).map(cleanPath);

/** What the turn's record shows, from the main agent's tool calls: paths changed and viewed (by the call that did it), the shell runs, and how many subagents it started. */
function recordOf(items: readonly Item[]): Record {
  const record: Record = { changed: new Map(), viewed: new Map(), runs: [], subagents: 0 };
  for (const item of items) {
    if (item.kind !== 'tool' || !item.tool || item.agent_id) continue;
    const { tool } = item;
    const name = tool.name.toLowerCase();
    const kind = toolKind(name);
    // A folded row may hold no input yet; the service's own argument for the row stands in.
    const arg = (tool.input ? mainArgument(name, tool.input) : '') || tool.display_arg || '';
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
      const exit = tool.exit_code;
      const failed = tool.status === 'failed' || (exit !== undefined && exit !== 0);
      record.runs.push({ itemId: item.id, command, exit, failed, output: tool.output ?? '' });
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

/** The run of a command the reply names: the one whose line holds it whole, at a boundary. */
function findRun(runs: Run[], claim: string): Run | undefined {
  const re = new RegExp(`(^|[\\s;&|(])${escape(claim)}($|[\\s;&|)])`);
  return runs.find((r) => re.test(r.command));
}

const exitNote = (run: Run) => (run.exit !== undefined ? `exit ${run.exit}` : run.failed ? 'failed' : 'ran');

/**
 * The reply's claims against the turn's record, at most `MAX_STAMPS`. A claim the main agent's
 * calls do not bear out is contradicted, unless the turn started subagents, whose work is not
 * read here: then it is only unseen, and the note says so.
 */
export function receipts(text: string, items: readonly Item[]): Stamp[] {
  const claims = claimsOf(text);
  if (!claims.length) return [];
  const record = recordOf(items);
  const aside = record.subagents ? ` · ${record.subagents} ${record.subagents === 1 ? 'subagent' : 'subagents'} ran` : '';
  const missing: Verdict = record.subagents ? 'unseen' : 'contradicted';
  const stamps: Stamp[] = [];
  for (const { kind, claim } of claims.slice(0, MAX_STAMPS)) {
    if (kind === 'path') {
      const changed = findPath(record.changed, claim);
      if (changed) stamps.push({ claim, kind, verdict: 'verified', note: 'changed', itemId: changed });
      else {
        const viewed = findPath(record.viewed, claim);
        if (viewed) stamps.push({ claim, kind, verdict: 'unseen', note: 'viewed, not changed', itemId: viewed });
        else stamps.push({ claim, kind, verdict: 'unseen', note: `not touched${aside}` });
      }
    } else if (kind === 'command') {
      const run = findRun(record.runs, claim);
      if (run) stamps.push({ claim, kind, verdict: run.failed ? 'contradicted' : 'verified', note: exitNote(run), itemId: run.itemId });
      else stamps.push({ claim, kind, verdict: missing, note: `not run${record.subagents ? ' by the main agent' : ' this turn'}${aside}` });
    } else {
      const tests = record.runs.filter((r) => TEST_COMMAND.test(r.command));
      const last = tests.at(-1);
      if (!last) stamps.push({ claim, kind, verdict: missing, note: `no test command ran${record.subagents ? ' in the main agent’s work' : ' this turn'}${aside}` });
      else if (last.failed || FAILED_OUTPUT.test(last.output)) stamps.push({ claim, kind, verdict: 'contradicted', note: `last test run ${last.failed ? exitNote(last) : 'reported failures'}`, itemId: last.itemId });
      else stamps.push({ claim, kind, verdict: 'verified', note: `${last.command.length > 40 ? `${last.command.slice(0, 40)}…` : last.command} · ${exitNote(last)}`, itemId: last.itemId });
    }
  }
  return stamps;
}

export const allVerified = (stamps: readonly Stamp[]) => stamps.length > 0 && stamps.every((s) => s.verdict === 'verified');

/** Every claim is unseen with no evidence either way (a turn whose work subagents did): one line says so instead of a stamp each. */
export const allUnseen = (stamps: readonly Stamp[]) => stamps.length > 0 && stamps.every((s) => s.verdict === 'unseen' && !s.itemId);
