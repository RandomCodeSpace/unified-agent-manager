/** The text uam's Copilot adapter appends to a shell request's command (permission.go `describePermission`). */
const CLI_WARNING = '\n\nWarning: ';
const SANDBOX_BYPASS = '\n\nRequests to run outside the sandbox.';

// shortcut: pattern list misses unknown destructive commands, upgrade if the CLI exposes risk
const SHELL_RISKS: ReadonlyArray<[RegExp, string]> = [
  [/(?:^|[\s;&|(])(?:sudo|doas)\s/, 'Runs as root'],
  [/(?:^|[\s;&|(])(?:rm|rmdir|unlink|shred)\s/, 'Deletes files'],
  [/(?:^|[\s;&|(])(?:chmod|chown|chgrp)\s/, 'Changes file permissions'],
  [/(?:^|[\s;&|(])mv\s/, 'Moves or replaces files'],
  [/\bgit\s+push\b[^;&|]*\s(?:--force(?:-with-lease)?|-f)\b|\bgit\s+reset\s[^;&|]*--hard\b/, 'Rewrites git history'],
  // A redirect into a file; `2>&1` and `> /dev/null` write nothing.
  [/(?<![<>&=\d-])\d?>>?(?!&)\s*(?!\/dev\/null\b)[^\s>&]/, 'Writes to a file'],
];

/** A home path, or an absolute one outside the Thread's folder (`/dev/...` aside). */
function outsideProject(command: string, roots: string[]): boolean {
  if (/(?:^|[\s'"=])(?:~|\$HOME|\$\{HOME\})(?:[/\s'"]|$)/.test(command)) return true;
  for (const [, path] of command.matchAll(/(?:^|[\s'"=])(\/[^\s'";|&<>()]*)/g)) {
    if (path.startsWith('/dev/')) continue;
    if (!roots.some((root) => path === root || path.startsWith(root.endsWith('/') ? root : `${root}/`))) return true;
  }
  return false;
}

/**
 * Why a shell request is risky, as one short line, or '' when nothing marks it. The CLI's own
 * warning and sandbox escape come first; the pattern list covers what the CLI does not flag.
 * Never a "safe" verdict: an unrecognised command just gets no line.
 */
export function shellRisk(detail: string, roots: string[]): string {
  const cuts = [detail.lastIndexOf(CLI_WARNING), detail.lastIndexOf(SANDBOX_BYPASS)].filter((i) => i >= 0);
  const command = cuts.length ? detail.slice(0, Math.min(...cuts)) : detail;
  const reasons: string[] = [];
  if (detail.includes(SANDBOX_BYPASS)) reasons.push('Runs outside the sandbox');
  if (detail.includes(CLI_WARNING)) reasons.push('Copilot warns about it');
  if (outsideProject(command, roots)) reasons.push('Outside the project');
  for (const [pattern, reason] of SHELL_RISKS) if (pattern.test(command)) reasons.push(reason);
  return reasons.slice(0, 3).join(' · ');
}
