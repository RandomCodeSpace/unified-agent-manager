import type { Item } from '../api';
import { readableInput, toolKind } from './transcript.ts';

// GitLab items a turn touched (DESIGN.md GitLab from the conversation): GitLab
// links on the Thread's own project, the merge requests, issues, epics,
// pipelines and jobs its `glab` commands name or print, the merge request a
// `git push` announces, and the pipeline of the Thread's own commit. Text in,
// references out; the service reads their state.

export type GitLabKind = 'mr' | 'issue' | 'epic' | 'pipeline' | 'job';
/** `group` is an epic's group when it is not the project's parent group. */
export interface GitLabRef { kind: GitLabKind; id: number; group?: string }
export interface GitLabOrigin { host: string; path: string }
/** The Thread's own tracked commit and its pipeline (the service's receipt). */
export interface GitLabOwnPipeline { sha: string; pipelineId: number }

/** The most items one turn lists. */
export const MAX_TURN_REFS = 20;
/** How much of one call's output is searched. */
const OUTPUT_LIMIT = 64 << 10;

/** The service's reference for an item: `mr:2`, `epic:3:group/sub`. */
export const refKey = (r: GitLabRef) => `${r.kind}:${r.id}${r.group ? `:${r.group}` : ''}`;
export const sameRef = (a?: GitLabRef | null, b?: GitLabRef | null) => !!a && !!b && refKey(a) === refKey(b);

/** A service reference back to a ref; undefined for anything else. */
export function parseRefKey(key: string): GitLabRef | undefined {
  const m = /^(mr|issue|epic|pipeline|job):([1-9]\d*)(?::(.+))?$/.exec(key);
  if (!m || (m[3] && m[1] !== 'epic')) return undefined;
  return { kind: m[1] as GitLabKind, id: Number(m[2]), ...(m[3] ? { group: m[3] } : {}) };
}

/** How GitLab writes the item: !2, #14, &3; a pipeline or job by its ID. */
export function refText(r: GitLabRef): string {
  if (r.kind === 'mr') return `!${r.id}`;
  if (r.kind === 'epic') return `&${r.id}`;
  return `#${r.id}`;
}

const parentGroup = (path: string) => path.slice(0, Math.max(0, path.lastIndexOf('/')));
const lower = (s: string) => s.toLowerCase();
const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');

function epic(id: number, group: string, origin: GitLabOrigin): GitLabRef | null {
  if (!group || !lower(origin.path).startsWith(`${lower(group)}/`)) return null;
  return lower(group) === lower(parentGroup(origin.path)) ? { kind: 'epic', id } : { kind: 'epic', id, group };
}

/** A GitLab URL of an item of the Thread's project (or, for an epic, one of its groups). */
export function parseGitLabUrl(href: string, origin: GitLabOrigin): GitLabRef | null {
  let url: URL;
  try { url = new URL(href); } catch { return null; }
  if ((url.protocol !== 'https:' && url.protocol !== 'http:') || lower(url.hostname) !== lower(origin.host)) return null;
  const path = decodeURIComponent(url.pathname).replace(/\/+$/, '');
  const group = /^\/groups\/(.+?)\/-\/(?:epics|work_items)\/(\d+)$/.exec(path);
  if (group) return epic(Number(group[2]), group[1], origin);
  const prefix = `/${origin.path}/-/`;
  if (!lower(path).startsWith(lower(prefix))) return null;
  const m = /^(merge_requests|issues|work_items|pipelines|jobs)\/(?:incident\/)?(\d+)$/.exec(path.slice(prefix.length));
  if (!m) return null;
  const id = Number(m[2]);
  if (!Number.isSafeInteger(id) || id <= 0) return null;
  const kind: GitLabKind = m[1] === 'merge_requests' ? 'mr' : m[1] === 'pipelines' ? 'pipeline' : m[1] === 'jobs' ? 'job' : 'issue';
  return { kind, id };
}

const URL_RE = /https?:\/\/[^\s<>"'`)\]]+/g;

function urlRefs(text: string, origin: GitLabOrigin): GitLabRef[] {
  const out: GitLabRef[] = [];
  for (const m of text.slice(0, OUTPUT_LIMIT).matchAll(URL_RE)) {
    const r = parseGitLabUrl(m[0].replace(/[.,;:]+$/, ''), origin);
    if (r) out.push(r);
  }
  return out;
}

/** The words of one simple command, after an `rtk` or `rtk proxy` prefix. */
function commands(script: string): string[][] {
  return script.split(/\n|&&|\|\||[;|]/).map((part) => {
    const words = part.trim().split(/\s+/).filter(Boolean).map((w) => w.replace(/^(['"])(.*)\1$/, '$2'));
    let i = 0;
    if (words[i] === 'rtk') i++;
    if (words[i] === 'proxy') i++;
    return words.slice(i);
  }).filter((words) => words.length > 0);
}

interface Glab { sub: string; verb: string; args: string[] }

/** A `glab` call on the Thread's own project; another `-R` project is not this Thread's. */
function glabCall(words: string[], origin: GitLabOrigin): Glab | null {
  if (words[0] !== 'glab') return null;
  const rest: string[] = [];
  let repo = '';
  for (let i = 1; i < words.length; i++) {
    const w = words[i];
    if (w === '-R' || w === '--repo') repo = words[++i] ?? '';
    else if (w.startsWith('--repo=')) repo = w.slice(7);
    else if (/^-R./.test(w)) repo = w.slice(2);
    else rest.push(w);
  }
  if (repo && lower(repo.replace(/^https?:\/\/[^/]+\//, '').replace(/\.git$/, '')) !== lower(origin.path)) return null;
  return rest.length ? { sub: rest[0], verb: rest[1] ?? '', args: rest.slice(2) } : null;
}

/** Flags whose next word is a value, not an item. */
const VALUE_FLAGS = new Set(['-P', '--per-page', '-p', '--page', '--pipeline-id', '-b', '--branch', '-t', '--type', '--state', '-F', '--output', '--after', '-g', '--group', '-m', '--message', '--title', '-d', '--description', '-l', '--label', '-a', '--assignee', '--jq']);

function firstNumber(args: string[]): number | undefined {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (VALUE_FLAGS.has(a)) { i++; continue; }
    if (a.startsWith('-')) continue;
    const m = /^[!#]?([1-9]\d*)$/.exec(a);
    return m ? Number(m[1]) : undefined;
  }
  return undefined;
}

function flagNumber(args: string[], names: string[]): number | undefined {
  for (let i = 0; i < args.length; i++) {
    for (const name of names) {
      const value = args[i] === name ? args[i + 1] : args[i].startsWith(`${name}=`) ? args[i].slice(name.length + 1) : undefined;
      if (value && /^[1-9]\d*$/.test(value)) return Number(value);
    }
  }
  return undefined;
}

function commandRefs(call: Glab): GitLabRef[] {
  const out: GitLabRef[] = [];
  const list = call.verb === 'list' || call.verb === 'ls';
  if ((call.sub === 'mr' || call.sub === 'issue' || call.sub === 'incident' || call.sub === 'work-items') && !list) {
    const id = firstNumber(call.args);
    // A group's work item (`-g`) is not one of this project's.
    const grouped = call.args.some((a) => a === '-g' || a === '--group' || a.startsWith('--group='));
    if (id && !(call.sub === 'work-items' && grouped)) out.push({ kind: call.sub === 'mr' ? 'mr' : 'issue', id });
  }
  if (call.sub === 'ci') {
    const pipeline = flagNumber(call.args, ['-p', '--pipeline-id']);
    if (pipeline) out.push({ kind: 'pipeline', id: pipeline });
    if (call.verb === 'trace' || call.verb === 'retry') {
      const job = firstNumber(call.args);
      if (job) out.push({ kind: 'job', id: job });
    }
  }
  return out;
}

function outputRefs(call: Glab, output: string, origin: GitLabOrigin): GitLabRef[] {
  const text = output.slice(0, OUTPUT_LIMIT);
  const out: GitLabRef[] = [];
  const project = `(?:${escape(origin.path)})?`;
  const short = (sigil: string) => new RegExp(`(?:^|[\\s(\\[,])${project}${escape(sigil)}([1-9]\\d*)\\b`, 'g');
  const numbered = (kind: GitLabKind) => { for (const m of text.matchAll(/^number:\s+([1-9]\d*)\s*$/gm)) out.push({ kind, id: Number(m[1]) }); };
  if (call.sub === 'mr') {
    for (const m of text.matchAll(short('!'))) out.push({ kind: 'mr', id: Number(m[1]) });
    numbered('mr');
  } else if (call.sub === 'issue' || call.sub === 'incident' || call.sub === 'work-items') {
    for (const m of text.matchAll(short('#'))) out.push({ kind: 'issue', id: Number(m[1]) });
    for (const m of text.matchAll(short('&'))) out.push({ kind: 'epic', id: Number(m[1]) });
    numbered('issue');
  } else if (call.sub === 'ci') {
    // "#2936820380" is a pipeline; "(#6)" is its per-project IID, not an ID.
    for (const m of text.matchAll(/(?:^|[^(\w#])#([1-9]\d*)\b/gm)) out.push({ kind: 'pipeline', id: Number(m[1]) });
    if (/^# Pipeline:/m.test(text)) for (const m of text.matchAll(/^id:\s+([1-9]\d*)\s*$/gm)) out.push({ kind: 'pipeline', id: Number(m[1]) });
  }
  return [...urlRefs(text, origin), ...out];
}

/** A short or full SHA the Thread's own commit starts with, in a `git commit` or `git push` result. */
function namesCommit(output: string, sha: string): boolean {
  for (const m of output.slice(0, OUTPUT_LIMIT).matchAll(/\b[0-9a-f]{7,64}\b/g)) if (sha.startsWith(m[0])) return true;
  return false;
}

/** A command call's script: its input, or in the compact view the argument it shows. */
function script(item: Item): string {
  const t = item.tool;
  if (item.kind !== 'tool' || !t || toolKind(t.name) !== 'command') return '';
  return t.input ? readableInput(t.name, t.input).text : t.display_arg ?? '';
}

const gitWrite = (words: string[]) => words[0] === 'git' && (words[1] === 'push' || words[1] === 'commit');

/**
 * The main agent's items of a turn that can name GitLab items: `glab` calls on the project,
 * `git commit` and `git push` calls, and replies with a link to the GitLab host. A call among
 * them whose output the view holds back needs its body read (`needsBody`).
 */
export function gitLabCandidates(items: Item[], origin: GitLabOrigin): Item[] {
  const host = lower(origin.host);
  return items.filter((item) => !item.agent_id && (
    (item.kind === 'assistant' && lower(item.text ?? '').includes(host)) ||
    commands(script(item)).some((words) => gitWrite(words) || !!glabCall(words, origin))));
}

/** A call whose output (or whole input) the compact view holds back. */
export const needsBody = (item: Item) => item.kind === 'tool' && !!item.tool && ((!!item.tool.has_output && item.tool.output === undefined) || (!!item.tool.has_input && item.tool.input === undefined));

/**
 * Every GitLab item the turn touched, in the order it touched them, each once: its main agent's
 * calls first (what each command names, then what it printed), then its replies' links.
 */
export function turnGitLabRefs(items: Item[], origin: GitLabOrigin, own?: GitLabOwnPipeline): GitLabRef[] {
  const found: GitLabRef[] = [];
  const replies: string[] = [];
  for (const item of items) {
    if (item.agent_id) continue;
    if (item.kind === 'assistant' && item.text) replies.push(item.text);
    const t = item.tool;
    if (!t) continue;
    const output = t.output ?? '';
    for (const words of commands(script(item))) {
      const call = glabCall(words, origin);
      if (call) {
        found.push(...commandRefs(call), ...outputRefs(call, output, origin));
        continue;
      }
      if (!gitWrite(words)) continue;
      if (words[1] === 'push') found.push(...urlRefs(output, origin));
      if (own && t.status === 'completed' && namesCommit(output, own.sha)) found.push({ kind: 'pipeline', id: own.pipelineId });
    }
  }
  for (const text of replies) found.push(...urlRefs(text, origin));
  const seen = new Set<string>();
  return found.filter((r) => {
    const key = refKey(r);
    if (seen.has(key) || !Number.isSafeInteger(r.id)) return false;
    seen.add(key);
    return true;
  }).slice(0, MAX_TURN_REFS);
}
