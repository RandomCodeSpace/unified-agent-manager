# Configure agents, skills, hooks, and instructions

Open **Settings** and choose the section for what you want to change.
Field and setting descriptions are available from the info icon beside their
labels. Hover, focus, or tap the icon to read the help. Errors and reasons an
action is unavailable stay visible.

General contains task defaults and shell access. Models contains model
visibility and Utility-model selection. Providers contains Copilot sign-in
and custom model endpoint setup. MCP servers and this browser's preferences have their own sections.

## Choose the scope

Agents, Skills, Hooks, and Instructions edit native Copilot configuration
on the machine running UAM. Choose **Global** for the service user's
configuration or select a registered project for that project's files.
Global changes can also affect Copilot sessions started outside UAM under
the same user account.

Copilot discovers these files when a task's conversation opens or reopens.
Saving a file does not restart a running task. Agents, skills, hooks, and
Copilot instructions are kept in the project's `.github` directory;
`AGENTS.md` belongs at the project root. Global configuration is kept
under `COPILOT_HOME`, or `~/.copilot` when that variable is unset.

## Add and edit configuration

- **Agents:** create a named custom agent with a description, instructions,
  and optional model and tool selection. The saved file is an `.agent.md`
  document with YAML frontmatter. Agent definitions can configure command
  execution, so editing them also requires Terminal to be enabled.
  The guided model picker offers currently available, visible models or
  **Inherit task model**. Hidden models, unavailable providers, custom models with missing credentials,
  and the automatic-routing model are excluded.
- **Skills:** create a skill with a name, description, and instructions.
  Its `SKILL.md` lives in a directory that can also contain scripts and
  reference material. The invocation controls separately allow automatic
  use (`disable-model-invocation`) and manual use (`user-invocable`). Editing requires Terminal because skill metadata
  can grant tool permissions.
- **Hooks:** configure actions for Copilot lifecycle events. Command hooks
  run on the service host. Turn on **General → Shell access → Terminal**
  before changing executable configuration.
- **Instructions:** edit `AGENTS.md` at the project root or the project’s
  `.github/copilot-instructions.md`. Global edits
  `$COPILOT_HOME/copilot-instructions.md` (default `~/.copilot`), which applies
  across projects. Copilot does not automatically load a global `AGENTS.md`;
  that requires its directory in `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`. UAM
  does not change that environment setting. See the
  [Copilot instruction locations](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions).

Choose **View** beside an agent, skill, hook, or instruction file to inspect
it without editing. For Markdown files, **Preview** shows configuration fields
and formatted instructions; **Source** shows the exact native document. Hook
files show their JSON source. Viewing works for read-only
sources and with Terminal disabled. Opening the viewer keeps an unsaved
editor draft intact.

Common fields stay in the main form. Open **Advanced settings** for
invocation controls, tool permissions, model fallback and reasoning settings,
MCP configuration, skill argument hints and metadata, and optional hook
execution settings. Hook setup includes shell commands, direct executables
with argument arrays, and HTTP requests. Inactive mode fields are not saved;
model effort options must work with every selected fallback model.

The full-file editor preserves native fields that the guided forms do not
cover. Supported fields and events depend on the installed Copilot CLI.
See GitHub's [custom agent configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration)
and [hook reference](https://docs.github.com/en/copilot/reference/hooks-reference).
For CLI-specific agent and skill fields, see the
[CLI command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference).
The live Copilot 1.0.89 parser accepts agent fallback models as a `model`
array, with `model-policy`, `reasoning-effort`, and
`include-custom-instructions`. Fields intended for other environments are
labelled accordingly. Prompt hooks are documented for new interactive CLI
sessions and are not offered as a guided UAM hook type.

Skills in the selected scope's native, `.agents/skills`, and `.claude/skills`
directories can be viewed, edited, and removed when their resolved files are
writable. Symlink aliases of the same `SKILL.md` appear once; actions apply
to the resolved file shown in the list. Editing or removing that shared file
affects tools that load it through another alias. UAM's built-in skills and
files outside those configuration roots remain read-only, with a reason shown.
Missing or unreadable skills appear as notices with their paths, rather than
usable skill rows. Skill notices appear only in Skills, not in Agents.
Changes to agents, skills, and hooks require Terminal. Check the displayed path
before editing or confirming removal. Changes to
files edited elsewhere are detected at save time. If a save conflicts,
copy the draft text you want to keep before choosing **Reload saved version**.
Confirm discarding the draft, then reconcile and reapply your changes to
the current file.
Distinct agent or skill files with the same filename/directory identity are
listed as duplicate definitions, with **View**, **Disable**, and **Remove**
beside each path. Project scope also checks global definitions; confirmations
identify whether the selected file belongs to the project or Global scope.
Read-only definitions show the reason they cannot be changed. Aliases of the same resolved file are deduplicated
and are not reported as conflicts. A warning appears if a source cannot be
checked. Hook files and instruction files can combine across scopes; their
contents are not automatically merged or rewritten by this editor.

**Disable** preserves an agent, skill, or hook file by appending `.uam-disabled`
to its filename, which excludes it from Copilot discovery. Disabled files stay
visible in Settings with **View**, **Enable**, and **Remove**. Enable restores
the original filename and refuses to overwrite an existing file. Disabling a
resolved skill also affects its symlink aliases. Contents, file permissions,
and supporting files are preserved. Running tasks keep their loaded configuration;
new and reopened tasks use the change. Disabled files do not participate in
duplicate checks or installed-skill suggestions.

Removing a skill removes its definition file, including a disabled definition. Supporting files stay
on disk, and an installation into a directory that still contains them is
refused. Empty skill directories are removed.

## Draft configuration with AI

In Agents, Skills, or Hooks, describe the configuration you want and generate
a draft with AI. UAM uses the Utility model configured in Models and counts
the call against the Background AI daily limit. Utility calls choose the
lowest reasoning effort the selected model supports; models without a known
effort setting use their default. Generation requires Terminal
to be enabled, just like editing these definitions.

The result fills the creation form for review. Change the name, instructions,
model, or command settings as needed, then choose Add to save. Generating a
draft does not write configuration files or execute the proposed commands.
The same Utility call compares the request with installed skills and can
suggest up to three relevant skills with a reason. Choose View on a suggestion
to inspect the existing skill while keeping your draft. UAM supplies a bounded
catalog of skill names and short document excerpts from global and selected
project skills. The AI call has no tools; it receives those excerpts, your
description, and the supported draft options. Suggested files are checked
against that catalog before being returned.

If generation fails, the description remains available for correction and
retry. If Utility AI is disabled or its daily limit has been reached, choose
a Utility model or adjust the limit before trying again.

## Install skills with npx

In Skills, choose the destination scope and enter a skills repository.
List its skills, then enter the exact skill names to install, one per line. UAM uses
the published `skills` CLI's `add` command; there is no `skills setup`
command. See the [skills CLI documentation](https://github.com/vercel-labs/skills).

Installation requires Terminal to be enabled and Node.js 22.20 or newer,
`npx`, and Git on the service's `PATH`. UAM invokes `skills@1.7.0`. The installer
runs in a temporary directory before
publishing skill files into the selected scope. Existing destination
directories are refused rather than replaced. The UI reports command
failures and leaves the source and selection available for correction.

Only install skills and hooks whose code you trust. Their scripts run
with the service user's access when invoked.

## Configuration API

These endpoints use UAM's existing authenticated session, same-origin
checks, and JSON error responses. Responses are not cached.

| Method and path | Behavior |
| --- | --- |
| `GET /api/configuration` | Lists agents, skills, hooks, and instruction files with their paths, contents, editability, and revisions. |
| `PUT /api/configuration/{kind}/{name}` | Saves `{content, revision, path?}`, or toggles an existing agent, skill, or hook with `{disabled, revision, path}`. An empty revision creates a managed file; an existing file requires its current revision. `path` selects an exact discovered file. |
| `DELETE /api/configuration/{kind}/{name}` | Removes the definition matching `{revision, path?}`. A supplied path must identify an editable file in the selected scope. |
| `POST /api/configuration/{kind}/draft` | Generates an editable draft from `{brief}` for `agents`, `skills`, or `hooks`, without saving it. |
| `POST /api/configuration/skills/list` | Lists a repository's skills using the JSON body's `{source}`. |
| `POST /api/configuration/skills/install` | Installs explicit names from `{source, skills: [...]}`. |
| `POST /api/configuration/skills/global-disabled` | Sets Copilot's global disabled-skills entry for one discovered name from `{name, disabled}`. Applies to every skill with that name in every project and changes no file. Requires Terminal on. |

Omit `project_id` for global configuration or supply a registered project
ID as a query parameter. Kinds are `agents`, `skills`, `hooks`, and
`instructions`. Instruction resources are `copilot-instructions` in either
scope and `agents` for project-root `AGENTS.md`. `instruction_files` lists
the available instruction documents; the original `instructions` field
continues to return Copilot instructions for compatibility. Names identify
native files. Optional mutation paths must match an existing editable catalog entry; they cannot create files or target arbitrary filesystem paths.

Edits are limited to 256 KiB per file. Stale revisions and existing
installation targets return `409`; disabled Terminal access returns `403`.
Installation does not replace files on retry. Skill commands time out
after two minutes and their output is bounded to 128 KiB per stream.
