// Development-only seed for the in-browser mock service (see install.ts).
// Shapes are the wire shapes from src/api.ts. Paths and names are fictional.

import type { Command, Interaction, Item, Meta, PreviousSession, Project, SessionDetail, Settings, Subagent, ToolCall } from '../api';

export interface MockTask extends SessionDetail {
  /** Subagent transcripts keyed by agent id (served by the subagent route). */
  agentItems: Record<string, Item[]>;
}

export interface MockChange {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  patch: string;
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
}

const NOW = Date.now();
const ago = (min: number) => new Date(NOW - min * 60000).toISOString();

const CAPS = { cancel: true, permissions: true, questions: true, session_diff: false, history: true, context_size: true, import: true };
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

export function seed(): MockState {
  const meta: Meta = {
    version: 'dev-mock',
    recent_workdirs: ['/home/user/projects/unified-agent-manager', '/home/user/dotfiles', '/home/user/projects/notes-site', '/home/user/projects/scratch'],
    providers: [
      {
        name: 'copilot',
        display_name: 'GitHub Copilot',
        available: true,
        capabilities: CAPS,
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

  // p1 has defaults and a long branch, p2 none of either, p3 a default model the provider no longer offers.
  const projects: Project[] = [
    {
      id: 'p1',
      name: 'unified-agent-manager',
      dir: '/home/user/projects/unified-agent-manager',
      created_at: ago(60 * 24 * 9),
      badge: { text: 'UM', color: 'blue' },
      defaults: { provider: 'copilot', model: 'claude-haiku-4.5', effort: 'high', context_size: 'long_context', mode: 'safe' },
      branch: 'feat/web-project-defaults-and-sidebar-revamp',
    },
    { id: 'p2', name: 'dotfiles', dir: '/home/user/dotfiles', created_at: ago(60 * 24 * 4), badge: { text: 'DF', color: 'teal' } },
    {
      id: 'p3',
      name: 'notes-site',
      dir: '/home/user/projects/notes-site',
      created_at: ago(60 * 24 * 2),
      badge: { text: 'NS', color: 'pink' },
      defaults: { provider: 'copilot', model: 'gpt-5.5-nova', effort: 'high', context_size: 'long_context', mode: 'yolo' },
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
        tool('i7', 1, { name: 'bash', title: 'go test ./internal/vterm/...', status: 'running', input: 'go test ./internal/vterm/... -run Redraw -count=1' }),
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
      created_at: ago(60),
      updated_at: ago(42),
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
          text: 'Yes. With `TERM=dumb` the probe is skipped and the row reads `terminal  dumb · ASCII glyphs`. Covered by `TestDoctorDumbTerminal`.',
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
      items: [{ id: 'i1', kind: 'user', time: ago(60 * 26 + 5), text: 'Remove every mention of the tmux backend from docs/ now that the native backend shipped.' }],
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
      name: '',
      title: '',
      state: 'working',
      created_at: ago(0),
      updated_at: ago(0),
      items: [{ id: 'i1', kind: 'user', time: ago(0), text: 'Which of these aliases are never used? Check the zsh history file.\n\n```sh\nalias gs="git status"\nalias gl="git log --oneline"\nalias dcu="docker compose up"\nalias serve="python -m http.server"\n```' }],
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
      ],
      subagents: [a1, a2, a3],
      agentItems: {
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
      id: 't9',
      project_id: 'p3',
      workdir: p('p3'),
      model: 'kimi-k3',
      name: 'Draft release notes',
      title: '',
      state: 'cancelled',
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
      created_at: ago(60 * 50),
      updated_at: ago(60 * 48),
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
      created_at: ago(60 * 24 * 3),
      updated_at: ago(60 * 24 * 3 - 20),
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
      created_at: ago(60 * 24 * 6),
      updated_at: ago(60 * 24 * 6 - 30),
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
        { id: 'tt-1', user_item_id: 'i1', started_at: ago(24), ended_at: ago(15), state: 'completed' },
        { id: 'tt-2', user_item_id: 'u2', started_at: ago(14), ended_at: ago(13.5), state: 'completed' },
        { id: 'tt-3', user_item_id: 'u3', started_at: ago(12), state: 'working' },
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
          ask('q-shot', 'Capture the whole page or only the viewport?', ['Whole page', 'Viewport only'], 'User selected: Viewport only'),
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
          think('r4', 'The root is now a different app.'),
          think('r5', 'Open the Sky Dodge assets directly instead.'),
          think('r6', 'The screenshot path stays the same.'),
          say('m2', 'The server\'s root page is now a different app ("Machine Monitor"), so the first capture was not the game. I will open the existing Sky Dodge assets directly without changing the project\'s current `index.html`, then replace the screenshot with the right page.'),
          tool('c10', step(0.05), { name: 'web_fetch', title: 'Fetch http://localhost:8000/sky-dodge/', status: 'completed', input: '{"url":"http://localhost:8000/sky-dodge/"}', output: '<!doctype html>…' }),
          think('r7', 'The assets load from the subfolder; capture that URL.'),
          bash('c11', 'chromium --headless --no-sandbox --screenshot=sky-dodge.png http://localhost:8000/sky-dodge/', {}, 0.05),
          read('c12', 'sky-dodge.png'),
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
  ];

  const changes: Record<string, MockChange[]> = {
    p1: [
      { path: 'internal/vterm/redraw.go', status: 'M', additions: 3, deletions: 0, patch: EDIT_DIFF },
      {
        path: 'internal/vterm/redraw_test.go',
        status: 'M',
        additions: 12,
        deletions: 0,
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
      { path: 'templates/post.html', status: 'M', additions: 1, deletions: 1, patch: TEMPLATE_DIFF },
      {
        path: 'assets/theme.css',
        status: 'M',
        additions: 1,
        deletions: 1,
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

  return { meta, projects, previous, settings: {
      send_default: 'steer',
      custom_models: [{ name: 'openrouter', display_name: 'Qwen3 Coder', base_url: 'https://openrouter.ai/api/v1', model_id: 'qwen/qwen3-coder', api_key_env: 'UAM_BYOM_OPENROUTER', key_present: false }],
    },
    tasks, changes, commands, files };
}
