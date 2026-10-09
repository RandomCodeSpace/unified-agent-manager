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
  plan?: boolean;
  session_diff: boolean;
  history: boolean;
  /** Recorded owner-turn prefixes can be copied natively, without resending prompts. */
  fork?: boolean;
  /** An open conversation's recorded suffix can be rewound natively, with a preview first. */
  rewind?: boolean;
  context_size?: boolean;
  /** An already-open conversation can report native context categories and sources on demand. */
  context_breakdown?: boolean;
  execution_modes?: boolean;
  /** The provider reports account quota and per-Task AI units (#188); the second parity exception. */
  usage?: boolean;
  /** Readonly native per-Task metrics, separate from account quotas and recorded AI units. */
  usage_metrics?: boolean;
  /** An open conversation answers a transient aside question; nothing is saved to the transcript. */
  aside?: boolean;
  /** Tasks may opt into the provider's assisted review of permission requests (`mode: 'assisted'`). */
  assisted_permissions?: boolean;
  /** Why the running runtime withdrew assisted permissions after refusing the mode; set until it restarts or updates. */
  assisted_unavailable?: string;
  /** A chosen model can title the provider's new Tasks (#183). */
  titles?: boolean;
  /** The provider can run Utility AI jobs with host tools. */
  host_tools?: boolean;
  import?: boolean;
  /** The provider's runtime sign-in is shown and changed in Settings (`api.account`). */
  account?: boolean;
  /** The runtime can sign in with a GitHub device code (`api.startDeviceSignIn`). */
  device_sign_in?: boolean;
  /** The provider's MCP servers can be listed and managed (Settings → MCP servers, a Task's MCP servers). */
  mcp?: boolean;
  /** The runtime's CLI version shows in Settings, which can update it (`api.providerCli`). */
  cli_update?: boolean;
  /** The models the provider's subagents may use can be limited (`Settings.subagent_models`). */
  subagent_models?: boolean;
  /** Its built-in GitHub MCP server is turned on and off for Tasks by `Settings.github_mcp`. */
  github_mcp?: boolean;
  /** A Task can run as one of the Project's custom agents (`api.taskAgents`, `SessionSummary.agent`). */
  custom_agents?: boolean;
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

/** Native occupied counts and capacity; buffer overlaps the output and compaction reservations. */
export interface ContextInfo {
  model: string;
  total_tokens: number;
  limit: number;
  prompt_token_limit: number;
  compaction_threshold: number;
  buffer_tokens: number;
  system_tokens: number;
  conversation_tokens: number;
  tool_definition_tokens: number;
  mcp_tools_tokens: number;
}

export interface ContextAttribution extends Pick<ContextInfo, 'model' | 'total_tokens' | 'limit' | 'prompt_token_limit' | 'compaction_threshold' | 'buffer_tokens'> {
  model_source: string;
  compactions: number;
  categories: { system_prompt: number; custom_instructions: number; system_tools: number; mcp_tools: number; messages: number; free_space: number; buffer: number };
  /** A parent's count includes its children; entries are not additive totals. */
  entries: { id: string; kind: string; label: string; parent_id?: string; tokens: number }[];
  truncated?: boolean;
}

/** Read only for the current reader, never attached to Task stream or history state. */
export interface ContextBreakdown {
  info?: ContextInfo;
  attribution?: ContextAttribution;
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

/** Input includes cache reads and writes. Total is the collector-normalized token count. */
export interface TokenCounts {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  total: number;
}

/** One aside answer; transient, never part of the transcript. */
export interface AsideAnswer {
  text: string;
  truncated?: boolean;
}

export type TokenPeriodKey = 'today' | '7d' | '30d' | 'lifetime';
/** Transient native snapshot: model and agent projections overlap and are never added. */
export interface TaskUsageMetrics {
  started_at: string;
  current_model?: string;
  user_requests: number;
  premium_request_cost: number;
  api_duration_ms: number;
  ai_units?: number;
  last_input: number;
  last_output: number;
  code_changes: { files: number; added: number; removed: number };
  token_details: UsageTokenDetail[];
  models: UsageMetricModel[];
  agents: UsageMetricAgent[];
  truncated?: boolean;
}
export interface UsageTokenDetail { type: string; tokens: number }
export interface UsageMetricModel {
  model: string;
  requests: number;
  premium_request_cost: number;
  ai_units?: number;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  reasoning?: number;
  cache_expires_at?: string;
  token_details: UsageTokenDetail[];
}
export interface UsageMetricAgent {
  id: string;
  name?: string;
  display_name?: string;
  api_duration_ms: number;
  ai_units: number;
  models: UsageMetricModel[];
}

export interface TokenUsageReport {
  since: string;
  today: string;
  collection?: {
    status: 'starting' | 'ready' | 'partial' | 'unavailable';
    updated_at?: string;
    copilot_since: string;
  };
  periods: Record<TokenPeriodKey, {
    models: (TokenCounts & { provider: string; model: string; cost_usd: number | null; cost_partial?: boolean })[];
    total: TokenCounts;
    cost_usd: number | null;
    unpriced_models: number;
  }>;
}

export interface TokenPrice {
  input: number;
  output: number;
  cache_read?: number;
  cache_write?: number;
}

export interface TokenPriceCatalog {
  commit: string;
  models: { provider: string; model: string; rates: TokenPrice | null; source: 'manual' | 'bundled' | 'unpriced' }[];
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
  /** Unavailable because the runtime is signed in as another account than the one this server is linked to; `reason` names both. */
  account_mismatch?: boolean;
  capabilities: Capabilities;
  /** Selectable models; empty means "provider default only". */
  models: Model[];
  /** The cheapest priced model not hidden in Settings, the Utility model while `title_model` names none; omitted when none is priced. */
  cheapest_model?: string;
  /** The CLI release Settings can update to (`ProviderCLI.latest` while `update_available`); omitted otherwise. */
  cli_update?: string;
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
  /** The one account this server is linked to; absent until the first sign-in links one. */
  linked?: { login: string; host: string; linked_at: string };
}

/**
 * A device-code sign-in, one per server: `waiting` carries the page and the code to enter there, `signed_in` the
 * account, `failed` the reason. `idle` means none has started.
 */
export interface DeviceSignIn {
  state: 'idle' | 'starting' | 'waiting' | 'signed_in' | 'failed' | 'canceled';
  verification_uri?: string;
  user_code?: string;
  error?: string;
  account?: ProviderAccount;
}

/**
 * The provider's CLI on the server, one per server: the installed version, the newest stable release and the update
 * job. The server picks the update's target; `state` is `idle` until an update has started.
 */
export interface ProviderCLI {
  /** The version installed, e.g. "1.0.89"; may carry a prerelease suffix ("1.0.93-1"). */
  installed?: string;
  /** After an update, the older version the runtime still runs until no Copilot Task works or waits and it restarts. */
  running?: string;
  /** The newest stable release. */
  latest?: string;
  /** `latest` is newer than `installed`, not `incompatible`, and nothing is `manual`. */
  update_available: boolean;
  /** Why UAM cannot update the CLI here. */
  manual?: string;
  /** The newest release the SDK refused; never offered as an update. */
  incompatible?: string;
  /** RFC 3339 time of the last successful check. */
  checked_at?: string;
  /** Why the last check failed. */
  check_error?: string;
  state: 'idle' | 'updating' | 'updated' | 'failed';
  /** The version being installed (`updating`), or the last one installed (`updated`, `failed`). */
  target?: string;
  /** Why the update failed (`failed`). */
  error?: string;
}

/** Refusal code of a create or send while the Task's provider is signed out. */
export const SIGNED_OUT = 'provider_signed_out';

/** Refusal code of a sign-in as another account than the linked one, and of a create or send while the runtime is signed in as one. */
export const ACCOUNT_NOT_LINKED = 'account_not_linked';

export interface Meta {
  instance_id?: string;
  protocol_major?: number;
  capabilities?: string[];
  version: string;
  providers: ProviderInfo[];
  recent_workdirs: string[];
  /** Syntax-only temporary-file eligibility; availability is checked only on Open. */
  temp_root?: string;
  temp_root_aliases?: string[];
  /** The UAM installed at the service's binary path and the restart onto it, as last read; absent on a service that cannot restart itself. */
  service?: ServiceStatus;
}

/**
 * The UAM the service runs and the one installed at its binary's path (`go install`, `make install`, a package
 * manager). A different one installed offers a restart onto it, which waits until no Task works or waits.
 */
export interface ServiceStatus {
  running: string;
  /** The version at the binary's path; absent while it cannot be read (`error` says why). */
  installed?: string;
  restart: 'none' | 'available' | 'pending' | 'restarting';
  error?: string;
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
  /** Manual USD prices per million tokens, keyed by provider and exact model ID. PATCH replaces the map. */
  token_prices?: Record<string, Record<string, TokenPrice>>;
  /** What Enter does while a turn runs. */
  send_default: SendDefault;
  /** Model IDs not offered anywhere a model is chosen, by provider; omitted when none is hidden (#191). */
  hidden_models?: Record<string, string[]>;
  /**
   * The model IDs a provider's subagents may use, by provider, the fallback first; omitted when no provider is limited. A
   * subagent keeps its requested model if listed, else takes the Task's model if listed, else the fallback. PATCH replaces
   * each named provider's list; `[]` lifts its limit.
   */
  subagent_models?: Record<string, string[]>;
  /**
   * The Utility model by provider, the model UAM uses for its own small AI jobs such as titling new Tasks: a model ID,
   * or `none` to keep the provider's own title. A provider without an entry titles a Task with the Task's own model at
   * its lowest effort (its `cheapest_model` when that fails) and uses its `cheapest_model` for the other jobs. PATCH `''` unsets.
   */
  title_model?: Record<string, string>;
  /** OpenAI-compatible models the owner brought; omitted when there are none. PATCH replaces the whole list. */
  custom_models?: CustomModel[];
  /** What a new Task starts with, shared by every browser; omitted until set, and the provider's own defaults apply then. */
  task_defaults?: TaskDefaults;
  /** Whether a Task's header offers a shell in its Project folder; off by default. Turning it off ends every open shell. */
  terminal: boolean;
  /** `false` when replies to send next are not offered after a turn; absent while they are (the default). */
  suggest_replies?: boolean;
  /** Utility model calls a day, 0 for none; omitted for the service default (GET /api/utility reports the limit in force). */
  utility_daily_limit?: number;
  /** The share of the context, in percent (50 to 90), at which a Task's conversation starts compacting; omitted for the default, 80. PATCH null puts it back. */
  compact_threshold?: number | null;
  /** Whether Tasks, open ones included, start the provider's built-in GitHub MCP server; absent while off, the default. */
  github_mcp?: boolean;
}

/** The provider's own compaction threshold, in percent: where `Settings.compact_threshold` starts. */
export const DEFAULT_COMPACT_THRESHOLD = 80;

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
  /** A title made with the Task's own model because no Utility model is set. */
  session_model?: boolean;
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

/** The service's error code, when a refusal carries one. The UI decides on the code, never on the status. */
export function errorCode(e: unknown): string | undefined {
  return e instanceof ApiError && typeof e.body.code === 'string' ? e.body.code : undefined;
}

/** A route this service does not have yet: 404 with no code. */
export function routeMissing(e: unknown): boolean {
  return isStatus(e, 404) && errorCode(e) === undefined;
}

/**
 * A custom (BYOM) model, offered by Copilot as `name/model_id`. Keys may be supplied
 * directly or through `api_key_env`; saved key values never come back in responses.
 */
export interface CustomModel {
  /** Provider name: letters, digits, `.`, `_`, `-`. Models with one name share its URL and key variable. */
  name: string;
  display_name?: string;
  base_url: string;
  model_id: string;
  /** Enable image input for this model. Defaults to false. */
  vision?: boolean;
  wire_api?: 'completions' | 'responses';
  api_key_env: string;
  /** Write only. Omit to keep a saved key for the same provider connection. */
  api_key?: string;
  /** Output only: whether a saved or environment key is available. */
  key_present?: boolean;
}

export type ConfigurationKind = 'agents' | 'skills' | 'hooks' | 'instructions';
/** An AI suggestion for the guided creator. It is not saved until explicitly added. */
export interface ConfigurationDraft {
  name: string;
  description?: string;
  prompt?: string;
  model?: string;
  tools?: string[];
  disable_model_invocation?: boolean;
  user_invocable?: boolean;
  event?: string;
  bash?: string;
  powershell?: string;
  cwd?: string;
  timeout_sec?: number;
  env?: Record<string, string>;
  similar_skills?: { name: string; path: string; project_id?: string; reason: string }[];
  provider: string;
  utility_model: string;
}
export interface ConfigurationDefinition {
  id: string;
  name: string;
  display_name?: string;
  description?: string;
  source: string;
  path?: string;
  enabled?: boolean;
  user_invocable?: boolean;
}
export interface ConfigurationFile {
  name: string;
  path: string;
  content: string;
  revision: string;
  editable: boolean;
  disabled?: boolean;
  read_only_reason?: string;
  error?: string;
  native?: ConfigurationDefinition;
  metadata_only?: boolean;
}
export interface Configuration {
  scope: 'global' | 'project';
  discovery?: Partial<Record<ConfigurationKind, { supported: boolean; ready: boolean; warnings?: string[] }>>;
  project_id?: string;
  terminal_allowed: boolean;
  agents: ConfigurationFile[];
  skills: ConfigurationFile[];
  hooks: ConfigurationFile[];
  instructions: ConfigurationFile;
  /** Named instruction files for this scope; older services return only instructions. */
  instruction_files?: ConfigurationFile[];
  /** Different canonical files share one discovered agent or skill name. */
  conflicts?: { kind: 'agents' | 'skills'; name: string; paths: string[] }[];
  conflict_details?: { kind: 'agents' | 'skills'; path?: string; message: string }[];
  conflict_warnings?: string[];
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
  kind: 'line' | 'bar' | 'echarts';
  /** A validated JSON chart specification; advanced charts do not have tabular rows. */
  options?: import('echarts').EChartsOption;
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
  /** The custom agent's ID, selected on every open before anything is sent; absent means the provider's default agent. */
  agent?: string;
  context?: ContextUsage;
  /** AI units the conversation used so far, once the provider reports them; never zero. */
  usage?: { ai_units: number };
  mode?: PermissionMode;
  /** A failed change left the runtime's permission mode unread: `mode` is not a fact, and nothing is allowed automatically. */
  mode_unknown?: boolean;
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
  /** Native recorded-history lineage, separate from a last-message rerun. */
  fork_of?: string;
  fork_user_item_id?: string;
  /** A native rewind that holds the Task until it is reconciled; absent otherwise. */
  rewind?: RewindStatus;
  /** The last completed turn in one line ("Fixed the flaky test; 3 files changed; tests pass"); absent while a turn runs or when there is nothing to say. */
  outcome?: string;
  /** Set while the conversation is being compacted (/compact or the provider's automatic compaction); absent otherwise. */
  compacting?: boolean;
  /** The open conversation's compaction threshold in percent: Settings' value when it opened; absent while none is open. */
  compact_threshold?: number;
  /** Why the last turn was cancelled, while the Task is (lib/stop): your Stop, a routine's time limit, autopilot's credit limit, a remote command or an MCP server; absent otherwise, also when the provider gave no reason. */
  stop_reason?: 'owner' | 'time_limit' | 'credit_limit' | 'remote' | 'mcp';
  queued?: number;
  state: SessionState;
  state_detail?: string;
  open: boolean;
  pending: number | boolean;
  /** The request the Task waits on, for the Task list's status line and notices; absent when nothing waits, and from older servers. */
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

export interface NativeFileEdit {
  path: string;
  kind: string;
  additions?: number;
  deletions?: number;
  /** Counts are known only for available native textual patches. */
  diff_status: string;
}
export interface ItemDiffData {
  session_id: string;
  epoch: string;
  agent_id: string;
  item_id: string;
  event_id: string;
  path: string;
  status: string;
  patch?: string;
}

export interface ToolCall {
  name: string;
  title?: string;
  status: ToolStatus;
  edit_event_id?: string;
  file_edits?: NativeFileEdit[];
  file_edits_truncated?: boolean;
  input?: string;
  output?: string;
  /** Latest human status of a running call, at most 512 UTF-8 bytes; absent once it ends. */
  progress?: string;
  /** Exact local tool metadata; eligibility only, never proof that a file exists. */
  file_paths?: string[];
  declaration?: FileDeclaration;
  display_arg?: string;
  path?: string;
  has_input?: boolean;
  has_output?: boolean;
  /** A running shell call's newest output lines, oldest first, at most 10; absent once it ends. */
  tail?: OutputLine[];
  /** Client-only semantic outcome retained after page eviction. */
  question_outcome?: 'pending' | 'answered' | 'declined' | 'cancelled' | 'none' | 'failed';
}

/** One line of a running shell call's output; `err` marks stderr. */
export interface OutputLine {
  text: string;
  err?: boolean;
}

/** An image a tool's result returned, stored with the Task; served by the attachment route. */
export interface ToolImage {
  id: string;
  mime: string;
  size: number;
  name?: string;
}

export interface TaskCompletion {
  decision: 'accepted' | 'rejected' | 'blocked' | 'unknown';
  /** Exact ordinary user-message ID; absent when correlation is unresolved. */
  user_item_id?: string;
  summary?: string;
  reason?: string;
  blocker?: { kind: string; reason: string; resumable: boolean };
}

export interface Item {
  plan?: PlanReview;
  compact?: { has_reasoning: boolean; has_text: boolean };
  id: string;
  kind: ItemKind;
  delivery?: 'steer' | 'autopilot';
  /** A local steer receipt; absent after the provider records its user message. */
  steer_status?: 'accepted' | 'not_delivered';
  text?: string;
  tool?: ToolCall;
  completion?: TaskCompletion;
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

/** `idle` is not terminal: the subagent finished and the main agent may resume it. */
export type SubagentStatus = 'running' | 'idle' | 'completed' | 'failed' | 'cancelled';

/** An outline entry: a user message (its first line) or a subagent's tool call (its name only). */
export type OutlineItem = Pick<Item, 'id' | 'kind' | 'time' | 'text' | 'delivery'> & { tool?: { name: string } };

export interface Subagent {
  preview?: string;
  result_summary?: string;
  id: string;
  /** Item id of the `task` tool call that started this subagent (a tool item's id is the provider tool call id). */
  parent_tool_call_id?: string;
  /** The subagent that spawned this one, when not the main agent: `parent_tool_call_id` is then in that subagent's transcript. */
  parent_agent_id?: string;
  name: string;
  description?: string;
  /** Launched in the background: its `task` call's output only acknowledges the launch, and its result is its own last message. */
  background?: boolean;
  /** Failed and cancelled are final; completed turns idle only on the provider's report. Close, runtime exit and service stop mark running ones cancelled and idle ones completed. */
  status: SubagentStatus;
  error?: string;
  started_at?: string;
  ended_at?: string;
  /** Model id and effort level the subagent runs with, when the provider reports them. */
  model?: string;
  effort?: string;
  /**
   * Its periods of work, oldest first, when the provider tracks them: a reused subagent keeps its id and
   * gains a run each time the main agent (`agent`) or a UAM follow-up (`user`) starts it again; the first
   * run is `spawn`. The latest run carries `status` and `ended_at`; an ended run keeps the status it ended
   * with. At most 50: the first and the newest. `started_at` is absent when it was not recorded.
   */
  runs?: { started_at?: string; ended_at?: string; status: SubagentStatus; trigger: 'spawn' | 'agent' | 'user' }[];
  /** Input and output tokens it consumed over all runs: live while it runs, the provider's total once it ends. */
  tokens?: number;
  /** Tool calls it made over all runs: live while it runs, the provider's total once it ends. */
  tool_calls?: number;
  /** Set while it runs and its current model call is being retried; live only. */
  retry?: Retry;
}

/** A model call the provider is retrying: how many times so far, its reason code, and the failed attempt's HTTP status or a network failure. */
export interface Retry { count: number; reason?: string; status?: number; network?: boolean; at: string }

export type TodoStatus = 'pending' | 'in_progress' | 'done' | 'blocked';

/** One row of the agents' todo list: `note` says why a blocked row is blocked; `agent_id` is the subagent that first wrote it, when uam could tell; `changed_at` is when its status last changed, as far as uam saw. */
export interface Todo { id: string; title: string; status: TodoStatus; note?: string; agent_id?: string; changed_at?: string }

/** Rows by status over the whole list: `open` is `in_progress` plus `pending`, and counts kept before uam split those two carry `open` alone. */
export interface TodoCounts { done?: number; total?: number; blocked?: number; in_progress?: number; pending?: number; open?: number; omitted?: number }

/**
 * The open conversation's todo list, which the agents keep in Copilot's session `todos` table:
 * `known` is false while it could not be read or no conversation is open; `touched` once the
 * running turn changed it (the last turn's answer between turns); the first 100 rows in the
 * provider's order, `omitted` past them; `counts` over all; `now` the id of the row the work is at.
 */
export interface TodoView { known: boolean; touched: boolean; todos: Todo[]; omitted?: number; counts: TodoCounts; now?: string }

/** A turn's todo list as uam kept it when the turn ended: the rows it changed and those still open, at most 50 (`counts.omitted` past them), each with the name its subagent had then. */
export interface TurnTodos { timing_id: string; ended_at: string; intent?: string; todos: (Todo & { agent?: string })[]; counts: TodoCounts }

/** The running turn's live activity: what the main agent says it is doing (`assistant.intent`), a model call being retried, and the todo list; never persisted. */
export interface TurnActivity { plan?: boolean; intent?: string; retry?: Retry; todos: TodoView }

/** Live provider-owned shells. Unknown snapshots retain the last observation only. */
export interface BackgroundTasks {
  known: boolean;
  tasks: { id: string; description?: string; command: string; status: string; started_at?: string; ended_at?: string }[];
}

/**
 * The open conversation's native schedules, display only: no prompts, and nothing UAM runs. `known`
 * false with rows is a partial list (`truncated`); without rows the list is unknown, never empty.
 */
export interface ScheduleSnapshot {
  supported: boolean;
  known: boolean;
  truncated?: boolean;
  reason?: string;
  entries: { id: string; recurring: boolean; self_paced?: boolean; interval_ms?: number; cron?: string; timezone?: string; next_run_at?: string }[];
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

export type InteractionKind = 'permission' | 'question' | 'plan_review';
export type InteractionState = 'pending' | 'answered' | 'rejected' | 'expired';

export type PlanAction = 'autopilot' | 'autopilot_fleet' | 'interactive' | 'exit_only';

/** A pending or on-demand native review; transcript metadata omits both bodies. */
export interface PlanReview {
  request_id: string;
  summary?: string;
  revision?: number;
  actions?: PlanAction[];
  recommended?: PlanAction;
  content?: string;
  previous?: string;
  truncated?: boolean;
  previous_truncated?: boolean;
  previous_unavailable?: boolean;
}

export interface PlanDraft { exists: boolean; content?: string; truncated?: boolean }

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
  /** The typed form field this question asks for, on an elicitation form only. */
  field?: FormField;
}

/** One form field; the service checks every bound again before the provider sees the answer. */
export interface FormField {
  name: string;
  type: 'string' | 'number' | 'integer' | 'boolean' | 'array';
  /** An optional field may be left empty. */
  required?: boolean;
  format?: 'email' | 'uri' | 'date' | 'date-time';
  minimum?: number;
  maximum?: number;
  min_length?: number;
  max_length?: number;
  min_items?: number;
  max_items?: number;
}

/** A question that stands for a provider's form or link (an MCP elicitation). */
export interface Elicitation {
  mode: 'form' | 'url';
  /** Who asked, such as an MCP server. */
  source?: string;
  /** URL mode: the https page the user opens themselves; UAM never opens or fetches it. */
  url?: string;
}

export interface Interaction {
  id: string;
  kind: InteractionKind;
  title: string;
  detail?: string;
  options?: Option[];
  questions?: Question[];
  plan?: PlanReview;
  state: InteractionState;
  resolution?: string;
  time: string;
  agent_id?: string;
  /** The `tool` item (same `agent_id`) this request is for; absent or unmatched means no link. */
  tool_call_id?: string;
  /** Yolo mode or an approving assisted review is answering this pending request: it does not wait for the user. */
  auto?: boolean;
  /** The provider's assisted review of a permission request; absent when it was not reviewed. */
  assisted?: { recommendation: string; model?: string; reason?: string };
  elicitation?: Elicitation;
}

/** A Task's permission policy. Assisted is opt-in per Task; Task defaults and routines stay Safe or Yolo. */
export type PermissionMode = 'safe' | 'yolo' | 'assisted';
/** A new Task's settings: the defaults, with any permission mode chosen for this Task. */
export type TaskSettings = Omit<TaskDefaults, 'mode'> & { mode: PermissionMode };

/** A summary's `ask`: a permission's title, or the first line of a question's first prompt. The Task's detail holds the request whole. */
export interface Ask {
  kind: InteractionKind;
  title: string;
}

export interface Answer {
  decision?: string;
  answers?: string[][];
  reject?: boolean;
  plan?: { action?: PlanAction; feedback?: string };
  /** Dismisses an elicitation without declining it. */
  cancel?: boolean;
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

export type RewindMode = 'conversation' | 'conversation-and-files';
/** What rewinding to before an owner message would discard and restore. `token` binds the confirmation. */
export interface RewindPreview {
  user_item_id: string;
  token: string;
  /** Owner prompts discarded, this one included. */
  turns: number;
  files_available: boolean;
  files_reason?: string;
  /** Native forward counts over the discarded turns; a rewind shows their inverse. */
  files: TurnChangeCounts & { entries?: { path: string; kind: string; additions?: number; deletions?: number }[] };
}
/** Every native outcome is kept with the presence of its optional fields. */
export interface RewindResult {
  outcome: string;
  error?: string;
  events_removed?: number;
  restored_files: string[];
  skipped_files: { path: string; reason: string }[];
  restored_omitted?: number;
  skipped_omitted?: number;
}
export interface RewindReceipt {
  request_id: string;
  user_item_id: string;
  mode: RewindMode;
  /** pending and applied clear after the conversation is read again; uncertain only by an explicit reconcile. */
  state: 'pending' | 'applied' | 'uncertain' | 'done';
  result?: RewindResult;
  reconcile_failed?: boolean;
  /** The owner released the hold while the outcome stayed unknown. */
  released?: boolean;
}
/** Edit and resend: the rewind's receipt, and the edited prompt's submission when it was sent. */
export interface ResendResult {
  rewind: RewindReceipt;
  submission?: Submission;
  /** Why the edited prompt was not sent after the rewind truncated history. */
  send_error?: string;
  send_code?: string;
}
export interface RewindStatus {
  request_id: string;
  state: 'pending' | 'applied' | 'uncertain';
  mode: RewindMode;
  outcome?: string;
  /** The reread could not establish the outcome: the explicit release is offered. */
  reconcile_failed?: boolean;
}

export interface TurnChangeCounts {
  status: 'available' | 'unknown' | 'busy' | 'unsupported';
  event_id?: string;
  files?: number;
  additions?: number;
  deletions?: number;
  omitted?: number;
}
export interface TurnChanges {
  timing_id: string;
  ended_at: string;
  counts: TurnChangeCounts;
  files: { path: string; kind: string; additions?: number; deletions?: number }[];
}

export interface TurnTiming {
  id: string;
  user_item_id?: string;
  started_at: string;
  ended_at?: string;
  paused_at?: string;
  paused_ms?: number;
  state: 'working' | 'completed' | 'cancelled' | 'failed' | 'unknown';
  /** The turn's model calls (main agent and subagents) as reported: tokens in and out, and the calls' own duration. */
  input_tokens?: number;
  output_tokens?: number;
  generation_ms?: number;
  /** The todo list as the turn left it, for a turn that changed it; its rows come from `turnTodos`. */
  todo?: TodoCounts;
  /** Immutable native captured changes as this owner turn left them, never current Git. */
  changes?: TurnChangeCounts;
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
  /** Every user message and subagent call of the transcript the server holds, in order (compact-v1), so subagents find their reply before its page is loaded. */
  outline?: OutlineItem[];
  queue?: QueuedPrompt[];
  queue_paused?: boolean;
  /** Main agent items only; subagent items come from the subagent route. */
  items: Item[];
  interactions: Interaction[];
  subagents: Subagent[];
  /** Cursor for older subagents only Copilot's record still lists; absent when `subagents` has them all. */
  subagents_before?: string;
  background_tasks?: BackgroundTasks;
  turn_activity?: TurnActivity;
  plan_version?: number;
  /** The provider's scratch plan file, once known. */
  plan_path?: string;
  schedules?: ScheduleSnapshot | null;
  /** Live MCP state only; descriptions are fetched for an expanded server. */
  mcp_status?: McpStatusSnapshot;
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

/** Scope the header counts and the Changes sheet open on: the provider's own diff when it has one, else the files this Task's agent edited. */
export function defaultScope(s: SessionSummary): Scope {
  return s.capabilities.session_diff ? 'session' : 'task';
}

export interface DiffStat {
  files: number;
  additions: number;
  deletions: number;
}

export interface ChangeFile {
  /** Native patches can omit textual counts (binary or truncated). */
  counts_unknown?: boolean;
  binary?: boolean;
  truncated?: boolean;
  old_path?: string;
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

/** One command of a turn that ran checks, as the service read it (GET /api/sessions/{id}/evidence). */
export interface EvidenceCheck {
  item_id: string;
  kinds: CheckKind[];
  command: string;
  /** The worst of its checks: a failing run is never shown as passed. */
  outcome: 'pass' | 'fail' | 'unclear' | 'running';
  exit?: number;
  /** What its output counted ("2 packages ok"). */
  counts?: string;
  took_ms?: number;
  /** Why the outcome is unclear, or how it passed. */
  note?: string;
  has_output: boolean;
}

export type CheckKind = 'test' | 'build' | 'lint' | 'vet' | 'typecheck';

/** A sentence of the final message that claims what evidence should back. */
export interface EvidenceClaim {
  text: string;
  verified: boolean;
  /** What backs it, or "Not verified · why". */
  detail: string;
}

/**
 * The evidence of a Task's latest turn, read by the service from the whole turn (the finish
 * card), and what changed since a look ("Since you left"); the outcome line reads the same.
 */
export interface TurnEvidence {
  checks: EvidenceCheck[];
  claims: EvidenceClaim[];
  /** The files the turn's edit tools changed, relative to the repository as Changes lists them; counts when Changes lists them. */
  files: { path: string; additions?: number; deletions?: number }[];
  since?: { text: string; ids: string[] };
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
  counts_unknown?: boolean;
  binary?: boolean;
  truncated?: boolean;
  old_path?: string;
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
  | { name: 'turn_activity'; seq: number; session_id: string; turn_activity: TurnActivity }
  | { name: 'plan_version'; seq: number; session_id: string; plan_version: number; plan_path?: string }
  | { name: 'schedules'; seq: number; session_id: string; schedules: ScheduleSnapshot | null }
  | { name: 'mcp_status'; seq: number; session_id: string; mcp_status: McpStatusSnapshot };

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
  'turn_activity',
  'plan_version',
  'schedules',
  'mcp_status',
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

/** Click-only file reads share admission and authentication handling with other UI reads. */

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
export interface McpServerStatus {
  name: string;
  status: 'connected' | 'failed' | 'needs-auth' | 'pending' | 'disabled' | 'stopped' | 'not_configured' | (string & {});
  error?: string;
  source?: string;
  /** A remote server, which may need a sign-in. */
  remote?: boolean;
  needs_reconnect?: boolean;
}

export interface McpStatus extends McpServerStatus {
  tools?: McpTool[];
}

export interface McpStatusSnapshot {
  /** Typed status capability, independent of whether an event was observed. */
  supported: boolean;
  /** A full initial snapshot is available. */
  ready: boolean;
  servers: McpServerStatus[];
  truncated?: boolean;
}

export interface McpTaskStatus {
  servers: McpStatus[];
  /** Absent from older services. */
  mcp_status?: McpStatusSnapshot;
}

/** A started MCP sign-in: the browser page (none when kept credentials sufficed) and how its callback completes. */
export interface McpSignIn {
  url?: string;
  relay?: boolean;
  /** The browser completes directly at the configured HTTPS service origin. */
  callback?: boolean;
}


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
  /** Each run's Task turns autopilot on before its first message. */
  autopilot: boolean;
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

export interface Upload {
  done: Promise<Attachment & { id: string }>;
  abort: () => void;
}

/**
 * Uploads one file as the raw body (`application/octet-stream`, the name in the query).
 * XMLHttpRequest, not fetch: it is the only same-origin transport that reports upload
 * progress over HTTP/1.1. `onProgress` gets 0…1.
 */

export interface ConnectedInstance {
  id: string;
  instance_id: string;
  label: string;
  base_url: string;
  enabled: boolean;
  generation: number;
  has_key: boolean;
  version: string;
  protocol_major: number;
  capabilities: string[];
  allow_private: boolean;
  /** Set while the instance breaks the one-Copilot-account rule; its workload routes are refused. */
  status?: 'account_mismatch';
  reason?: string;
}
export interface AddConnectionInput { label: string; base_url: string; token: string; allow_private?: boolean }
export type UpdateConnectionInput = Partial<AddConnectionInput> & { enabled?: boolean };
export interface ConnectedStatus { status: 'connecting' | 'online' | 'offline' | 'auth-required' | 'unsupported' | 'account-mismatch'; error?: string }

/** The capability a connected instance must have for a route family; the first match wins. */
const CAPABILITY_FAMILIES: ReadonlyArray<readonly [string, (path: string) => boolean]> = [
  ['configuration-v1', (path) => path.startsWith('/api/configuration')],
  ['provider-accounts-v1', (path) => /^\/api\/providers\/[^/]+\/account/.test(path)],
  ['routines-v1', (path) => /^\/api\/(?:projects\/[^/]+\/)?routines(?:[/?]|$)/.test(path)],
  ['usage-v1', (path) => path.startsWith('/api/usage')],
  ['terminal-v1', (path) => /\/terminal(?:[?]|$)/.test(path)],
  ['files-v1', (path) => /^\/api\/fs(?:[/?]|$)/.test(path) || /\/(?:files|attachments|file-grants)(?:[/?]|$)/.test(path)],
];

/** A client owns one immutable connection generation. It is never retargeted. */
export function createApiClient(connection: ConnectedInstance | null = null, valid: () => boolean = () => true, onError?: (error: ApiError) => void) {
  const owner = connection && { ...connection };
  function url(path: string): string {
    if (!owner) return path;

    // Grants returned by the home server already include signed generation authority.
    if (path.startsWith(`/api/connected/${enc(owner.id)}/`)) return path;
    if (!path.startsWith('/api/')) throw new ApiError(400, 'Invalid connected resource URL');
    return `/api/connected/${enc(owner.id)}${path}${path.includes('?') ? '&' : '?'}uam_generation=${owner.generation}`;
  }
  function admit(path: string): void {
    if (!owner) return;
    if (!valid()) throw new ApiError(409, 'This connection changed. Select the instance again.', { code: 'connection_changed' });
    const family = CAPABILITY_FAMILIES.find(([, matches]) => matches(path))?.[0] ?? null;
    if (family && !owner.capabilities.includes(family)) throw new ApiError(412, `This instance does not support ${family.replace('-v1', '').replaceAll('-', ' ')}.`, { code: 'feature_unavailable' });
  }
  function report(error: ApiError): ApiError { onError?.(error); return error; }
  function homeCall<T>(method: Method, path: string, body?: unknown): Promise<T> {
    return owner ? homeTransport<T>(method, path, body) : call<T>(method, path, body);
  }
  async function call<T>(method: Method, path: string, body?: unknown, omitBody = false, signal?: AbortSignal): Promise<T> {
    // Existing DELETE routes are bodyless; revision-checked deletes carry JSON.
    const bodyless = method === 'GET' || (method === 'DELETE' && body === undefined);
    let res: Response;
    try {
      admit(path);
      res = await fetch(url(path), {
        method,
        credentials: 'same-origin',
        signal,
        headers: bodyless ? undefined : { 'Content-Type': 'application/json' },
        body: bodyless || omitBody ? undefined : JSON.stringify(body ?? {}),
      });
    } catch (error) {
      if (signal?.aborted) throw error;
      if (error instanceof ApiError) throw report(error);
      throw report(new ApiError(0, 'Could not reach the server'));
    }
    if (res.status === 401 && path !== '/api/login') notifyUnauthorized();
    if (!res.ok) {
      const parsed = await errorBody(res);
      throw report(new ApiError(res.status, parsed.message, parsed.body));
    }
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }


  async function download(path: string): Promise<{ blob: Blob; name: string }> {
    let res: Response;
    try {
      admit(path);
      res = await fetch(url(path), { credentials: 'same-origin' });
    } catch (error) {
      if (error instanceof ApiError) throw report(error);
      throw report(new ApiError(0, 'Could not reach the server'));
    }
    if (res.status === 401) notifyUnauthorized();
    if (!res.ok) {
      const parsed = await errorBody(res);
      throw report(new ApiError(res.status, parsed.message, parsed.body));
    }
    const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'download';
    return { blob: await res.blob(), name };
  }


  /** `source` reads an HTML page's text too. */
  async function filePreview(path: string, signal: AbortSignal, knownMetadata?: PreviewMetadata, source = false): Promise<PreviewMetadata & Partial<TextPreview>> {
    return foregroundRead(async () => {
      const request = async (method: 'HEAD' | 'GET') => {
        admit(path);
      const response = await fetch(url(path), { method, credentials: 'same-origin', signal, headers: method === 'GET' ? { Range: `bytes=0-${PREVIEW_BYTES - 1}` } : undefined });
        signal.throwIfAborted();
        if (response.status === 401) notifyUnauthorized();
        if (!response.ok && !(method === 'GET' && response.status === 416)) {
          const parsed = await errorBody(response);
          throw report(new ApiError(response.status, response.status === 404 ? 'This file is no longer available.' : parsed.message, parsed.body));
        }
        return response;
      };
      const metadata = knownMetadata ?? previewMetadata((await request('HEAD')).headers);
      if (metadata.kind !== 'text' && !(source && metadata.kind === 'html')) return metadata;
      const response = await request('GET');
      if (response.status !== 416 && previewMetadata(response.headers).kind !== metadata.kind) {
        await response.body?.cancel();
        throw new Error('The file type changed. Close and open the preview again.');
      }
      return { ...metadata, ...await readTextPreview(response, signal) };
    }, signal);
  }


  function uploadFile(id: string, file: File, onProgress: (fraction: number) => void, model?: string): Upload {
    const xhr = new XMLHttpRequest();
    const modelQuery = model ? `&model=${enc(model)}` : '';
    const done = new Promise<Attachment & { id: string }>((resolve, reject) => {
      admit(`/api/sessions/${enc(id)}/attachments`);
      xhr.open('POST', url(`/api/sessions/${enc(id)}/attachments?name=${enc(file.name)}${modelQuery}`));
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
          reject(report(new ApiError(xhr.status, message, body)));
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


  return {
    owner,
    supports: (capability: string) => !owner || owner.capabilities.includes(capability),
    url,
    storageKey: (key: string) => owner ? `uam.owner.${enc(owner.instance_id)}.${enc(owner.id)}.${key}` : key,
    cacheKey: (id: string) => owner ? `@uam:${enc(owner.instance_id)}:${enc(owner.id)}:${enc(id)}` : id,
    connections: () => call<{ instance_id: string; connections: ConnectedInstance[] }>('GET', '/api/connections'),
    addConnection: (input: AddConnectionInput) => call<ConnectedInstance>('POST', '/api/connections', input),
    updateConnection: (id: string, input: UpdateConnectionInput) => call<ConnectedInstance>('PATCH', `/api/connections/${enc(id)}`, input),
    removeConnection: (id: string) => call<void>('DELETE', `/api/connections/${enc(id)}`),
    auth: () => homeCall<{ authenticated: boolean; required?: boolean }>('GET', '/api/auth'),
    login: (token: string) => homeCall<void>('POST', '/api/login', { token }),
    logout: () => homeCall<void>('POST', '/api/logout'),
    meta: () => call<Meta>('GET', '/api/meta'),
    /** The running and installed UAM; the service looks at its binary first, at most every few seconds. */
    service: () => call<ServiceStatus>('GET', '/api/service'),
    /** Restarts the service onto the installed UAM now, or once no Task works or waits. */
    restartService: () => call<ServiceStatus>('POST', '/api/service/restart', {}),
    account: (provider: string) => call<ProviderAccount>('GET', `/api/providers/${enc(provider)}/account`),
    /** The token goes to the provider's runtime only; nothing here keeps it. */
    signIn: (provider: string, token: string) => call<ProviderAccount>('POST', `/api/providers/${enc(provider)}/account/sign-in`, { token }),
    signOut: (provider: string) => call<ProviderAccount>('POST', `/api/providers/${enc(provider)}/account/sign-out`),
    /** Starts a device-code sign-in, or returns the one in progress. */
    startDeviceSignIn: (provider: string) => call<DeviceSignIn>('POST', `/api/providers/${enc(provider)}/account/device`),
    deviceSignIn: (provider: string) => call<DeviceSignIn>('GET', `/api/providers/${enc(provider)}/account/device`),
    cancelDeviceSignIn: (provider: string) => call<void>('DELETE', `/api/providers/${enc(provider)}/account/device`),
    /** Clears the linked account and signs out a sign-in the runtime stored; the next sign-in links its account. */
    unlink: (provider: string) => call<ProviderAccount>('DELETE', `/api/providers/${enc(provider)}/account/link`),
    /** The CLI's versions and update job, as last checked; `refresh` checks for a newer release first. */
    providerCli: (provider: string, refresh = false) => call<ProviderCLI>('GET', `/api/providers/${enc(provider)}/cli${refresh ? '?refresh=1' : ''}`),
    /** Starts the update to the release the server picked, or returns the one running. */
    updateProviderCli: (provider: string) => call<ProviderCLI>('POST', `/api/providers/${enc(provider)}/cli/update`, {}),

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
    configuration: (projectId = '') => call<Configuration>('GET', `/api/configuration${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`),
    draftConfiguration: (kind: Exclude<ConfigurationKind, 'instructions'>, brief: string, projectId = '') => call<ConfigurationDraft>('POST', `/api/configuration/${kind}/draft${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, { brief }),
    saveConfiguration: (kind: ConfigurationKind, name: string, body: { content: string; revision: string; path?: string }, projectId = '') => call<ConfigurationFile>('PUT', `/api/configuration/${kind}/${encodeURIComponent(name)}${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, body),
    setConfigurationDisabled: (kind: ConfigurationKind, name: string, body: { disabled: boolean; revision: string; path: string }, projectId = '') => call<ConfigurationFile>('PUT', `/api/configuration/${kind}/${encodeURIComponent(name)}${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, body),
    deleteConfiguration: (kind: ConfigurationKind, name: string, revision: string, projectId = '', path?: string) => call<void>('DELETE', `/api/configuration/${kind}/${encodeURIComponent(name)}${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, { revision, ...(path ? { path } : {}) }),
    /** Copilot's global disabled-skills setting for one name; applies to every skill with that name and changes no file. */
    setSkillGloballyDisabled: (name: string, disabled: boolean, projectId = '') => call<void>('POST', `/api/configuration/skills/global-disabled${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, { name, disabled }),
    listSkills: (source: string, projectId = '') => call<{ output: string }>('POST', `/api/configuration/skills/list${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, { source }),
    installSkills: (source: string, skills: string[], projectId = '') => call<{ output: string; installed: string[] }>('POST', `/api/configuration/skills/install${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`, { source, skills }),
    /** The service refuses an unknown key or value with 400 and changes nothing. */
    updateWebSettings: (body: Partial<Settings>) => call<Settings>('PATCH', '/api/settings', body),
    /** The model IDs an OpenAI-compatible endpoint lists; the service fetches them with the named key variable. */
    discoverModels: (body: { name?: string; base_url: string; api_key_env: string; api_key?: string; wire_api?: string }) =>
      call<{ models: string[]; truncated?: boolean; key_present: boolean }>('POST', '/api/settings/custom-models/discover', body),
    /** The cached account quotas; never calls the provider. */
    usage: () => call<AccountUsage>('GET', '/api/usage'),
    tokenUsage: () => call<TokenUsageReport>('GET', '/api/usage/tokens'),
    tokenPrices: () => call<TokenPriceCatalog>('GET', '/api/usage/prices'),
    utility: (before?: number, limit = 25) => call<UtilityLog>('GET', `/api/utility?limit=${limit}${before ? `&before=${before}` : ''}`),
    /** Web Push: the service's public key, and this browser's subscription (lib/notify.ts). */
    pushKey: () => call<{ public_key: string }>('GET', '/api/push'),
    pushSubscribe: (subscription: PushSubscriptionJSON) => call<void>('POST', '/api/push/subscribe', subscription),
    pushUnsubscribe: (endpoint: string) => call<void>('POST', '/api/push/unsubscribe', { endpoint }),
    /** The Task this tab shows while it is visible ("" for none): the service pushes no notice for it. */
    viewing: (task: string) => call<void>('POST', '/api/viewing', { page: PAGE_ID, task }),

    createSession: (body: {
      project_id: string;
      provider: string;
      model?: string;
      effort?: string;
      context_size?: string;
      agent?: string;
      mode?: PermissionMode;
      name?: string;
      prompt?: string;
      request_id: string;
    }) => call<SessionSummary>('POST', '/api/sessions', body),
    rename: (id: string, name: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { name }),
    setModel: (id: string, model: string) => call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, { model }),
    settings: (id: string, body: { model?: string; effort?: string; context_size?: string; mode?: PermissionMode; agent?: string }) =>
      call<SessionSummary>('PATCH', `/api/sessions/${enc(id)}`, body),
    /** The custom agents a Task of the Project can run as with this provider: user-invocable ones, metadata only. */
    taskAgents: (projectId: string, provider: string) =>
      call<{ agents: ConfigurationDefinition[] }>('GET', `/api/projects/${enc(projectId)}/agents?provider=${encodeURIComponent(provider)}`),
    stage: (id: string, action: 'settle' | 'reopen' | 'archive') => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/${action}`),
    queueAction: (id: string, action: 'resume' | 'clear') => call<void>('POST', `/api/sessions/${enc(id)}/queue/${action}`),
    cancelQueued: (id: string, requestId: string) => call<void>('DELETE', `/api/sessions/${enc(id)}/queue/${enc(requestId)}`),
    cancelSubagent: (id: string, agentId: string) => call<Subagent>('POST', `/api/sessions/${enc(id)}/subagents/${enc(agentId)}/cancel`, undefined, true),
    deleteSession: (id: string) => call<void>('DELETE', `/api/sessions/${enc(id)}`),
    /** Replies to send next for the Task's last completed turn; the service asks the Utility model once per state. */
    suggestions: (id: string, signal?: AbortSignal) => call<Suggestions>('POST', `/api/sessions/${enc(id)}/suggestions`, undefined, false, signal),
    /** A new Task in the same Project with the same settings (or `model`) whose first message is this Task's last one. */
    rerun: (id: string, body: { model?: string; request_id: string }) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/rerun`, body),
    fork: (id: string, body: { user_item_id: string; model: string; request_id: string }) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/fork`, body),
    /** Clears the unresolved branch request for this reply and model; a Copilot session may still exist for it. */
    dismissFork: (id: string, body: { user_item_id: string; model: string }) => call<void>('POST', `/api/sessions/${enc(id)}/fork/dismiss`, body),
    /** Readonly: what rewinding to before this owner message would remove and restore. */
    rewindPreview: (id: string, userItemId: string, signal?: AbortSignal) => call<RewindPreview>('GET', `/api/sessions/${enc(id)}/rewind/preview?user_item_id=${enc(userItemId)}`, undefined, false, signal),
    /** Executes a confirmed preview once; a repeated request ID returns its receipt. */
    rewind: (id: string, body: { user_item_id: string; mode: RewindMode; token: string; request_id: string }) => call<RewindReceipt>('POST', `/api/sessions/${enc(id)}/rewind`, body),
    /** Rereads the conversation and releases a Task held by an applied or uncertain rewind; never rewinds again. */
    /** Rewinds to before an owner prompt, then sends the edited prompt once; nothing is sent unless history was truncated. */
    resend: (id: string, body: { rewind: { user_item_id: string; mode: RewindMode; token: string; request_id: string }; prompt: { text: string; request_id: string; files?: string[]; attachments?: string[]; settings?: PromptSettings } }) =>
      call<ResendResult>('POST', `/api/sessions/${enc(id)}/resend`, body),
    reconcileRewind: (id: string, requestId: string) => call<RewindReceipt>('POST', `/api/sessions/${enc(id)}/rewind/reconcile`, { request_id: requestId }),
    /** Only after a failed reconcile: clears the hold with the outcome unknown; no history request is made. */
    releaseRewind: (id: string, requestId: string) => call<RewindReceipt>('POST', `/api/sessions/${enc(id)}/rewind/release`, { request_id: requestId }),
    /** The whole conversation as a Markdown file. */
    exportMarkdown: (id: string) => download(`/api/sessions/${enc(id)}/export`),
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
    attachmentUrl: (id: string, attachmentId: string) => url(`/api/sessions/${enc(id)}/attachments/${enc(attachmentId)}`),
    /** An image file of the Task's directory, by absolute path or one relative to it. */
    rawFileUrl: (id: string, path: string) => url(`/api/sessions/${enc(id)}/files/raw?path=${enc(path)}`),
    /** Any file of the Task's directory by its "/"-separated path relative to it, as a path so a page's relative links resolve to its siblings. */
    viewFileUrl: (id: string, path: string) => url(`/api/sessions/${enc(id)}/files/view/${path.split('/').map(enc).join('/')}`),
    cancelBackgroundTask: (id: string, taskId: string) => call<{ accepted: true; background_tasks: BackgroundTasks }>('POST', `/api/sessions/${enc(id)}/background-tasks/${enc(taskId)}/cancel`),
    cancel: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/cancel`),
    close: (id: string) => call<SessionSummary>('POST', `/api/sessions/${enc(id)}/close`),
    planReview: (id: string, requestId: string, signal?: AbortSignal) => call<PlanReview>('GET', `/api/sessions/${enc(id)}/plan-reviews/${enc(requestId)}`, undefined, false, signal),
    planDraft: (id: string, signal?: AbortSignal) => call<PlanDraft>('GET', `/api/sessions/${enc(id)}/plan`, undefined, false, signal),
    respond: (id: string, iid: string, answer: Answer) =>
      call<Interaction>('POST', `/api/sessions/${enc(id)}/interactions/${enc(iid)}`, answer),
    /** The latest turn's evidence; with `since`, also what changed between `since` and `until`. */
    turnChanges: (id: string, timingId: string, signal?: AbortSignal) => call<TurnChanges>('GET', `/api/sessions/${enc(id)}/turns/${enc(timingId)}/changes`, undefined, false, signal),
    turnTodos: (id: string, timingId: string, signal?: AbortSignal) => call<TurnTodos>('GET', `/api/sessions/${enc(id)}/turns/${enc(timingId)}/todos`, undefined, false, signal),
    contextBreakdown: (id: string, attribution = false, signal?: AbortSignal) => call<ContextBreakdown>('GET', `/api/sessions/${enc(id)}/context${attribution ? '?attribution=true' : ''}`, undefined, false, signal),
    evidence: (id: string, look?: { since: string; until: string }, signal?: AbortSignal) =>
      call<TurnEvidence>('GET', `/api/sessions/${enc(id)}/evidence${look ? `?since=${enc(look.since)}&until=${enc(look.until)}` : ''}`, undefined, false, signal),
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
    itemDiff: (id: string, itemId: string, agentId: string, eventId: string, path: string, signal?: AbortSignal) => foregroundRead(() => call<ItemDiffData>('GET', `/api/sessions/${enc(id)}/items/${enc(itemId)}/diff?agent_id=${enc(agentId)}&event_id=${enc(eventId)}&path=${enc(path)}`, undefined, false, signal), signal),
    itemBody: (id: string, itemId: string, agentId: string, signal?: AbortSignal) => foregroundRead(() => call<BodyData>('GET', `/api/sessions/${enc(id)}/items/${enc(itemId)}?agent_id=${enc(agentId)}`, undefined, false, signal), signal),
    detailEventsUrl: (id: string, agentId: string, bodies: BodyReference[], agentBefore?: string, epoch?: string, agentUntil?: string) => url(`/api/events/detail?${detailEventsQuery(id, agentId, bodies, agentBefore, epoch, agentUntil)}`),
    eventsUrl: (id: string | null) => url(id ? `/api/events?session=${enc(id)}&tool_output=delta&history=recent&view=compact-v1&page=${PAGE_ID}` : `/api/events?page=${PAGE_ID}`),

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
    taskUsageMetrics: (id: string, signal?: AbortSignal) => call<TaskUsageMetrics>('GET', `/api/sessions/${enc(id)}/usage-metrics`, undefined, false, signal),
    askAside: (id: string, question: string, signal?: AbortSignal) => call<AsideAnswer>('POST', `/api/sessions/${enc(id)}/aside`, { question }, false, signal),
    taskMcp: (id: string, summary = false) => call<McpTaskStatus>('GET', `/api/sessions/${enc(id)}/mcp${summary ? '?summary=1' : ''}`),
    taskMcpAction: (id: string, name: string, action: 'enable' | 'disable' | 'restart') => call<McpTaskStatus>('POST', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/${action}?summary=1`),
    reconnectTaskMcp: (id: string) => call<McpTaskStatus>('POST', `/api/sessions/${enc(id)}/mcp/reconnect?summary=1`),
    taskMcpTools: (id: string, name: string) => call<{ tools: McpTool[] }>('GET', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/tools`),
    startMcpSignIn: (id: string, name: string, again: boolean) => call<McpSignIn>('POST', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/sign-in`, { again }),
    finishMcpSignIn: (id: string, name: string, url: string) => call<void>('POST', `/api/sessions/${enc(id)}/mcp/servers/${enc(name)}/sign-in/finish`, { url }),
    routines: (projectId: string) => call<{ routines: Routine[] }>('GET', `/api/projects/${enc(projectId)}/routines`),
    /** Every Project's routines, oldest first. */
    allRoutines: () => call<{ routines: Routine[] }>('GET', '/api/routines'),
    createRoutine: (projectId: string, body: RoutineInput) => call<Routine>('POST', `/api/projects/${enc(projectId)}/routines`, body),
    updateRoutine: (id: string, body: Partial<RoutineInput>) => call<Routine>('PATCH', `/api/routines/${enc(id)}`, body),
    deleteRoutine: (id: string) => call<void>('DELETE', `/api/routines/${enc(id)}`),
    runRoutine: (id: string) => call<Routine>('POST', `/api/routines/${enc(id)}/run`),
  };
}
export type ApiClient = ReturnType<typeof createApiClient>;
export const api = createApiClient();
function homeTransport<T>(method: Method, path: string, body?: unknown): Promise<T> {
  if (path === '/api/auth') return api.auth() as Promise<T>;
  if (path === '/api/logout') return api.logout() as Promise<T>;
  if (method === 'POST' && path === '/api/login') return api.login((body as {token: string}).token) as Promise<T>;
  throw new Error('Unsupported home authentication request');
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

/** This tab's name for its event streams, so what it reports showing (`api.viewing`) applies to them. */
export const PAGE_ID = newRequestId();

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

export const readOnly = (s: Pick<SessionSummary, 'stage'>): boolean => s.stage === 'settled' || s.stage === 'archived';
export const stageLabel = (s: SessionSummary): string => {
  if (s.stage === 'settled') return 'Settled';
  if (s.stage === 'archived') return 'Archived';
  return 'Active';
};
