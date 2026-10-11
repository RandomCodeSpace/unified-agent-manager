/**
 * Applying a redeployed service to a page that stays open (an installed app can sit for days).
 * The service reports its version in GET /api/meta; a different one than the page loaded with
 * means new assets are on the server. The page reloads on its own when nothing would be lost;
 * otherwise it offers a Reload control and keeps the user's work.
 */

/** What would be lost by reloading now. */
export interface UpdateBlockers {
  /** The composer holds unsent text, picked files or uploads (finished or in flight). */
  draft: boolean;
  /** A menu, dialog or other popup is open. */
  popup: boolean;
}

export type UpdateDecision = 'none' | 'reload' | 'offer';

/** Checks the page makes on coming back into view are at least this far apart; a reconnect always checks. */
export const CHECK_GAP_MS = 60_000;

export function decideUpdate(loaded: string | undefined, latest: string | undefined, blockers: UpdateBlockers): UpdateDecision {
  if (!loaded || !latest || loaded === latest) return 'none';
  return blockers.draft || blockers.popup ? 'offer' : 'reload';
}

export function checkDue(lastAt: number, now: number, gap = CHECK_GAP_MS): boolean {
  return now - lastAt >= gap;
}

/** A `git describe` build: its release, the commits after it, the commit and `-dirty` for local changes. */
export const BUILD_VERSION = /^(.+)-(\d+)-g([0-9a-f]{7,})(-dirty)?$/;

/** "v0.7.1-161-g228dbee0" reads as "v0.7.1+161"; release versions read as they are. */
export function shortVersion(version: string): string {
  const build = BUILD_VERSION.exec(version);
  return build ? `${build[1]}+${build[2]}` : version;
}
