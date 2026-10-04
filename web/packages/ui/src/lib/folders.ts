// Path and breadcrumb logic for the folder picker (Add project → Browse). Pure, so the
// tests in tests/folders.test.mjs cover it without a DOM.

export interface Crumb {
  /** Display: `/` for the root, else the path element. */
  name: string;
  path: string;
}

/**
 * Turns what was typed into the clean, absolute form the server accepts: repeated and
 * trailing slashes go, `.` and `..` resolve. A relative or empty value comes back trimmed and
 * otherwise untouched, so the server refuses it and the picker falls back to home.
 */
export function cleanPath(input: string): string {
  const s = input.trim();
  if (!s.startsWith('/')) return s;
  const parts: string[] = [];
  for (const p of s.split('/')) {
    if (p === '' || p === '.') continue;
    if (p === '..') parts.pop();
    else parts.push(p);
  }
  return '/' + parts.join('/');
}

/** The clickable segments of an absolute path, root first: `/home/dev` → `/`, `home`, `dev`. */
export function breadcrumbs(path: string): Crumb[] {
  const out: Crumb[] = [{ name: '/', path: '/' }];
  let acc = '';
  for (const p of path.split('/').filter(Boolean)) {
    acc += '/' + p;
    out.push({ name: p, path: acc });
  }
  return out;
}

/** The parent of an absolute path, undefined at the root. Used when the server could not list a folder, so Up still works. */
export function parentOf(path: string): string | undefined {
  if (path === '/' || path === '') return undefined;
  const i = path.lastIndexOf('/');
  return i <= 0 ? '/' : path.slice(0, i);
}

/** What the picker says when a listing fails; the server's technical reason is kept for anything unexpected. */
export function listingError(status: number, message: string): string {
  switch (status) {
    case 403:
      return "You don't have access to this folder";
    case 404:
      return 'This folder does not exist';
    case 400:
      return 'This is not a folder';
    default:
      return message;
  }
}

/**
 * Type-ahead: the index of the first name after `from` (wrapping) that starts with `prefix`,
 * case-insensitively, or -1. Pass `from` one back to include the current row when extending the prefix.
 */
export function matchFrom(names: string[], from: number, prefix: string): number {
  const p = prefix.toLowerCase();
  if (!p || names.length === 0) return -1;
  for (let k = 1; k <= names.length; k++) {
    const i = (((from + k) % names.length) + names.length) % names.length;
    if (names[i].toLowerCase().startsWith(p)) return i;
  }
  return -1;
}
