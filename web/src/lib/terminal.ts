/** The Project's terminal socket on the page's own host (ws, or wss under https), opened at the terminal's first size. */
export function terminalUrl(page: { protocol: string; host: string }, projectId: string, cols: number, rows: number): string {
  return `${page.protocol === 'https:' ? 'wss' : 'ws'}://${page.host}/api/projects/${encodeURIComponent(projectId)}/terminal?cols=${cols}&rows=${rows}`;
}

/** The shell's exit code from a text frame (`{"type":"exit","code":N}`); null for anything else. */
export function exitCode(text: string): number | null {
  try {
    const message = JSON.parse(text) as { type?: unknown; code?: unknown };
    return message.type === 'exit' && Number.isInteger(message.code) ? (message.code as number) : null;
  } catch {
    return null;
  }
}
