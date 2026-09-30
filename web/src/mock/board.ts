// Development-only: the planner's routes (ADR 0005 §14) and `board` frames (§15) for the
// in-browser mock service (install.ts). The rules follow the ADR closely enough to exercise
// the UI: derived containers (§2), holds and requests (§5–§7), cascade and restore (§8),
// staleness markers (§9) and the import (§11). Not part of the production bundle.

import { LIVE, type BoardRequest, type Card, type CardComment, type CardKind, type CardStatus, type ChecklistItem, type Evidence, type Hold, type Project, type SessionSummary, type Settings } from '../api';
import { cardPath, childIndex, deriveBoard, leavesUnder } from '../lib/board';

type Json = Record<string, unknown>;

/** What the board needs from the rest of the mock service. */
export interface BoardHost {
  broadcast: (name: string, payload: Json) => void;
  projects: () => Project[];
  settings: () => Settings;
  task: (id: string) => SessionSummary | undefined;
  /** A new Task through the create path, its first prompt sent (launch and plan). */
  createTask: (projectId: string, name: string, prompt: string) => SessionSummary;
}

const NOW = Date.now();
const ago = (min: number) => new Date(NOW - min * 60000).toISOString();
const now = () => new Date().toISOString();
const inDays = (days: number) => new Date(Date.now() + days * 86400000).toISOString();

/** HEAD of each Project's checkout in the mock. */
const HEAD: Record<string, string> = { p1: 'c41d2e8', p3: '7be0913' };

const RANK: Record<CardKind, number> = { epic: 0, story: 1, subtask: 2 };

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}
/** A refusal as internal/web writes one: `{error, code, refs?, cards?}`, its status from the code (web.boardError). */
const STATUS: Record<string, number> = { invalid: 400, not_found: 404, forbidden: 403, planner_unavailable: 503 };
const refuse = (code: string, error: string, extra: Json = {}) => json(STATUS[code] ?? 409, { error, code, ...extra });
/** A route the service does not have: the catch-all's plain 404, with no code. */
const noRoute = () => json(404, { error: 'not found' });

interface Seeded {
  cards: Card[];
  requests: BoardRequest[];
  comments: Record<string, CardComment[]>;
  holds: Record<string, Hold[]>;
}

/** A card with the wire defaults; `n` is its `#seq`. */
function card(n: number, project_id: string, kind: CardKind, parent: number | null, title: string, extra: Partial<Card> & { min?: number } = {}): Card {
  const { min = 60 * 24 * 6 - n * 40, ...rest } = extra;
  return {
    id: `c${project_id || 'u'}-${n}`,
    seq: n,
    project_id,
    kind,
    parent_id: parent === null ? null : `c${project_id || 'u'}-${parent}`,
    rank: n,
    title,
    desc: '',
    win_condition: '',
    status: 'planned',
    prio: 3,
    labels: [],
    checklist: [],
    blocked: false,
    blocked_by: [],
    blocks: [],
    confirmed: true,
    pinned_sha: HEAD[project_id] ?? '',
    accept_cmd: null,
    paths: [],
    pending_requests: 0,
    revision: 1,
    created_at: ago(min),
    updated_at: ago(Math.max(1, min - 30)),
    moved_at: ago(Math.max(1, min - 30)),
    ...rest,
  };
}

const items = (...texts: [string, boolean][]): ChecklistItem[] => texts.map(([text, done]) => ({ text, done }));

/** The seed: a realistic plan for unified-agent-manager, a small one for notes-site, and one Unassigned card. */
function seedBoard(big: boolean): Seeded {
  const P = 'p1';
  const suggestion = { confirmed: false, expires_at: inDays(9) };
  const cards: Card[] = [
    card(1, P, 'epic', null, 'Faster first load of long transcripts', { win_condition: 'A 5,000-item Task shows its last page in under a second on a laptop.', desc: 'Long Tasks load every item before the first paint. The plan:\n\n- page older history from the provider record\n- cache what was read in `historyCache`\n- keep the view **steady** while pages land', prio: 1 }),
    card(2, P, 'story', 1, 'Page history from the provider record', { win_condition: 'Older history arrives a page at a time and survives a restart.' }),
    card(3, P, 'subtask', 2, 'Serve archive pages with stable cursors', { status: 'done', win_condition: 'An archive cursor still resolves after the service restarts.', effort: 'M' }),
    card(4, P, 'subtask', 2, 'Cache archive pages in IndexedDB', { status: 'done', win_condition: 'Revisiting an archived page reads no network.', effort: 'M' }),
    card(5, P, 'subtask', 2, 'Evict cached pages past the reading window', { status: 'doing', held_by: 't15', win_condition: 'The cache holds at most the reading window and the live tail.', effort: 'S', checklist: items(['Evict on window move', true], ['Keep the live tail', true], ['Cover it in historyCache tests', true]), paths: ['web/src/lib/historyCache.ts'] }),
    card(6, P, 'subtask', 2, 'Measure first paint on a 5,000-item Task', { status: 'todo', win_condition: 'A number for first paint, before and after, in the pull request.', stale: { behind: 14, diverged: false, files: ['web/src/lib/historyWindow.ts'] }, paths: ['web/src/lib/history*.ts'], pinned_sha: '9d313ef' }),
    card(7, P, 'story', 1, 'Trim the snapshot payload', { win_condition: 'The first snapshot of a 30-Task workspace is under 64 KiB.' }),
    card(8, P, 'subtask', 7, 'Drop unused fields from the task summary', { status: 'doing', held_by: 't1', win_condition: 'No summary field goes unread by the web client.', effort: 'S' }),
    card(9, P, 'subtask', 7, 'Send subagent previews only for the open Task', { win_condition: 'Closed Tasks carry no subagent previews in the snapshot.', blocked_by: ['cp1-8'] }),
    card(10, P, 'subtask', 7, 'Compress the snapshot with gzip', { status: 'cancelled' }),
    card(11, P, 'subtask', 7, 'Benchmark snapshot size per Task count', { ...suggestion, win_condition: 'A table of snapshot sizes for 10, 30 and 100 Tasks.' }),
    card(12, P, 'story', 1, 'Keep the transcript steady while pages land', { win_condition: 'Nothing on screen moves when an older page arrives.' }),
    card(13, P, 'subtask', 12, 'Anchor on the first visible row', { status: 'done' }),
    card(14, P, 'subtask', 12, 'Hold page writes until scrolling settles on iOS', { status: 'done' }),
    card(15, P, 'subtask', 12, 'Cover anchoring with a DOM test', { win_condition: 'A test fails when a landed page shifts the visible row.', checklist: items(['Scroll to the middle', false], ['Land an older page', false]) }),
    card(16, P, 'story', 1, 'Lazy-load the diagram renderer', { ...suggestion, win_condition: 'The first load fetches no diagram code.' }),
    card(17, P, 'subtask', 16, 'Split the diagram renderer into its own chunk', { ...suggestion }),
    card(18, P, 'epic', null, 'Release automation', { win_condition: 'A tag produces signed binaries for every platform and the release notes, with no manual step.', prio: 2 }),
    card(19, P, 'story', 18, 'Cross-compile the release matrix', { win_condition: 'Every supported platform has an archive on the release.' }),
    card(20, P, 'subtask', 19, 'Add darwin and windows targets', { status: 'doing', held_by: 't19', win_condition: 'The release workflow builds six targets.', effort: 'M' }),
    card(21, P, 'subtask', 19, 'Upload archives to the GitHub release', { blocked_by: ['cp1-20'] }),
    card(22, P, 'subtask', 19, 'Set up the macOS signing step', { status: 'todo', win_condition: 'darwin archives are signed and notarized in CI.' }),
    card(23, P, 'story', 18, 'Sign release binaries', { win_condition: 'Every archive has a signature a user can verify.' }),
    card(24, P, 'subtask', 23, 'Generate the signing key in CI secrets', { status: 'done' }),
    card(25, P, 'subtask', 23, 'Sign archives and publish signatures', { checklist: items(['Sign in the release job', true], ['Upload .sig files', false], ['Document the key', false]) }),
    card(26, P, 'subtask', 23, 'Verify signatures in the install script', { win_condition: 'install.sh refuses an archive whose signature does not match.' }),
    card(27, P, 'story', 18, 'Changelog from merged pull requests', { win_condition: 'Release notes draft themselves from conventional commits.' }),
    card(28, P, 'subtask', 27, 'Group merged pull requests by conventional type', { status: 'doing', held_by: 't18', checklist: items(['Parse the commit type', true], ['Group feat and fix', false], ['Fold chores into one line', false]) }),
    card(29, P, 'subtask', 27, 'Write the upgrade notes section', {}),
    card(30, P, 'subtask', 27, 'Link each entry to its pull request', {}),
    card(31, P, 'subtask', 23, 'Publish SHA-256 checksums beside the archives', { ...suggestion }),
    // notes-site: a small plan.
    card(32, 'p3', 'epic', null, 'Accessible post template', { win_condition: 'The post page passes axe with no serious findings.' }),
    card(33, 'p3', 'story', 32, 'Alt text for every image', {}),
    card(34, 'p3', 'subtask', 33, 'Add an alt field to the front matter', { status: 'done' }),
    card(35, 'p3', 'subtask', 33, 'Fail the build on an image without alt text', { status: 'todo' }),
    // Unassigned: imported earlier, read-only until moved into a Project.
    card(36, '', 'subtask', null, 'Investigate the flaky clipboard test on Firefox', { status: 'todo', pinned_sha: '', win_condition: 'The clipboard test passes 50 runs in a row on Firefox.' }),
  ];
  // Blocker links point both ways on the wire.
  for (const c of cards) for (const b of c.blocked_by) cards.find((x) => x.id === b)?.blocks.push(c.id);
  if (big) cards.push(...bigPlan(cards.length + 1));

  const req = (n: number, cardSeq: number, kind: BoardRequest['kind'], task: string, comment: string, extra: Partial<BoardRequest> = {}): BoardRequest => ({
    id: `rq${n}`,
    card_id: `cp1-${cardSeq}`,
    task_id: task,
    agent_id: '',
    kind,
    comment,
    payload: {},
    evidence: {},
    flags: [],
    base_revision: 1,
    status: 'pending',
    created_at: ago(20 - n),
    ...extra,
  });
  const evidence5: Evidence = {
    baseline: { head: '9e64233', dirty: [] },
    diff: {
      added: 97,
      deleted: 14,
      files: [
        { path: 'web/src/lib/historyCache.ts', added: 61, deleted: 9, by_task: true },
        { path: 'web/tests/historyCache.test.mjs', added: 29, deleted: 2, by_task: true },
        { path: 'web/src/state.ts', added: 7, deleted: 3, by_task: true, overlap: { card: 8, task_id: 't1' } },
      ],
    },
    commits: [
      { sha: '4f2c9ab', subject: 'feat(web): evict cached history pages past the window' },
      { sha: 'b71d0e3', subject: 'test(web): cover page eviction' },
    ],
    accept: { cmd: 'make test', cmd_hash: 'sha256:5d1e…a90c', head: 'b71d0e3', dirty: false, exit: 0, tail: 'ok  \tinternal/web\t2.114s\n# tests 212\n# pass 212\n# fail 0', ran_at: ago(6), stale: false },
    transcript: { task_id: 't15', from_item: 'h580', to_item: 'h-done' },
    checklist: { done: 3, total: 3 },
  };
  const requests: BoardRequest[] = [
    req(1, 5, 'done', 't15', 'Cached pages past the reading window are evicted on every window move; the live tail stays. Three tests cover it.', { evidence: evidence5, flags: ['tests_or_build_changed', 'overlap'] }),
    req(2, 8, 'done', 't1', 'The unused fields were already gone on this branch; nothing to change.', {
      flags: ['no_change_in_tree', 'acceptance_could_not_run'],
      evidence: { baseline: { head: 'c41d2e8', dirty: ['internal/web/snapshot.go'] }, diff: { added: 0, deleted: 0, files: [] }, commits: [], transcript: { task_id: 't1', from_item: 'i1', to_item: 'i7', partial: true }, checklist: { done: 0, total: 0 } },
    }),
    req(3, 9, 'change', 't1', 'Previews are also needed for the Task the user last opened, not only the open one.', { payload: { patch: { title: 'Send subagent previews only for recent Tasks', win_condition: 'Only the five recent Tasks carry subagent previews.' } } }),
    req(4, 20, 'blocked', 't19', 'The darwin targets need the signing step first; the notarization call fails without a key.', { payload: { blocker: 'cp1-22' } }),
    req(5, 28, 'split', 't18', 'This is four pieces of work: reading the merged pull requests comes first, then the three checklist items; the commit type already parses.', {
      payload: { children: [{ title: 'Read merged pull requests since the last tag', win_condition: 'The list matches git log between the two tags.' }] },
    }),
    req(6, 29, 'cancel', 't18', 'The pull request template already produces upgrade notes; this section would repeat them.'),
    // Decided: accepted as it was filed, because the Project's acceptance command passed (ADR 0005 decision 5).
    req(7, 4, 'done', 't4', 'Archived pages are cached per cursor in IndexedDB; a revisit reads no network.', {
      status: 'accepted',
      created_at: ago(60 * 26),
      decided_at: ago(60 * 26),
      decided_by: 'uam',
      evidence: {
        baseline: { head: '7a2b3c4', dirty: [] },
        diff: { added: 48, deleted: 6, files: [{ path: 'web/src/lib/historyCache.ts', added: 48, deleted: 6, by_task: true }] },
        commits: [{ sha: '8d25dbb', subject: 'feat(web): cache archive pages in IndexedDB' }],
        accept: { cmd: 'make test', cmd_hash: 'sha256:5d1e…a90c', head: '8d25dbb', dirty: false, exit: 0, tail: 'ok  \tinternal/web\t2.087s\n# tests 204\n# pass 204\n# fail 0', ran_at: ago(60 * 26), stale: false },
        transcript: { task_id: 't4', from_item: 'h310', to_item: 'h-done' },
        checklist: { done: 0, total: 0 },
      },
    }),
  ];
  const comment = (n: number, author: string, body: string, min: number, automatic = false): CardComment => ({ id: `cm${n}`, author, body, automatic, created_at: ago(min) });
  const comments: Record<string, CardComment[]> = {
    'cp1-3': [comment(1, 'task:t3', 'Cursors now encode the item id; the restart test passes.', 60 * 30), comment(2, 'owner', 'Accepted: cursors survive the restart.', 60 * 29)],
    'cp1-4': [comment(6, 'task:t4', 'Archived pages are cached per cursor in IndexedDB; a revisit reads no network.', 60 * 26), comment(7, 'uam', 'Accepted automatically: the acceptance command passed', 60 * 26, true)],
    'cp1-5': [comment(3, 'uam', 'attempt #1 ended, uncommitted: web/src/lib/historyCache.ts', 60 * 3, true), comment(4, 'task:t15', 'Eviction keeps the reading window and the live tail.', 30)],
    'cp1-10': [comment(5, 'owner', 'The sign-in proxy already compresses every response.', 60 * 50)],
  };
  const hold = (n: number, task: string, min: number, head: string, end?: [number, string]): Hold => ({ id: `ho${n}`, task_id: task, started_at: ago(min), baseline_head: head, baseline_dirty: [], ...(end ? { ended_at: ago(end[0]), end_reason: end[1] } : {}) });
  const holds: Record<string, Hold[]> = {
    'cp1-5': [hold(1, 't15', 60 * 5, '8d25dbb', [60 * 3, 'released']), hold(2, 't15', 60 * 2, '9e64233')],
    'cp1-8': [hold(3, 't1', 50, 'c41d2e8')],
    'cp1-20': [hold(4, 't19', 40, 'c41d2e8')],
    'cp1-28': [hold(5, 't18', 9, 'c41d2e8')],
    'cp1-3': [hold(6, 't3', 60 * 31, '7a2b3c4', [60 * 29, 'accepted'])],
    'cp1-4': [hold(7, 't4', 60 * 28, '7a2b3c4', [60 * 26, 'accepted'])],
  };
  return { cards, requests, comments, holds };
}

/** `?mock&bigplan`: about 200 more cards on notes-site, to check that the views stay smooth. */
function bigPlan(start: number): Card[] {
  const out: Card[] = [];
  const statuses: CardStatus[] = ['planned', 'todo', 'done', 'done', 'planned', 'cancelled', 'todo', 'planned'];
  let n = start;
  for (let e = 0; e < 4; e++) {
    const epic = card(n++, 'p3', 'epic', null, `Site area ${e + 1}: ${['Search', 'Feeds', 'Comments', 'Themes'][e]}`);
    out.push(epic);
    for (let s = 0; s < 6; s++) {
      const story = card(n++, 'p3', 'story', epic.seq, `${['Index', 'Render', 'Cache', 'Test', 'Document', 'Measure'][s]} ${['search', 'feeds', 'comments', 'themes'][e]}`);
      out.push(story);
      for (let t = 0; t < 7; t++) {
        const leaf = card(n++, 'p3', 'subtask', story.seq, `Step ${t + 1} of ${story.title.toLowerCase()}`, { status: statuses[(e + s + t) % statuses.length] });
        if (t === 3 && out.length > 3) leaf.blocked_by = [out[out.length - 2].id];
        out.push(leaf);
      }
    }
  }
  for (const c of out) for (const b of c.blocked_by) out.find((x) => x.id === b)?.blocks.push(c.id);
  return out;
}

export function boardMock(host: BoardHost, options: { big: boolean }) {
  const seeded = seedBoard(options.big);
  let cards = seeded.cards;
  let requests = seeded.requests;
  const comments = seeded.comments;
  const holds = seeded.holds;
  const revisions: Record<string, number> = { p1: 1, p3: 1, '': 1 };
  const acceptDefaults: Record<string, string> = { p1: 'make test', p3: '' };
  let nextSeq = Math.max(...cards.map((c) => c.seq)) + 1;
  let counter = 100;
  const id = (prefix: string) => `${prefix}${++counter}`;
  const imported = new Set<string>();
  /** Which cascade cancelled a node (`cascade_id`, §8); kept by the store, not sent. */
  const cascadeOf = new Map<string, string>();

  const byId = (cid: string) => cards.find((c) => c.id === cid);
  const find = (ref: string) => {
    const raw = decodeURIComponent(ref).replace(/^#/, '');
    return /^\d+$/.test(raw) ? cards.find((c) => c.seq === Number(raw)) : byId(raw);
  };
  const head = (c: Card) => HEAD[c.project_id] ?? '';
  const say = (c: Card, author: string, body: string, automatic = false) => (comments[c.id] ??= []).push({ id: id('cm'), author, body, automatic, created_at: now() });
  const liveTask = (taskId: string) => {
    const t = host.task(taskId);
    return !!t && (t.stage ?? 'active') === 'active' && LIVE.includes(t.state);
  };
  const confirmAndPin = (c: Card) => {
    c.confirmed = true;
    delete c.expires_at;
    c.pinned_sha = head(c);
    delete c.stale;
  };
  /**
   * An owner touch (save, create, move, checklist, confirm, launch, accept, restore, status done
   * or todo): the card is confirmed and re-pinned (§1), and so is every unconfirmed ancestor.
   */
  const touch = (c: Card) => {
    confirmAndPin(c);
    for (let at = c.parent_id ? byId(c.parent_id) : undefined; at; at = at.parent_id ? byId(at.parent_id) : undefined) if (!at.confirmed) confirmAndPin(at);
  };
  const withdraw = (c: Card) => {
    for (const r of requests) if (r.card_id === c.id && r.status === 'pending') Object.assign(r, { status: 'withdrawn', decided_at: now() });
  };
  const endHold = (c: Card, reason: string) => {
    const open = holds[c.id]?.find((h) => !h.ended_at);
    if (open) Object.assign(open, { ended_at: now(), end_reason: reason });
    delete c.held_by;
  };
  /** The one exit of a hold (§5): withdraws the card's requests and moves it on; an unconfirmed card's expiry is armed again. */
  const releaseHold = (c: Card, to: CardStatus, reason: string, comment?: string, author = 'owner') => {
    endHold(c, reason);
    withdraw(c);
    c.status = to;
    if (!c.confirmed) c.expires_at = inDays(14);
    if (comment) say(c, author, comment, author === 'uam');
  };
  const openBlockers = (c: Card) => c.blocked_by.map(byId).filter((b): b is Card => !!b && b.status !== 'done' && b.status !== 'cancelled');
  /** A parent must outrank its child (epic < story < subtask); the root may hold any kind (§1). Null when the place is valid. */
  const misplaced = (kind: CardKind, parentId: unknown, project: string): Response | null => {
    if (parentId === null || parentId === undefined || parentId === '') return null;
    const parent = byId(String(parentId));
    if (!parent || parent.project_id !== project) return refuse('not_found', 'parent not found');
    return RANK[parent.kind] < RANK[kind] ? null : refuse('invalid', `a ${parent.kind} cannot hold a ${kind}`);
  };

  /**
   * A split (§7): the given children first, then the checklist items. Under a story they become
   * sibling subtasks right after the original, which is cancelled ("split into #a, #b, …"); under
   * an epic or at the root the original turns into a story holding them. Ticked items are
   * accepted with the split when a request is accepted, else they wait with a done request. A
   * live hold moves to the first pending new subtask.
   */
  function splitCard(c: Card, given: { title: string; win_condition?: string; done?: boolean }[], accepted: boolean) {
    const parent = c.parent_id ? byId(c.parent_id) : undefined;
    const under = parent?.kind === 'story' ? parent : undefined;
    const items = [...given, ...c.checklist.map((i) => ({ title: i.text, win_condition: '', done: i.done }))];
    const holder = c.held_by;
    endHold(c, 'split');
    withdraw(c);
    const made: Card[] = [];
    for (const it of items) {
      const leaf = card(nextSeq++, c.project_id, 'subtask', null, it.title, { parent_id: under ? under.id : c.id, win_condition: it.win_condition ?? '', status: it.done && accepted ? 'done' : 'planned', created_at: now(), min: 0 });
      cards.push(leaf);
      made.push(leaf);
      if (!it.done) continue;
      if (accepted) say(leaf, 'owner', `Accepted with the split: ticked on #${c.seq}.`);
      else requests.push({ id: id('rq'), card_id: leaf.id, task_id: holder ?? '', agent_id: '', kind: 'done', comment: `Ticked on #${c.seq}'s checklist before the split.`, payload: {}, evidence: {}, flags: [], base_revision: 0, status: 'pending', created_at: now() });
    }
    if (under) {
      // Right after the original, in order: the story's children are ranked again.
      const order = cards.filter((x) => x.parent_id === under.id && !made.includes(x)).sort((a, b) => a.rank - b.rank || a.seq - b.seq);
      order.splice(order.indexOf(c) + 1, 0, ...made);
      order.forEach((x, i) => (x.rank = i));
      c.status = 'cancelled';
      say(c, 'uam', `split into ${made.map((x) => `#${x.seq}`).join(', ')}`, true);
    } else {
      c.kind = 'story';
      c.checklist = [];
      made.forEach((x, i) => (x.rank = i));
    }
    const first = made.find((x) => x.status !== 'done');
    if (holder && first) {
      first.status = 'doing';
      first.held_by = holder;
      (holds[first.id] ??= []).push({ id: id('ho'), task_id: holder, started_at: now(), baseline_head: head(first), baseline_dirty: [] });
    }
  }

  /**
   * Every write goes through here: the change, then the derived fields (confirmed, pending
   * counts, container status and progress, and a container's close when it reaches done), then
   * one `board` frame per Project it touched, each with the next revision.
   */
  function commit(mutate: () => void): void {
    const before = new Map(cards.map((c) => [c.id, { json: JSON.stringify(c), project: c.project_id, status: c.status }]));
    const beforeReq = new Map(requests.map((r) => [r.id, JSON.stringify(r)]));
    mutate();
    for (let pass = 0; pass < 4; pass++) {
      refresh();
      // A container that just reached done closes its remaining suggestions (§2).
      const index = childIndex(cards);
      let again = false;
      for (const c of cards) {
        if (c.kind === 'subtask' || c.status !== 'done' || before.get(c.id)?.status === 'done') continue;
        for (const leaf of leavesUnder(c.id, index)) {
          if (leaf.confirmed || leaf.held_by || leaf.status === 'cancelled') continue;
          leaf.status = 'cancelled';
          say(leaf, 'uam', `closed unconfirmed with #${c.seq}`, true);
          again = true;
        }
        // Each done subtask's close comment: its last one that is not automatic.
        const closes = leavesUnder(c.id, index).filter((l) => l.status === 'done').map((l) => comments[l.id]?.filter((cm) => !cm.automatic).at(-1)?.body).filter(Boolean);
        if (closes.length) say(c, 'uam', closes.map((b) => `• ${b}`).join('\n'), true);
      }
      if (!again) break;
    }
    const frames = new Map<string, { cards: Card[]; removed: string[]; requests: BoardRequest[] }>();
    const frame = (project: string) => {
      let f = frames.get(project);
      if (!f) frames.set(project, (f = { cards: [], removed: [], requests: [] }));
      return f;
    };
    for (const c of cards) {
      const was = before.get(c.id);
      if (was && was.json === JSON.stringify(c)) continue;
      if (was && was.project !== c.project_id) frame(was.project).removed.push(c.id);
      frame(c.project_id).cards.push(c);
    }
    for (const [cid, was] of before) if (!byId(cid)) frame(was.project).removed.push(cid);
    for (const r of requests) {
      if (beforeReq.get(r.id) === JSON.stringify(r)) continue;
      const owner = byId(r.card_id);
      if (owner) frame(owner.project_id).requests.push(r);
    }
    for (const [project, f] of frames) {
      const revision = (revisions[project] = (revisions[project] ?? 0) + 1);
      for (const c of f.cards) c.revision = revision;
      for (const c of f.cards) c.updated_at = now();
      host.broadcast('board', { project_id: project, revision, cards: f.cards, removed: f.removed, requests: f.requests });
    }
  }

  /**
   * The derived fields: pending counts, then every container's status and progress. A container
   * the owner cancelled stays cancelled whatever its subtasks are, as the service keeps it (§2).
   */
  function refresh() {
    for (const c of cards) c.pending_requests = requests.filter((r) => r.card_id === c.id && r.status === 'pending').length;
    cards = deriveBoard(cards).map((c) => (c.kind !== 'subtask' && cascadeOf.has(c.id) && c.status !== 'cancelled' ? { ...c, status: 'cancelled' } : c));
  }
  refresh();

  const done = (_: void, ok: () => Response) => ok();

  function preamble(c: Card, pending: Card[] = []): string {
    const map = new Map(cards.map((x) => [x.id, x]));
    const lines = [cardPath(c, map).map((x) => `#${x.seq} ${x.title}`).join(' › ')];
    if (c.win_condition) lines.push(`Done means: ${c.win_condition}`);
    if (c.desc) lines.push(c.desc);
    if (c.checklist.length) lines.push(...c.checklist.map((i) => `- [${i.done ? 'x' : ' '}] ${i.text}`));
    if (pending.length) lines.push('Pending in this story:', ...pending.map((p) => `- #${p.seq} ${p.title}`));
    lines.push('Use the board tools, finish with a done request, and never mark anything done yourself.');
    return lines.join('\n');
  }

  function launch(c: Card): Response {
    let leaf = c;
    let pending: Card[] = [];
    if (c.kind !== 'subtask') {
      pending = leavesUnder(c.id, childIndex(cards)).filter((l) => l.confirmed && (l.status === 'planned' || l.status === 'todo'));
      const first = pending.find((l) => !openBlockers(l).length && !l.blocked);
      if (!first) return refuse('invalid', 'nothing under this card is ready to launch');
      leaf = first;
    }
    if (leaf.status !== 'planned' && leaf.status !== 'todo') return refuse('invalid', `a ${leaf.status} subtask cannot be launched`);
    const session = host.createTask(leaf.project_id, `#${leaf.seq} ${leaf.title}`, preamble(leaf, pending));
    commit(() => {
      touch(leaf);
      leaf.status = 'doing';
      leaf.held_by = session.id;
      (holds[leaf.id] ??= []).push({ id: id('ho'), task_id: session.id, started_at: now(), baseline_head: head(leaf), baseline_dirty: [] });
    });
    return json(201, { card: leaf, session });
  }

  function accept(r: BoardRequest, comment: string): Response | null {
    const c = byId(r.card_id);
    if (!c) return refuse('not_found', 'card not found');
    if (r.kind === 'done' && (c.status !== 'doing' || c.held_by !== r.task_id)) return refuse('not_held', 'the subtask is not held by the Task that asked');
    commit(() => {
      Object.assign(r, { status: 'accepted', decided_at: now(), decision_comment: comment, decided_by: 'owner' });
      touch(c);
      switch (r.kind) {
        case 'done':
          releaseHold(c, 'done', 'accepted');
          say(c, 'owner', comment || r.comment);
          break;
        case 'cancel':
          if (c.held_by) releaseHold(c, 'cancelled', 'cancelled', comment || r.comment);
          else {
            withdraw(c);
            c.status = 'cancelled';
            say(c, 'owner', comment || r.comment);
          }
          break;
        case 'blocked': {
          c.blocked = true;
          const blocker = typeof r.payload.blocker === 'string' ? byId(r.payload.blocker) : undefined;
          if (blocker && !c.blocked_by.includes(blocker.id)) {
            c.blocked_by.push(blocker.id);
            blocker.blocks.push(c.id);
          }
          if (c.held_by) releaseHold(c, 'todo', 'blocked', `blocked: ${r.comment}`, 'uam');
          break;
        }
        case 'split':
          splitCard(c, Array.isArray(r.payload.children) ? (r.payload.children as { title: string; win_condition?: string; done?: boolean }[]) : [], true);
          break;
        case 'change': {
          const patch = (r.payload.patch ?? {}) as Json;
          for (const key of ['title', 'desc', 'win_condition', 'prio', 'effort', 'labels'] as const) if (key in patch) (c as unknown as Json)[key] = patch[key];
          break;
        }
      }
    });
    return null;
  }

  /** Owner cancel of a container (§8): one comment on each open subtask, one shared cascade id. */
  function cascade(c: Card, comment: string) {
    const tag = id('cascade');
    const index = childIndex(cards);
    const walk = (pid: string): Card[] => (index.get(pid) ?? []).flatMap((k) => [k, ...walk(k.id)]);
    cascadeOf.set(c.id, tag);
    for (const node of walk(c.id)) {
      if (node.kind !== 'subtask' || node.status === 'done' || node.status === 'cancelled') continue;
      cascadeOf.set(node.id, tag);
      releaseHold(node, 'cancelled', 'cancelled', `cancelled with #${c.seq}: ${comment}`, 'uam');
    }
    say(c, 'owner', comment);
  }

  /** `?`: the board routes, or null for any other path. */
  function route(method: string, url: URL, body: Json): Response | null {
    const path = url.pathname;
    if (!path.startsWith('/api/board')) return null;
    if (!host.settings().planner) return refuse('planner_off', 'the planner is off');
    const project = (pid: string) => host.projects().find((p) => p.id === pid);
    const gitless = (pid: string) => {
      const p = project(pid);
      if (pid && !p) return refuse('not_found', 'project not found');
      return p?.no_git ? refuse('no_git', p.no_git === 'not_installed' ? 'git is not installed' : 'the project is not a git repository') : null;
    };
    let m: RegExpMatchArray | null;

    if (path === '/api/board' && method === 'GET') {
      const raw = url.searchParams.get('project_id') ?? '';
      const pid = raw === 'unassigned' ? '' : raw;
      const refused = pid ? gitless(pid) : null;
      if (refused) return refused;
      const own = cards.filter((c) => c.project_id === pid);
      const ids = new Set(own.map((c) => c.id));
      return json(200, { cards: own, requests: requests.filter((r) => r.status === 'pending' && ids.has(r.card_id)), revision: revisions[pid] ?? 0 });
    }
    if ((m = path.match(/^\/api\/board\/projects\/([^/]+)$/))) {
      const pid = decodeURIComponent(m[1]);
      const p = project(pid);
      if (!p) return refuse('not_found', 'project not found');
      if (method === 'GET') return json(200, { accept_cmd: acceptDefaults[pid] ?? '', git: p.no_git ?? '' });
      if (method === 'PATCH') {
        acceptDefaults[pid] = String(body.accept_cmd ?? '');
        return json(200, { accept_cmd: acceptDefaults[pid], git: p.no_git ?? '' });
      }
    }
    if (path === '/api/board/cards' && method === 'POST') {
      const pid = String(body.project_id ?? '');
      const refused = gitless(pid);
      if (refused) return refused;
      const kind = body.kind as CardKind;
      const title = String(body.title ?? '').trim();
      if (!title || !['epic', 'story', 'subtask'].includes(kind)) return refuse('invalid', 'a card needs a kind and a title');
      const wrong = misplaced(kind, body.parent_id, pid);
      if (wrong) return wrong;
      const parent = body.parent_id ? byId(String(body.parent_id)) : undefined;
      const c = card(nextSeq++, pid, kind, null, title, { parent_id: parent?.id ?? null, win_condition: String(body.win_condition ?? ''), desc: String(body.desc ?? ''), created_at: now(), min: 0 });
      return done(commit(() => {
        cards.push(c);
        touch(c);
      }), () => json(201, c));
    }
    if (path === '/api/board/links') {
      const blocker = byId(String(body.blocker ?? url.searchParams.get('blocker') ?? ''));
      const blocked = byId(String(body.blocked ?? url.searchParams.get('blocked') ?? ''));
      if (!blocker || !blocked) return refuse('not_found', 'card not found');
      if (method === 'POST') {
        // Links point only at confirmed cards (§3); the refusal is a plain invalid.
        if (!blocker.confirmed) return refuse('invalid', 'a blocker link may point only at a confirmed card');
        return done(commit(() => {
          if (!blocked.blocked_by.includes(blocker.id)) blocked.blocked_by.push(blocker.id);
          if (!blocker.blocks.includes(blocked.id)) blocker.blocks.push(blocked.id);
        }), () => json(204));
      }
      if (method === 'DELETE') {
        return done(commit(() => {
          blocked.blocked_by = blocked.blocked_by.filter((b) => b !== blocker.id);
          blocker.blocks = blocker.blocks.filter((b) => b !== blocked.id);
        }), () => json(204));
      }
    }
    if ((m = path.match(/^\/api\/board\/requests\/([^/]+)\/(accept|reject)$/)) && method === 'POST') {
      const r = requests.find((x) => x.id === decodeURIComponent(m![1]));
      if (!r || r.status !== 'pending') return refuse('not_found', 'no pending request');
      if (m[2] === 'accept') return accept(r, String(body.comment ?? '').trim()) ?? json(200, r);
      const reason = String(body.reason ?? '').trim();
      if (!reason) return refuse('invalid', 'a rejection needs a reason');
      const c = byId(r.card_id)!;
      // An Active Task hears the reason as a steer and keeps its hold (steered); otherwise the hold is released with the reason.
      const steered = liveTask(r.task_id);
      return done(commit(() => {
        Object.assign(r, { status: 'rejected', decided_at: now(), decision_comment: reason, decided_by: 'owner' });
        if (c.held_by === r.task_id && !steered) releaseHold(c, 'todo', 'rejected', reason, 'uam');
        else say(c, 'owner', `Rejected: ${reason}`);
      }), () => json(200, { ...r, steered }));
    }
    if (path === '/api/board/purge' && method === 'POST') {
      const pid = String(body.project_id ?? '');
      const index = childIndex(cards);
      const allCancelled = (c: Card): boolean => c.status === 'cancelled' && (index.get(c.id) ?? []).every(allCancelled);
      const gone = new Set(cards.filter((c) => c.project_id === pid && allCancelled(c)).map((c) => c.id));
      return done(commit(() => {
        cards = cards.filter((c) => !gone.has(c.id)).map((c) => (c.blocked_by.some((b) => gone.has(b)) || c.blocks.some((b) => gone.has(b)) ? { ...c, blocked_by: c.blocked_by.filter((b) => !gone.has(b)), blocks: c.blocks.filter((b) => !gone.has(b)) } : c));
        requests = requests.filter((r) => !gone.has(r.card_id));
      }), () => json(200, { purged: gone.size }));
    }
    if (path === '/api/board/import' && method === 'POST') {
      const dir = String(body.dir ?? '').trim();
      if (!dir.startsWith('/')) return refuse('invalid', 'dir must be an absolute path');
      if (dir.endsWith('/missing')) return refuse('invalid', `no board database in ${dir}`);
      const incoming = [
        { key: 'imp-1', title: 'Document the release checklist', status: 'todo' as const },
        { key: 'imp-2', title: 'Rotate the deploy token', status: 'done' as const },
        { key: 'imp-3', title: 'Try the new Go toolchain on CI', status: 'planned' as const },
      ];
      const fresh = incoming.filter((i) => !imported.has(i.key));
      commit(() => {
        for (const i of fresh) {
          imported.add(i.key);
          const c = card(nextSeq++, '', 'subtask', null, i.title, { status: i.status, pinned_sha: '', created_at: now(), min: 0 });
              cards.push(c);
          if (i.status === 'done') say(c, 'uam', 'imported from kb', true);
        }
      });
      return json(200, { imported: fresh.length, updated: incoming.length - fresh.length, unassigned: fresh.length, comments: fresh.filter((i) => i.status === 'done').length, links: 0, skipped: [{ id: '0f3a9c2e-7d41-4b8e-9a60-2c5d1e8b7f14', reason: 'a card without a title' }] });
    }

    if (!(m = path.match(/^\/api\/board\/cards\/([^/]+)(?:\/([a-z]+))?$/))) return noRoute();
    const c = find(m[1]);
    if (!c) return refuse('not_found', 'card not found');
    const action = m[2] ?? '';
    const comment = String(body.comment ?? '').trim();
    const ok = () => json(200, c);
    if (action === '' && method === 'GET') return json(200, { card: c, comments: comments[c.id] ?? [], requests: requests.filter((r) => r.card_id === c.id), holds: holds[c.id] ?? [] });
    // Unassigned cards are read-only until the owner moves one into a git Project (§11).
    if (!c.project_id && !(action === '' && method === 'PATCH' && Object.keys(body).every((k) => k === 'project_id'))) return refuse('read_only', 'move this card into a project first');
    switch (`${method} ${action}`) {
      case 'PATCH ': {
        if (typeof body.project_id === 'string' && body.project_id !== c.project_id) {
          const refused = gitless(body.project_id);
          if (refused) return refused;
        }
        return done(commit(() => {
          for (const key of ['title', 'desc', 'win_condition', 'prio', 'effort', 'due', 'labels', 'checklist', 'accept_cmd', 'paths', 'project_id'] as const) if (key in body) (c as unknown as Json)[key] = body[key];
          if ('project_id' in body) c.moved_at = now();
          touch(c);
        }), ok);
      }
      case 'POST confirm':
        return done(commit(() => touch(c)), ok);
      case 'POST dismiss':
        if (c.confirmed) return refuse('invalid', 'only a suggestion can be dismissed');
        return done(commit(() => {
          const walk = (pid: string): Card[] => cards.filter((k) => k.parent_id === pid).flatMap((k) => [k, ...walk(k.id)]);
          for (const node of [c, ...walk(c.id)]) {
            if (node.held_by) continue;
            node.status = 'cancelled';
            say(node, 'uam', 'dismissed', true);
          }
        }), ok);
      case 'POST move': {
        const wrong = misplaced(c.kind, body.parent_id, c.project_id);
        if (wrong) return wrong;
        return done(commit(() => {
          c.parent_id = (body.parent_id as string | null) || null;
          const siblings = cards.filter((x) => x.project_id === c.project_id && x.parent_id === c.parent_id && x.id !== c.id).sort((a, b) => a.rank - b.rank);
          const at = body.rank === undefined || body.rank === null ? siblings.length : Math.min(Number(body.rank), siblings.length);
          [...siblings.slice(0, at), c, ...siblings.slice(at)].forEach((x, i) => (x.rank = i));
          touch(c);
        }), ok);
      }
      case 'POST status': {
        const status = body.status as CardStatus;
        if (!comment && status !== 'todo') return refuse('invalid', 'a comment is required');
        if (c.kind !== 'subtask') {
          if (status !== 'cancelled') return refuse('invalid', "a container's status is derived");
          return done(commit(() => cascade(c, comment)), ok);
        }
        if (status === 'todo') {
          // internal/board's underCancelled: a subtask under a cancelled card waits for its restore.
          const cancelled = cardPath(c, new Map(cards.map((x) => [x.id, x]))).find((a) => a !== c && a.status === 'cancelled');
          if (cancelled) return refuse('invalid', `#${c.seq} is under cancelled #${cancelled.seq}; restore that first`);
        }
        if (status === 'done' && !body.force) {
          // internal/board's guard: open items (refs: their texts), the blocked flag, open blockers (refs: #seq).
          const open = c.checklist.filter((i) => !i.done);
          if (open.length) return refuse('guard_open_items', `${open.length} of ${c.checklist.length} checklist items are still open on #${c.seq}`, { refs: open.map((i) => i.text) });
          if (c.blocked) return refuse('guard_blocked', `#${c.seq} is flagged blocked`);
          const blockers = openBlockers(c);
          if (blockers.length) return refuse('guard_blockers', `${blockers.map((b) => `#${b.seq}`).join(', ')} still ${blockers.length === 1 ? 'blocks' : 'block'} #${c.seq}`, { refs: blockers.map((b) => `#${b.seq}`) });
        }
        return done(commit(() => {
          if (status === 'done' || status === 'todo') touch(c);
          if (c.held_by) releaseHold(c, status, status === 'cancelled' ? 'cancelled' : 'owner');
          else {
            withdraw(c);
            c.status = status;
          }
          if (comment) say(c, 'owner', comment);
        }), ok);
      }
      case 'POST restore': {
        if (!comment) return refuse('invalid', 'a comment is required');
        if (c.status !== 'cancelled') return refuse('invalid', 'only a cancelled card can be restored');
        const tag = cascadeOf.get(c.id);
        const parent = c.parent_id ? byId(c.parent_id) : undefined;
        if (parent?.status === 'cancelled' && cascadeOf.get(parent.id) === tag) return refuse('invalid', 'restore the cancelled parent instead');
        return done(commit(() => {
          const nodes = tag ? cards.filter((x) => cascadeOf.get(x.id) === tag) : [c];
          for (const node of nodes) {
            cascadeOf.delete(node.id);
            touch(node);
            if (node.kind === 'subtask' && node.status === 'cancelled') node.status = holds[node.id]?.length ? 'todo' : 'planned';
          }
          say(c, 'owner', comment);
        }), ok);
      }
      case 'POST comments': {
        const text = String(body.body ?? '').trim();
        if (!text) return refuse('invalid', 'a comment needs a body');
        return done(commit(() => say(c, 'owner', text)), () => json(201, comments[c.id].at(-1)));
      }
      case 'POST split': {
        const children = Array.isArray(body.children) ? (body.children as { title: string; win_condition: string }[]) : [];
        if (c.kind !== 'subtask') return refuse('invalid', 'only a subtask can be split');
        if (!children.length && !c.checklist.length) return refuse('invalid', 'a split needs children');
        return done(commit(() => splitCard(c, children, false)), ok);
      }
      case 'POST launch':
        return launch(c);
      case 'POST plan': {
        if (c.kind === 'subtask') return refuse('invalid', 'plan with an agent on an epic or a story');
        const brief = String(body.brief ?? '').trim();
        const session = host.createTask(c.project_id, `Plan #${c.seq} ${c.title}`, `${preamble(c)}\nPlan the work under this card: create and edit unconfirmed stories and subtasks only.${brief ? `\nBrief: ${brief}` : ''}`);
        return json(201, { session });
      }
      case 'POST release':
        if (c.status !== 'doing') return refuse('not_held', 'the subtask is not held');
        return done(commit(() => releaseHold(c, 'todo', 'released', comment || undefined)), ok);
      case 'POST check': {
        const cmd = c.accept_cmd ?? acceptDefaults[c.project_id] ?? '';
        if (!cmd) return refuse('invalid', 'no acceptance command for this subtask');
        const failing = c.seq % 2 === 0;
        // A job, as the service runs it: the run comes in its last board_job frame.
        const job = id('job');
        host.broadcast('board_job', { job_id: job, card_id: c.id, kind: 'check', status: 'running' });
        window.setTimeout(() => {
          const accept = { cmd, cmd_hash: `sha256:${(c.seq * 7919).toString(16)}…`, head: head(c), dirty: false, exit: failing ? 1 : 0, tail: failing ? `--- FAIL: TestRelease (0.02s)\n    release_test.go:41: missing target darwin/arm64\nFAIL` : 'ok  \tinternal/web\t2.114s', ran_at: now(), stale: false };
          host.broadcast('board_job', { job_id: job, card_id: c.id, kind: 'check', status: 'done', accept });
        }, 600);
        return json(202, { job_id: job });
      }
      case 'POST triage': {
        const verdict = c.stale?.diverged ? 'conflicts' : (c.stale?.behind ?? 0) > 10 ? 'valid' : 'moot';
        const sentence = { valid: 'The change still applies: the files it names moved but kept their shape.', moot: 'HEAD already does this: the eviction landed with the cache rewrite.', conflicts: 'The pin is no longer an ancestor of HEAD; the plan needs a new base.' }[verdict];
        return json(200, { verdict, sentence, head: head(c) });
      }
      case 'POST suggest': {
        if (c.kind === 'subtask') return refuse('invalid', 'suggest stories under an epic or a story');
        const job = id('job');
        const max = Math.max(1, Math.min(5, Number(body.max) || 2));
        host.broadcast('board_job', { job_id: job, card_id: c.id, kind: 'suggest', status: 'running' });
        window.setTimeout(() => {
          commit(() => {
            for (let i = 0; i < max; i++) {
              const kind: CardKind = c.kind === 'epic' ? 'story' : 'subtask';
              const s = card(nextSeq++, c.project_id, kind, null, kind === 'story' ? ['Harden the retry path', 'Measure it on a slow network', 'Document the new limits'][i % 3] : ['Write the failing test first', 'Handle the empty case', 'Add a metric'][i % 3], { parent_id: c.id, rank: 100 + i, confirmed: false, expires_at: inDays(14), created_at: now(), min: 0 });
              cards.push(s);
            }
          });
          host.broadcast('board_job', { job_id: job, card_id: c.id, kind: 'suggest', status: 'done' });
        }, 1200);
        return json(202, { job_id: job });
      }
    }
    return noRoute();
  }

  /**
   * Settle's hold decisions (§5): null lets the settle go ahead; a Response refuses it. Without
   * a decision for every subtask the Task holds, the answer is 409 `holds_undecided` with them.
   */
  function settle(taskId: string, body: Json): Response | null {
    const held = cards.filter((c) => c.held_by === taskId);
    if (!held.length) return null;
    const decisions = (body.holds ?? {}) as Record<string, { action?: string; comment?: string }>;
    if (held.some((c) => !decisions[c.id]?.action)) return refuse('holds_undecided', 'decide what happens to the subtasks this task holds', { cards: held });
    if (held.some((c) => decisions[c.id].action === 'cancel' && !decisions[c.id].comment?.trim())) return refuse('invalid', 'cancelling a subtask needs a comment');
    commit(() => {
      for (const c of held) {
        const d = decisions[c.id];
        if (d.action === 'release') releaseHold(c, 'todo', 'settled', d.comment?.trim() || undefined);
        else if (d.action === 'cancel') releaseHold(c, 'cancelled', 'cancelled', d.comment!.trim());
        // keep: the hold stays, and resumes after Reopen.
      }
    });
    return null;
  }

  /** Archive or Delete ends the Task's holds: back to todo with the automatic comment (§5). */
  function ended(taskId: string) {
    const held = cards.filter((c) => c.held_by === taskId);
    if (!held.length) return;
    commit(() => {
      for (const c of held) releaseHold(c, 'todo', 'archived', `attempt #${holds[c.id]?.length ?? 1} ended, uncommitted: nothing`, 'uam');
    });
  }

  /** Each Board's revision, for the snapshot (`''` is the Unassigned list). */
  const boardRevisions = () => ({ ...revisions });

  return { route, settle, ended, revisions: boardRevisions };
}
