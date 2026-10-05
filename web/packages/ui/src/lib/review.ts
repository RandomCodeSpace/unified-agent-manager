// Review aids for the Changes panel: risky files first, per-file "Viewed" that clears when the
// file changes again, and line comments that go to the Task as one message. Kept per Task in
// localStorage (`uam.review.<id>`) until sent or unticked. Pure rules; the panel owns storage.

import type { ChangeFile } from '../api';

export const REVIEW_PREFIX = 'uam.review.';
export const reviewKey = (taskId: string): string => `${REVIEW_PREFIX}${taskId}`;

/**
 * The first listed path (repository-relative) that a turn edited. The turn names files as its tools did:
 * absolute, or relative to the Task's folder, which may sit below the repository root.
 */
export function turnFile(listed: readonly string[], edited: readonly string[]): string | null {
  return listed.find((p) => edited.some((e) => e === p || e.endsWith(`/${p}`) || (!e.startsWith('/') && p.endsWith(`/${e}`)))) ?? null;
}

/** Past this many changed lines a review misses more; the panel says so. */
export const LARGE_CHANGE = 400;

export interface ReviewComment {
  id: string;
  path: string;
  /** The line number on its side: `new` for added and unchanged lines, `old` for removed ones. */
  line: number;
  side: 'new' | 'old';
  /** The line's text when the comment was written, quoted in the message. */
  code: string;
  body: string;
}

export interface Review {
  /** Path → the file's digest when it was marked viewed. */
  viewed: Record<string, string>;
  comments: ReviewComment[];
}

export const emptyReview = (): Review => ({ viewed: {}, comments: [] });

export function parseReview(raw: string | null): Review {
  const out = emptyReview();
  if (!raw) return out;
  try {
    const v: unknown = JSON.parse(raw);
    if (!v || typeof v !== 'object') return out;
    const o = v as Record<string, unknown>;
    if (o.viewed && typeof o.viewed === 'object') {
      for (const [path, digest] of Object.entries(o.viewed as Record<string, unknown>)) if (typeof digest === 'string') out.viewed[path] = digest;
    }
    if (Array.isArray(o.comments)) {
      for (const c of o.comments as Record<string, unknown>[]) {
        if (c && typeof c.id === 'string' && typeof c.path === 'string' && typeof c.line === 'number' && (c.side === 'new' || c.side === 'old') && typeof c.body === 'string' && c.body.trim()) {
          out.comments.push({ id: c.id, path: c.path, line: c.line, side: c.side, code: typeof c.code === 'string' ? c.code : '', body: c.body });
        }
      }
    }
  } catch {
    // Malformed: start over.
  }
  return out;
}

/** The stored form, or null when nothing is worth keeping (the key goes). */
export function serializeReview(r: Review): string | null {
  return Object.keys(r.viewed).length || r.comments.length ? JSON.stringify(r) : null;
}

/** Review keys whose Task no longer exists. */
export function staleReviewKeys(keys: readonly string[], liveIds: Iterable<string>): string[] {
  const live = new Set(liveIds);
  return keys.filter((k) => k.startsWith(REVIEW_PREFIX) && !live.has(k.slice(REVIEW_PREFIX.length)));
}

/** What identifies a file's current diff: the service's digest, else its status and counts. */
export const fileDigest = (f: ChangeFile): string => f.digest ?? `${f.status}:${f.additions}:${f.deletions}`;

/** `changed`: marked viewed, but the file changed since. */
export function viewState(r: Review, f: ChangeFile): 'viewed' | 'changed' | 'unviewed' {
  const seen = r.viewed[f.path];
  if (seen === undefined) return 'unviewed';
  return seen === fileDigest(f) ? 'viewed' : 'changed';
}

export interface Risk {
  label: string;
  detail: string;
}

const LOCKFILES = new Set(['package-lock.json', 'npm-shrinkwrap.json', 'yarn.lock', 'pnpm-lock.yaml', 'bun.lock', 'bun.lockb', 'go.sum', 'Cargo.lock', 'Gemfile.lock', 'poetry.lock', 'uv.lock', 'Pipfile.lock', 'composer.lock', 'flake.lock']);
const DEPLOY_DIRS = /^(deploy|deploys|deployment|deployments|k8s|kubernetes|helm|charts|terraform|ansible|infra)$/i;
const DEPLOY_FILES = /^(deploy\b.*|\.goreleaser\.ya?ml|procfile|fly\.toml|vercel\.json|netlify\.toml|app\.ya?ml|serverless\.ya?ml|.*\.tf|.*\.tfvars)$/i;
// `auth` but not "author(s)"; authorization, authorize and authority count.
const AUTH = /(auth(?!ors?(?![a-z]))|login|secur|secret|crypt|permission|credential|password|csrf|oauth|jwt|saml|\bsso\b|\bacl\b)/i;
/** Files that usually hold secrets or registry tokens. */
const SECRET_FILES = /^(\.env.*|\.npmrc)$/i;

/** Why a path deserves a closer look, or null. CI and deploy files run with credentials; lockfiles hide supply-chain changes. */
export function riskOf(path: string): Risk | null {
  const parts = path.split('/');
  const base = parts.at(-1) ?? path;
  const dirs = parts.slice(0, -1);
  if (path.startsWith('.github/workflows/') || path.startsWith('.github/actions/') || path.startsWith('.circleci/') || path.startsWith('.buildkite/') || /^(\.gitlab-ci\.ya?ml|jenkinsfile|azure-pipelines\.ya?ml|\.travis\.ya?ml|bitbucket-pipelines\.ya?ml)$/i.test(base)) {
    return { label: 'CI workflow', detail: 'Runs in CI, often with repository secrets: check what it executes.' };
  }
  if (LOCKFILES.has(base)) return { label: 'Lockfile', detail: 'Pins dependency versions: check which packages changed and where they come from.' };
  if (/^(dockerfile|containerfile)(\..*)?$/i.test(base) || /\.dockerfile$/i.test(base) || /^(docker-)?compose(\..*)?\.ya?ml$/i.test(base)) {
    return { label: 'Docker', detail: 'Changes what the container image runs or contains.' };
  }
  if (dirs.some((d) => /^migrat/i.test(d)) || /^\d+.*\.sql$/i.test(base)) return { label: 'Migration', detail: 'Changes the database schema or data; it may be hard to undo.' };
  if (dirs.some((d) => DEPLOY_DIRS.test(d)) || DEPLOY_FILES.test(base)) return { label: 'Deploy', detail: 'Changes how or where the project is deployed.' };
  if (SECRET_FILES.test(base) || parts.some((p) => AUTH.test(p))) return { label: 'Auth/security', detail: 'Touches sign-in, permissions, secrets or cryptography.' };
  return null;
}

/** Risky files first, each group in the listing's order. */
export function byRisk<T extends { path: string }>(files: readonly T[]): T[] {
  return [...files.filter((f) => riskOf(f.path)), ...files.filter((f) => !riskOf(f.path))];
}

/** The letter for each status word, as git shows it; the Files tree and Changes both use it. */
const STATUS_LETTERS: Record<string, string> = { modified: 'M', added: 'A', deleted: 'D', renamed: 'R', copied: 'C', untracked: 'U', conflicted: '!' };

/** One letter for a status word ("modified" → M), as git shows it; letters pass through. */
export function statusLetter(status: string): string {
  return STATUS_LETTERS[status] ?? (status.slice(0, 1).toUpperCase() || '?');
}

/** All comments as one message to the Task, in file then line order. */
export function commentsMessage(comments: readonly ReviewComment[]): string {
  const sorted = [...comments].sort((a, b) => (a.path === b.path ? a.line - b.line : a.path < b.path ? -1 : 1));
  const lines = [sorted.length === 1 ? 'I reviewed your changes and have 1 comment. Please address it.' : `I reviewed your changes and have ${sorted.length} comments. Please address each one.`, ''];
  sorted.forEach((c, i) => {
    lines.push(`${i + 1}. ${c.path}, ${c.side === 'old' ? 'removed line' : 'line'} ${c.line}:`);
    if (c.code.trim()) lines.push(`   > ${c.code.trim()}`);
    for (const l of c.body.trim().split('\n')) lines.push(`   ${l}`);
    lines.push('');
  });
  return lines.join('\n').trimEnd();
}
