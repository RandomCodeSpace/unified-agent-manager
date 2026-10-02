// Typed mirror of docs/adr/0004-web-interface.md plus the additions decided in
// issue #142 (projects, model catalog, titles, subagents). Field names are the
// wire names (snake_case). Paths keep the `sessions` name; the UI calls them
// Tasks.

import { visibleModels } from './lib/models';
import { foregroundRead } from './lib/reads';
import { PREVIEW_BYTES, previewMetadata, readTextPreview, type PreviewMetadata, type TextPreview } from './lib/preview';

export type SessionState =
  | 'idle'
  | 'starting'
  | 'working'
  | 'awaiting_permission'
  | 'awaiting_answer'
  | 'completed'
  | 'cancelled'
  | 'failed'
  | 'interrupted'
  | 'closed';

/** States in which the provider still holds the turn. */
export const LIVE: readonly SessionState[] = ['starting', 'working', 'awaiting_permission', 'awaiting_answer'];
/** States in which the provider is waiting for the user. */
export const ATTENTION: readonly SessionState[] = ['awaiting_permission', 'awaiting_answer'];

export interface Capabilities {
  cancel: boolean;
  permissions: boolean;
  questions: boolean;
  session_diff: boolean;
  history: boolean;
  context_size?: boolean;
  execution_modes?: boolean;
  /** The provider reports account quota and per-Task AI units (#188); the second parity exception. */
  usage?: boolean;
  /** A chosen model can title the provider's new Tasks (#183). */
  titles?: boolean;
  import?: boolean;
  /** The provider's runtime sign-in is shown and changed in Settings (`api.account`). */
  account?: boolean;
  /** The provider's MCP servers can be listed and managed (Settings → MCP servers, a Task's MCP servers). */
  mcp?: boolean;
}

/** What a model accepts as uploads; absent on the model means it reports nothing and is not gated. */
export interface Media {
  images: boolean;
  pdf: boolean;
  /** Most images one prompt may carry; absent or 0 means no reported limit. */
  max_images?: number;
  types?: string[];
}

export type CostTier = 'low' | 'medium' | 'high' | 'very_high';

/** One context tier's token prices in AI Credits per `Prices.batch_size` tokens; an absent price was not reported. */
export interface TierPrices {
  input?: number;
  output?: number;
  cache_read?: number;
  cache_write?: number;
  /** The tier's prompt budget; the long-context prices apply past it. */
  max_prompt_tokens?: number;
}

export interface Prices extends TierPrices {
  /** Tokens per priced batch (1,000,000 for Copilot); absent means 1,000,000. */
  batch_size?: number;
  long_context?: TierPrices;
}

/** One selectable model of the signed-in account. */
export interface Model {
  id: string;
  name: string;
  efforts?: string[];
  context_sizes?: { id: string; tokens: number }[];
  media?: Media;
  /** The provider's relative cost of the model; absent when it reports none. */
  cost_tier?: CostTier;
  /** Whole-number discount on usage billed through this model (Copilot's `auto`). */
  discount_percent?: number;
  /** Token prices; absent when the provider reports none (`auto`). */
  prices?: Prices;
}

/** The latest live context report: `prompt` and `cached` come from the latest main-agent call and may lag `used`. */
export interface ContextUsage {
  used: number;
  limit: number;
  prompt?: number;
  cached?: number;
}

/** One account quota of a provider with the `usage` capability. */
export interface Quota {
  provider: string;
  type: string;
  used: number;
  entitlement: number;
  unlimited: boolean;
  remaining_percent: number;
  overage: number;
  /** Present only while it lies in the future. */
  reset_at?: string;
}

/** `GET /api/usage`, the `usage` frame and the snapshot's `usage`: the last quota read that succeeded. */
export interface AccountUsage {
  quotas: Quota[];
  /** The latest read failed; the quotas are the previous ones. */
  stale: boolean;
  updated_at?: string;
}

/** A command the Task can run from the composer (`/name`). */
export interface Command {
  name: string;
  description: string;
  kind: 'skill' | 'command';
  input_hint: string;
  aliases?: string[];
  allow_during_turn?: boolean;
  disabled_reason?: string;
  input_choices?: { name: string; description: string }[];
  input_required?: boolean;
}

export type CommandResult =
  | { kind: 'text'; text: string; markdown?: boolean; prefill_input?: string }
  | { kind: 'select'; title: string; command: string; options: { name: string; description: string; group?: string }[] }
  | { kind: 'action'; action: 'model' | 'permissions' | 'context' | 'usage' | 'rename' }
  | { kind: 'completed'; text?: string };

export interface ExecutionState {
  known: boolean;
  mode?: 'interactive' | 'plan' | 'autopilot';
  objective?: {
    id: number;
    objective: string;
    status: 'active' | 'paused' | 'completed';
    turn_count: number;
    pause_reason?: string;
    completion_summary?: string;
    credits_used?: number;
    credit_limit?: number;
  };
}

export interface FileEntry {
  path: string;
  type: 'file' | 'directory';
  /** Tree listings only: a file's git status as Changes names it (`modified`, `untracked`, `deleted`, ...), or `changed` for a folder holding changed files. */
  status?: string;
}

export interface FileList {
  files: FileEntry[];
  /** Why the list is empty or short (not a git tree, too many files); empty otherwise. */
  reason: string;
}

/** One folder in a `GET /api/fs/dirs` listing. `name` is sanitised for display; `path` is exact and is what navigation sends back. */
export interface DirEntry {
  name: string;
  path: string;
  /** Holds `.git` (a directory or, in a linked worktree, a file). */
  git: boolean;
  /** The name starts with `.`; the server lists these and the picker filters them. */
  hidden: boolean;
  /** A symbolic link to a directory. */
  link: boolean;
}

export interface DirList {
  path: string;
  /** Absent at `/`. */
  parent?: string;
  entries: DirEntry[];
  /** More than 1,000 folders; only the first 1,000 are listed. */
  truncated: boolean;
}

/** An upload of this Task. Without `id` there is no stored copy (a provider record only). */
export interface Attachment {
  id?: string;
  name: string;
  mime: string;
  size?: number;
  /** A document the provider did not pass to the model as a document: the agent read it from disk with its own tools. */
  not_native?: boolean;
}

export interface ProviderInfo {
  name: string;
  display_name: string;
  available: boolean;
  reason?: string;
  /** Unavailable because the provider's runtime is signed out; `reason` then says to sign in in Settings. */
  signed_out?: boolean;
  capabilities: Capabilities;
  /** Selectable models; empty means "provider default only". */
  models: Model[];
  /** The cheapest priced model not hidden in Settings, the Utility model while `title_model` names none; omitted when none is priced. */
  cheapest_model?: string;
}

/** A provider runtime's sign-in, shared by every Task on the server; it never carries a credential. */
export interface ProviderAccount {
  signed_in: boolean;
  login?: string;
  host?: string;
  /** How a signed-in runtime gets its credential. */
  source?: 'stored' | 'env' | 'gh-cli' | 'other';
  /** The service environment variable whose token takes precedence over a sign-in made here; never its value. */
  env_var?: string;
  message?: string;
  /** False after a sign-in the runtime could not store: it lasts until the service restarts. */
  stored?: boolean;
}

/** Refusal code of a create or send while the Task's provider is signed out. */
export const SIGNED_OUT = 'provider_signed_out';

export interface Meta {
  version: string;
  providers: ProviderInfo[];
  recent_workdirs: string[];
  /** Syntax-only temporary-file eligibility; availability is checked only on Open. */
  temp_root?: string;
  temp_root_aliases?: string[];
}

export interface FileGrant {
  id: string;
  url: string;
  name: string;
  mime: string;
  size: number;
  expires_at: string;
}

/** Settings a new Task starts with. `context_size` is `default` when unset; `effort` may be empty. */
export interface TaskDefaults {
  provider: string;
  model: string;
  effort: string;
  context_size: string;
  mode: 'safe' | 'yolo';
}

export const BADGE_COLORS = ['red', 'orange', 'amber', 'lime', 'green', 'teal', 'cyan', 'blue', 'violet', 'pink'] as const;
export type BadgeColor = (typeof BADGE_COLORS)[number];

/** A Project's badge: two uppercase characters on one of the ten palette tones (DESIGN.md Badges). */
export interface Badge {
  text: string;
  color: BadgeColor;
}

export type SendDefault = 'steer' | 'queue';

/** The web interface's settings, kept by the service so they apply in every browser. */
export interface Settings {
  /** What Enter does while a turn runs. */
  send_default: SendDefault;
  /** Model IDs not offered anywhere a model is chosen, by provider; omitted when none is hidden (#191). */
  hidden_models?: Record<string, string[]>;
  /**
   * The Utility model by provider, the model UAM uses for its own small AI jobs such as titling new Tasks: a model ID,
   * or `none` to keep the provider's own title. A provider without an entry uses its `cheapest_model`. PATCH `''` unsets.
   */
  title_model?: Record<string, string>;
  /** OpenAI-compatible models the owner brought; omitted when there are none. PATCH replaces the whole list. */
  custom_models?: CustomModel[];
  /** What a new Task starts with, shared by every browser; omitted until set, and the provider's own defaults apply then. */
  task_defaults?: TaskDefaults;
  /** Whether a Task's header offers a shell in its Project folder; off by default. Turning it off ends every open shell. */
  terminal: boolean;
  /** The planner (ADR 0005): the Boards of git Projects; off by default, absent from a service older than it. */
  planner?: boolean;
  /** `false` when replies to send next are not offered after a turn; absent while they are (the default). */
  suggest_replies?: boolean;
  /** The prompts the owner saved, oldest first; changed one at a time through `api.addPrompt` and friends, never PATCH. */
  saved_prompts?: SavedPrompt[];
  /** Utility model calls a day, 0 for none; omitted for the service default (GET /api/utility reports the limit in force). */
  utility_daily_limit?: number;
  /** The share of the context, in percent (50 to 90), at which a Task's conversation starts compacting; omitted for the default, 80. PATCH null puts it back. */
  compact_threshold?: number | null;
}

/** The provider's own compaction threshold, in percent: where `Settings.compact_threshold` starts. */
export const DEFAULT_COMPACT_THRESHOLD = 80;

/** A message the owner saved to insert again from a composer; `project_id` limits it to that Project's Tasks. */
export interface SavedPrompt {
  id: string;
  name: string;
  text: string;
  project_id?: string;
  created_at: string;
}

/** Replies suggested for the transcript ending with item `item_id`; both empty when none are offered. */
export interface Suggestions {
  item_id: string;
  replies: string[];
}

/** One Utility model call (Background AI); tokens are the provider's figures unless `estimated` (4 characters a token). */
export interface UtilityCall {
  id: number;
  at: string;
  /** The server's local date, YYYY-MM-DD. */
  day: string;
  purpose: string;
  provider?: string;
  model?: string;
  task_id?: string;
  project_id?: string;
  prompt_chars: number;
  reply_chars: number;
  input_tokens: number;
  output_tokens: number;
  estimated?: boolean;
  credits?: number;
  duration_ms: number;
  outcome: 'ok' | 'error' | 'skipped';
  /** Why it was skipped (`daily_limit`, `off`) or failed. */
  reason?: string;
}

export interface UtilityDay {
  day: string;
  calls: number;
  errors: number;
  skipped: number;
  prompt_chars: number;
  reply_chars: number;
  input_tokens: number;
  output_tokens: number;
  estimated?: boolean;
  credits?: number;
}

/** GET /api/utility: today against the daily limit, every day kept with its totals, and a page of calls, newest first. */
export interface UtilityLog {
  today: { day: string; calls: number; limit: number; paused: boolean; resets_at: string };
  days: UtilityDay[];
  calls: UtilityCall[];
  /** Pass as `before` for the next page; absent on the last. */
  next?: number;
}

/* ---------- The planner (ADR 0005 §14–§15) ---------- */

export type CardKind = 'epic' | 'story' | 'subtask';
/** A subtask's stored status; a container's is derived from its confirmed subtasks and is never `todo`. */
export type CardStatus = 'planned' | 'todo' | 'doing' | 'done' | 'cancelled';

export interface ChecklistItem {
  text: string;
  done: boolean;
}

/** Done ÷ non-cancelled confirmed subtasks, counted; `proposed` is the unconfirmed ones. Containers only. */
export interface CardProgress {
  done: number;
  total: number;
  proposed: number;
}

/** How far a confirmed, unheld subtask is behind HEAD since its pin (§9). */
export interface Staleness {
  behind: number;
  diverged: boolean;
  files: string[];
}

/** One node on a Board: an epic, a story or a subtask. `{ref}` in a route is its id or its `#seq`. */
export interface Card {
  id: string;
  seq: number;
  /** Empty for the Unassigned list (kb import cards not moved into a Project yet). */
  project_id: string;
  kind: CardKind;
  parent_id: string | null;
  rank: number;
  title: string;
  desc: string;
  win_condition: string;
  status: CardStatus;
  progress?: CardProgress;
  prio: number;
  due?: string;
  effort?: 'S' | 'M' | 'L' | '';
  labels: string[];
  checklist: ChecklistItem[];
  blocked: boolean;
  blocked_by: string[];
  blocks: string[];
  confirmed: boolean;
  /** Sent only while the card is unconfirmed. */
  expires_at?: string;
  /** The Task holding a doing subtask. */
  held_by?: string;
  pinned_sha: string;
  /** Owner only: null inherits the Project default, `''` means none, otherwise the command. */
  accept_cmd: string | null;
  /** Owner only: globs the staleness check watches. */
  paths: string[];
  /** Sent only once computed. */
  stale?: Staleness;
  pending_requests: number;
  revision: number;
  created_at: string;
  updated_at: string;
  moved_at: string;
}

export type RequestKind = 'done' | 'cancel' | 'blocked' | 'split' | 'change';
export type RequestFlag = 'acceptance_could_not_run' | 'baseline_missing' | 'no_change_in_tree' | 'tests_or_build_changed' | 'overlap';

/** The evidence rows of a done request (§6). */
export interface Evidence {
  baseline?: { head: string; dirty: string[] };
  /** `pre_dirty`: the path was uncommitted when the hold began, and counts because its content changed since. */
  diff?: { added: number; deleted: number; files: { path: string; added: number; deleted: number; by_task: boolean; pre_dirty?: boolean; overlap?: { card: number; task_id: string } }[] };
  commits?: { sha: string; subject: string }[];
  accept?: AcceptRun;
  /** `partial`: uam's copy of the transcript may not reach back to the hold's start, so the touched files may be incomplete. */
  transcript?: { task_id: string; from_item: string; to_item: string; partial?: boolean };
  checklist?: { done: number; total: number };
}

export interface AcceptRun {
  cmd: string;
  cmd_hash: string;
  head: string;
  dirty: boolean;
  exit: number;
  tail: string;
  ran_at: string;
  /** The command changed since this run. */
  stale: boolean;
}

/** An agent's ask for an owner decision on a card; the pending ones are the Inbox (the ADR's Request). */
export interface BoardRequest {
  id: string;
  card_id: string;
  task_id: string;
  agent_id: string;
  kind: RequestKind;
  comment: string;
  /**
   * split: `{children: [{title, win_condition}]}`; change: `{patch: {the proposed fields}, proposed_accept_cmd?}`;
   * blocked: `{blocker}` (a card id); a done request a split filed for a ticked item: `{split_of, tick}`.
   */
  payload: Record<string, unknown>;
  evidence: Evidence;
  flags: RequestFlag[];
  base_revision: number;
  status: 'pending' | 'accepted' | 'rejected' | 'withdrawn';
  created_at: string;
  decided_at?: string;
  decision_comment?: string;
  /** Who decided: the owner, or `uam` for a done request accepted automatically because its acceptance command passed. */
  decided_by?: 'owner' | 'uam';
}

export interface CardComment {
  id: string;
  /** `owner`, `task:<id>` or `uam` (automatic comments). */
  author: string;
  body: string;
  automatic: boolean;
  created_at: string;
}

/** One attempt: a hold from its start to its end. */
export interface Hold {
  id: string;
  task_id: string;
  started_at: string;
  baseline_head: string;
  baseline_dirty: string[];
  ended_at?: string;
  end_reason?: string;
}

/** `GET /api/board`: one Project's cards, its pending requests and its revision. */
export interface BoardData {
  cards: Card[];
  requests: BoardRequest[];
  revision: number;
}

export interface CardDetail {
  card: Card;
  comments: CardComment[];
  requests: BoardRequest[];
  holds: Hold[];
}

export type BoardFrame = Extract<UpdateData, { name: 'board' }>;
export type BoardJob = Extract<UpdateData, { name: 'board_job' }>;

export type TriageVerdict = 'valid' | 'moot' | 'conflicts';

export interface ImportReport {
  imported: number;
  updated: number;
  unassigned: number;
  comments: number;
  links: number;
  skipped: { id: string; reason: string }[];
}

/** The fields an owner edit may carry (`PATCH /api/board/cards/{ref}`); the edit confirms the card. */
export type CardPatch = Partial<Pick<Card, 'title' | 'desc' | 'win_condition' | 'prio' | 'effort' | 'due' | 'labels' | 'checklist' | 'accept_cmd' | 'paths' | 'project_id'>>;

/** What Settle decides for each subtask the Task holds (§5). */
export type HoldDecision = { action: 'keep' | 'release' | 'cancel'; comment: string };

/**
 * The planner's error code, when a refusal carries one (`planner_off`, `no_git`, `guard_open_items`,
 * `holds_undecided`, `invalid`, `not_found`, …). The UI decides on the code, never on the status.
 */
export function errorCode(e: unknown): string | undefined {
  return e instanceof ApiError && typeof e.body.code === 'string' ? e.body.code : undefined;
}

/** What a refusal was about (`refs`): the open checklist items of `guard_open_items`, the `#seq` of each open blocker of `guard_blockers`. */
function errorRefs(e: unknown): string[] {
  return e instanceof ApiError && Array.isArray(e.body.refs) ? e.body.refs.map(String) : [];
}

/** A route this service does not have yet (a planner feature that lands later): 404 with no code. */
export function routeMissing(e: unknown): boolean {
  return isStatus(e, 404) && errorCode(e) === undefined;
}

/**
 * A planner refusal in words: the service's own message, except where the UI can say it better
 * (the planner off or unavailable, a busy acceptance run or import, a feature not here yet).
 */
export function plannerErrorText(e: unknown): string {
  if (routeMissing(e)) return 'this service does not offer it yet';
  switch (errorCode(e)) {
    case 'planner_off':
      return 'the planner is off; turn it on in Settings';
    case 'planner_unavailable':
      return 'the planner database could not be opened; see the service log';
    case 'acceptance_busy':
      return 'the acceptance command is already running in this project; try again in a moment';
    case 'import_busy':
      return 'the source board changed while it was copied; try again in a moment';
  }
  return describeError(e);
}

/** What stops an owner's Mark done (`guard_open_items`, `guard_blockers` or `guard_blocked`, §6), or null for any other outcome. */
export type DoneGuard = { code: 'guard_open_items'; items: string[] } | { code: 'guard_blockers'; blockers: string[] } | { code: 'guard_blocked' };

export function doneGuard(e: unknown): DoneGuard | null {
  const code = errorCode(e);
  if (code === 'guard_open_items') return { code, items: errorRefs(e) };
  if (code === 'guard_blockers') return { code, blockers: errorRefs(e) };
  if (code === 'guard_blocked') return { code };
  return null;
}

/** The subtasks a Settle left undecided (`holds_undecided`), or null for any other outcome. */
export function undecidedHolds(e: unknown): Card[] | null {
  if (!(e instanceof ApiError) || errorCode(e) !== 'holds_undecided') return null;
  return Array.isArray(e.body.cards) ? (e.body.cards as Card[]) : [];
}

/** A rejected request, and whether its reason reached the Task (§14). When it did not and the subtask is still held, the owner may Release it. */
export type Rejection = BoardRequest & { steered: boolean };

/**
 * A custom (BYOM) model, offered by Copilot as `name/model_id`. No key passes through the
 * browser: `api_key_env` names the service environment variable that holds it.
 */
export interface CustomModel {
  /** Provider name: letters, digits, `.`, `_`, `-`. Models with one name share its URL and key variable. */
  name: string;
  display_name?: string;
  base_url: string;
  model_id: string;
  wire_api?: 'completions' | 'responses';
  api_key_env: string;
  /** Output only: whether the variable is set and non-empty in the service environment. */
  key_present?: boolean;
}

export interface Project {
  id: string;
  name: string;
  dir: string;
  created_at: string;
  badge: Badge;
  /** Current git branch of the directory; absent unless it is a checkout on a named branch. May change between `project` frames. */
  branch?: string;
  /** Why the directory has no Changes or Files views: git is not installed, or the directory is in no Git repository. Absent in a repository and when git cannot tell. */
  no_git?: 'not_installed' | 'not_repository';
  /** How many charts are pinned to the Project; absent when none. */
  charts?: number;
}

/** A chart an agent drew with `uam_chart`: what it shows, where its rows came from, and the rows column-wise. */
export interface Chart {
  title: string;
  kind: 'line' | 'bar';
  x_label?: string;
  y_label?: string;
  /** The shell command that printed the rows, run in the Project directory; absent for rows the agent passed. */
  command?: string;
  format?: 'csv' | 'json';
  x: string;
  y: string[];
  labels: string[];
  series: { name: string; values: number[] }[];
  /** When the rows were read. */
  at: string;
  /** The Project's pinned copy of this chart, if any. */
  pinned_id?: string;
}

/** A chart pinned to a Project. `error` says why the latest refresh failed; the rows are then the last good ones. */
export interface PinnedChart extends Omit<Chart, 'pinned_id'> {
  id: string;
  project_id: string;
  pinned_at: string;
  error?: string;
  error_at?: string;
}

export interface SessionSummary {
  id: string;
  project_id: string;
  provider: string;
  /** User-given name; empty means "display the provider title". */
  name: string;
  /** Provider-generated conversation title; empty until it arrives. */
  title: string;
  workdir: string;
  conversation_id: string;
  /** Selected model; empty means the provider default (and cannot be set back to empty). */
  model: string;
  /** Model reported by the latest turn; live only, empty when unknown. */
  last_model: string;
  subagents_running: number;
  /** Background shell tasks of the open conversation that have not finished; absent from older servers. */
  background_tasks_running?: number;
  effort?: string;
  context_size?: string;
  context?: ContextUsage;
  /** AI units the conversation used so far, once the provider reports them; never zero. */
  usage?: { ai_units: number };
  mode?: 'safe' | 'yolo';
  execution?: ExecutionState | null;
  stage?: 'active' | 'settled' | 'archived';
  /** When the Task was settled (cleared by Reopen) and archived; absent otherwise and from older records. */
  settled_at?: string;
  archived_at?: string;
  /** The Task whose agent started this one with uam_create_task; absent otherwise. */
  spawned_by?: string;
  /** The routine whose run started this Task; absent otherwise. */
  routine_id?: string;
  /** This Task's own changes (the Changes "This task" scope) once known; absent while it has none. */
  diff?: DiffStat;
  /** The Task whose last message this one runs again (Run again, Try with another model); absent otherwise. */
  rerun_of?: string;
  /** The last completed turn in one line ("Fixed the flaky test; 3 files changed; tests pass"); absent while a turn runs or when there is nothing to say. */
  outcome?: string;
  /** Set while the conversation is being compacted (/compact or the provider's automatic compaction); absent otherwise. */
  compacting?: boolean;
  queued?: number;
  state: SessionState;
  state_detail?: string;
  open: boolean;
  pending: number | boolean;
  /** The request the Task waits on, for the Task list to answer in place; absent when nothing waits, and from older servers. */
  ask?: Ask;
  /** When the provider last reported anything for the open conversation, to the minute; absent until it does. */
  event_at?: string;
  created_at: string;
  updated_at: string;
  capabilities: Capabilities;
}

export type ItemKind = 'user' | 'assistant' | 'reasoning' | 'tool' | 'notice';
export type ToolStatus = 'pending' | 'running' | 'completed' | 'failed';

/** Display metadata from a successful local file declaration; opening still checks the file. */
export interface FileDeclaration {
  artifact_id: string;
  path: string;
  title?: string;
  type_hint?: string;
}

/** A planner card as a `board_*` tool's result names it. */
export interface ToolBoardCard {
  id: string;
  seq: number;
  kind: 'epic' | 'story' | 'subtask';
  title: string;
  status: string;
}

export interface ToolCall {
  name: string;
  title?: string;
  status: ToolStatus;
  input?: string;
  output?: string;
  /** Exact local tool metadata; eligibility only, never proof that a file exists. */
  file_paths?: string[];
  declaration?: FileDeclaration;
  display_arg?: string;
  path?: string;
  has_input?: boolean;
  has_output?: boolean;
  /** The planner card a completed `board_*` call was about, read from its result. */
  board_card?: ToolBoardCard;
  /** Client-only semantic outcome retained after page eviction. */
  question_outcome?: 'pending' | 'answered' | 'declined' | 'none' | 'failed';
}

/** An image a tool's result returned, stored with the Task; served by the attachment route. */
export interface ToolImage {
  id: string;
  mime: string;
  size: number;
  name?: string;
}

export interface Item {
  compact?: { has_reasoning: boolean; has_text: boolean };
  id: string;
  kind: ItemKind;
  delivery?: 'steer' | 'autopilot';
  /** A local steer receipt; absent after the provider records its user message. */
  steer_status?: 'accepted' | 'not_delivered';
  text?: string;
  tool?: ToolCall;
  /** When it began: a tool call's start, a thought's model call start. */
  time: string;
  /** When a tool call or thought finished, from the provider's record; absent while it runs or when unknown. */
  ended_at?: string;
  /** Subagent instance that produced the item; absent for the main agent. */
  agent_id?: string;
  /** Uploads a user item carried; never their bytes. */
  attachments?: Attachment[];
  /** Images a tool item's result returned, in the provider's order. */
  images?: ToolImage[];
  /** Why some of a tool item's images were left out. */
  images_note?: string;
  /** The service holds this item's texts shortened; its body (the item route, detail `body` frames) is whole. */
  clipped?: boolean;
}

/** `idle` is not terminal: the subagent finished and accepts a follow-up (see promptSubagent). */
export type SubagentStatus = 'running' | 'idle' | 'completed' | 'failed' | 'cancelled';

export interface Subagent {
  preview?: string;
  result_summary?: string;
  /** Utility-model summary of this completed result, when generation succeeded. */
  summary?: string;
  id: string;
  /** Item id of the `task` tool call that started this subagent (a tool item's id is the provider tool call id). */
  parent_tool_call_id?: string;
  name: string;
  description?: string;
  /** Failed and cancelled are final; completed turns idle only on the provider's report. Close, runtime exit and service stop mark running ones cancelled and idle ones completed. */
  status: SubagentStatus;
  error?: string;
  started_at?: string;
  ended_at?: string;
  /** Model id and effort level the subagent runs with, when the provider reports them. */
  model?: string;
  effort?: string;
}

/** Live provider-owned shells. Unknown snapshots retain the last observation only. */
export interface BackgroundTasks {
  known: boolean;
  tasks: { id: string; description?: string; command: string; status: string; started_at?: string; ended_at?: string }[];
}

/** Subagents recorded before a cursor, read from Copilot's record on request, oldest first. */
export interface SubagentPage extends Representation {
  seq: number;
  subagents: Subagent[];
  /** Cursor for the ones recorded before these; empty at the first. */
  before: string;
}

export interface SubagentDetail extends Representation {
  before?: string;
  /** SSE sequence captured with the transcript and metadata. */
  seq: number;
  subagent: Subagent;
  items: Item[];
}

export type InteractionKind = 'permission' | 'question';
export type InteractionState = 'pending' | 'answered' | 'rejected' | 'expired';

export interface Option {
  id: string;
  label: string;
  reject?: boolean;
}

export interface Question {
  text: string;
  header?: string;
  choices?: string[];
  multiple?: boolean;
  custom: boolean;
}

export interface Interaction {
  id: string;
  kind: InteractionKind;
  title: string;
  detail?: string;
  options?: Option[];
  questions?: Question[];
  state: InteractionState;
  resolution?: string;
  time: string;
  agent_id?: string;
  /** The `tool` item (same `agent_id`) this request is for; absent or unmatched means no link. */
  tool_call_id?: string;
  /** Yolo mode is answering this pending request: it does not wait for the user. */
  auto?: boolean;
}

/** A summary's `ask`: a permission's options and the first line of its detail, or a question's first prompt (`title`) with its choices. */
export interface Ask {
  id: string;
  kind: InteractionKind;
  title: string;
  detail?: string;
  options?: Option[];
  choices?: string[];
  multiple?: boolean;
  custom?: boolean;
  /** How many prompts the question has; the list answers only one in place. */
  questions?: number;
}

export interface Answer {
  decision?: string;
  answers?: string[][];
  reject?: boolean;
}

export type PromptMode = 'send' | 'steer' | 'queue';
export type PromptSettings = Pick<TaskDefaults, 'model' | 'effort' | 'context_size'>;
export type SubmissionStatus = 'accepted' | 'rejected' | 'uncertain' | 'queued' | 'cancelled';
export interface QueuedPrompt {
  request_id: string;
  text: string;
  queued_at: string;
  files?: string[];
  attachments?: Attachment[];
  settings?: PromptSettings;
}

/** Structured parts of a prompt beside its text. */
export interface PromptExtras {
  /** Project paths relative to the Task's directory (at most 20). */
  files?: string[];
  /** Upload IDs from `api.upload` (at most 5). */
  attachments?: string[];
}

export interface Submission {
  request_id: string;
  status: SubmissionStatus;
  command_result?: CommandResult;
  error?: string;
  time: string;
}

export interface PreviousSession {
  provider: string;
  conversation_id: string;
  title: string;
  created_at: string;
  updated_at: string;
  in_use: boolean;
}

export interface TurnTiming {
  id: string;
  user_item_id?: string;
  started_at: string;
  ended_at?: string;
  paused_at?: string;
  paused_ms?: number;
  state: 'working' | 'completed' | 'cancelled' | 'failed' | 'unknown';
}

export interface Representation {
  representation?: 'compact-v1';
  epoch?: string;
  detail_stream?: boolean;
}

export interface BodyReference { agentId: string; itemId: string; coveredSeq?: number }
export interface BodyData { seq: number; epoch: string; session_id: string; agent_id?: string; item: Item }
export interface DetailSnapshot { seq: number; epoch: string; session_id: string; agent_id?: string; subagent?: Subagent; items?: Item[]; before?: string; after?: string; range?: boolean; range_reset?: boolean; scope?: 'window' }
export type DetailFrame =
  | ({ name: 'detail_snapshot' | 'detail_page' } & DetailSnapshot)
  | ({ name: 'body' } & BodyData)
  | { name: 'body_current'; seq: number; epoch: string; session_id: string; agent_id?: string; item_id: string }
  | { name: 'body_unavailable'; seq: number; epoch: string; session_id: string; agent_id?: string; item_id: string }
  | { name: 'detail_ready'; seq: number; epoch: string; session_id: string }
  | { name: 'detail_reset'; seq: number; epoch: string; session_id: string }
  | { name: 'body_delta' | 'body_output'; seq: number; epoch: string; session_id: string; agent_id?: string; item_id: string; kind?: ItemKind; text: string }
  | (Extract<UpdateData, { name: 'item' | 'delta' | 'items_trimmed' }> & { epoch?: string });

export interface SessionDetail extends SessionSummary, Representation {
  turn_timings?: TurnTiming[];
  seq?: number;
  history?: 'loaded' | 'loading' | 'unavailable';
  history_reason?: string;
  /** Opaque cursor for earlier items. Empty at the beginning; absent on older servers. */
  history_before?: string;
  history_after?: string;
  /** Client-owned bounded live tail and lightweight grouping index. */
  recent_items?: Item[];
  recent_before?: string;
  history_index?: Item[];
  queue?: QueuedPrompt[];
  queue_paused?: boolean;
  /** Main agent items only; subagent items come from the subagent route. */
  items: Item[];
  interactions: Interaction[];
  subagents: Subagent[];
  /** Cursor for older subagents only Copilot's record still lists; absent when `subagents` has them all. */
  subagents_before?: string;
  background_tasks?: BackgroundTasks;
  history_truncated: boolean;
  last_submission: Submission | null;
}

export interface HistoryPage extends Representation {
  seq: number;
  items: Item[];
  before: string;
  after?: string;
  /** Older history read back from Copilot's record: immutable, so this browser may cache it (lib/historyArchive.ts). */
  archive?: boolean;
}

/** `task`: files this Task's agent edited; `turn`: those of its latest turn; `workspace`: every uncommitted change; `session`: the provider's own record. */
export type Scope = 'task' | 'turn' | 'workspace' | 'session';

export interface DiffStat {
  files: number;
  additions: number;
  deletions: number;
}

export interface ChangeFile {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  /** Changes whenever the file's content may have; absent where the scope cannot tell. */
  digest?: string;
}

export interface Changes {
  scope: Scope;
  label: string;
  supported: boolean;
  reason?: string;
  files: ChangeFile[];
  /** File counts of the task, turn and workspace scopes; set on those scopes. */
  counts?: { task: number; turn: number; workspace: number };
}

/** A changed file of the Task's repository, with whose edit tools touched it. */
export interface GitFile extends ChangeFile {
  /** This Task's edit tools touched it. */
  mine?: boolean;
  /** Another Task's edit tools touched it and this Task's did not. */
  other_task?: boolean;
}

/** The Task's repository as the commit panel shows it (`GET /api/sessions/{id}/git`). */
export interface GitState {
  /** False when the Task's folder is in no repository: `reason` says why, `can_init` whether "Set up git here" may run. */
  repo: boolean;
  reason?: string;
  can_init?: boolean;
  /** Absent when HEAD is detached. */
  branch?: string;
  /** The branch's upstream, e.g. `origin/main`; ahead/behind count against it as last fetched. */
  upstream?: string;
  ahead: number;
  behind: number;
  /** Whether Push has somewhere to go: an upstream, or a remote named origin. */
  remote: boolean;
  has_commits: boolean;
  files: GitFile[];
  /** False when edits from before the Task's history was read may be unknown (after a restart), so `mine` may be incomplete. */
  task_files_known: boolean;
  /** Why commit, push, pull and set-up are refused now (a Task in the repository could still be writing); absent when they may run. */
  busy?: string;
}

export interface GitResult {
  summary: string;
  commit?: string;
  output?: string;
}

/** A commit message the Utility model drafted; `conventional` says the repository's recent subjects use Conventional Commits. */
export interface CommitDraft {
  message: string;
  conventional: boolean;
  model: string;
}

export interface FileDiff {
  path: string;
  status?: string;
  additions: number;
  deletions: number;
  before?: string;
  after?: string;
  patch?: string;
}

export interface SnapshotData extends Representation {
  seq: number;
  projects: Project[];
  /** Absent from a service older than settings; the defaults apply then. */
  settings?: Settings;
  /** Absent from a service older than usage (#188). */
  usage?: AccountUsage;
  /**
   * With the planner on: each Board's revision by Project id (`''` for the Unassigned list), so
   * a new stream fetches again only the Boards that moved while no stream was open (ADR 0005 §15).
   */
  boards?: Record<string, number>;
  sessions: SessionSummary[];
  session: SessionDetail | null;
}

export type UpdateData =
  | { name: 'history'; seq: number; session_id: string; history: 'loaded' | 'loading' | 'unavailable'; history_reason?: string; history_before?: string; history_truncated: boolean; items: Item[]; subagents: Subagent[]; subagents_before?: string }
  | { name: 'queue'; seq: number; session_id: string; queue: QueuedPrompt[]; paused: boolean }
  | { name: 'session'; seq: number; session: SessionSummary }
  | { name: 'session_removed'; seq: number; session_id: string }
  | { name: 'project'; seq: number; project: Project }
  | { name: 'project_removed'; seq: number; project_id: string }
  | { name: 'settings'; seq: number; settings: Settings }
  | { name: 'usage'; seq: number; usage: AccountUsage }
  | { name: 'item'; seq: number; session_id: string; item: Item; agent_id?: string; append?: boolean }
  | { name: 'tool_output'; seq: number; session_id: string; item_id: string; text: string; agent_id?: string }
  | { name: 'items_trimmed'; seq: number; session_id: string; items: { id: string; agent_id?: string }[] }
  | {
      name: 'delta';
      seq: number;
      session_id: string;
      item_id: string;
      kind: ItemKind;
      text: string;
      agent_id?: string;
    }
  | { name: 'interaction'; seq: number; session_id: string; interaction: Interaction }
  | { name: 'submission'; seq: number; session_id: string; submission: Submission }
  | { name: 'subagent'; seq: number; session_id: string; subagent: Subagent }
  | { name: 'turn_timing'; seq: number; session_id: string; turn_timing: TurnTiming }
  | { name: 'background_tasks'; seq: number; session_id: string; background_tasks: BackgroundTasks }
  /** One committed planner write, to everyone (§15); `project_id` is empty for the Unassigned list. */
  | { name: 'board'; seq: number; project_id: string; revision: number; cards: Card[]; removed: string[]; requests: BoardRequest[] }
  /** A planner job on a card: a suggestion, or a Check at HEAD whose last frame carries its run (`accept`). */
  | { name: 'board_job'; seq: number; job_id: string; card_id: string; kind: 'suggest' | 'check'; status: 'running' | 'done' | 'failed'; error?: string; accept?: AcceptRun };

export const UPDATE_EVENTS = [
  'session',
  'history',
  'session_removed',
  'project',
  'project_removed',
  'settings',
  'usage',
  'item',
  'delta',
  'tool_output',
  'items_trimmed',
  'interaction',
  'submission',
  'queue',
  'subagent',
  'background_tasks',
  'turn_timing',
  'board',
  'board_job',
] as const;

export class ApiError extends Error {
  status: number;
  /** Parsed JSON error body, when the server sent one (e.g. `project_id` on 409). */
  body: Record<string, unknown>;
  constructor(status: number, message: string, body: Record<string, unknown> = {}) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

let unauthorized: () => void = () => {};
const authLossListeners = new Set<() => void>();
export function subscribeAuthLoss(listener: () => void): () => void {
  authLossListeners.add(listener);
  return () => { authLossListeners.delete(listener); };
}
function notifyUnauthorized() {
  authLossListeners.forEach(listener => listener());
  unauthorized();
}

/** Registers the handler run when any request (other than login) gets a 401. */
export function onUnauthorized(fn: () => void): void {
  unauthorized = fn;
}

export function describeError(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export function isStatus(e: unknown, status: number): e is ApiError {
  return e instanceof ApiError && e.status === status;
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

async function call<T>(method: Method, path: string, body?: unknown, omitBody = false, signal?: AbortSignal): Promise<T> {
  // GET and DELETE carry no body; the others are JSON (the server rejects anything else).
  const bodyless = method === 'GET' || method === 'DELETE';
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      signal,
      headers: bodyless ? undefined : { 'Content-Type': 'application/json' },
      body: bodyless || omitBody ? undefined : JSON.stringify(body ?? {}),
    });
  } catch {
    throw new ApiError(0, 'Could not reach the server');
  }
  if (res.status === 401 && path !== '/api/login') notifyUnauthorized();
  if (!res.ok) {
    const parsed = await errorBody(res);
    throw new ApiError(res.status, parsed.message, parsed.body);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

async function errorBody(res: Response): Promise<{ message: string; body: Record<string, unknown> }> {
  try {
    const j = (await res.json()) as Record<string, unknown>;
    if (j && typeof j === 'object') {
      const message = typeof j.error === 'string' && j.error ? j.error : `${res.status} ${res.statusText}`.trim();
      return { message, body: j };
    }
  } catch {
    // not JSON
  }
  return { message: `${res.status} ${res.statusText}`.trim(), body: {} };
}

/** A file the service sends as an attachment, with the name its Content-Disposition gives. */
async function download(path: string): Promise<{ blob: Blob; name: string }> {
  let res: Response;
  try {
    res = await fetch(path, { credentials: 'same-origin' });
  } catch {
    throw new ApiError(0, 'Could not reach the server');
  }
  if (res.status === 401) notifyUnauthorized();
  if (!res.ok) {
    const parsed = await errorBody(res);
    throw new ApiError(res.status, parsed.message, parsed.body);
  }
  const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'download';
  return { blob: await res.blob(), name };
}

/** Click-only file reads share admission and authentication handling with other UI reads. */
async function filePreview(url: string, signal: AbortSignal, knownMetadata?: PreviewMetadata): Promise<PreviewMetadata & Partial<TextPreview>> {
  return foregroundRead(async () => {
    const request = async (method: 'HEAD' | 'GET') => {
      const response = await fetch(url, { method, credentials: 'same-origin', signal, headers: method === 'GET' ? { Range: `bytes=0-${PREVIEW_BYTES - 1}` } : undefined });
      signal.throwIfAborted();
      if (response.status === 401) notifyUnauthorized();
      if (!response.ok && !(method === 'GET' && response.status === 416)) {
        await response.body?.cancel();
        throw new ApiError(response.status, response.status === 404 ? 'This file is no longer available.' : 'The file could not be opened.');
      }
      return response;
    };
    const metadata = knownMetadata ?? previewMetadata((await request('HEAD')).headers);
    if (metadata.kind !== 'text') return metadata;
    const response = await request('GET');
    if (response.status !== 416 && previewMetadata(response.headers).kind !== 'text') {
      await response.body?.cancel();
      throw new Error('The file type changed. Close and open the preview again.');
    }
    return { ...metadata, ...await readTextPreview(response, signal) };
  }, signal);
}

const enc = encodeURIComponent;

/** The query of a directory listing: the path and the hidden flag, each only when given. */
function dirsQuery(path: string | undefined, hidden: boolean): string {
  if (path) return `?path=${enc(path)}${hidden ? '&hidden=1' : ''}`;
  return hidden ? '?hidden=1' : '';
}

/** The detail stream's query: the agent window's cursors only when given, then one `item` per body. */
function detailEventsQuery(id: string, agentId: string, bodies: BodyReference[], agentBefore?: string, epoch?: string, agentUntil?: string): string {
  const query = [`session=${enc(id)}`];
  if (agentId) query.push(`agent=${enc(agentId)}`);
  if (agentBefore) query.push(`agent_before=${enc(agentBefore)}`);
  if (epoch) query.push(`epoch=${enc(epoch)}`);
  if (agentUntil) query.push(`agent_until=${enc(agentUntil)}`);
  for (const b of bodies) query.push(`item=${enc(JSON.stringify(b.coveredSeq === undefined ? [b.agentId, b.itemId] : [b.agentId, b.itemId, b.coveredSeq]))}`);
  return query.join('&');
}

/** An env variable or header of an MCP server: its name and that a value is stored; the value never leaves the service. */
export interface McpSecret {
  key: string;
  set: boolean;
}

export type McpType = 'stdio' | 'http' | 'sse';

/** One server of the provider's user-wide MCP configuration (Settings → MCP servers). `source` other than `user` is read-only. */
export interface McpServer {
  name: string;
  type: McpType | '';
  command?: string;
  args?: string[];
  cwd?: string;
  url?: string;
  env: McpSecret[];
  headers: McpSecret[];
  enabled: boolean;
  source: string;
}

export interface McpServers {
  available: boolean;
  /** Settings → Shell access → Terminal: a server that runs a command can be added or edited only while it is on. */
  stdio_allowed: boolean;
  servers: McpServer[];
}

/** An env variable or header to save; no `value` keeps the stored one (edit only). */
export interface McpSecretInput {
  key: string;
  value?: string;
}

export interface McpServerInput {
  name?: string;
  type: McpType;
  command?: string;
  args?: string[];
  cwd?: string;
  url?: string;
  env?: McpSecretInput[];
  headers?: McpSecretInput[];
}

export interface McpTool {
  name: string;
  description?: string;
}

/** One MCP server as a Task's conversation sees it. */
export interface McpStatus {
  name: string;
  status: 'connected' | 'failed' | 'needs-auth' | 'pending' | 'disabled' | 'stopped' | 'not_configured' | (string & {});
  error?: string;
  source?: string;
  /** A remote server, which may need a sign-in. */
  remote?: boolean;
  tools?: McpTool[];
}

/** A started MCP sign-in: the page to open (none when a kept sign-in sufficed) and whether the address the browser ends on can be pasted back. */
export interface McpSignIn {
  url?: string;
  relay?: boolean;
}

export const api = {
  auth: () => call<{ authenticated: boolean; required?: boolean }>('GET', '/api/auth'),
  login: (token: string) => call<void>('POST', '/api/login', { token }),
  logout: () => call<void>('POST', '/api/logout'),
  meta: () => call<Meta>('GET', '/api/meta'),
  account: (provider: string) => call<ProviderAccount>('GET', `/api/providers/${enc(provider)}/account`),
  /** The token goes to the provider's runtime only; nothing here keeps it. */
  signIn: (provider: string, token: string) => call<ProviderAccount>('POST', `/api/providers/${enc(provider)}/account/sign-in`, { token }),
  signOut: (provider: string) => call<ProviderAccount>('POST', `/api/providers/${enc(provider)}/account/sign-out`),

  session: (id: string) => call<SessionDetail>('GET', `/api/sessions/${enc(id)}?history=recent&view=compact-v1`),
  history: (id: string, before: string, signal?: AbortSignal, direction: 'older' | 'newer' = 'older') => foregroundRead(() => call<HistoryPage>('GET', `/api/sessions/${enc(id)}/history?${direction === 'older' ? 'before' : 'after'}=${enc(before)}&view=compact-v1`, undefined, false, signal), signal),
  previous: (id: string) => call<PreviousSession[]>('GET', `/api/projects/${enc(id)}/previous`),
  importPrevious: (id: string, conversationId: string) => call<SessionSummary>('POST', `/api/projects/${enc(id)}/previous/${enc(conversationId)}/import`),

  projects: async () => (await call<{ projects: Project[] }>('GET', '/api/projects')).projects,
  createProject: (body: { dir: string; name?: string }) => call<Project>('POST', '/api/projects', body),
  updateProject: (id: string, body: { name: string }) => call<Project>('PATCH', `/api/projects/${enc(id)}`, body),
  deleteProject: (id: string) => call<void>('DELETE', `/api/projects/${enc(id)}`),
  /** Subdirectories of an absolute directory; the service user's home without `path`. Dot-folders only with `hidden`; the 1,000 cap counts what is listed. */
  listDirs: (path?: string, hidden = false) => call<DirList>('GET', `/api/fs/dirs${dirsQuery(path, hidden)}`),
  makeDir: (parent: string, name: string) => call<{ path: string }>('POST', '/api/fs/dirs', { parent, name }),

  webSettings: () => call<Settings>('GET', '/api/settings'),
  /** The service refuses an unknown key or value with 400 and changes nothing. */
  updateWebSettings: (body: Partial<Settings>) => call<Settings>('PATCH', '/api/settings', body),
  /** The model IDs an OpenAI-compatible endpoint lists; the service fetches them with the named key variable. */
  discoverModels: (body: { base_url: string; api_key_env: string; wire_api?: string }) =>
    call<{ models: string[]; truncated?: boolean; key_present: boolean }>('POST', '/api/settings/custom-models/discover', body),
  /** The cached account quotas; never calls the provider. */
  usage: () => call<AccountUsage>('GET', '/api/usage'),
  utility: (before?: number, limit = 25) => call<UtilityLog>('GET', `/api/utility?limit=${limit}${before ? `&before=${before}` : ''}`),
  /** Web Push: the service's public key, and this browser's subscription (lib/notify.ts). */
  pushKey: () => call<{ public_key: string }>('GET', '/api/push'),
  pushSubscribe: (subscription: PushSubscriptionJSON) => call<void>('POST', '/api/push/subscribe', subscription),
  pushUnsubscribe: (endpoint: string) => call<void>('POST', '/api/push/unsubscribe', { endpoint }),

  createSession: (body: {
    project_id: string;
    provider: string;
    model?: string;
    effort?: string;
    context_size?: string;
    mode?: 'safe' | 'yolo';
    name?: string;
    prompt?: string;
    request_id: string;
  }) => call<SessionSummary>('POST', '/api/sessions', body),
  rename: (id: string, name: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { name }),
  setModel: (id: string, model: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { model }),
  settings: (id: string, body: { model?: string; effort?: string; context_size?: string; mode?: 'safe' | 'yolo' }) =>
    call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, body),
  stage: (id: string, action: 'settle' | 'reopen' | 'archive') => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/${action}`),
  queueAction: (id: string, action: 'resume' | 'clear') => call<void>('POST', `/api/sessions/${enc(id)}/queue/${action}`),
  cancelQueued: (id: string, requestId: string) => call<void>('DELETE', `/api/sessions/${enc(id)}/queue/${enc(requestId)}`),
  cancelSubagent: (id: string, agentId: string) => call<Subagent>('POST', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}/cancel`, undefined, true),
  /** Follow-up to an idle subagent; the main agent never sees it. A repeated request_id returns the recorded outcome without resending. */
  promptSubagent: (id: string, agentId: string, text: string, request_id: string) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}/prompt`, { text, request_id }),
  deleteSession: (id: string) => call<void>('DELETE', `/api/sessions/${enc(id)}`),
  /** Replies to send next for the Task's last completed turn; the service asks the Utility model once per state. */
  suggestions: (id: string, signal?: AbortSignal) => call<Suggestions>('POST', `/api/sessions/${enc(id)}/suggestions`, undefined, false, signal),
  /** A new Task in the same Project with the same settings (or `model`) whose first message is this Task's last one. */
  rerun: (id: string, body: { model?: string; request_id: string }) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/rerun`, body),
  /** The whole conversation as a Markdown file. */
  exportMarkdown: (id: string) => download(`/api/sessions/${enc(id)}/export`),
  addPrompt: (body: { name: string; text: string; project_id?: string }) => call<SavedPrompt>('POST', '/api/prompts', body),
  renamePrompt: (id: string, name: string) => call<SavedPrompt>('PATCH', `/api/prompts/${enc(id)}`, { name }),
  deletePrompt: (id: string) => call<void>('DELETE', `/api/prompts/${enc(id)}`),
  prompt: (id: string, text: string, request_id: string, mode: PromptMode = 'send', extras: PromptExtras & { settings?: PromptSettings } = {}) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/prompt`, { text, request_id, mode, ...extras }),
  /** Runs a listed command; the rules of a send (409 while a turn runs, no queue or steer). */
  command: (id: string, name: string, args: string, request_id: string, extras: PromptExtras = {}) =>
    call<Submission>('POST', `/api/sessions/${enc(id)}/command`, { request_id, name, arguments: args, ...extras }),
  commands: async (id: string) => (await call<{ commands: Command[] }>('GET', `/api/sessions/${enc(id)}/commands`)).commands,
  files: (id: string, q: string, limit = 50) => call<FileList>('GET', `/api/sessions/${enc(id)}/files?q=${enc(q)}&limit=${limit}`),
  /** The same listing for a Project's directory: a new Task's `@` picker, before the Task exists. */
  projectFiles: (id: string, q: string, limit = 50) => call<FileList>('GET', `/api/projects/${enc(id)}/files?q=${enc(q)}&limit=${limit}`),
  /** The entries directly inside one folder of the Task's directory ("" is the top), folders first; git decides what is listed. */
  tree: (id: string, dir: string, signal?: AbortSignal) => call<FileList>('GET', `/api/sessions/${enc(id)}/files/tree?dir=${enc(dir)}`, undefined, false, signal),
  upload: uploadFile,
  filePreview,
  createFileGrant: (id: string, path: string, signal: AbortSignal) => call<FileGrant>('POST', `/api/sessions/${enc(id)}/file-grants`, { path }, false, signal),
  revokeFileGrant: (id: string, grantId: string) => call<void>('DELETE', `/api/sessions/${enc(id)}/file-grants/${enc(grantId)}`),
  resolveFiles: (id: string, paths: string[], signal?: AbortSignal) => foregroundRead(() => call<{ files: { path: string; exists: boolean; kind: 'file' | 'unavailable' }[] }>('POST', `/api/sessions/${enc(id)}/files/resolve`, { paths }, false, signal), signal, 'low'),
  attachmentUrl: (id: string, attachmentId: string) => `/api/sessions/${enc(id)}/attachments/${enc(attachmentId)}`,
  /** An image file of the Task's directory, by absolute path or one relative to it. */
  rawFileUrl: (id: string, path: string) => `/api/sessions/${enc(id)}/files/raw?path=${enc(path)}`,
  /** Any file of the Task's directory by its "/"-separated path relative to it, as a path so a page's relative links resolve to its siblings. */
  viewFileUrl: (id: string, path: string) => `/api/sessions/${enc(id)}/files/view/${path.split('/').map(enc).join('/')}`,
  cancelBackgroundTask: (id: string, taskId: string) => call<{ accepted: true; background_tasks: BackgroundTasks }>('POST', `/api/sessions/${enc(id)}/background-tasks/${enc(taskId)}/cancel`),
  cancel: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/cancel`),
  close: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/close`),
  respond: (id: string, iid: string, answer: Answer) =>
    call<Interaction>('POST', `/api/sessions/${enc(id)}/interactions/${enc(iid)}`, answer),
  changes: (id: string, scope: Scope, signal?: AbortSignal) => call<Changes>('GET', `/api/sessions/${enc(id)}/changes?scope=${scope}`, undefined, false, signal),
  changeFile: (id: string, scope: Scope, path: string, signal?: AbortSignal) =>
    call<FileDiff>('GET', `/api/sessions/${enc(id)}/changes/file?scope=${scope}&path=${enc(path)}`, undefined, false, signal),
  /** Git actions refuse with 409 `git_busy` while a Task in the repository could still be writing (GitState.busy). */
  git: (id: string, signal?: AbortSignal) => call<GitState>('GET', `/api/sessions/${enc(id)}/git`, undefined, false, signal),
  gitInit: (id: string) => call<GitState>('POST', `/api/sessions/${enc(id)}/git/init`),
  gitCommit: (id: string, paths: string[], message: string) => call<GitResult>('POST', `/api/sessions/${enc(id)}/git/commit`, { paths, message }),
  gitPush: (id: string) => call<GitResult>('POST', `/api/sessions/${enc(id)}/git/push`),
  gitPull: (id: string) => call<GitResult>('POST', `/api/sessions/${enc(id)}/git/pull`),
  /** Drafts a message on the Utility model; it never commits. */
  gitMessage: (id: string, paths: string[]) => call<CommitDraft>('POST', `/api/sessions/${enc(id)}/git/message`, { paths }),
  subagent: (id: string, agentId: string, signal?: AbortSignal) =>
    foregroundRead(() => call<SubagentDetail>('GET', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}`, undefined, false, signal), signal),
  olderSubagents: (id: string, before: string, signal?: AbortSignal) => foregroundRead(() => call<SubagentPage>('GET', `/api/sessions/${enc(id)}/subagents?before=${enc(before)}`, undefined, false, signal), signal),
  subagentHistory: (id: string, agentId: string, before: string, signal?: AbortSignal, direction: 'older' | 'newer' = 'older') => foregroundRead(() => call<HistoryPage>('GET', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}/history?${direction === 'older' ? 'before' : 'after'}=${enc(before)}&view=compact-v1`, undefined, false, signal), signal),
  itemBody: (id: string, itemId: string, agentId: string, signal?: AbortSignal) => foregroundRead(() => call<BodyData>('GET', `/api/sessions/${enc(id)}/items/${enc(itemId)}?agent_id=${enc(agentId)}`, undefined, false, signal), signal),
  detailEventsUrl: (id: string, agentId: string, bodies: BodyReference[], agentBefore?: string, epoch?: string, agentUntil?: string) => `/api/events/detail?${detailEventsQuery(id, agentId, bodies, agentBefore, epoch, agentUntil)}`,
  eventsUrl: (id: string | null) => (id ? `/api/events?session=${enc(id)}&tool_output=delta&history=recent&view=compact-v1` : '/api/events'),

  /**
   * Settle, deciding the subtasks the Task holds (§5): without a decision for each, the service
   * answers 409 `holds_undecided` with the held cards (`undecidedHolds`), and the Settle dialog asks.
   */
  settle: (id: string, holds?: Record<string, HoldDecision>) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/settle`, holds ? { holds } : {}),
  planner: plannerApi(),

  /** The chart a Task drew with the `uam_chart` call `callId`. */
  chart: (id: string, callId: string, signal?: AbortSignal) => call<Chart>('GET', `/api/sessions/${enc(id)}/chart?call=${enc(callId)}`, undefined, false, signal),
  pinChart: (id: string, callId: string) => call<PinnedChart>('POST', `/api/sessions/${enc(id)}/chart/pin`, { call_id: callId }),
  pinnedCharts: async (projectId: string, signal?: AbortSignal) => (await call<{ charts: PinnedChart[] }>('GET', `/api/projects/${enc(projectId)}/charts`, undefined, false, signal)).charts,
  /** Runs the chart's command again, no model involved: on demand at most once a minute (429 sooner); `auto` at most once an hour, else the chart as it is. */
  refreshChart: (projectId: string, chartId: string, auto = false) => call<PinnedChart>('POST', `/api/projects/${enc(projectId)}/charts/${enc(chartId)}/refresh`, { auto }),
  unpinChart: (projectId: string, chartId: string) => call<void>('DELETE', `/api/projects/${enc(projectId)}/charts/${enc(chartId)}`),
  mcpServers: () => call<McpServers>('GET', '/api/mcp'),
  addMcpServer: (body: McpServerInput) => call<McpServers>('POST', '/api/mcp/servers', body),
  updateMcpServer: (name: string, body: McpServerInput) => call<McpServers>('PUT', `/api/mcp/servers/${enc(name)}`, body),
  enableMcpServer: (name: string, enabled: boolean) => call<McpServers>('PATCH', `/api/mcp/servers/${enc(name)}`, { enabled }),
  removeMcpServer: (name: string) => call<McpServers>('DELETE', `/api/mcp/servers/${enc(name)}`),
  taskMcp: (id: string) => call<{ servers: McpStatus[] }>('GET', `/api/sessions/${enc(id)}/mcp`),
  taskMcpAction: (id: string, name: string, action: 'enable' | 'disable' | 'restart') => call<{ servers: McpStatus[] }>('POST', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/${action}`),
  reconnectTaskMcp: (id: string) => call<{ servers: McpStatus[] }>('POST', `/api/sessions/${enc(id)}/mcp/reconnect`),
  startMcpSignIn: (id: string, name: string, again: boolean) => call<McpSignIn>('POST', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/sign-in`, { again }),
  finishMcpSignIn: (id: string, name: string, url: string) => call<void>('POST', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/sign-in/finish`, { url }),
  routines: (projectId: string) => call<{ routines: Routine[] }>('GET', `/api/projects/${enc(projectId)}/routines`),
  createRoutine: (projectId: string, body: RoutineInput) => call<Routine>('POST', `/api/projects/${enc(projectId)}/routines`, body),
  updateRoutine: (id: string, body: Partial<RoutineInput>) => call<Routine>('PATCH', `/api/routines/${enc(id)}`, body),
  deleteRoutine: (id: string) => call<void>('DELETE', `/api/routines/${enc(id)}`),
  runRoutine: (id: string) => call<Routine>('POST', `/api/routines/${enc(id)}/run`),
};

/** When a routine runs, in the service's local time: `time` is HH:MM, `weekday` 0 (Sunday) to 6. */
export type RoutineSchedule =
  | { kind: 'daily' | 'weekdays'; time: string }
  | { kind: 'weekly'; time: string; weekday: number }
  | { kind: 'hours'; hours: number };

export type RoutineOutcome = 'running' | 'finished' | 'failed' | 'cancelled' | 'time_limit' | 'skipped';

/** One firing of a routine: the schedule's, one missed while the service was down, or Run now. */
export interface RoutineRun {
  id: string;
  at: string;
  trigger: 'schedule' | 'missed' | 'manual';
  task_id?: string;
  outcome: RoutineOutcome;
  reason?: string;
  ended_at?: string;
}

export interface RoutineInput {
  name: string;
  prompt: string;
  model: string;
  schedule: RoutineSchedule;
  enabled: boolean;
  mode: 'safe' | 'yolo';
  max_runs_per_day: number;
  max_minutes: number;
}

/** Recurring work in a Project: each run starts a Task. `next_run` is absent while paused; `runs` is newest first. */
export interface Routine extends RoutineInput {
  id: string;
  project_id: string;
  provider: string;
  created_at: string;
  next_run?: string;
  runs: RoutineRun[];
}

/**
 * The planner's routes (ADR 0005 §14). `ref` is a card id or its `#seq`; `project` is a Project
 * id or `unassigned`. Replies whose shape the ADR leaves open resolve to `unknown`: the views
 * follow the `board` frame, never those bodies.
 */
function plannerApi() {
  const card = (ref: string, action = '') => `/api/board/cards/${enc(ref)}${action ? `/${action}` : ''}`;
  type TaskSettings = Partial<Pick<TaskDefaults, 'model' | 'effort' | 'mode' | 'context_size'>>;
  return {
    board: (project: string, signal?: AbortSignal) => call<BoardData>('GET', `/api/board?project_id=${enc(project)}`, undefined, false, signal),
    project: (id: string) => call<{ accept_cmd: string; git: string }>('GET', `/api/board/projects/${enc(id)}`),
    setProject: (id: string, accept_cmd: string) => call<unknown>('PATCH', `/api/board/projects/${enc(id)}`, { accept_cmd }),
    card: (ref: string, signal?: AbortSignal) => call<CardDetail>('GET', card(ref), undefined, false, signal),
    create: (body: { project_id: string; kind: CardKind; parent_id: string | null; title: string; desc?: string; win_condition?: string; prio?: number; effort?: string; due?: string; labels?: string[]; checklist?: ChecklistItem[] }) => call<Card>('POST', '/api/board/cards', body),
    edit: (ref: string, patch: CardPatch) => call<unknown>('PATCH', card(ref), patch),
    confirm: (ref: string) => call<unknown>('POST', card(ref, 'confirm')),
    dismiss: (ref: string) => call<unknown>('POST', card(ref, 'dismiss')),
    /** Without a rank the card goes after its new parent's last child; `rank` is an index among the siblings. */
    move: (ref: string, parent_id: string | null, rank?: number) => call<unknown>('POST', card(ref, 'move'), rank === undefined ? { parent_id } : { parent_id, rank }),
    status: (ref: string, status: 'done' | 'cancelled' | 'todo', comment: string, force = false) => call<unknown>('POST', card(ref, 'status'), { status, comment, force }),
    restore: (ref: string, comment: string) => call<unknown>('POST', card(ref, 'restore'), { comment }),
    split: (ref: string, children: { title: string; win_condition: string }[]) => call<unknown>('POST', card(ref, 'split'), { children }),
    comment: (ref: string, body: string) => call<unknown>('POST', card(ref, 'comments'), { body }),
    link: (blocker: string, blocked: string) => call<unknown>('POST', '/api/board/links', { blocker, blocked }),
    unlink: (blocker: string, blocked: string) => call<unknown>('DELETE', `/api/board/links?blocker=${enc(blocker)}&blocked=${enc(blocked)}`),
    /** On a subtask it launches that subtask; on a story it is Do whole story. */
    launch: (ref: string, body: TaskSettings = {}) => call<{ card: Card; session: SessionSummary }>('POST', card(ref, 'launch'), body),
    plan: (ref: string, body: { brief: string } & TaskSettings) => call<{ session: SessionSummary }>('POST', card(ref, 'plan'), body),
    release: (ref: string, comment: string) => call<unknown>('POST', card(ref, 'release'), { comment }),
    /** Starts Check at HEAD; its `board_job` frames carry the run. */
    check: (ref: string) => call<{ job_id: string }>('POST', card(ref, 'check')),
    triage: (ref: string) => call<{ verdict: TriageVerdict; sentence: string; head: string }>('POST', card(ref, 'triage')),
    suggest: (ref: string, body: { brief: string; document: string; max: number }) => call<{ job_id: string }>('POST', card(ref, 'suggest'), body),
    accept: (id: string, comment: string) => call<unknown>('POST', `/api/board/requests/${enc(id)}/accept`, { comment }),
    reject: (id: string, reason: string) => call<Rejection>('POST', `/api/board/requests/${enc(id)}/reject`, { reason }),
    purge: (project_id: string) => call<{ purged: number }>('POST', '/api/board/purge', { project_id }),
    import: (dir: string) => call<ImportReport>('POST', '/api/board/import', { dir }),
  };
}

export interface Upload {
  done: Promise<Attachment & { id: string }>;
  abort: () => void;
}

/**
 * Uploads one file as the raw body (`application/octet-stream`, the name in the query).
 * XMLHttpRequest, not fetch: it is the only same-origin transport that reports upload
 * progress over HTTP/1.1. `onProgress` gets 0…1.
 */
function uploadFile(id: string, file: File, onProgress: (fraction: number) => void, model?: string): Upload {
  const xhr = new XMLHttpRequest();
  const modelQuery = model ? `&model=${enc(model)}` : '';
  const done = new Promise<Attachment & { id: string }>((resolve, reject) => {
    xhr.open('POST', `/api/sessions/${enc(id)}/attachments?name=${enc(file.name)}${modelQuery}`);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(Math.min(1, e.loaded / e.total));
    };
    xhr.onload = () => {
      let body: Record<string, unknown> = {};
      try {
        body = JSON.parse(xhr.responseText) as Record<string, unknown>;
      } catch {
        // not JSON
      }
      if (xhr.status === 401) notifyUnauthorized();
      if (xhr.status < 200 || xhr.status >= 300) {
        const message = typeof body.error === 'string' && body.error ? body.error : `${xhr.status} ${xhr.statusText}`.trim();
        reject(new ApiError(xhr.status, message, body));
        return;
      }
      resolve(body as unknown as Attachment & { id: string });
    };
    xhr.onerror = () => reject(new ApiError(0, 'Could not reach the server'));
    xhr.onabort = () => reject(new ApiError(0, 'Upload cancelled'));
    xhr.send(file);
  });
  return { done, abort: () => xhr.abort() };
}

/** UUID v4; crypto.randomUUID needs a secure context, which a plain-HTTP tunnel host may not be. */
export function newRequestId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export function basename(path: string): string {
  const parts = path.replace(/(?<!\/)\/+$/, '').split('/');
  return parts.at(-1) || path;
}

/** Display name of a Task: the user's name, else the provider title, else a placeholder. */
export function taskName(s: Pick<SessionSummary, 'name' | 'title'>): string {
  return s.name || s.title || '';
}

export function pendingCount(s: SessionSummary): number {
  if (typeof s.pending === 'number') return s.pending;
  return s.pending ? 1 : 0;
}

export function needsYou(s: SessionSummary): boolean {
  return ATTENTION.includes(s.state) || pendingCount(s) > 0;
}

export function provider(meta: Meta | null, name: string): ProviderInfo | undefined {
  return meta?.providers.find((p) => p.name === name);
}

export function providerLabel(meta: Meta | null, name: string): string {
  return provider(meta, name)?.display_name ?? name;
}

export function modelCatalog(meta: Meta | null, providerName: string): Model[] {
  // `?? []` tolerates a server older than the catalog.
  return provider(meta, providerName)?.models ?? [];
}

/**
 * What a new Task starts with, from the Task defaults of Settings checked against the live catalog:
 * the default provider if listed and available, else the first available one; the default
 * model if offered and not hidden in Settings, keeping its effort and context size only
 * where still offered; a model no longer offered, or hidden, falls back to `auto` (else the
 * first visible model) with effort cleared and context size `default`. With the setting unset:
 * `auto`, no effort, `default`, safe. Null until the provider list has loaded.
 */
export function resolveTaskDefaults(meta: Meta | null, defaults?: TaskDefaults, hidden?: Settings['hidden_models']): TaskDefaults | null {
  const providers = meta?.providers ?? [];
  const chosen =
    (defaults && providers.find((p) => p.name === defaults.provider && p.available)) ?? providers.find((p) => p.available) ?? providers[0];
  if (!chosen) return null;
  const mode = defaults?.mode ?? 'safe';
  const offered = visibleModels(chosen.models, hidden?.[chosen.name]);
  const model = defaults && offered.find((m) => m.id === defaults.model);
  if (!defaults || !model) {
    const first = offered.some((m) => m.id === 'auto') ? 'auto' : (offered[0]?.id ?? '');
    return { provider: chosen.name, model: first, effort: '', context_size: 'default', mode };
  }
  const contextOffered = !!chosen.capabilities.context_size && !!model.context_sizes?.some((s) => s.id === defaults.context_size);
  return {
    provider: chosen.name,
    model: model.id,
    effort: model.efforts?.includes(defaults.effort) ? defaults.effort : '',
    context_size: contextOffered ? defaults.context_size : 'default',
    mode,
  };
}

export function modelName(meta: Meta | null, providerName: string, id: string): string {
  if (!id) return 'Default model';
  return modelCatalog(meta, providerName).find((m) => m.id === id)?.name ?? id;
}

export const readOnly = (s: SessionSummary): boolean => s.stage === 'settled' || s.stage === 'archived';
export const stageLabel = (s: SessionSummary): string => {
  if (s.stage === 'settled') return 'Settled';
  if (s.stage === 'archived') return 'Archived';
  return 'Active';
};
