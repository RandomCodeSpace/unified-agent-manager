import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api, ApiError, type ConfigurationFile } from '../../src/api';
import { renderApp } from './render';

async function open(kind: string) {
  const rendered = renderApp('#settings');
  await rendered.user.click(await screen.findByRole('button', { name: kind, exact: true }));
  const card = within(await screen.findByRole('region', { name: kind, exact: true }));
  await card.findByRole('list', { name: /in this scope/ });
  return { ...rendered, card };
}

describe('native configuration', () => {
  test('native configuration guidance opens beside its reference link', async () => {
    const { user, card } = await open('Skills');
    expect(card.getByRole('link', { name: 'Native skills reference' }).getAttribute('href')).toContain('skill-frontmatter-fields');
    expect(document.querySelector('[data-popup="tooltip"]')).toBeNull();
    await user.click(card.getByRole('button', { name: 'About skill configuration' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).not.toBeNull());
    const tip = document.querySelector<HTMLElement>('[data-popup="tooltip"]')!;
    expect(tip.textContent).toContain('Advanced documents support the full native format');
    expect(tip.textContent).toContain('A running task keeps its current configuration.');
    expect(within(tip).queryByRole('link')).toBeNull();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
  });

  test.each([
    { kind: 'agents', tab: 'Agents', action: 'View agent reviewer' },
    { kind: 'skills', tab: 'Skills', action: 'View skill release-check' },
    { kind: 'hooks', tab: 'Hooks', action: 'View hook file audit' },
    { kind: 'instructions', tab: 'Instructions', action: 'View copilot-instructions.md' },
  ] as const)('reads imported $kind with Terminal off and keeps the exact source without mutations', async ({ kind, tab, action }) => {
    const original = api.configuration;
    let expected = '';
    let name = '';
    let path = '';
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      const file = kind === 'instructions' ? result.instruction_files![0] : result[kind][0];
      file.editable = false;
      file.read_only_reason = 'Bundled configuration is read only.';
      if (kind !== 'hooks') file.content += '\n## Review checklist\n\n- Check the requested change.\n';
      expected = file.content; name = file.name; path = file.path;
      return result;
    });
    const save = vi.spyOn(api, 'saveConfiguration');
    const remove = vi.spyOn(api, 'deleteConfiguration');
    try {
      const { user, card } = await open(tab);
      expect(card.getByText('Bundled configuration is read only.')).toBeTruthy();
      expect(card.queryByRole('button', { name: 'Edit', exact: true })).toBeNull();
      expect(card.queryByRole('button', { name: 'Remove', exact: true })).toBeNull();
      await user.click(screen.getByRole('button', { name: 'General', exact: true }));
      await user.click(screen.getByRole('switch', { name: 'Terminal' }));
      await waitFor(() => expect(screen.getByRole('switch', { name: 'Terminal' }).getAttribute('aria-checked')).toBe('false'));
      await user.click(screen.getByRole('button', { name: tab, exact: true }));
      const trigger = card.getByRole('button', { name: action });
      await user.click(trigger);
      const dialog = within(await screen.findByRole('dialog'));
      expect(dialog.getByText(path)).toBeTruthy();
      expect(dialog.getByText(/This shows the saved file/)).toBeTruthy();
      expect(dialog.queryByRole('textbox')).toBeNull();
      if (kind !== 'hooks') {
        expect(dialog.getByRole('heading', { name: 'Review checklist' })).toBeTruthy();
        if (kind === 'agents') expect(dialog.getByRole('region', { name: `${name} configuration` }).textContent).toContain('custom-field: keep-this');
        await user.click(dialog.getByRole('radio', { name: 'Source', exact: true }));
      }
      expect(dialog.getByRole('region', { name: `${name} source` }).textContent).toBe(expected);
      expect(save).not.toHaveBeenCalled();
      expect(remove).not.toHaveBeenCalled();
      await user.keyboard('{Escape}');
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      expect(document.activeElement).toBe(trigger);
    } finally { read.mockRestore(); save.mockRestore(); remove.mockRestore(); }
  });

  test('viewing a saved agent leaves an unfinished edit intact', async () => {
    const { user, card } = await open('Agents');
    await user.click(card.getByRole('button', { name: 'Edit', exact: true }));
    const editor = card.getByLabelText('Full native document') as HTMLTextAreaElement;
    await user.type(editor, '\nMy unsaved rule.');
    const before = editor.value;
    await user.click(card.getByRole('button', { name: 'View agent reviewer' }));
    const dialog = within(await screen.findByRole('dialog'));
    await user.click(dialog.getByRole('radio', { name: 'Source', exact: true }));
    expect(dialog.getByRole('region', { name: 'reviewer source' }).textContent).not.toContain('My unsaved rule.');
    await user.click(dialog.getByRole('button', { name: 'Close', exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(editor.value).toBe(before);
    expect((await api.configuration()).agents[0].content).not.toContain('My unsaved rule.');
  });

  test.each([false, true])('an empty document viewer reports whether the file could be read (error: %s)', async (failed) => {
    const original = api.configuration;
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      result.agents[0].content = '';
      if (failed) result.agents[0].error = 'Could not read this file.';
      return result;
    });
    try {
      const { user, card } = await open('Agents');
      await user.click(card.getByRole('button', { name: 'View agent reviewer' }));
      const dialog = within(await screen.findByRole('dialog'));
      expect(dialog.getByText(failed ? 'No document content is available.' : 'This file is empty.')).toBeTruthy();
      if (failed) expect(dialog.getByRole('alert').textContent).toBe('Could not read this file.');
      await user.click(dialog.getByRole('radio', { name: 'Source', exact: true }));
      expect(dialog.getByRole('region', { name: 'reviewer source' }).textContent).toBe('');
    } finally { read.mockRestore(); }
  });

  test('existing project and task deletion stay bodyless', async () => {
    const fetchBefore = globalThis.fetch;
    const requests: RequestInit[] = [];
    globalThis.fetch = vi.fn(async (_input, init) => { requests.push(init!); return new Response(null, { status: 204 }); });
    try {
      await api.deleteProject('example');
      await api.deleteSession('example');
      expect(requests).toHaveLength(2);
      for (const request of requests) { expect(request.body).toBeUndefined(); expect(request.headers).toBeUndefined(); }
    } finally { globalThis.fetch = fetchBefore; }
  });

  test('Terminal off disables skill creation while instructions remain editable', async () => {
    const { user } = renderApp('#settings');
    await user.click(await screen.findByRole('switch', { name: 'Terminal' }));
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Terminal' }).getAttribute('aria-checked')).toBe('false'));
    await user.click(screen.getByRole('button', { name: 'Skills', exact: true }));
    expect((await screen.findByRole('button', { name: 'Add skill', exact: true }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Instructions', exact: true }));
    expect((await screen.findByRole('button', { name: 'Add copilot-instructions.md', exact: true }) as HTMLButtonElement).disabled).toBe(false);
  });

  test('creates a guided agent and retains advanced fields when editing its native document', async () => {
    const { user, card } = await open('Agents');
    await user.click(card.getByRole('button', { name: 'Add agent' }));
    const form = within(card.getByRole('form', { name: 'Add agent' }));
    await user.type(form.getByLabelText('Name'), 'planner');
    await user.type(form.getByLabelText('Description'), 'Plan focused changes');
    await user.click(form.getByRole('combobox', { name: 'Model (optional)' }));
    await user.click(await screen.findByRole('option', { name: 'GPT-5 mini', exact: true }));
    expect(form.getByLabelText('Tools (optional, one per line)').closest('details')?.open).toBe(false);
    await user.click(form.getByText('Advanced settings'));
    await user.type(form.getByLabelText('Tools (optional, one per line)'), 'read\nsearch');
    await user.type(form.getByLabelText('Agent instructions'), 'Read the issue first.');
    await user.click(form.getByRole('button', { name: 'Add agent' }));
    await card.findByText('planner saved. New and reopened tasks use the updated file.');
    const created = (await api.configuration()).agents.find((entry) => entry.name === 'planner');
    expect(created?.content).toContain('tools: ["read","search"]');
    expect(created?.content).toContain('model: "gpt-5-mini"');
    const reviewer = card.getByText('reviewer').closest('li')!;
    await user.click(within(reviewer).getByRole('button', { name: 'Edit' }));
    const editor = card.getByLabelText('Full native document') as HTMLTextAreaElement;
    expect(editor.value).toContain('custom-field: keep-this');
    await user.type(editor, '\nCheck race conditions.');
    await user.click(card.getByRole('button', { name: 'Save agent' }));
    await card.findByText('reviewer saved. New and reopened tasks use the updated file.');
    expect((await api.configuration()).agents.find((entry) => entry.name === 'reviewer')?.content).toContain('custom-field: keep-this');
  });

  test('guided skills serialize invocation, argument hint and preapproved tools from Advanced settings', async () => {
    const { user, card } = await open('Skills');
    await user.click(card.getByRole('button', { name: 'Add skill' }));
    const form = within(card.getByRole('form', { name: 'Add skill' }));
    await user.type(form.getByLabelText('Name'), 'manual-check');
    await user.type(form.getByLabelText('Description'), 'A controlled review checklist.');
    await user.type(form.getByLabelText('Skill instructions'), 'Review the diff.');
    expect(form.getByRole('checkbox', { name: 'Allow automatic skill invocation' }).closest('details')?.open).toBe(false);
    await user.click(form.getByText('Advanced settings'));
    await user.click(form.getByLabelText('Argument hint (optional)'));
    await user.paste('[target]');
    await user.type(form.getByLabelText('Allowed tools (optional, one per line)'), 'read\nsearch');
    const automatic = form.getByRole('checkbox', { name: 'Allow automatic skill invocation' }) as HTMLInputElement;
    const manual = form.getByRole('checkbox', { name: 'Allow manual skill invocation' }) as HTMLInputElement;
    expect(automatic.checked).toBe(true);
    expect(manual.checked).toBe(true);
    await user.click(automatic);
    await user.click(manual);
    expect(form.getByText(/Both automatic and manual invocation are disabled/)).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'Add skill' }));
    await card.findByText('manual-check saved. New and reopened tasks use the updated file.');
    const skill = (await api.configuration()).skills.find((file) => file.name === 'manual-check');
    expect(skill?.content).toContain('disable-model-invocation: true');
    expect(skill?.content).toContain('user-invocable: false');
    expect(skill?.content).toContain('argument-hint: "[target]"');
    expect(skill?.content).toContain('allowed-tools: ["read","search"]');
  });

  test('agent priority models replace the single choice and use only shared reasoning efforts', async () => {
    const { user, card } = await open('Agents');
    await user.click(card.getByRole('button', { name: 'Add agent' }));
    const form = within(card.getByRole('form', { name: 'Add agent' }));
    await user.type(form.getByLabelText('Name'), 'fallback-reviewer');
    await user.type(form.getByLabelText('Description'), 'Review with a fallback model.');
    await user.type(form.getByLabelText('Agent instructions'), 'Read the diff.');
    await user.click(form.getByRole('combobox', { name: 'Model (optional)' }));
    await user.click(await screen.findByRole('option', { name: 'GPT-5 mini', exact: true }));
    await user.click(form.getByText('Advanced settings'));
    await user.click(form.getByRole('combobox', { name: 'Model priority list (optional)' }));
    await user.click(await screen.findByRole('option', { name: 'GPT-5.6 Luna', exact: true }));
    expect((form.getByRole('combobox', { name: 'Model (optional)' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(form.getByRole('combobox', { name: 'Reasoning effort' }));
    await user.click(await screen.findByRole('option', { name: 'xhigh', exact: true }));
    await user.click(form.getByRole('combobox', { name: 'Model priority list (optional)' }));
    await user.click(await screen.findByRole('option', { name: 'Claude Haiku 4.5', exact: true }));
    expect(form.getByRole('alert').textContent).toContain('not supported by every selected model');
    expect((form.getByRole('button', { name: 'Add agent' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(form.getByRole('combobox', { name: 'Reasoning effort' }));
    expect(screen.queryByRole('option', { name: 'xhigh', exact: true })).toBeNull();
    await user.click(await screen.findByRole('option', { name: 'low', exact: true }));
    expect(form.getByRole('combobox', { name: 'Reasoning effort' }).textContent).toBe('low');
    await user.click(form.getByRole('combobox', { name: 'Model policy' }));
    await user.click(await screen.findByRole('option', { name: 'Required', exact: true }));
    await user.click(form.getByRole('button', { name: 'Move model 2 earlier' }));
    expect(form.getByRole('combobox', { name: 'Reasoning effort' }).textContent).toBe('low');
    await user.click(form.getByRole('checkbox', { name: 'Include repository instructions when used as a subagent' }));
    await user.type(form.getByLabelText('Tools (optional, one per line)'), 'read');
    await user.click(form.getByRole('checkbox', { name: 'Disable all tools' }));
    expect((form.getByLabelText('Tools (optional, one per line)') as HTMLTextAreaElement).disabled).toBe(true);
    await user.click(form.getByRole('button', { name: 'Add agent' }));
    await card.findByText('fallback-reviewer saved. New and reopened tasks use the updated file.');
    const content = (await api.configuration()).agents.find((entry) => entry.name === 'fallback-reviewer')!.content;
    expect(content).toContain('model: ["claude-haiku-4.5","gpt-5.6-luna"]');
    expect(content).not.toContain('gpt-5-mini');
    expect(content).toContain('model-policy: "required"');
    expect(content).toContain('reasoning-effort: "low"');
    expect(content).toContain('include-custom-instructions: true');
    expect(content).toContain('tools: []');
  });

  test('inheriting the task model omits inactive agent model policy and effort', async () => {
    const { user, card } = await open('Agents');
    await user.click(card.getByRole('button', { name: 'Add agent' }));
    const form = within(card.getByRole('form', { name: 'Add agent' }));
    await user.type(form.getByLabelText('Name'), 'inherited-reviewer');
    await user.type(form.getByLabelText('Description'), 'Use task preferences.');
    await user.type(form.getByLabelText('Agent instructions'), 'Read the diff.');
    await user.click(form.getByText('Advanced settings'));
    expect((form.getByRole('combobox', { name: 'Model policy' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(form.getByRole('combobox', { name: 'Model (optional)' }));
    await user.click(await screen.findByRole('option', { name: 'GPT-5 mini', exact: true }));
    await user.click(form.getByRole('combobox', { name: 'Model policy' }));
    await user.click(await screen.findByRole('option', { name: 'Required', exact: true }));
    await user.click(form.getByRole('combobox', { name: 'Reasoning effort' }));
    await user.click(await screen.findByRole('option', { name: 'low', exact: true }));
    await user.click(form.getByRole('combobox', { name: 'Model (optional)' }));
    await user.click(await screen.findByRole('option', { name: 'Inherit task model', exact: true }));
    await user.click(form.getByRole('button', { name: 'Add agent' }));
    await card.findByText('inherited-reviewer saved. New and reopened tasks use the updated file.');
    const content = (await api.configuration()).agents.find((entry) => entry.name === 'inherited-reviewer')!.content;
    expect(content).not.toMatch(/model:|model-policy:|reasoning-effort:/);
  });

  test('saves root AGENTS.md separately from project Copilot and global instructions', async () => {
    const { user, card } = await open('Instructions');
    expect(card.queryByRole('button', { name: 'Add AGENTS.md' })).toBeNull();
    await user.click(card.getByRole('combobox', { name: 'Scope' }));
    await user.click(await screen.findByRole('option', { name: 'unified-agent-manager', exact: true }));
    await user.click(await card.findByRole('button', { name: 'Add copilot-instructions.md' }));
    await user.type(card.getByLabelText('copilot-instructions.md (Markdown)'), 'Keep changes focused.');
    expect((card.getByRole('combobox', { name: 'Scope' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(card.getByRole('button', { name: 'Save copilot-instructions.md' }));
    await card.findByText('copilot-instructions.md saved. New and reopened tasks use the updated file.');
    await user.click(await card.findByRole('button', { name: 'Add AGENTS.md' }));
    const form = within(card.getByRole('form', { name: 'Add AGENTS.md' }));
    expect(form.getByText('/projects/p1/AGENTS.md')).toBeTruthy();
    await user.type(form.getByLabelText('AGENTS.md (Markdown)'), 'Run the nearest focused tests.');
    await user.click(form.getByRole('button', { name: 'Save AGENTS.md' }));
    await card.findByText('AGENTS.md saved. New and reopened tasks use the updated file.');
    expect((await api.configuration()).instructions.content).toBe('');
    const project = await api.configuration('p1');
    expect(project.instructions.content).toBe('Keep changes focused.');
    expect(project.instruction_files?.find((file) => file.name === 'agents')?.content).toBe('Run the nearest focused tests.');
    expect(await card.findByRole('button', { name: 'Edit AGENTS.md' })).toBeTruthy();
  });

  test('a conflicting AGENTS.md reload keeps the selected file', async () => {
    const { user, card } = await open('Instructions');
    await user.click(card.getByRole('combobox', { name: 'Scope' }));
    await user.click(await screen.findByRole('option', { name: 'unified-agent-manager', exact: true }));
    await user.click(await card.findByRole('button', { name: 'Add AGENTS.md' }));
    const editor = card.getByLabelText('AGENTS.md (Markdown)') as HTMLTextAreaElement;
    await user.type(editor, 'My AGENTS draft.');
    await api.saveConfiguration('instructions', 'agents', { content: 'External AGENTS change.', revision: '' }, 'p1');
    await user.click(card.getByRole('button', { name: 'Save AGENTS.md' }));
    expect((await card.findByRole('alert')).textContent).toContain('changed since it was loaded');
    expect(editor.value).toBe('My AGENTS draft.');
    await user.click(card.getByRole('button', { name: 'Reload saved version' }));
    const dialog = within(await screen.findByRole('alertdialog', { name: 'Reload saved version?' }));
    await user.click(dialog.getByRole('button', { name: 'Discard draft and reload' }));
    await waitFor(() => expect(editor.value).toBe('External AGENTS change.'));
    expect(card.getByRole('form', { name: 'Edit AGENTS.md' })).toBeTruthy();
    expect((await api.configuration('p1')).instructions.content).toBe('');
  });

  test('an older service still shows and saves its single instruction file', async () => {
    const original = api.configuration;
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      delete result.instruction_files;
      return result;
    });
    try {
      const { user, card } = await open('Instructions');
      await user.click(card.getByRole('button', { name: 'Add copilot-instructions.md' }));
      await user.type(card.getByLabelText('copilot-instructions.md (Markdown)'), 'Global rules.');
      await user.click(card.getByRole('button', { name: 'Save copilot-instructions.md' }));
      await card.findByText('copilot-instructions.md saved. New and reopened tasks use the updated file.');
      expect((await original()).instructions.content).toBe('Global rules.');
    } finally { read.mockRestore(); }
  });

  test('creates command hooks and enforces the Terminal gate', async () => {
    const { user, card } = await open('Hooks');
    await user.click(card.getByRole('button', { name: 'Add hook file' }));
    const form = within(card.getByRole('form', { name: 'Add hook file' }));
    expect(form.getByLabelText('PowerShell command').closest('details')?.open).toBe(false);
    expect(form.getByLabelText('Timeout (seconds)').closest('details')?.open).toBe(false);
    await user.type(form.getByLabelText('Name'), 'check');
    await user.click(form.getByRole('combobox', { name: 'Event' }));
    await user.click(await screen.findByRole('option', { name: 'preToolUse', exact: true }));
    await user.type(form.getByLabelText('Bash command'), 'echo ready');
    await user.click(form.getByRole('button', { name: 'Add hook file' }));
    await card.findByText('check saved. New and reopened tasks use the updated file.');
    const hook = (await api.configuration()).hooks.find((entry) => entry.name === 'check');
    expect(JSON.parse(hook!.content).hooks.preToolUse).toEqual([{ type: 'command', bash: 'echo ready' }]);
    await user.click(screen.getByRole('button', { name: 'General', exact: true }));
    await user.click(screen.getByRole('switch', { name: 'Terminal' }));
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Terminal' }).getAttribute('aria-checked')).toBe('false'));
    await user.click(screen.getByRole('button', { name: 'Hooks', exact: true }));
    expect((card.getByRole('button', { name: 'Add hook file' }) as HTMLButtonElement).disabled).toBe(true);
  });

  test.each(['exec', 'http'] as const)('switching a hook to %s saves only that mode and keeps the other draft inputs', async (mode) => {
    const { user, card } = await open('Hooks');
    await user.click(card.getByRole('button', { name: 'Add hook file' }));
    const form = within(card.getByRole('form', { name: 'Add hook file' }));
    await user.type(form.getByLabelText('Name'), `notify-${mode}`);
    await user.click(form.getByRole('combobox', { name: 'Event' }));
    await user.click(await screen.findByRole('option', { name: 'notification', exact: true }));
    await user.type(form.getByLabelText('Bash command'), 'echo shell');
    await user.click(form.getByText('Advanced settings'));
    await user.type(form.getByLabelText('Fallback command'), 'echo fallback');
    await user.type(form.getByLabelText('Working folder (optional)'), '/repo');
    await user.click(form.getByLabelText('Environment variables (optional JSON)'));
    await user.paste('{"MODE":"check"}');
    await user.click(form.getByRole('combobox', { name: 'Command mode' }));
    await user.click(await screen.findByRole('option', { name: 'Direct executable', exact: true }));
    await user.type(form.getByLabelText('Executable'), 'node');
    await user.click(form.getByLabelText('Executable arguments (optional JSON)'));
    await user.paste('["--check", "file name"]');
    await user.click(form.getByRole('combobox', { name: 'Hook type' }));
    await user.click(await screen.findByRole('option', { name: 'HTTP request', exact: true }));
    await user.type(form.getByLabelText('Webhook URL'), 'https://example.com/hook');
    await user.click(form.getByLabelText('HTTP headers (optional JSON)'));
    await user.paste('{"X-Mode":"check"}');
    await user.type(form.getByLabelText('Header environment variables (one per line)'), 'HOOK_TOKEN');
    await user.type(form.getByLabelText('Event matcher (optional regex)'), '^permission');
    await user.type(form.getByLabelText('Timeout (seconds)'), '45');
    await user.click(form.getByRole('checkbox', { name: 'Disable hooks in this file' }));
    if (mode === 'exec') {
      await user.click(form.getByRole('combobox', { name: 'Hook type' }));
      await user.click(await screen.findByRole('option', { name: 'Command', exact: true }));
      expect((form.getByLabelText('Executable') as HTMLInputElement).value).toBe('node');
    }
    await user.click(form.getByRole('button', { name: 'Add hook file' }));
    await card.findByText(`notify-${mode} saved. New and reopened tasks use the updated file.`);
    const content = JSON.parse((await api.configuration()).hooks.find((entry) => entry.name === `notify-${mode}`)!.content);
    expect(content.disableAllHooks).toBe(true);
    expect(content.hooks.notification).toEqual([mode === 'exec'
      ? { type: 'command', exec: 'node', args: ['--check', 'file name'], cwd: '/repo', env: { MODE: 'check' }, matcher: '^permission', timeoutSec: 45 }
      : { type: 'http', url: 'https://example.com/hook', headers: { 'X-Mode': 'check' }, allowedEnvVars: ['HOOK_TOKEN'], matcher: '^permission', timeoutSec: 45 }]);
  });

  test('a malformed hook matcher keeps the draft and prevents saving', async () => {
    const save = vi.spyOn(api, 'saveConfiguration');
    try {
      const { user, card } = await open('Hooks');
      await user.click(card.getByRole('button', { name: 'Add hook file' }));
      const form = within(card.getByRole('form', { name: 'Add hook file' }));
      await user.type(form.getByLabelText('Name'), 'invalid-matcher');
      await user.type(form.getByLabelText('Bash command'), 'echo ready');
      await user.click(form.getByRole('combobox', { name: 'Event' }));
      await user.click(await screen.findByRole('option', { name: 'notification', exact: true }));
      await user.click(form.getByText('Advanced settings'));
      await user.click(form.getByLabelText('Event matcher (optional regex)'));
      await user.paste('[');
      await user.click(form.getByRole('button', { name: 'Add hook file' }));
      expect((await form.findByRole('alert')).textContent).toBe('Matcher must be a valid regular expression.');
      expect((form.getByLabelText('Bash command') as HTMLTextAreaElement).value).toBe('echo ready');
      expect(save).not.toHaveBeenCalled();
    } finally { save.mockRestore(); }
  });

  test('duplicate definitions report their exact source paths without changing either file', async () => {
    const original = api.configuration;
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      const existing = result.skills[0];
      result.skills.push({ ...existing, path: '/shared/skills/release-check/SKILL.md', content: '# Different checklist', editable: false });
      result.conflicts = [{ kind: 'skills', name: existing.name, paths: result.skills.map((file) => file.path) }];
      result.conflict_details = [{ kind: 'skills', path: '/global/skills', message: 'Could not read the global skill catalog.' }];
      result.conflict_warnings = ['Legacy untyped warning.'];
      return result;
    });
    const save = vi.spyOn(api, 'saveConfiguration');
    const remove = vi.spyOn(api, 'deleteConfiguration');
    try {
      const { user, card } = await open('Skills');
      const notice = card.getAllByRole('status').find((entry) => entry.textContent?.includes('Multiple skill folders'))!;
      expect(card.getByText(/Could not read the global skill catalog/).textContent).toContain('/global/skills');
      expect(card.queryByText(/Legacy untyped warning/)).toBeNull();
      expect(notice.textContent).toContain('Multiple skill folders share the directory name release-check');
      expect(within(notice).getByText('/home/demo/.copilot/skills/release-check/SKILL.md')).toBeTruthy();
      expect(within(notice).getByText('/shared/skills/release-check/SKILL.md')).toBeTruthy();
      const duplicateRows = within(notice).getAllByRole('listitem');
      expect((within(duplicateRows[0]).getByRole('button', { name: 'Disable', exact: true }) as HTMLButtonElement).disabled).toBe(false);
      expect((within(duplicateRows[0]).getByRole('button', { name: 'Remove', exact: true }) as HTMLButtonElement).disabled).toBe(false);
      expect((within(duplicateRows[1]).getByRole('button', { name: 'Disable', exact: true }) as HTMLButtonElement).disabled).toBe(true);
      expect((within(duplicateRows[1]).getByRole('button', { name: 'Remove', exact: true }) as HTMLButtonElement).disabled).toBe(true);
      const files = within(card.getByRole('list', { name: 'skills in this scope' })).getAllByRole('listitem');
      expect(files).toHaveLength(2);
      await user.click(within(duplicateRows[1]).getByRole('button', { name: 'View', exact: true }));
      expect(within(await screen.findByRole('dialog')).getByRole('heading', { name: 'Different checklist' })).toBeTruthy();
      expect(save).not.toHaveBeenCalled();
      expect(remove).not.toHaveBeenCalled();
    } finally { read.mockRestore(); save.mockRestore(); remove.mockRestore(); }
  });

  test.each([
    { kind: 'agents', tab: 'Agents' },
    { kind: 'skills', tab: 'Skills' },
    { kind: 'hooks', tab: 'Hooks' },
  ] as const)('$kind can be disabled, enabled and removed without changing saved content', async ({ kind, tab }) => {
    const toggle = vi.spyOn(api, 'setConfigurationDisabled');
    const remove = vi.spyOn(api, 'deleteConfiguration');
    try {
      const { user, card } = await open(tab);
      const original = (await api.configuration())[kind][0];
      await user.click(card.getByRole('button', { name: 'Disable', exact: true }));
      let dialog = within(await screen.findByRole('alertdialog', { name: `Disable ${original.name}?` }));
      expect(dialog.getByText(original.path)).toBeTruthy();
      expect(dialog.getByText(/Scope: Global/)).toBeTruthy();
      expect(toggle).not.toHaveBeenCalled();
      await user.click(dialog.getByRole('button', { name: 'Cancel', exact: true }));
      await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
      expect((await api.configuration())[kind][0]).toEqual(original);
      await user.click(card.getByRole('button', { name: 'Disable', exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `Disable ${original.name}?` }));
      await user.click(dialog.getByRole('button', { name: 'Disable', exact: true }));
      await card.findByRole('button', { name: 'Enable', exact: true });
      const disabled = (await api.configuration())[kind][0];
      expect(disabled).toEqual({ ...original, disabled: true, path: original.path + '.uam-disabled' });
      expect(toggle).toHaveBeenLastCalledWith(kind, original.name, { disabled: true, path: original.path, revision: original.revision }, '');
      expect(card.queryByRole('button', { name: 'Edit', exact: true })).toBeNull();
      await user.click(card.getByRole('button', { name: 'Enable', exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `Enable ${original.name}?` }));
      expect(dialog.getByText(disabled.path)).toBeTruthy();
      expect(toggle).toHaveBeenCalledTimes(1);
      await user.click(dialog.getByRole('button', { name: 'Cancel', exact: true }));
      await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
      expect(toggle).toHaveBeenCalledTimes(1);
      expect((await api.configuration())[kind][0]).toEqual(disabled);
      await user.click(card.getByRole('button', { name: 'Enable', exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `Enable ${original.name}?` }));
      await user.click(dialog.getByRole('button', { name: 'Enable', exact: true }));
      await card.findByRole('button', { name: 'Edit', exact: true });
      expect((await api.configuration())[kind][0]).toEqual({ ...original, disabled: false });
      expect(toggle).toHaveBeenLastCalledWith(kind, original.name, { disabled: false, path: disabled.path, revision: disabled.revision }, '');
      await user.click(card.getByRole('button', { name: 'Disable', exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `Disable ${original.name}?` }));
      await user.click(dialog.getByRole('button', { name: 'Disable', exact: true }));
      await card.findByRole('button', { name: 'Enable', exact: true });
      await user.click(card.getByRole('button', { name: 'Remove', exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `Remove ${original.name}?` }));
      expect(dialog.getByText(disabled.path)).toBeTruthy();
      expect(remove).not.toHaveBeenCalled();
      await user.click(dialog.getByRole('button', { name: 'Cancel', exact: true }));
      await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
      expect(remove).not.toHaveBeenCalled();
      expect((await api.configuration())[kind][0]).toEqual(disabled);
      await user.click(card.getByRole('button', { name: 'Remove', exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `Remove ${original.name}?` }));
      await user.click(dialog.getByRole('button', { name: 'Remove', exact: true }));
      await card.findByText(`No ${kind} in this scope. Add one to get started.`);
      expect(remove).toHaveBeenLastCalledWith(kind, original.name, original.revision, '', disabled.path);
      expect((await api.configuration())[kind]).toHaveLength(0);
    } finally { toggle.mockRestore(); remove.mockRestore(); }
  });

  test.each([
    { kind: 'agents', tab: 'Agents', action: 'Disable' },
    { kind: 'agents', tab: 'Agents', action: 'Remove' },
    { kind: 'skills', tab: 'Skills', action: 'Disable' },
    { kind: 'skills', tab: 'Skills', action: 'Remove' },
  ] as const)('$action a Global $kind duplicate from a project leaves the project file intact', async ({ kind, tab, action }) => {
    const { user, card } = await open(tab);
    const original = (await api.configuration())[kind][0];
    const project = await api.saveConfiguration(kind, original.name, { content: original.content + '\nProject-specific content.', revision: '' }, 'p1');
    const toggle = vi.spyOn(api, 'setConfigurationDisabled');
    const remove = vi.spyOn(api, 'deleteConfiguration');
    try {
      await user.click(card.getByRole('combobox', { name: 'Scope' }));
      await user.click(await screen.findByRole('option', { name: 'unified-agent-manager', exact: true }));
      const globalRow = await waitFor(() => {
        const row = card.getByText(original.path).closest('li')!;
        expect((within(row).getByRole('button', { name: action, exact: true }) as HTMLButtonElement).disabled).toBe(false);
        return within(row);
      });
      await user.click(globalRow.getByRole('button', { name: 'View', exact: true }));
      let dialog = within(await screen.findByRole('dialog'));
      expect(dialog.getByText(original.path)).toBeTruthy();
      await user.click(dialog.getByRole('button', { name: 'Close', exact: true }));
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      await user.click(globalRow.getByRole('button', { name: action, exact: true }));
      dialog = within(await screen.findByRole('alertdialog', { name: `${action} ${original.name}?` }));
      expect(dialog.getByText(original.path)).toBeTruthy();
      expect(dialog.getByText(/Scope: Global/)).toBeTruthy();
      expect(remove).not.toHaveBeenCalled(); expect(toggle).not.toHaveBeenCalled();
      await user.click(dialog.getByRole('button', { name: action, exact: true }));
      await waitFor(() => expect(card.queryByText(/Multiple (agent files|skill folders)/)).toBeNull());
      expect((await api.configuration('p1'))[kind]).toEqual([project]);
      if (action === 'Remove') {
        expect(remove).toHaveBeenCalledWith(kind, original.name, original.revision, '', original.path);
        expect((await api.configuration())[kind]).toHaveLength(0);
      } else {
        expect(toggle).toHaveBeenCalledWith(kind, original.name, { disabled: true, path: original.path, revision: original.revision }, '');
        expect((await api.configuration())[kind][0]).toEqual({ ...original, disabled: true, path: original.path + '.uam-disabled' });
      }
    } finally { toggle.mockRestore(); remove.mockRestore(); }
  });

  test('Global duplicate actions stay unavailable after a failed lookup and recover on retry', async () => {
    const { user, card } = await open('Skills');
    const original = (await api.configuration()).skills[0];
    await api.saveConfiguration('skills', original.name, { content: original.content + '\nProject copy.', revision: '' }, 'p1');
    const configuration = api.configuration;
    let fail = true;
    const read = vi.spyOn(api, 'configuration').mockImplementation((projectId) => !projectId && fail ? Promise.reject(new Error('Global catalog offline')) : configuration(projectId));
    const remove = vi.spyOn(api, 'deleteConfiguration');
    const toggle = vi.spyOn(api, 'setConfigurationDisabled');
    try {
      await user.click(card.getByRole('combobox', { name: 'Scope' }));
      await user.click(await screen.findByRole('option', { name: 'unified-agent-manager', exact: true }));
      await card.findByText(/Could not load Global definitions: Global catalog offline/);
      const row = () => within(card.getByText(original.path).closest('li')!);
      for (const name of ['View', 'Disable', 'Remove']) expect((row().getByRole('button', { name, exact: true }) as HTMLButtonElement).disabled).toBe(true);
      fail = false;
      await user.click(card.getByRole('button', { name: 'Retry Global definitions' }));
      await waitFor(() => expect((row().getByRole('button', { name: 'Remove', exact: true }) as HTMLButtonElement).disabled).toBe(false));
      expect(card.queryByText(/Could not load Global definitions/)).toBeNull();
      expect(remove).not.toHaveBeenCalled(); expect(toggle).not.toHaveBeenCalled();
    } finally { read.mockRestore(); remove.mockRestore(); toggle.mockRestore(); }
  });

  test('enabling a disabled skill reports a destination collision without replacing either definition', async () => {
    const { user, card } = await open('Skills');
    const original = (await api.configuration()).skills[0];
    await user.click(card.getByRole('button', { name: 'Disable', exact: true }));
    let dialog = within(await screen.findByRole('alertdialog', { name: `Disable ${original.name}?` }));
    await user.click(dialog.getByRole('button', { name: 'Disable', exact: true }));
    await card.findByRole('button', { name: 'Enable', exact: true });
    const replacement = await api.saveConfiguration('skills', original.name, { content: original.content + '\nReplacement content.', revision: '' });
    await user.click(card.getByRole('button', { name: 'Enable', exact: true }));
    dialog = within(await screen.findByRole('alertdialog', { name: `Enable ${original.name}?` }));
    await user.click(dialog.getByRole('button', { name: 'Enable', exact: true }));
    expect((await card.findByRole('alert')).textContent).toContain('The destination already exists');
    const files = (await api.configuration()).skills;
    expect(files).toEqual([{ ...original, disabled: true, path: original.path + '.uam-disabled' }, replacement]);
    expect(card.getByRole('button', { name: 'Enable', exact: true })).toBeTruthy();
  });

  test.each([false, true])('unreadable skills appear only as Skills notices with structured details: %s', async (structured) => {
    const original = api.configuration;
    const path = '/home/demo/.claude/skills/missing/SKILL.md';
    const message = 'The skill link target does not exist.';
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      result.skills.push({ name: 'missing', path, content: '', revision: '', editable: false, error: message });
      result.conflict_warnings = ['Some skills files could not be checked for duplicate definitions.'];
      if (structured) result.conflict_details = [{ kind: 'skills', path, message }];
      return result;
    });
    try {
      const { user, card } = await open('Skills');
      expect(within(card.getByRole('list', { name: 'skills in this scope' })).getAllByRole('listitem')).toHaveLength(1);
      expect(card.queryByRole('button', { name: 'View skill missing' })).toBeNull();
      const notice = card.getAllByRole('status').find((entry) => entry.textContent?.includes(message))!;
      expect(within(notice).getByText(path)).toBeTruthy();
      await user.click(screen.getByRole('button', { name: 'Agents', exact: true }));
      const agents = within(await screen.findByRole('region', { name: 'Agents', exact: true }));
      await agents.findByRole('list', { name: 'agents in this scope' });
      expect(agents.queryByText(path)).toBeNull();
      expect(agents.queryByText(/skill link target|Some skills files|Duplicate definition check is incomplete/)).toBeNull();
    } finally { read.mockRestore(); }
  });

  test('same-name skills retain exact file identity when reloading, saving and removing', async () => {
    const original = api.configuration;
    let shared: ConfigurationFile | undefined;
    let removed = false;
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      shared ??= { ...result.skills[0], path: '/home/demo/.agents/skills/release-check/SKILL.md', content: 'Shared checklist.', revision: 'shared-1' };
      if (!removed) result.skills.push({ ...shared });
      return result;
    });
    const save = vi.spyOn(api, 'saveConfiguration').mockRejectedValueOnce(new ApiError(409, 'The file changed since it was loaded.')).mockImplementation(async (_kind, _name, body) => {
      shared = { ...shared!, content: body.content, revision: 'shared-3' };
      return { ...shared };
    });
    const remove = vi.spyOn(api, 'deleteConfiguration').mockImplementation(async () => { removed = true; });
    try {
      const { user, card } = await open('Skills');
      const sharedRow = () => card.getByText(shared!.path).closest('li')!;
      await user.click(within(sharedRow()).getByRole('button', { name: 'Edit', exact: true }));
      const editor = card.getByLabelText('Full native document') as HTMLTextAreaElement;
      expect(editor.value).toBe('Shared checklist.');
      await user.type(editor, '\nDraft change.');
      await user.click(card.getByRole('button', { name: 'Save skill' }));
      await card.findByRole('alert');
      shared = { ...shared!, content: 'External shared change.', revision: 'shared-2' };
      await user.click(card.getByRole('button', { name: 'Reload saved version' }));
      await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard draft and reload' }));
      await waitFor(() => expect(editor.value).toBe('External shared change.'));
      await user.type(editor, '\nReviewed.');
      await user.click(card.getByRole('button', { name: 'Save skill' }));
      await card.findByText('release-check saved. New and reopened tasks use the updated file.');
      expect(save).toHaveBeenLastCalledWith('skills', 'release-check', { content: 'External shared change.\nReviewed.', revision: 'shared-2', path: shared!.path }, '');
      await user.click(within(sharedRow()).getByRole('button', { name: 'Remove', exact: true }));
      const dialog = within(await screen.findByRole('alertdialog'));
      expect(dialog.getByText(shared!.path)).toBeTruthy();
      expect(dialog.getByText(/removing it affects those aliases/)).toBeTruthy();
      await user.click(dialog.getByRole('button', { name: 'Remove', exact: true }));
      await card.findByText('release-check removed.');
      expect(remove).toHaveBeenCalledWith('skills', 'release-check', 'shared-3', '', shared!.path);
      expect((await original()).skills[0].content).toContain('Inspect the release checklist.');
    } finally { read.mockRestore(); save.mockRestore(); remove.mockRestore(); }
  });

  test('a removed definition cannot reload a different file with the same name', async () => {
    const original = api.configuration;
    let missing = false;
    const path = '/home/demo/.agents/skills/release-check/SKILL.md';
    const read = vi.spyOn(api, 'configuration').mockImplementation(async (projectId) => {
      const result = await original(projectId);
      if (!missing) result.skills.push({ ...result.skills[0], path, content: 'Selected shared checklist.', revision: 'shared-1' });
      return result;
    });
    const save = vi.spyOn(api, 'saveConfiguration').mockRejectedValue(new ApiError(409, 'The file changed since it was loaded.'));
    try {
      const { user, card } = await open('Skills');
      await user.click(within(card.getByText(path).closest('li')!).getByRole('button', { name: 'Edit', exact: true }));
      const editor = card.getByLabelText('Full native document') as HTMLTextAreaElement;
      await user.type(editor, '\nKeep my draft.');
      const draft = editor.value;
      await user.click(card.getByRole('button', { name: 'Save skill' }));
      await card.findByRole('alert');
      missing = true;
      await user.click(card.getByRole('button', { name: 'Reload saved version' }));
      await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard draft and reload' }));
      await waitFor(() => expect(card.getByRole('alert').textContent).toContain(`The file at ${path} is no longer available for editing.`));
      expect(editor.value).toBe(draft);
    } finally { read.mockRestore(); save.mockRestore(); }
  });

  test('a stale save preserves the draft and reload requires explicit discard confirmation', async () => {
    const { user, card } = await open('Agents');
    await user.click(card.getByRole('button', { name: 'Edit' }));
    const editor = card.getByLabelText('Full native document') as HTMLTextAreaElement;
    await user.type(editor, '\nMy draft.');
    const saved = (await api.configuration()).agents[0];
    await api.saveConfiguration('agents', saved.name, { content: saved.content + '\nExternal edit.', revision: saved.revision });
    await user.click(card.getByRole('button', { name: 'Save agent' }));
    expect((await card.findByRole('alert')).textContent).toContain('changed since it was loaded');
    expect(editor.value).toContain('My draft.');
    await user.click(card.getByRole('button', { name: 'Reload saved version' }));
    let dialog = within(await screen.findByRole('alertdialog', { name: 'Reload saved version?' }));
    expect(editor.value).toContain('My draft.');
    const draft = editor.value;
    await user.click(dialog.getByRole('button', { name: 'Cancel', exact: true }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(editor.value).toBe(draft);
    await user.click(card.getByRole('button', { name: 'Reload saved version' }));
    dialog = within(await screen.findByRole('alertdialog', { name: 'Reload saved version?' }));
    await user.click(dialog.getByRole('button', { name: 'Discard draft and reload' }));
    await waitFor(() => expect(editor.value).toContain('External edit.'));
    expect(editor.value).not.toContain('My draft.');
  });

  test('deletion confirms the target and sends its path and revision', async () => {
    const { user, card } = await open('Skills');
    const existing = (await api.configuration()).skills[0];
    await user.click(card.getByRole('button', { name: 'Remove' }));
    const dialog = within(await screen.findByRole('alertdialog', { name: 'Remove release-check?' }));
    expect(dialog.getByText(/keeping supporting files/)).toBeTruthy();
    expect(dialog.getByText(existing.path)).toBeTruthy();
    expect((await api.configuration()).skills).toHaveLength(1);
    const fetchBefore = window.fetch;
    const bodies: unknown[] = [];
    window.fetch = (input, init) => {
      if (init?.method === 'DELETE') bodies.push(JSON.parse(String(init.body)));
      return fetchBefore(input, init);
    };
    await user.click(dialog.getByRole('button', { name: 'Remove', exact: true }));
    await card.findByText('No skills in this scope. Add one to get started.');
    expect(bodies).toEqual([{ revision: existing.revision, path: existing.path }]);
  });

  test('npx skill listing and installation report failures without clearing input', async () => {
    const { user, card } = await open('Skills');
    await user.click(card.getByText('Install with npx skills'));
    const source = card.getByLabelText('Repository') as HTMLInputElement;
    await user.type(source, 'unavailable/repo');
    await user.click(card.getByRole('button', { name: 'List skills' }));
    expect((await card.findByRole('alert')).textContent).toContain('Could not read the repository');
    expect(source.value).toBe('unavailable/repo');
    await user.clear(source);
    await user.type(source, 'vercel-labs/agent-skills');
    await user.click(card.getByRole('button', { name: 'List skills' }));
    await card.findByText(/Available skills:/);
    await user.type(card.getByLabelText('Skill names to install, one per line'), 'web-design-guidelines');
    await user.click(card.getByRole('button', { name: 'Install skills', exact: true }));
    await card.findByText('web-design-guidelines', { exact: true });
    expect((await api.configuration()).skills.map((entry) => entry.name)).toContain('web-design-guidelines');
  });
});
