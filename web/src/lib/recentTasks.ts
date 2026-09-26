import type { SessionDetail, SnapshotData, UpdateData } from '../api';

const MAX_TASKS = 5;
const MAX_BYTES = 16 * 1024 * 1024;
const PAGE_ITEMS = 50;
const PAGE_BYTES = 64 * 1024;
const encoder = new TextEncoder();

/** In-memory presentation only. A hit never confirms status or permits an action. */
export class RecentTasks {
  private entries = new Map<string, { detail: SessionDetail; bytes: number }>();
  private epoch = '';
  private retained = 0;

  get size() { return this.entries.size; }
  /** UTF-16 strings plus fixed object charges, not a total heap measurement. */
  get bytes() { return this.retained; }

  /** Only a negotiated snapshot establishes the current service instance. */
  confirm(snapshot: SnapshotData) {
    if (snapshot.representation !== 'compact-v1' || !snapshot.epoch || !snapshot.detail_stream) {
      this.clear();
      return;
    }
    this.setEpoch(snapshot.epoch);
    this.retain(new Set(snapshot.sessions.map(session => session.id)));
    if (snapshot.session) this.remember(snapshot.session);
  }

  remember(detail: SessionDetail, epoch = detail.epoch) {
    if (detail.representation !== 'compact-v1' || !epoch || detail.epoch !== epoch) return;
    // A delayed task reference cannot roll the cache back to an old instance.
    if (this.epoch && this.epoch !== epoch) return;
    this.setEpoch(epoch);
    this.remove(detail.id);
    // Reject oversized records before allocating JSON or encoded copies.
    if (charge({ ...detail, items: [] }) > MAX_BYTES || charge(detail.items.at(-1)) > MAX_BYTES) return;
    let start = detail.items.length;
    let pageBytes = 0;
    while (start > 0 && detail.items.length - start < PAGE_ITEMS) {
      if (pageBytes >= PAGE_BYTES) break;
      const item = detail.items[start - 1];
      const lowerBound = Math.max(item.text?.length ?? 0, item.tool?.input?.length ?? 0, item.tool?.output?.length ?? 0);
      if (start < detail.items.length && pageBytes + lowerBound > PAGE_BYTES) break;
      // Large retained text stays intact without allocating another JSON copy.
      // Smaller records use the same serialized byte target as the server.
      const bytes = lowerBound > PAGE_BYTES ? PAGE_BYTES + 1 : encoder.encode(JSON.stringify(item)).byteLength + 1;
      if (start < detail.items.length && pageBytes + bytes > PAGE_BYTES) break;
      pageBytes += bytes;
      start--;
    }
    const items = detail.items.slice(start);
    const before = start > 0 ? cursor(items[0].id) : detail.history_before;
    const recent = { ...detail, items, history_before: before };
    const bytes = charge(recent);
    if (bytes > MAX_BYTES) return;
    while (this.entries.size >= MAX_TASKS || this.retained + bytes > MAX_BYTES) {
      this.remove(this.entries.keys().next().value!);
    }
    this.entries.set(detail.id, { detail: recent, bytes });
    this.retained += bytes;
  }

  private setEpoch(epoch: string) {
    if (this.epoch !== epoch) {
      this.clear();
      this.epoch = epoch;
    }
  }

  get(id: string, epoch = this.epoch) {
    if (!epoch || epoch !== this.epoch) return undefined;
    const entry = this.entries.get(id);
    if (!entry) return undefined;
    this.entries.delete(id);
    this.entries.set(id, entry);
    return entry.detail;
  }

  remove(id: string) {
    const old = this.entries.get(id);
    if (!old) return;
    this.entries.delete(id);
    this.retained -= old.bytes;
  }

  retain(ids: ReadonlySet<string>) {
    for (const id of this.entries.keys()) if (!ids.has(id)) this.remove(id);
  }

  invalidate(update: UpdateData) {
    if (update.name === 'history' || update.name === 'session_removed' || update.name === 'items_trimmed') this.remove(update.session_id);
    if (update.name === 'project_removed') this.removeProject(update.project_id);
  }

  removeProject(projectId: string) {
    for (const [id, entry] of this.entries) if (entry.detail.project_id === projectId) this.remove(id);
  }

  clear() {
    this.entries.clear();
    this.retained = 0;
    this.epoch = '';
  }
}

function cursor(id: string) {
  return btoa(String.fromCharCode(...encoder.encode(id))).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

// String lengths are constant-time, so accounting a large chat does not copy
// its text. Object/property charges leave room for the small compact records.
function charge(value: unknown): number {
  if (typeof value === 'string') return value.length * 2 + 16;
  if (typeof value === 'number') return 8;
  if (typeof value === 'boolean') return 4;
  if (value == null) return 4;
  if (Array.isArray(value)) return value.reduce((bytes, entry) => bytes + 8 + charge(entry), 24);
  if (typeof value === 'object') return Object.entries(value).reduce((bytes, [key, entry]) => bytes + 16 + key.length * 2 + charge(entry), 32);
  return 0;
}
