// Development-only seed for the in-browser mock service (see install.ts).
// Shapes are the wire shapes from src/api.ts. Paths and names are fictional.

import type { Command, Interaction, Item, Meta, PreviousSession, Project, SessionDetail, Settings, Subagent, ToolCall, TurnTodos } from '../api';

export interface MockTask extends SessionDetail {
  /** Subagent transcripts keyed by agent id (served by the subagent route). */
  agentItems: Record<string, Item[]>;
  /** Older subagents only Copilot's record still has: listed on request (`subagents_before`), openable like the rest. */
  recordSubagents?: Subagent[];
}

export interface MockChange {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  patch: string;
  /** Tasks whose edit tools touched it (the "This task" scope); `turn` lists those that did in their latest turn. */
  by?: string[];
  turn?: string[];
}

export interface MockState {
  meta: Meta;
  projects: Project[];
  settings: Settings;
  tasks: MockTask[];
  changes: Record<string, MockChange[]>;
  /** What every open Task lists for `/`. */
  commands: Command[];
  /** Git-visible paths per project; a project without an entry is not a Git tree. */
  files: Record<string, string[]>;
  /** Recorded CLI conversations per project that can be imported as Tasks; one already is (t3's), one is open elsewhere. */
  previous: Record<string, PreviousSession[]>;
  /** Whole texts of `clipped` items by item ID, for the item route; the items hold the shortened part. */
  wholeTexts: Record<string, string>;
  /** The todo lists uam kept when turns ended, by timing ID, for the turn route. */
  turnTodos: Record<string, TurnTodos>;
}

const NOW = Date.now();
const ago = (min: number) => new Date(NOW - min * 60000).toISOString();

const CAPS = { cancel: true, permissions: true, questions: true, session_diff: false, history: true, context_size: true, import: true, mcp: true };
const SIZES = [
  { id: 'default', tokens: 200_000 },
  { id: 'long_context', tokens: 1_000_000 },
];

const EDIT_DIFF = `--- a/internal/vterm/redraw.go
+++ b/internal/vterm/redraw.go
@@ -41,5 +41,8 @@ func (v *VTerm) Redraw(w io.Writer) error {
 	if err := v.replayModes(w); err != nil {
 		return err
 	}
+	if err := v.replayFocusEvents(w); err != nil {
+		return err
+	}
 	return v.replayScreen(w)
 }`;

const TEMPLATE_DIFF = `--- a/templates/post.html
+++ b/templates/post.html
@@ -12,4 +12,4 @@
   <header class="post-head">
-    <img src="{{ .Cover }}">
+    <img src="{{ .Cover }}" alt="{{ .CoverAlt }}">
     <h1>{{ .Title }}</h1>
   </header>`;

function task(
  base: Pick<MockTask, 'id' | 'project_id' | 'workdir' | 'model' | 'name' | 'title' | 'state' | 'created_at' | 'updated_at'> &
    Partial<MockTask>,
): MockTask {
  return {
    provider: 'copilot',
    conversation_id: `conv-${base.id}`,
    last_model: '',
    subagents_running: 0,
    open: base.state !== 'closed',
    pending: 0,
    capabilities: CAPS,
    items: [],
    interactions: [],
    subagents: [],
    history_truncated: false,
    last_submission: null,
    agentItems: {},
    ...base,
  };
}

const tool = (id: string, min: number, t: Item['tool'] & { title?: string }, agent_id?: string): Item => ({
  id,
  kind: 'tool',
  time: ago(min),
  tool: t,
  ...(agent_id ? { agent_id } : {}),
});

/** `count` short items in turns of four (a prompt or step, a search, a thought, a result), three minutes apart. IDs are stable across reloads. */
function longHistory(prefix: string, count: number, agent_id?: string): Item[] {
  return Array.from({ length: count }, (_, n): Item => {
    const id = `${prefix}${n}`, turn = Math.floor(n / 4) + 1, min = (count - n) * 3 + 3, agent = agent_id ? { agent_id } : {};
    if (n % 4 === 1) return tool(id, min, { name: 'grep', title: `Search "export" in pkg${turn}/`, status: 'completed', output: `${(turn % 7) + 2} matches` }, agent_id);
    if (n % 4 === 2) return { id, kind: 'reasoning', time: ago(min), text: `Package ${turn} exports ${(turn % 5) + 1} names; ${turn % 3} have no callers.`, ...agent };
    if (n % 4 === 3) return { id, kind: 'assistant', time: ago(min), text: turn % 3 ? `Package ${turn}: removed ${turn % 3} unused export${turn % 3 === 1 ? '' : 's'}.` : `Package ${turn}: nothing unused.`, ...agent };
    return { id, kind: agent_id ? 'assistant' : 'user', time: ago(min), text: agent_id ? `Step ${turn}: reading pkg${turn}.` : `Turn ${turn}: check pkg${turn} for unused exports.`, ...agent };
  });
}

/**
 * A message the service holds shortened, as it holds any text over 4 MiB: the first `held`
 * characters and its cut marker (the mock cuts far earlier, so the transcript stays light). The
 * whole text goes to `wholeTexts`.
 */
function clipped(item: Item, whole: string, held: number, wholeTexts: Record<string, string>): Item {
  wholeTexts[item.id] = whole;
  return { ...item, text: `${whole.slice(0, held)}\n[truncated by uam]`, clipped: true };
}

/** About 2 MB of Markdown: every removed export, one list line each. */
function removedExports(): string {
  const lines = ['Here is every export removed, by package.', ''];
  for (let pkg = 1; pkg <= 150; pkg++) {
    lines.push(`### pkg${pkg}`, '');
    for (let n = 0; n < 160; n++) lines.push(`- \`pkg${pkg}/src/module${n % 12}.ts\`: removed \`helper${pkg}x${n}\` (no callers since the ${n % 2 ? 'parser' : 'loader'} rewrite)`);
    lines.push('');
  }
  lines.push('That is the whole list: 24,000 exports across 150 packages.');
  return lines.join('\n');
}

export function seed(): MockState {
  const wholeTexts: Record<string, string> = {};
  const meta: Meta = {
    version: 'dev-mock',
    recent_workdirs: ['/home/user/projects/unified-agent-manager', '/home/user/dotfiles', '/home/user/projects/notes-site', '/home/user/projects/scratch'],
    providers: [
      {
        name: 'copilot',
        display_name: 'GitHub Copilot',
        available: true,
        capabilities: { ...CAPS, titles: true, host_tools: true, github_mcp: true, assisted_permissions: true, custom_agents: true },
        cheapest_model: 'gpt-5-mini',
        models: [
          // `auto` reports no media and is not gated; kimi-k3 and the flash model take text only.
          { id: 'auto', name: 'Auto' },
          { id: 'claude-haiku-4.5', name: 'Claude Haiku 4.5', efforts: ['low', 'medium', 'high'], context_sizes: SIZES, media: { images: true, pdf: true, max_images: 20, types: ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'application/pdf'] } },
          { id: 'gpt-5.6-luna', name: 'GPT-5.6 Luna', efforts: ['low', 'medium', 'high', 'xhigh'], context_sizes: SIZES, media: { images: true, pdf: true, max_images: 10 } },
          { id: 'gpt-5-mini', name: 'GPT-5 mini', efforts: ['low', 'medium', 'high'], media: { images: true, pdf: false, max_images: 4, types: ['image/png', 'image/jpeg', 'image/gif', 'image/webp'] } },
          { id: 'mai-code-1.1-flash', name: 'MAI-Code-1.1-Flash', media: { images: false, pdf: false } },
          { id: 'kimi-k3', name: 'Kimi K3', media: { images: false, pdf: false } },
          // A custom (BYOM) model, as the service lists it after Copilot's own.
          { id: 'openrouter/qwen/qwen3-coder', name: 'Qwen3 Coder', efforts: [], context_sizes: [], media: { images: false, pdf: false } },
        ],
      },
    ],
  };

  // p1 has a long branch, p2 none.
  const projects: Project[] = [
    {
      id: 'p1',
      name: 'unified-agent-manager',
      dir: '/home/user/projects/unified-agent-manager',
      created_at: ago(60 * 24 * 9),
      badge: { text: 'UM', color: 'blue' },
      branch: 'feat/web-project-defaults-and-sidebar-revamp',
    },
    { id: 'p2', name: 'dotfiles', dir: '/home/user/dotfiles', created_at: ago(60 * 24 * 4), badge: { text: 'DF', color: 'teal' }, no_git: 'not_repository' },
    {
      id: 'p3',
      name: 'notes-site',
      dir: '/home/user/projects/notes-site',
      created_at: ago(60 * 24 * 2),
      badge: { text: 'NS', color: 'pink' },
      branch: 'main',
    },
  ];

  const p = (id: string) => projects.find((x) => x.id === id)!.dir;

  const q1: Interaction = {
    id: 'q1',
    kind: 'question',
    title: 'Which terminals should the explanation cover?',
    state: 'pending',
    time: ago(14),
    questions: [
      {
        text: 'Pick the terminals to cover',
        choices: ['Windows Terminal', 'GNOME Terminal', 'tmux inside either', 'VS Code integrated terminal'],
        multiple: true,
        custom: true,
      },
      { text: 'Anything specific the doctor line should say?', custom: true },
    ],
  };

  const perm1: Interaction = {
    id: 'perm1',
    kind: 'permission',
    title: 'Run a shell command outside the project',
    detail: 'chmod 0644 ~/.config/zsh/aliases.zsh && rm -f ~/.zcompdump*',
    state: 'pending',
    time: ago(3),
    options: [
      { id: 'once', label: 'Allow once' },
      { id: 'session', label: 'Allow for this task' },
      { id: 'deny', label: 'Deny', reject: true },
    ],
  };

  const a1: Subagent = {
    id: 'a1',
    parent_tool_call_id: 'i3',
    name: 'Survey templates for missing alt text and labels',
    description: 'Check every template under templates/ for images without alt text and inputs without labels.',
    status: 'running',
    started_at: ago(11),
    model: 'claude-haiku-4.5',
    effort: 'medium',
    tokens: 182_400,
    tool_calls: 14,
  };
  const a2: Subagent = {
    id: 'a2',
    parent_tool_call_id: 'i4',
    name: 'Check contrast of the theme tokens',
    description: 'Compute WCAG contrast for the colour tokens in assets/theme.css.',
    status: 'running',
    started_at: ago(11),
    model: 'gpt-5-mini',
    effort: 'low',
    tokens: 96_300,
    tool_calls: 9,
    // Resumed a minute ago; nothing of this run is recorded yet.
    runs: [
      { started_at: ago(11), ended_at: ago(10.5), status: 'completed', trigger: 'spawn' },
      { started_at: ago(1), status: 'running', trigger: 'agent' },
    ],
  };
  const a3: Subagent = {
    id: 'a3',
    parent_tool_call_id: 'i5',
    name: 'Run the accessibility linter',
    description: 'Run axe on the rendered post page and report violations.',
    status: 'failed',
    error: 'axe-core is not installed in this project.',
    started_at: ago(11),
    ended_at: ago(10),
    model: 'claude-haiku-4.5',
    effort: 'high',
    tokens: 41_800,
    tool_calls: 3,
  };
  const a4: Subagent = {
    id: 'a4',
    parent_tool_call_id: 'i6',
    name: 'Check the heading order',
    description: 'Check that headings in templates/ nest without skipping a level.',
    status: 'completed',
    started_at: ago(11),
    ended_at: ago(9),
    model: 'claude-haiku-4.5',
    effort: 'low',
    tokens: 1_326_764,
    tool_calls: 88,
    // Asked once more after its first pass (a follow-up).
    runs: [
      { started_at: ago(11), ended_at: ago(10), status: 'completed', trigger: 'spawn' },
      { started_at: ago(9.5), ended_at: ago(9), status: 'completed', trigger: 'user' },
    ],
  };
  const oldAudits: Subagent[] = [1, 2, 3].map((n) => ({
    id: `a-old-${n}`,
    parent_tool_call_id: `h-old-${n}`,
    name: `Audit package batch ${n}`,
    description: `Check batch ${n} of the packages for exports without callers.`,
    status: 'completed',
    started_at: ago(1800 - n * 100),
    ended_at: ago(1750 - n * 100),
    model: 'claude-haiku-4.5',
    effort: 'low',
  }));
  const a5: Subagent = {
    id: 'a5',
    parent_tool_call_id: 'h-audit',
    name: 'Audit the remaining packages',
    description: 'Check the packages not covered yet for exports without callers.',
    status: 'completed',
    started_at: ago(900),
    ended_at: ago(2),
    model: 'claude-haiku-4.5',
    effort: 'medium',
    tokens: 2_140_000,
    tool_calls: 212,
  };
  // Spawned by a2 for the caller sweep: its call is in a2's transcript.
  const a6: Subagent = {
    id: 'a6',
    parent_tool_call_id: 'k-a6',
    parent_agent_id: 'a2',
    name: 'Verify store callers',
    description: 'Check the callers of store.Open for the old cursor.',
    status: 'running',
    started_at: ago(0.8),
    model: 'gpt-5-mini',
    effort: 'low',
    tokens: 22_100,
    tool_calls: 4,
  };

  const tasks: MockTask[] = [
    task({
      id: 't1',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'auto',
      last_model: 'mai-code-1.1-flash',
      name: 'Fix re-attach redraw regression',
      title: '',
      state: 'working',
      background_tasks_running: 1,
      capabilities: { ...CAPS, execution_modes: true, assisted_permissions: true },
      execution: { known: true, mode: 'interactive' },
      turn_activity: { intent: 'Running the redraw tests', todos: { known: false, touched: false, todos: [], counts: {} } },
      background_tasks: {
        known: true,
        tasks: [
          { id: 'bg-watch', description: 'Repeat the redraw tests', command: 'go test ./internal/vterm/... -run Redraw -count=200', status: 'running', started_at: ago(3) },
          { id: 'bg-docs', description: 'Serve the docs preview', command: 'npx vite --host 127.0.0.1 --port 8765 docs', status: 'completed', started_at: ago(8), ended_at: ago(5) },
        ],
      },
      queued: 1,
      queue: [
        {
          request_id: 'q-seed-1',
          text: 'Then describe the new behaviour in @docs/terminal.md, matching the flow in the screenshot.',
          queued_at: ago(1),
          files: ['docs/terminal.md'],
          attachments: [{ id: 'att-png-seed2', name: 'attach-flow.png', mime: 'image/png', size: 20480 }],
        },
      ],
      created_at: ago(9),
      updated_at: ago(1),
      items: [
        {
          id: 'i1',
          kind: 'user',
          time: ago(9),
          text: 'After detaching and re-attaching, focus events stop arriving in the provider. The first attach is fine. Find the cause in @internal/vterm and fix it with a regression test.',
        },
        {
          id: 'r1',
          kind: 'reasoning',
          time: ago(9),
          text: 'The first attach works, so the mode is applied once. On re-attach the client resets the terminal and `Redraw` must replay every mode. The earlier fix (#28) covered private modes; focus events (`?1004`) are tracked separately in `modes.go`.\n\nPlan: confirm with grep, add `replayFocusEvents`, and cover it with a test that detaches and re-attaches.',
        },
        {
          id: 'i2',
          kind: 'assistant',
          time: ago(8),
          text: 'I will look at what `Redraw` replays on attach. The pattern from the earlier private-mode fix suggests the focus-event mode (`?1004`) is tracked but never re-emitted.',
        },
        tool('i3', 8, { name: 'view', title: 'Read internal/vterm/redraw.go', status: 'completed', input: '{"path":"internal/vterm/redraw.go"}', output: '112 lines' }),
        { ...tool('i3b', 8, { name: 'view', status: 'completed', input: '{"path":"docs/assets/attach-flow.png"}', output: 'Image: docs/assets/attach-flow.png' }), images: [{ id: 'img-png-seed3', mime: 'image/png', size: 20480, name: 'attach-flow.png' }] },
        tool('i4', 6, {
          name: 'grep',
          title: 'Search "1004" in internal/vterm',
          status: 'completed',
          input: '{"pattern":"1004","path":"internal/vterm"}',
          output: 'internal/vterm/modes.go:58: case 1004: v.focusEvents = set',
        }),
        {
          id: 'i5',
          kind: 'assistant',
          time: ago(5),
          text: 'Confirmed: `focusEvents` is recorded in `modes.go` but `Redraw` never replays it. Adding `replayFocusEvents` after the private-mode replay so the order matches the attach client.',
        },
        { id: 'trail-i6', kind: 'notice', time: ago(4), text: 'uam trail: Task "Fix terminal modes" edited internal/vterm/redraw.go 3 min ago and is still running. Re-read it first.' },
        tool('i6', 4, { name: 'edit', title: 'Edit internal/vterm/redraw.go', status: 'completed', input: '{"path":"internal/vterm/redraw.go"}', output: EDIT_DIFF }),
        {
          id: 'i6b',
          kind: 'assistant',
          time: ago(3),
          text: `Before and after, from the PTY harness (the first is in the project, the second one level up, which the service does not serve):

![Focus events after re-attach](${p('p1')}/docs/assets/attach-flow.png)

![Missing capture](file://${p('p1')}/shots/missing.png)

![Outside the project](/home/dev/private/secret.png)

The full-size capture is in [attach-flow.png](docs/assets/attach-flow.png); the upstream tracker has the [original report](https://example.com/issue/56.png).`,
        },
        tool('fuse-i6', 2, { name: 'web_fetch', title: 'Fetch reference documentation', status: 'failed', input: '{"url":"https://example.com/docs/reference"}', output: 'uam fuse: example.com/docs refused 403 at 14:02 UTC; reopens 20:02 UTC' }),
        tool('i7', 1, { name: 'bash', title: 'go test ./internal/vterm/...', status: 'running', input: 'go test ./internal/vterm/... -run Redraw -count=1' }),
        tool('i8', 1, {
          name: 'bash', title: 'go test ./internal/attach/...', status: 'failed',
          display_arg: 'go test ./internal/attach/...', has_input: true, has_output: true,
          tests: { passed: 14, failed: 1 },
        }),
      ],
      interactions: [
        { id: 'perm-i6', kind: 'permission', title: 'Write file', detail: 'internal/vterm/redraw.go', state: 'answered', resolution: 'allowed (yolo)', time: ago(4), tool_call_id: 'i6' },
        { id: 'perm-i7', kind: 'permission', title: 'Run shell command', detail: 'go test ./internal/vterm/... -run Redraw -count=1', state: 'answered', resolution: 'Allow once', time: ago(1), tool_call_id: 'i7' },
      ],
    }),
    task({
      id: 't2',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: '',
      title: 'Explain how the CPR probe decides the glyph set',
      state: 'awaiting_answer',
      pending: 1,
      created_at: ago(20),
      updated_at: ago(14),
      items: [
        { id: 'i1', kind: 'user', time: ago(20), text: 'Explain how the CPR probe decides the glyph set, and whether the doctor command reports it.' },
        {
          id: 'i2',
          kind: 'assistant',
          time: ago(15),
          text: 'The probe writes a wide glyph, asks for the cursor position, and compares the column. Before I write this up, I need to know which terminals matter to you.',
        },
      ],
      interactions: [q1],
    }),
    task({
      id: 't3',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'auto',
      last_model: 'mai-code-1.1-flash',
      name: 'Doctor: add terminal line',
      title: '',
      state: 'completed',
      // Every composer tool (Tools panel and `/btw`), with a context report and recorded usage to show.
      capabilities: { ...CAPS, aside: true, usage: true, usage_metrics: true, context_breakdown: true, custom_agents: true },
      context: { used: 84_300, limit: 200_000, prompt: 82_100, cached: 61_400 },
      usage: { ai_units: 1.84 },
      outcome: 'Explained how a dumb terminal is reported',
      created_at: ago(60),
      updated_at: ago(42),
      turn_timings: [{ id: 'tt-t3', user_item_id: 'i1', started_at: ago(60), ended_at: ago(42), state: 'completed', input_tokens: 48210, output_tokens: 1860, generation_ms: 41200, todo: { done: 3, total: 6, blocked: 1, in_progress: 1, pending: 1, open: 2 } }],
      items: [
        { id: 'i1', kind: 'user', time: ago(60), text: 'Add a line to `uam doctor` that reports the detected terminal and glyph set.' },
        { id: 'r1', kind: 'reasoning', time: ago(58), text: '`doctor.go` prints rows through `printRow`; the terminal probe already exposes `term.Describe()`. One line plus a test.' },
        tool('i2', 55, { name: 'edit', title: 'Edit cmd/doctor.go', status: 'completed', output: '@@ -30,4 +30,5 @@\n \tprintRow("shell", shell)\n+\tprintRow("terminal", term.Describe())' }),
        tool('i3', 50, { name: 'bash', title: 'go test ./cmd/...', status: 'completed', output: 'ok  \tcmd\t0.41s' }),
        {
          id: 'i4',
          kind: 'assistant',
          time: ago(42),
          text: 'Done. `uam doctor` now prints a `terminal` row with the name and the glyph set the probe chose:\n\n```text\nshell     zsh 5.9\nterminal  Windows Terminal · wide glyphs\n```\n\nThe existing doctor tests pass; I added one that fakes a narrow terminal.',
        },
        {
          id: 'i5',
          kind: 'user',
          time: ago(44),
          text: 'Does it handle `TERM=dumb`? This is what @cmd/doctor.go printed on the dumb terminal; the log is attached too.',
          attachments: [
            { id: 'att-png-seed1', name: 'dumb-terminal.png', mime: 'image/png', size: 48213 },
            { id: 'att-txt-seed1', name: 'doctor-output.txt', mime: 'text/plain', size: 1320 },
            { name: 'earlier-run.png', mime: 'image/png' },
          ],
        },
        {
          id: 'i6',
          kind: 'assistant',
          time: ago(42),
          text: 'Yes. With `TERM=dumb` the probe is skipped and the row reads `terminal  dumb · ASCII glyphs`. Covered by `TestDoctorDumbTerminal`, and the doctor tests pass.',
        },
      ],
    }),
    task({
      id: 't4',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'gpt-5-mini',
      name: 'Bump GitHub Actions pins',
      title: '',
      state: 'failed',
      state_detail: 'Provider process exited (code 1)',
      spawned_by: 't3',
      capabilities: { ...CAPS, assisted_unavailable: 'the Copilot CLI kept permission mode "manual" instead of assisted; Assisted needs the CLI\'s AUTO_APPROVAL feature flag, which this CLI or its policy did not apply' },
      created_at: ago(140),
      updated_at: ago(130),
      items: [
        { id: 'i1', kind: 'user', time: ago(140), text: 'Bump every action in .github/workflows to the current release and keep the SHA pins.' },
        tool('i2', 138, { name: 'bash', title: 'gh api repos/actions/checkout/releases/latest', status: 'failed', output: 'gh: HTTP 401 Bad credentials' }),
        { id: 'i3', kind: 'notice', time: ago(130), text: 'Error: provider process exited (code 1).' },
      ],
    }),
    task({
      id: 't5',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'auto',
      name: 'Remove the old tmux backend notes',
      title: '',
      state: 'interrupted',
      created_at: ago(60 * 26 + 5),
      updated_at: ago(60 * 26),
      items: [
        { id: 'i1', kind: 'user', time: ago(60 * 26 + 5), text: 'Remove every mention of the tmux backend from docs/ now that the native backend shipped.' },
        // A message of only attachments, one of them no longer stored.
        {
          id: 'i2',
          kind: 'user',
          time: ago(60 * 26 + 2),
          text: '',
          attachments: [
            { id: 'att-png-seed5', name: 'tmux-pane.png', mime: 'image/png', size: 20480 },
            { name: 'old-notes.txt', mime: 'text/plain' },
          ],
        },
      ],
    }),
    task({
      id: 't6',
      project_id: 'p2',
      workdir: p('p2'),
      model: 'auto',
      last_model: 'mai-code-1.1-flash',
      name: 'Tidy zsh startup',
      title: '',
      state: 'awaiting_permission',
      pending: 1,
      created_at: ago(7),
      updated_at: ago(3),
      items: [
        { id: 'i1', kind: 'user', time: ago(7), text: 'Startup takes 800ms. Find what is slow and move the aliases into their own file.' },
        tool('i2', 6, { name: 'bash', title: 'zsh -xv -i -c exit 2>&1 | head -200', status: 'completed', output: '… compinit: 412ms\n… nvm.sh: 290ms' }),
        {
          id: 'i3',
          kind: 'assistant',
          time: ago(4),
          text: 'Two culprits: `compinit` runs without a cache and `nvm.sh` loads eagerly. I moved the aliases into `~/.config/zsh/aliases.zsh`. To make the new file readable I need to run a command outside the project directory.',
        },
      ],
      interactions: [perm1],
    }),
    task({
      id: 't7',
      project_id: 'p2',
      workdir: p('p2'),
      model: 'auto',
      // Near the point where the conversation compacts (the ring's mark).
      context: { used: 141_000, limit: 200_000 },
      name: '',
      title: '',
      state: 'working',
      // The provider compacts the conversation while the turn runs; an earlier compaction left its notice.
      compacting: true,
      created_at: ago(0),
      updated_at: ago(0),
      items: [{ id: 'n0', kind: 'notice', time: ago(0), text: 'Compacted the conversation · freed 41,210 tokens.' }, { id: 'i1', kind: 'user', time: ago(0), text: 'Which of these aliases are never used? Check the zsh history file.\n\n```sh\nalias gs="git status"\nalias gl="git log --oneline"\nalias dcu="docker compose up"\nalias serve="python -m http.server"\n```' }],
    }),
    task({
      id: 't8',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: 'Accessibility pass on the post template',
      title: '',
      state: 'working',
      subagents_running: 2,
      // The agents' todo list in all four stages: two subagents' rows in progress, one blocked, one to do.
      turn_activity: {
        todos: {
          known: true,
          touched: true,
          todos: [
            { id: 'plan', title: 'Split the audit into markup and contrast', status: 'done', changed_at: ago(11) },
            { id: 'alt', title: 'Survey templates for missing alt text and labels', status: 'in_progress', agent_id: 'a1', changed_at: ago(10) },
            { id: 'contrast', title: 'Check contrast of the theme tokens', status: 'in_progress', agent_id: 'a2', changed_at: ago(1) },
            { id: 'lint', title: 'Run the accessibility linter', status: 'blocked', note: 'axe-core is not installed in this project.', agent_id: 'a3', changed_at: ago(10) },
            { id: 'headings', title: 'Fix the heading order', status: 'done', agent_id: 'a4', changed_at: ago(9) },
            { id: 'summary', title: 'Summarise the fixes in the reply', status: 'pending' },
          ],
          counts: { done: 2, total: 6, blocked: 1, in_progress: 2, pending: 1, open: 3 },
          now: 'contrast',
        },
      },
      created_at: ago(12),
      updated_at: ago(2),
      items: [
        { id: 'i1', kind: 'user', time: ago(12), text: 'Audit templates/post.html for accessibility problems and fix what is safe to fix. Check contrast of the theme tokens too.' },
        { id: 'i2', kind: 'assistant', time: ago(11), text: 'Splitting this into two independent surveys so they can run in parallel: markup and contrast.' },
        tool('i3', 11, { name: 'task', title: 'Survey templates for missing alt text and labels', status: 'running', input: '{"description":"Survey templates for missing alt text and labels"}' }),
        tool('i4', 11, { name: 'task', title: 'Check contrast of the theme tokens', status: 'running', input: '{"description":"Check contrast of the theme tokens"}' }),
        tool('i5', 11, {
          name: 'task',
          title: 'Run the accessibility linter',
          status: 'failed',
          input: '{"description":"Run the accessibility linter"}',
          output: 'axe-core is not installed in this project.',
        }),
        tool('i6', 11, {
          name: 'task',
          title: 'Check the heading order',
          status: 'completed',
          input: '{"description":"Check the heading order"}',
          output: '## Heading order\n\n**The byline skips a level** in `templates/post.html`: it jumps from `h1` to `h3`. Demoted it to `h2`; [list.html](templates/list.html) nests correctly.\n\n- `templates/post.html`: 1 change\n- `templates/list.html`: no change',
        }),
      ],
      subagents: [a1, a2, a3, a4, a6],
      agentItems: {
        a4: [
          tool('w1', 11, { name: 'grep', title: 'Search "<h[1-6]" in templates', status: 'completed', output: 'templates/post.html:9\ntemplates/post.html:14\ntemplates/list.html:7' }, 'a4'),
          tool('w2', 10, { name: 'edit', title: 'Edit templates/post.html', status: 'completed', output: '@@ -14,1 +14,1 @@\n-<h3 class="byline">\n+<h2 class="byline">' }, 'a4'),
          { id: 'w3', kind: 'assistant', time: ago(9), agent_id: 'a4', text: 'The byline was an `h3` under an `h1`; it is an `h2` now. `list.html` nests correctly.' },
        ],
        a3: [
          tool('v1', 11, { name: 'bash', title: 'npx axe http://localhost:4000/post', status: 'failed', output: 'npm ERR! could not determine executable to run' }, 'a3'),
          { id: 'v2', kind: 'assistant', time: ago(10), agent_id: 'a3', text: '`axe-core` is not installed and I was told not to add dependencies. Stopping here.' },
        ],
        a1: [
          { id: 's0', kind: 'reasoning', time: ago(10), agent_id: 'a1', text: 'Grep for `<img` first, then read each hit for a missing `alt`.' },
          tool('s1', 10, { name: 'grep', title: 'Search "<img" in templates', status: 'completed', output: 'templates/post.html:13\ntemplates/list.html:22' }, 'a1'),
          tool('s2', 9, { name: 'view', title: 'Read templates/post.html', status: 'completed', output: '64 lines' }, 'a1'),
          {
            id: 's3',
            kind: 'assistant',
            time: ago(8),
            agent_id: 'a1',
            text: 'The cover image has no `alt`. The list template uses a decorative image with an empty alt, which is fine. Fixing the cover.',
          },
          tool('s4', 7, { name: 'edit', title: 'Edit templates/post.html', status: 'completed', output: TEMPLATE_DIFF }, 'a1'),
          tool('s5', 2, { name: 'bash', title: 'npx html-validate templates/', status: 'running' }, 'a1'),
        ],
        a2: [
          tool('u1', 10, { name: 'view', title: 'Read assets/theme.css', status: 'completed', output: '41 lines' }, 'a2'),
          { id: 'u2', kind: 'assistant', time: ago(6), agent_id: 'a2', text: '`--muted: #999` on white is 2.85:1, below AA for body text. Trying a warmer, darker value.' },
          tool('u3', 3, { name: 'bash', title: 'node scripts/contrast.mjs', status: 'running' }, 'a2'),
        ],
      },
    }),
    task({
      id: 't-chart',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'gpt-6-luna',
      name: 'Chart this month\'s commits',
      title: '',
      state: 'completed',
      created_at: ago(14),
      updated_at: ago(11),
      items: [
        { id: 'i1', kind: 'user', time: ago(14), text: 'Chart commits per day this month, and lines of code per file type.' },
        tool('call-chart-1', 13, { name: 'uam_chart', title: 'Chart: Commits per day, September', status: 'completed', input: JSON.stringify({ title: 'Commits per day, September', kind: 'line', x: 'day', y: ['commits'], format: 'csv', command: 'git log … | uniq -c' }), output: 'Charted "Commits per day, September" (line) for the owner from 30 rows.' }),
        tool('call-chart-2', 12, { name: 'uam_chart', title: 'Chart: Lines of code per file type', status: 'completed', input: JSON.stringify({ title: 'Lines of code per file type', kind: 'bar', x: 'type', y: ['lines'], format: 'csv', command: 'git ls-files | …' }), output: 'Charted "Lines of code per file type" (bar) for the owner from 6 rows.' }),
        tool('call-chart-3', 12, { name: 'uam_chart', title: 'Chart: Changes per week, by area', status: 'completed', input: JSON.stringify({ title: 'Changes per week, by area', kind: 'line', x: 'week', y: ['web', 'go', 'docs', 'tests'], format: 'csv' }), output: 'Charted "Changes per week, by area" (line) for the owner from 10 rows.' }),
        tool('call-chart-4', 12, { name: 'uam_chart', title: 'Chart: CI runs per week, with failures and retries', status: 'completed', input: JSON.stringify({ title: 'CI runs per week, with failures and retries', kind: 'bar', x: 'week', y: ['runs', 'failed', 'retried'], format: 'csv' }), output: 'Charted "CI runs per week, with failures and retries" (bar) for the owner from 8 rows.' }),
        { id: 'i4', kind: 'assistant', time: ago(11), text: 'September had **113 commits**, about 3.8 a day. The busiest days were the 20th (9) and the 13th (8); weekends are mostly empty.\n\nGo is most of the code at 41k lines across 212 files; the web UI adds 28k in TSX and TS.' },
      ],
    }),
    task({
      id: 't9',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'kimi-k3',
      name: 'Draft release notes',
      title: '',
      state: 'cancelled',
      stop_reason: 'credit_limit',
      execution: { known: true, mode: 'autopilot', objective: { id: 1, objective: 'Draft release notes for the last 12 commits.', status: 'paused', turn_count: 6, credits_used: 300, credit_limit: 300 } },
      created_at: ago(60 * 5 + 3),
      updated_at: ago(60 * 5),
      items: [
        { id: 'i1', kind: 'user', time: ago(60 * 5 + 3), text: 'Draft release notes for the last 12 commits.' },
        { id: 'i2', kind: 'assistant', time: ago(60 * 5 + 1), text: 'Reading the log…' },
        { id: 'i3', kind: 'notice', time: ago(60 * 5), text: 'Turn stopped.' },
      ],
    }),
    task({
      id: 't10',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'gpt-5-mini',
      name: 'Bump dependencies',
      title: '',
      state: 'closed',
      created_at: ago(60 * 31),
      updated_at: ago(60 * 30),
      items: [
        { id: 'i1', kind: 'user', time: ago(60 * 31), text: 'Bump the dev dependencies and run the build.' },
        { id: 'i2', kind: 'assistant', time: ago(60 * 30), text: 'Bumped 6 packages; build passes.' },
      ],
    }),
    task({
      id: 't11',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: '',
      title: 'Explain the attach status bar design',
      state: 'closed',
      stage: 'settled',
      settled_at: ago(60 * 47),
      settled_by: 'auto',
      created_at: ago(60 * 50),
      updated_at: ago(60 * 47),
      items: [
        { id: 'i1', kind: 'user', time: ago(60 * 50), text: 'Explain why the attach status bar sits on a reserved row and never on codex.' },
        {
          id: 'i2',
          kind: 'assistant',
          time: ago(60 * 48),
          text: 'The bar takes one reserved terminal row so it never overwrites provider output. Codex and omp draw their own bottom line in cooked mode, so the bar is disabled for them; alt-screen providers keep it.',
        },
      ],
    }),
    task({
      id: 't12',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'auto',
      last_model: 'mai-code-1.1-flash',
      name: 'Pin GitHub Actions to Node 24',
      title: '',
      state: 'closed',
      stage: 'archived',
      settled_at: ago(60 * 24 * 2),
      archived_at: ago(60 * 24),
      created_at: ago(60 * 24 * 3),
      updated_at: ago(60 * 24),
      items: [
        { id: 'i1', kind: 'user', time: ago(60 * 24 * 3), text: 'Move every workflow to the Node 24 action releases and keep the SHA pins.' },
        tool('i2', 60 * 24 * 3 - 5, { name: 'edit', title: 'Edit .github/workflows/ci.yml', status: 'completed', output: '@@ -30,2 +30,2 @@\n-        uses: actions/setup-node@v4\n+        uses: actions/setup-node@v5' }),
        { id: 'i3', kind: 'assistant', time: ago(60 * 24 * 3 - 20), text: 'Done. Seven workflows now pin the Node 24 releases; CI is green on the branch.' },
      ],
    }),
    task({
      id: 't13',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'gpt-5-mini',
      name: '',
      title: 'Migrate the RSS template to Atom',
      state: 'closed',
      stage: 'archived',
      archived_at: ago(60 * 24 * 5),
      created_at: ago(60 * 24 * 6),
      updated_at: ago(60 * 24 * 5),
      items: [
        { id: 'i1', kind: 'user', time: ago(60 * 24 * 6), text: 'Replace the RSS 2.0 feed template with Atom and keep the same URL.' },
        { id: 'i2', kind: 'assistant', time: ago(60 * 24 * 6 - 30), text: 'Switched `templates/feed.xml` to Atom 1.0. The URL is unchanged and the validator passes.' },
      ],
    }),
    // t14: long runs of thinking and tool calls between short messages, with a running tool,
    // a failed one, two answered questions and a permission still waiting; the activity rows fold these.
    task({
      id: 't14',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: '',
      title: 'Launch Sky Dodge and capture the page',
      state: 'awaiting_permission',
      pending: 1,
      created_at: ago(24),
      updated_at: ago(1),
      turn_timings: [
        { id: 'tt-1', user_item_id: 'i1', started_at: ago(24), ended_at: ago(15), state: 'completed', model: 'gpt-6-luna', input_tokens: 23400, output_tokens: 940, cache_read_tokens: 18000, calls: 2, nano_aiu: 29243500, premium_cost: 2, generation_ms: 18800 },
        { id: 'tt-2', user_item_id: 'u2', started_at: ago(14), ended_at: ago(13.5), state: 'completed', model: 'ollama/deepseek-v4.1-flash', input_tokens: 1600, output_tokens: 80, cache_read_tokens: 800, calls: 1, generation_ms: 2400 },
        { id: 'tt-3', user_item_id: 'u3', started_at: ago(12), state: 'working', input_tokens: 31200, output_tokens: 610, generation_ms: 9400 },
      ],
      items: (() => {
        let min = 24;
        const step = (m: number) => (min -= m);
        // Each item sits `gap` minutes after the one before it, so a thought lasts the next item's gap.
        const bash = (id: string, cmd: string, extra: Partial<ToolCall> = {}, gap = 0.2) => tool(id, step(gap), { name: 'bash', title: cmd, status: 'completed', input: JSON.stringify({ command: cmd }), output: 'ok', ...extra });
        const read = (id: string, path: string, gap = 0.05) => tool(id, step(gap), { name: 'view', title: `Read ${path}`, status: 'completed', input: JSON.stringify({ path }), output: '40 lines' });
        const think = (id: string, text: string, gap = 0.01) => ({ id, kind: 'reasoning' as const, time: ago(step(gap)), text });
        const say = (id: string, text: string, gap = 0.1) => ({ id, kind: 'assistant' as const, time: ago(step(gap)), text });
        const ask = (id: string, question: string, choices: string[], output: string, gap = 0.1) => tool(id, step(gap), { name: 'ask_user', title: 'Ask user', status: 'completed', input: JSON.stringify({ question, choices }), output });
        return [
          { id: 'i1', kind: 'user' as const, time: ago(min), text: 'Launch Sky Dodge in the browser and save a screenshot of the game.' },
          think('r1', 'Find the game, start a static server, open it headless and capture the page.'),
          bash('c1', 'ls ~/projects/sky-dodge'),
          ask('q-port', 'Which port should the local server use?', ['8000', '8080'], 'User selected: 8000'),
          bash('c2', 'python3 -m http.server 8000 --directory ~/projects/sky-dodge &', {}, 0.01),
          ask('q-shot', 'Capture the whole page or only the viewport?', ['Whole page', 'Viewport only'], 'User responded: Only the viewport, at 1280 by 800, so the game fills the frame'),
          tool('c3', step(0.1), { name: 'web_fetch', title: 'Fetch http://localhost:8000', status: 'completed', input: '{"url":"http://localhost:8000"}', output: '<!doctype html>…' }),
          think('r2', 'The page came back; try a headless capture.'),
          say('m1', 'The browser needs `--no-sandbox` in this environment, and the earlier local server is no longer accepting connections. I will restart the server and capture the page.'),
          bash('c4', 'pkill -f http.server; python3 -m http.server 8000 &'),
          bash('c5', 'chromium --headless --no-sandbox --screenshot=shot.png http://localhost:8000', { status: 'failed', output: 'chromium: cannot open display; ERR_CONNECTION_REFUSED' }),
          think('r3', 'Connection refused: the server was bound to another root. Check what it serves.'),
          bash('c6', 'curl -s http://localhost:8000 | head -20'),
          bash('c7', 'ss -ltnp | grep 8000'),
          read('c8', 'index.html'),
          read('c9', 'game.js'),
          // Two edits: the turn's "Changed 2 files" line; the first was allowed by yolo mode (its mark sits on the row).
          tool('c9b', step(0.05), { name: 'edit', title: 'Edit notes/sky-dodge.md', status: 'completed', input: '{"path":"notes/sky-dodge.md"}', output: '@@ -1,2 +1,4 @@\n # Sky Dodge\n+\n+The server root now serves Machine Monitor; open /sky-dodge/ directly.' }),
          tool('c9c', step(0.05), { name: 'write', title: 'Write notes/capture.sh', status: 'completed', input: '{"path":"notes/capture.sh","content":"chromium --headless --no-sandbox --screenshot=sky-dodge.png http://localhost:8000/sky-dodge/"}', output: 'Wrote 1 line' }),
          think('r4', 'The root is now a different app.'),
          think('r5', 'Open the Sky Dodge assets directly instead.'),
          think('r6', 'The screenshot path stays the same.'),
          say('m2', 'The server\'s root page is now a different app ("Machine Monitor"), so the first capture was not the game. I will open the existing Sky Dodge assets directly without changing the project\'s current `index.html`, then replace the screenshot with the right page.'),
          tool('c10', step(0.05), { name: 'web_fetch', title: 'Fetch http://localhost:8000/sky-dodge/', status: 'completed', input: '{"url":"http://localhost:8000/sky-dodge/"}', output: '<!doctype html>…' }),
          think('r7', 'The assets load from the subfolder; capture that URL.'),
          bash('c11', 'chromium --headless --no-sandbox --screenshot=sky-dodge.png http://localhost:8000/sky-dodge/', {}, 0.05),
          // The capture, read back: a tool image, which stands in the answer while its call folds.
          { ...read('c12', 'sky-dodge.png'), images: [{ id: 'img-sky-dodge', mime: 'image/png', size: 20480, name: 'sky-dodge.png' }] },
          // The captured page declaration folds into activity and can hint inline file references.
          tool('c12b', step(0.05), { name: 'uam_show_file', title: 'Show sky-dodge/index.html', status: 'completed', input: JSON.stringify({ path: `${p('p3')}/sky-dodge/index.html`, title: 'The captured page', type_hint: 'html' }), output: 'Declared the file for display.', declaration: { artifact_id: 'artifact-sky-dodge', path: `${p('p3')}/sky-dodge/index.html`, title: 'The captured page', type_hint: 'html' } }),
          say('m3', 'Launched Sky Dodge in the browser and saved a screenshot to `sky-dodge.png`.\n\nThe server\'s root page had changed to a different app, so I loaded Sky Dodge directly without altering the current `index.html`.'),
          { id: 'u2', kind: 'user' as const, time: ago(step(1)), text: 'can you share inline image' },
          think('r8', 'Provide the markdown inline image path for the user, `![Sky Dodge](sky-dodge.png)`; no tools needed.'),
          say('m4', 'Here it is: `![Sky Dodge screenshot](sky-dodge.png)`.', 0.4),
          { id: 'u3', kind: 'user' as const, time: ago(step(1.5)), text: 'I something stuck?' },
          think('r9', 'Nothing is stuck; the image may not render inline in this client.'),
          say('m5', 'No, nothing is stuck. The image may not have rendered inline in your client; try opening the screenshot. I will keep the game server up while you look.'),
          think('r10', 'Keep the server running in the background and make the screenshot readable.'),
          bash('c13', 'python3 -m http.server 8000 --directory ~/projects/sky-dodge', { status: 'running', output: undefined }),
          say('m6', 'The server is up. To make the screenshot readable from your home directory I need to run a command outside the project.'),
          bash('c14', 'chmod 0644 ~/sky-dodge.png', { status: 'pending', output: undefined }),
        ];
      })(),
      interactions: [
        { id: 'perm-c9b', kind: 'permission', title: 'Write file', detail: 'notes/sky-dodge.md', state: 'answered', resolution: 'allowed (yolo)', time: ago(20), tool_call_id: 'c9b' },
        {
          id: 'perm-c14',
          kind: 'permission',
          title: 'Run a shell command outside the project',
          detail: 'chmod 0644 ~/sky-dodge.png',
          state: 'pending',
          time: ago(1),
          tool_call_id: 'c14',
          options: [
            { id: 'once', label: 'Allow once' },
            { id: 'session', label: 'Allow for this task' },
            { id: 'deny', label: 'Deny', reject: true },
          ],
        },
      ],
    }),
    // t15: a compact Task with a long history. The mock service holds only its newest items; older
    // pages, and all of its subagent's, come "from Copilot's record" as archive pages (install.ts).
    task({
      id: 't15',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: 'Remove unused exports across packages',
      title: '',
      state: 'completed',
      representation: 'compact-v1',
      detail_stream: true,
      created_at: ago(1900),
      updated_at: ago(1),
      items: [
        ...longHistory('h', 600),
        tool('h-audit', 2, { name: 'task', title: 'Audit the remaining packages', status: 'completed', input: '{"description":"Audit the remaining packages"}', output: 'Checked the remaining 75 packages; nothing else is unused.' }),
        { id: 'h-done', kind: 'assistant', time: ago(1), text: 'Every package is checked: 200 unused exports removed, the build and tests pass.' },
        // Held shortened: "Show full message" reads the whole text from the item route.
        clipped({ id: 'h-paste', kind: 'user', time: ago(0.8), text: '' }, `List every export you removed. The build log, for reference:\n\n${Array.from({ length: 1500 }, (_, n) => `build: pkg${(n % 150) + 1} compiled in ${(n % 9) + 1}ms`).join('\n\n')}`, 600, wholeTexts),
        clipped({ id: 'h-list', kind: 'assistant', time: ago(0.5), text: '' }, removedExports(), 6000, wholeTexts),
      ],
      subagents: [a5],
      recordSubagents: oldAudits,
      agentItems: { a5: longHistory('g', 300, 'a5'), ...Object.fromEntries(oldAudits.map((s) => [s.id, longHistory(`o${s.id}`, 20, s.id)])) },
    }),
    // Questions with one question, answered from the composer: options, options with a recommended one, free text only.
    task({
      id: 't16',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: '',
      title: 'Retry the flaky redraw test',
      state: 'awaiting_answer',
      pending: 1,
      created_at: ago(30),
      updated_at: ago(3),
      items: [
        { id: 'i1', kind: 'user', time: ago(30), text: 'The redraw test fails once in about twenty runs on CI. Add a retry so the suite stays green while I look for the cause.' },
        { id: 'i2', kind: 'assistant', time: ago(4), text: 'The failure is a timing race in `TestRedrawReplaysFocusEvents`. A retry hides it; how far should it go?' },
      ],
      interactions: [
        {
          id: 'q16',
          kind: 'question',
          title: 'How should the retry be bounded?',
          state: 'pending',
          time: ago(3),
          questions: [{ text: 'Pick a bound for the retry, or describe one', choices: ['Retry once', 'Retry up to 3 times', 'Retry until the test deadline'], custom: true }],
        },
      ],
    }),
    task({
      id: 't17',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'gpt-5-mini',
      last_model: 'gpt-5-mini',
      name: '',
      title: 'Set up the dependency lockfile',
      state: 'awaiting_answer',
      pending: 1,
      created_at: ago(12),
      updated_at: ago(2),
      items: [
        { id: 'i1', kind: 'user', time: ago(12), text: 'Add a lockfile and a CI step that installs from it.' },
        { id: 'i2', kind: 'assistant', time: ago(3), text: 'There is no lockfile and no package manager is pinned in `package.json`. I will use the one you name.' },
      ],
      interactions: [
        {
          id: 'q17',
          kind: 'question',
          title: 'Which package manager does this project use?',
          state: 'pending',
          time: ago(2),
          questions: [{ text: 'Which package manager should the lockfile and CI use?', choices: ['pnpm (Recommended)', 'npm', 'yarn'], custom: true }],
        },
      ],
    }),
    task({
      id: 't18',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: '',
      title: 'Draft the release note for v0.12',
      state: 'awaiting_answer',
      pending: 1,
      created_at: ago(9),
      updated_at: ago(1),
      items: [
        { id: 'i1', kind: 'user', time: ago(9), text: 'Draft the release note for v0.12 from the merged pull requests.' },
        { id: 'i2', kind: 'assistant', time: ago(2), text: 'Twelve pull requests merged since v0.11. Before I write the note I need the headline.' },
      ],
      interactions: [
        {
          id: 'q18',
          kind: 'question',
          title: 'What should the release note lead with?',
          state: 'pending',
          time: ago(1),
          questions: [{ text: 'Name the headline change for v0.12 in a sentence', custom: true }],
        },
      ],
    }),
    // A long question with many options: the composer's extension scrolls inside its cap.
    task({
      id: 't19',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: '',
      title: 'Cross-compile the release binaries',
      state: 'awaiting_answer',
      pending: 1,
      created_at: ago(40),
      updated_at: ago(2),
      items: [
        { id: 'i1', kind: 'user', time: ago(40), text: 'Build release binaries for every platform we support and attach them to the GitHub release.' },
        { id: 'i2', kind: 'assistant', time: ago(3), text: 'The release workflow builds `linux/amd64` only. Before I widen the matrix I need the platform list, since each one adds a CI job.' },
      ],
      interactions: [
        {
          id: 'q19',
          kind: 'question',
          title: 'Which platforms should the release build?',
          state: 'pending',
          time: ago(2),
          questions: [
            {
              header: 'Release matrix',
              text: 'Pick every platform the release should ship. Each one is a CI job of about four minutes, and a `darwin` target needs the signing step, which is **not** set up yet.\n\nNotes on the choices:\n\n- `linux/*` builds are static and need no runtime\n- `windows/arm64` has no tester on the team\n- `freebsd` and `openbsd` are best effort',
              choices: ['linux/amd64', 'linux/arm64', 'linux/armv7', 'linux/riscv64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64', 'freebsd/amd64', 'freebsd/arm64', 'openbsd/amd64', 'netbsd/amd64'],
              multiple: true,
              custom: true,
            },
          ],
        },
      ],
    }),
    // A finished turn with evidence: a passing test run, a piped vet run and claims the runs and edits do not all back (the finish card).
    task({
      id: 't20',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'gpt-5-mini',
      last_model: 'gpt-5-mini',
      name: 'Replay focus events on re-attach',
      title: '',
      state: 'completed',
      created_at: ago(70),
      updated_at: ago(42),
      turn_timings: [{ id: 'tt-f1', user_item_id: 'f1', started_at: ago(70), ended_at: ago(42), state: 'completed', input_tokens: 48210, output_tokens: 1860, generation_ms: 41200 }],
      items: [
        { id: 'f1', kind: 'user', time: ago(70), text: 'Focus events stop after a re-attach. Fix it, add a regression test, and document the replay order in docs/terminal.md.' },
        { id: 'f2', kind: 'reasoning', time: ago(69), ended_at: ago(68), text: '`Redraw` replays the private modes but not `?1004`. Add the replay after them and a test that re-attaches.' },
        tool('fq', 65, { name: 'ask_user', title: 'Ask user', status: 'completed', input: JSON.stringify({ question: 'Where should the replay order be documented?', choices: ['docs/terminal.md', 'A comment in redraw.go'] }), output: 'User selected: docs/terminal.md' }),
        { ...tool('f3', 60, { name: 'edit', title: 'Edit internal/vterm/redraw.go', status: 'completed', path: `${p('p1')}/internal/vterm/redraw.go`, output: '+\tif err := v.replayFocusEvents(w); err != nil {' }), ended_at: ago(60) },
        { ...tool('f4', 55, { name: 'edit', title: 'Edit internal/vterm/redraw_test.go', status: 'completed', path: `${p('p1')}/internal/vterm/redraw_test.go`, output: '+func TestRedrawReplaysFocusEvents(t *testing.T) {' }), ended_at: ago(55) },
        {
          ...tool('f5', 50, { name: 'bash', status: 'completed', input: JSON.stringify({ command: 'go test ./internal/vterm/... -run Redraw' }), output: '=== RUN   TestRedrawReplaysModes\n--- PASS: TestRedrawReplaysModes (0.00s)\n=== RUN   TestRedrawReplaysFocusEvents\n--- PASS: TestRedrawReplaysFocusEvents (0.01s)\nok  \tgithub.com/example/uam/internal/vterm\t1.204s\n<shellId: 0 completed with exit code 0>' }),
          ended_at: new Date(Date.parse(ago(50)) + 1400).toISOString(),
        },
        { ...tool('f6', 46, { name: 'bash', status: 'completed', input: JSON.stringify({ command: 'go vet ./internal/vterm/... 2>&1 | tail -5' }), output: '\n<shellId: 1 completed with exit code 0>' }), ended_at: ago(46) },
        { id: 'f7', kind: 'assistant', time: ago(42), text: 'Fixed: `Redraw` now replays focus events after the private-mode replay, so a re-attached client gets them again.\n\n- `TestRedrawReplaysFocusEvents` covers it, and the vterm tests pass.\n- go vet is clean.\n- docs/terminal.md describes the new replay order.' },
      ],
    }),
  ];

  // A Task that called an MCP server's tracker_* tools: they show as ordinary tool rows.
  tasks.push(
    task({
      id: 't21',
      project_id: 'p1',
      workdir: p('p1'),
      model: 'claude-haiku-4.5',
      last_model: 'claude-haiku-4.5',
      name: 'Sign the release archives',
      title: '',
      state: 'completed',
      created_at: ago(25),
      updated_at: ago(8),
      items: [
        { id: 'w1', kind: 'user', time: ago(25), text: 'Work on #25: sign the release archives in the release job and upload the signatures.' },
        tool('w2', 24, { name: 'tracker_get', title: 'Read card #25', status: 'completed', input: '{"card":"#25"}', output: '#25 Sign archives and publish signatures (doing)' }),
        { id: 'w3', kind: 'assistant', time: ago(23), text: 'Signing belongs to the *Sign release binaries* story, which waits for the release matrix: the archives it signs come from there. I will sign whatever the job builds today and pick up the new targets when they land.' },
        tool('w4', 22, { name: 'tracker_get', title: 'Read card #18', status: 'completed', input: '{"card":"#18"}', output: '#18 Release automation (doing)' }),
        tool('w5', 18, { name: 'edit', title: 'Edit .github/workflows/release.yml', status: 'completed', input: '{"path":".github/workflows/release.yml"}', output: '+      - run: cosign sign-blob --yes dist/*.tar.gz' }),
        tool('w6', 12, { name: 'tracker_checklist', title: 'Tick "Sign in the release job"', status: 'completed', input: '{"card":"#25","item":0,"done":true}', output: 'ticked' }),
        tool('w8', 9, { name: 'tracker_get', title: 'Read card #26', status: 'completed', input: '{"card":"#26"}', output: '#26 Verify signatures in the install script (planned)' }),
        { id: 'w7', kind: 'assistant', time: ago(8), text: 'The release job signs every archive now. Next: upload the `.sig` files, then document the key. #26 (verifying signatures in the install script) waits on the checksums in #31.' },
      ],
    }),
  );

  // Tasks the service judged done (`done_at`): more than the sidebar's Done section shows before "Show all".
  const judged = (id: string, project_id: string, name: string, min: number, line: string) =>
    task({
      id, project_id, workdir: p(project_id), model: 'gpt-6-luna', name, title: '', state: 'completed', created_at: ago(min + 40), updated_at: ago(min + 2),
      done_at: ago(min), done_item_id: `${id}-a`, done_line: line,
      items: [
        { id: `${id}-u`, kind: 'user', time: ago(min + 40), text: `${name}.` },
        { id: `${id}-a`, kind: 'assistant', time: ago(min + 2), text: `${line}\n\nNothing else is left to do.` },
      ],
    });
  tasks.push(
    judged('d1', 'p1', 'Rename SessionSummary.stage values', 25, 'Renamed the stage values; the store migration and its tests pass.'),
    judged('d2', 'p3', 'Add an RSS link to the footer', 60 * 2, 'The footer links the feed on every page.'),
    judged('d3', 'p2', 'Move git aliases into their own file', 60 * 3, 'The aliases live in git/aliases and .gitconfig includes it.'),
    judged('d4', 'p1', 'Drop the vterm build tag from CI', 60 * 5, 'CI builds without the vterm tag and stays green.'),
    judged('d5', 'p3', 'Lazy-load cover images', 60 * 9, 'Cover images load lazily; the page weight on first paint halved.'),
    judged('d6', 'p1', 'Explain the ProjectPicker search ranking', 60 * 20, 'Name matches rank before folder-only matches, in the given order.'),
    judged('d7', 'p2', 'Fix the tmux status line clock', 60 * 28, 'The clock reads the local time zone again.'),
    judged('d8', 'p1', 'Sonar: mark the folder-picker findings', 60 * 48, 'All five findings are marked accepted with the trust model as the reason.'),
    judged('d9', 'p3', 'Fix broken links in the 2025 archive', 60 * 72, 'Fixed 14 links; the link checker reports none broken.'),
  );

  // t22: subagents at scale. Three replies spawned 12, 22 and 7 audits (one per package); the first
  // reply's first audit runs again in the third, and four run while the turn has ended.
  tasks.push(
    (() => {
      const items: Item[] = [];
      const subagents: Subagent[] = [];
      const agentItems: Record<string, Item[]> = {};
      const audit = (n: number, min: number, pkg: string, status: Subagent['status'], extra: Partial<Subagent> = {}) => {
        const id = `sa${n}`, call = `sc${n}`;
        items.push(tool(call, min, { name: 'task', title: `Audit package ${pkg}`, status: status === 'running' ? 'running' : status === 'failed' ? 'failed' : 'completed', input: JSON.stringify({ description: `Audit package ${pkg}` }), output: status === 'failed' ? extra.error : status === 'running' ? undefined : `${pkg}: ${n % 4} exports without callers.` }));
        subagents.push({ id, parent_tool_call_id: call, name: `Audit package ${pkg}`, description: `List the exported symbols of ${pkg} that nothing calls.`, status, started_at: ago(min), ...(status === 'running' ? {} : { ended_at: ago(min - 1 - (n % 3)) }), model: n % 2 ? 'gpt-5-mini' : 'claude-haiku-4.5', effort: 'low', tokens: 8_000 + ((n * 37_919) % 400_000), tool_calls: 3 + (n % 17), ...(status === 'completed' || status === 'idle' ? { result_summary: `${pkg}: ${n % 4} exports without callers.` } : {}), ...extra });
        agentItems[id] = [
          tool(`${id}-g`, min - 0.5, { name: 'grep', title: `Search "export" in ${pkg}`, status: 'completed', output: `${(n % 5) + 2} matches` }, id),
          { id: `${id}-m`, kind: 'assistant', time: ago(min - 1), agent_id: id, text: status === 'running' ? `Reading ${pkg} for callers…` : `${pkg}: ${n % 4} exports without callers.` },
        ];
      };
      const say = (id: string, min: number, text: string, kind: Item['kind'] = 'assistant') => items.push({ id, kind, time: ago(min), text });
      const internal = ['store', 'web', 'web/items', 'web/archive', 'adapter', 'adapter/copilot', 'vterm', 'config', 'ids', 'logging', 'routines', 'charts'];
      say('d1', 60, 'Audit every Go and TS package for exported symbols with no callers. One subagent per package.', 'user');
      say('d2', 59.5, 'Pilot on internal/ first: 12 packages, one audit each.');
      internal.forEach((pkg, i) => audit(i + 1, 59, `internal/${pkg}`, 'completed'));
      say('d3', 50, 'Pilot on internal/: 12 packages, 19 dead exports. Want me to run the rest?');
      say('d4', 40, 'Yes, run the rest.', 'user');
      for (let i = 0; i < 22; i++) {
        const n = 13 + i;
        if (i === 4 || i === 15) audit(n, 39, `cmd/tool${i}`, 'failed', { error: i === 4 ? 'go vet: undefined: pageCursor' : 'context deadline exceeded' });
        else audit(n, 39, `cmd/tool${i}`, i === 9 ? 'idle' : 'completed');
      }
      say('d5', 30, '20 packages audited, 2 failed on build errors. Listed them below.');
      say('d6', 10, 'Retry the failed ones and do web/ too.', 'user');
      say('d7', 9.5, 'Retrying the two failed audits and starting web/.');
      audit(35, 9, 'cmd/tool4', 'running', { preview: 'go vet ./cmd/tool4 · step 3' });
      audit(36, 9, 'cmd/tool15', 'failed', { error: 'context deadline exceeded again' });
      audit(37, 9, 'web/src/lib', 'running', { preview: 'Reading transcript.ts · step 4' });
      audit(38, 9, 'web/src/components', 'running', { preview: 'grep -rn "trimSubagents" · step 9' });
      audit(39, 9, 'web/src/mock', 'completed');
      audit(40, 9, 'web/src/hooks', 'completed');
      audit(41, 9, 'web/tests', 'cancelled');
      // The first pilot audit, asked again: it keeps its first call and runs now, its third run
      // (a follow-up from the person during the second reply, then the agent resuming it).
      subagents[0] = {
        ...subagents[0],
        status: 'running',
        started_at: ago(5),
        ended_at: undefined,
        result_summary: undefined,
        preview: 'Re-checking internal/store after the fix',
        runs: [
          { started_at: ago(59), ended_at: ago(57), status: 'completed', trigger: 'spawn' },
          { started_at: ago(35), ended_at: ago(33), status: 'completed', trigger: 'user' },
          { started_at: ago(5), status: 'running', trigger: 'agent' },
        ],
      };
      agentItems.sa1.push(
        { id: 'sa1-u2', kind: 'user', time: ago(35), agent_id: 'sa1', text: 'Also check the test helpers in internal/store.' },
        { id: 'sa1-m2', kind: 'assistant', time: ago(33), agent_id: 'sa1', text: 'internal/store test helpers: 1 export without callers.' },
        tool('sa1-g3', 4.5, { name: 'grep', title: 'Search "export" in internal/store', status: 'completed', output: '3 matches' }, 'sa1'),
      );
      return task({
        id: 't22',
        project_id: 'p1',
        workdir: p('p1'),
        model: 'claude-haiku-4.5',
        last_model: 'claude-haiku-4.5',
        name: 'Audit every package for dead exports',
        title: '',
        state: 'completed',
        subagents_running: 4,
        created_at: ago(60),
        updated_at: ago(5),
        items,
        subagents,
        agentItems,
      });
    })(),
  );

  const changes: Record<string, MockChange[]> = {
    p1: [
      { path: 'internal/vterm/redraw.go', status: 'M', additions: 3, deletions: 0, patch: EDIT_DIFF, by: ['t1', 't20'], turn: ['t20'] },
      {
        path: '.github/workflows/ci.yml',
        status: 'M',
        additions: 2,
        deletions: 1,
        by: ['t1'],
        turn: ['t1'],
        patch: `--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -14,4 +14,5 @@ jobs:
       - uses: actions/setup-go@v5
         with:
-          go-version: '1.26'
+          go-version: '1.26.6'
+      - run: go test -race ./internal/vterm/...
       - run: make test`,
      },
      {
        path: 'internal/vterm/redraw_test.go',
        status: 'M',
        additions: 12,
        deletions: 0,
        by: ['t1', 't20'],
        turn: ['t1', 't20'],
        patch: `--- a/internal/vterm/redraw_test.go
+++ b/internal/vterm/redraw_test.go
@@ -88,2 +88,14 @@ func TestRedrawReplaysPrivateModes(t *testing.T) {
 	}
 }
+
+func TestRedrawReplaysFocusEvents(t *testing.T) {
+	v := New(80, 24)
+	v.Write([]byte("\\x1b[?1004h"))
+	var out bytes.Buffer
+	if err := v.Redraw(&out); err != nil {
+		t.Fatal(err)
+	}
+	if !bytes.Contains(out.Bytes(), []byte("\\x1b[?1004h")) {
+		t.Fatalf("focus events not replayed: %q", out.String())
+	}
+}`,
      },
      {
        path: 'docs/terminal.md',
        status: 'A',
        additions: 3,
        deletions: 0,
        patch: `--- /dev/null
+++ b/docs/terminal.md
@@ -0,0 +1,3 @@
+# Terminal adaptation
+
+Redraw replays every reset the attach client sends on detach.`,
      },
    ],
    p2: [
      {
        path: '.zshrc',
        status: 'M',
        additions: 1,
        deletions: 3,
        patch: `--- a/.zshrc
+++ b/.zshrc
@@ -20,3 +20,1 @@
-alias gs='git status'
-alias gd='git diff'
-alias gl='git log --oneline'
+source ~/.config/zsh/aliases.zsh`,
      },
    ],
    p3: [
      { path: 'templates/post.html', status: 'M', additions: 1, deletions: 1, patch: TEMPLATE_DIFF, by: ['t8'] },
      {
        path: 'assets/theme.css',
        status: 'M',
        additions: 1,
        deletions: 1,
        by: ['t8'],
        turn: ['t8'],
        patch: `--- a/assets/theme.css
+++ b/assets/theme.css
@@ -3,3 +3,3 @@
 :root {
-  --muted: #999;
+  --muted: #6b6560;
 }`,
      },
    ],
  };

  const commands: Command[] = [
    { name: 'init', description: 'Create a copilot-instructions.md for this project', kind: 'command', input_hint: '' },
    { name: 'review', description: 'Review the uncommitted changes and report problems', kind: 'command', input_hint: '' },
    { name: 'autopilot', description: 'Keep working between turns until the task is done', kind: 'command', input_hint: '[on|off]', aliases: ['goal'], allow_during_turn: true },
    { name: 'allow-all', description: 'Allow every permission request without asking', kind: 'command', input_hint: '[on|off]', aliases: ['yolo'], allow_during_turn: true },
    { name: 'context', description: 'Show context window token usage', kind: 'command', input_hint: '' },
    { name: 'compact', description: 'Summarize conversation history to reduce context', kind: 'command', input_hint: '', disabled_reason: 'This native command has no supported web handler yet' },
    { name: 'commit', description: 'Write a conventional commit for the staged changes', kind: 'skill', input_hint: '[scope]' },
    { name: 'release-notes', description: 'Draft release notes from recent commits', kind: 'skill', input_hint: '<range>' },
    { name: 'diagnosing-bugs', description: 'Reproduce, isolate and fix a bug from its symptom', kind: 'skill', input_hint: '<symptom>' },
    { name: 'tdd', description: 'Build the next change test-first', kind: 'skill', input_hint: '' },
  ];

  // p2 (dotfiles) has no entry: it is not a Git tree, so `@` explains itself there.
  const files: Record<string, string[]> = {
    p1: [
      '.github/workflows/ci.yml',
      'Makefile',
      'go.mod',
      'cmd/uam/main.go',
      'cmd/doctor.go',
      'internal/vterm/redraw.go',
      'internal/vterm/redraw_test.go',
      'internal/vterm/modes.go',
      'internal/web/server.go',
      'docs/web.md',
      'docs/terminal.md',
      'docs/adr/0004-web-interface.md',
      'web/src/App.tsx',
      'web/src/components/Composer.tsx',
    ],
    p3: ['templates/post.html', 'templates/list.html', 'templates/feed.xml', 'assets/theme.css', 'content/posts/hello.md', 'scripts/contrast.mjs'],
  };

  const prev = (conversation_id: string, title: string, min: number, in_use = false): PreviousSession => ({ provider: 'copilot', conversation_id, title, created_at: ago(min + 20), updated_at: ago(min), in_use });
  const previous: Record<string, PreviousSession[]> = {
    p1: [
      prev('conv-t3', 'Doctor: add terminal line', 42),
      prev('cli-p1-1', 'Explain the vterm replay order', 60 * 3),
      prev('cli-p1-2', 'Why does golangci-lint ignore the build tag?', 60 * 9),
      prev('cli-p1-3', 'Rename the tmux package', 60 * 30, true),
      // `-broken`: the mock refuses to import it, so a failure in "Import all" can be seen.
      prev('cli-p1-broken', 'Sketch the web attach flow', 60 * 50),
      prev('cli-p1-5', 'Doctor: colour probe timing', 60 * 70),
      prev('cli-p1-6', 'Split the attach status bar', 60 * 90),
    ],
    p2: [prev('cli-p2-1', 'Trim the zsh prompt', 60 * 24 * 3)],
    p3: [prev('cli-p3-1', 'Feed validator errors', 60 * 24), prev('cli-p3-2', 'Dark cover images', 60 * 26)],
  };

  // Each Task's own changes, totalled as the service sends them (SessionSummary.diff).
  for (const t of tasks) {
    const mine = (changes[t.project_id] ?? []).filter((f) => f.by?.includes(t.id));
    if (mine.length) t.diff = { files: mine.length, additions: mine.reduce((n, f) => n + f.additions, 0), deletions: mine.reduce((n, f) => n + f.deletions, 0) };
  }

  return { meta, projects, previous, settings: {
      send_default: 'steer',
      // On here, unlike the service, so the Task header shows Terminal.
      terminal: true,
      // The setting names a model with effort and a long context, so the Settings section and a draft show them resolved.
      task_defaults: { provider: 'copilot', model: 'claude-haiku-4.5', effort: 'high', context_size: 'long_context', mode: 'safe' },
      custom_models: [{ name: 'openrouter', display_name: 'Qwen3 Coder', base_url: 'https://openrouter.ai/api/v1', model_id: 'qwen/qwen3-coder', api_key_env: 'UAM_BYOM_OPENROUTER', key_present: false }],
    },
    tasks, changes, commands, files, wholeTexts,
    // t3's turn, in all four stages: one row blocked, one cut short in progress, one left for later, a subagent's row done.
    turnTodos: {
      'tt-t3': {
        timing_id: 'tt-t3',
        ended_at: ago(42),
        counts: { done: 3, total: 6, blocked: 1, in_progress: 1, pending: 1, open: 2 },
        todos: [
          { id: 'win', title: 'Check the line on Windows Terminal', status: 'blocked', note: 'No Windows machine is reachable from this session.', changed_at: ago(43) },
          { id: 'mac', title: 'Check the line in macOS Terminal', status: 'in_progress', changed_at: ago(44) },
          { id: 'docs', title: 'Mention the line in docs/doctor.md', status: 'pending' },
          { id: 'test', title: 'Test the new doctor line', status: 'done', changed_at: ago(50) },
          { id: 'edit', title: 'Print the terminal and glyph set in uam doctor', status: 'done', changed_at: ago(55) },
          { id: 'probe', title: 'Find where the terminal probe is exposed', status: 'done', agent_id: 'a1', agent: 'explore', changed_at: ago(57) },
        ],
      },
    } };
}
