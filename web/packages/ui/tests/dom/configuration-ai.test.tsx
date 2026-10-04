import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import { api, type ConfigurationKind } from '../../src/api';
import { ConfigurationSettings } from '../../src/components/ConfigurationSettings';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { seed } from '../../src/mock/data';
import { install } from '../../src/mock/install';

async function creator(kind: ConfigurationKind = 'agents', change?: (context: AppContextValue) => void) {
  install();
  const fixture = seed();
  const context: AppContextValue = { meta: fixture.meta, metaError: null, loaded: true, settings: fixture.settings, dispatch: () => {}, narrow: false, hasNews: () => false, usage: null, refreshMeta: vi.fn() };
  change?.(context);
  const tree = () => <AppContext.Provider value={{ ...context }}><ConfigurationSettings kind={kind} projects={fixture.projects} terminal={!!context.settings.terminal} /></AppContext.Provider>;
  const view = render(tree());
  await screen.findByRole('list', { name: `${kind} in this scope` });
  return { context, user: userEvent.setup(), update: () => view.rerender(tree()) };
}

describe('AI configuration creator', () => {
  test.each([
    { kind: 'agents', label: 'agent', name: 'focused-reviewer', field: 'Agent instructions' },
    { kind: 'skills', label: 'skill', name: 'review-checklist', field: 'Skill instructions' },
    { kind: 'hooks', label: 'hook file', name: 'tool-audit', field: 'Bash command' },
  ] as const)('reviews an editable $kind draft and saves only on Add', async ({ kind, label, name, field }) => {
    const { user } = await creator(kind);
    const original = window.fetch;
    const draftCalls: string[] = [];
    window.fetch = (input, init) => { if (String(input).endsWith('/draft')) draftCalls.push(String(input)); return original(input, init); };
    const before = (await api.configuration())[kind].length;
    await user.click(screen.getByText('Create with AI'));
    await user.type(screen.getByLabelText(`Describe your ${label}`), 'Help review a focused change.');
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    const form = within(await screen.findByRole('form', { name: `Add ${label}` }));
    expect(screen.getByText(/Nothing has been saved or run/)).toBeTruthy();
    expect((await api.configuration())[kind]).toHaveLength(before);
    expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe(name);
    expect(form.queryByLabelText('Full native document')).toBeNull();
    expect(screen.queryByText('Create with AI')).toBeNull();
    await user.type(form.getByLabelText(field), '\n# Reviewed by the user');
    const suggested = within(screen.getByRole('region', { name: 'Similar installed skills' }));
    expect(suggested.getByText('This installed checklist covers reviewing a change before release.')).toBeTruthy();
    await user.click(suggested.getByRole('button', { name: 'View existing skill release-check' }));
    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByText('Inspect the release checklist.')).toBeTruthy();
    await user.click(dialog.getByRole('radio', { name: 'Source', exact: true }));
    expect(dialog.getByRole('region', { name: 'release-check source' }).textContent).toContain('name: release-check');
    await user.click(dialog.getByRole('button', { name: 'Close', exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect((form.getByLabelText(field) as HTMLTextAreaElement).value).toContain('Reviewed by the user');
    expect(draftCalls).toHaveLength(1);
    expect((await api.configuration())[kind]).toHaveLength(before);
    await user.click(form.getByRole('button', { name: `Add ${label}` }));
    await screen.findByText(`${name} saved. New and reopened tasks use the updated file.`);
    expect(screen.queryByRole('region', { name: 'Similar installed skills' })).toBeNull();
    const saved = (await api.configuration())[kind].find((file) => file.name === name)!;
    expect(saved.content).toContain('Reviewed by the user');
    if (kind === 'agents') expect(saved.content).toContain('model: "gpt-5-mini"');
    if (kind === 'hooks') expect(JSON.parse(saved.content).hooks.postToolUse[0]).toMatchObject({ env: { LOG_LEVEL: 'info' }, timeoutSec: 10 });
  });

  test('generation locks conflicting controls, keeps a failed brief, and retries without saving', async () => {
    const { user } = await creator('skills');
    await user.click(screen.getByText('Create with AI'));
    await user.click(screen.getByText('Install with npx skills'));
    const brief = screen.getByLabelText('Describe your skill') as HTMLTextAreaElement;
    await user.type(brief, 'Review my changes.');
    const original = window.fetch;
    let finish!: (response: Response) => void;
    window.fetch = vi.fn((input, init) => String(input).includes('/skills/draft') ? new Promise<Response>((resolve) => { finish = resolve; }) : original(input, init));
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    expect(screen.getByRole('status').textContent).toContain('Nothing has been saved');
    for (const name of ['Generate draft', 'Add skill', 'Edit', 'Remove', 'List skills', 'Install skills']) expect((screen.getByRole('button', { name, exact: true }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole('combobox', { name: 'Scope' }) as HTMLButtonElement).disabled).toBe(true);
    expect(brief.disabled).toBe(true);
    await act(async () => finish(Response.json({ error: 'Provider timed out. Try again.' }, { status: 502 })));
    expect((await screen.findByRole('alert')).textContent).toContain('Provider timed out');
    expect(brief.value).toBe('Review my changes.');
    expect(brief.disabled).toBe(false);
    window.fetch = original;
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    await screen.findByRole('form', { name: 'Add skill' });
    expect((await api.configuration()).skills).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('region', { name: 'Similar installed skills' })).toBeNull();
    await user.click(screen.getByText('Create with AI'));
    expect((screen.getByLabelText('Describe your skill') as HTMLTextAreaElement).value).toBe('Review my changes.');
  });

  test('uses the selected project in the draft request', async () => {
    const { user } = await creator('hooks');
    await user.click(screen.getByRole('combobox', { name: 'Scope' }));
    await user.click(await screen.findByRole('option', { name: 'unified-agent-manager', exact: true }));
    await screen.findByText('No hooks in this scope. Add one to get started.');
    await user.click(screen.getByText('Create with AI'));
    await user.type(screen.getByLabelText('Describe your hook file'), 'Log completed tools.');
    const original = window.fetch;
    const requests: string[] = [];
    window.fetch = (input, init) => { if (String(input).includes('/hooks/draft')) requests.push(String(input)); return original(input, init); };
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    await screen.findByRole('form', { name: 'Add hook file' });
    expect(requests).toEqual(['/api/configuration/hooks/draft?project_id=p1']);
    expect((await api.configuration('p1')).hooks).toHaveLength(0);
    const scopeReads: string[] = [];
    window.fetch = (input, init) => { if (String(input).startsWith('/api/configuration') && init?.method === 'GET') scopeReads.push(String(input)); return original(input, init); };
    await user.click(screen.getByRole('button', { name: 'View existing skill release-check' }));
    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByText('Inspect the release checklist.')).toBeTruthy();
    expect(scopeReads).toEqual(['/api/configuration']);
  });

  test('a missing suggested path reports the error without replacing the draft and can retry', async () => {
    const { user } = await creator('agents');
    const original = window.fetch;
    const missingPath = '/shared/release-check/SKILL.md';
    window.fetch = async (input, init) => {
      const response = await original(input, init);
      if (!String(input).endsWith('/agents/draft')) return response;
      const result = await response.json();
      result.similar_skills[0].path = missingPath;
      return Response.json(result);
    };
    await user.click(screen.getByText('Create with AI'));
    await user.type(screen.getByLabelText('Describe your agent'), 'Review my release.');
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    const form = within(await screen.findByRole('form', { name: 'Add agent' }));
    const prompt = form.getByLabelText('Agent instructions') as HTMLTextAreaElement;
    await user.type(prompt, '\nKeep this edit.');
    const before = prompt.value;
    await user.click(screen.getByRole('button', { name: 'View existing skill release-check' }));
    expect((await screen.findByRole('alert')).textContent).toContain('no longer available at the suggested path');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(prompt.value).toBe(before);
    window.fetch = async (input, init) => {
      const response = await original(input, init);
      if (String(input) !== '/api/configuration') return response;
      const result = await response.json();
      result.skills[0].path = missingPath;
      return Response.json(result);
    };
    await user.click(screen.getByRole('button', { name: 'View existing skill release-check' }));
    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByText(missingPath)).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Close', exact: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(prompt.value).toBe(before);
    expect((await api.configuration()).agents).toHaveLength(1);
  });

  test('a late suggested skill load cannot reopen a canceled draft', async () => {
    const { user } = await creator('hooks');
    await user.click(screen.getByRole('combobox', { name: 'Scope' }));
    await user.click(await screen.findByRole('option', { name: 'unified-agent-manager', exact: true }));
    await screen.findByText('No hooks in this scope. Add one to get started.');
    await user.click(screen.getByText('Create with AI'));
    await user.type(screen.getByLabelText('Describe your hook file'), 'Review tools.');
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    await screen.findByRole('form', { name: 'Add hook file' });
    const original = window.fetch;
    const response = await original('/api/configuration', { method: 'GET' });
    let finish!: (response: Response) => void;
    window.fetch = (input, init) => String(input) === '/api/configuration' ? new Promise<Response>((resolve) => { finish = resolve; }) : original(input, init);
    await user.click(screen.getByRole('button', { name: 'View existing skill release-check' }));
    expect(screen.getByText('Loading the existing skill…')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Cancel', exact: true }));
    await act(async () => finish(response));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Similar installed skills' })).toBeNull();
  });

  test.each([
    { kind: 'agents', label: 'agent', automatic: 'Allow automatic agent selection', manual: 'Allow manual agent selection' },
    { kind: 'skills', label: 'skill', automatic: 'Allow automatic skill invocation', manual: 'Allow manual skill invocation' },
  ] as const)('keeps AI invocation choices editable in the $kind draft', async ({ kind, label, automatic, manual }) => {
    const { user } = await creator(kind);
    await user.click(screen.getByText('Create with AI'));
    await user.type(screen.getByLabelText(`Describe your ${label}`), 'Create a manual-only review checklist.');
    await user.click(screen.getByRole('button', { name: 'Generate draft' }));
    const form = within(await screen.findByRole('form', { name: `Add ${label}` }));
    await user.click(form.getByText('Advanced settings'));
    expect((form.getByRole('checkbox', { name: automatic }) as HTMLInputElement).checked).toBe(false);
    expect((form.getByRole('checkbox', { name: manual }) as HTMLInputElement).checked).toBe(true);
    await user.click(form.getByRole('checkbox', { name: manual }));
    await user.click(form.getByRole('button', { name: `Add ${label}` }));
    await screen.findByText(/saved\. New and reopened tasks use the updated file/);
    const saved = (await api.configuration())[kind].at(-1)!;
    expect(saved.content).toContain('disable-model-invocation: true');
    expect(saved.content).toContain('user-invocable: false');
  });

  test.each([
    { reason: 'Terminal', change: (c: AppContextValue) => { c.settings.terminal = false; } },
    { reason: 'No available Utility model', change: (c: AppContextValue) => { c.settings.title_model = { copilot: 'none' }; } },
    { reason: 'Background AI is off', change: (c: AppContextValue) => { c.settings.utility_daily_limit = 0; } },
    { reason: 'model catalog could not be loaded', change: (c: AppContextValue) => { c.metaError = 'Network error'; } },
    { reason: 'Loading Utility AI settings', change: (c: AppContextValue) => { c.meta = null; } },
    { reason: 'No available provider supports Utility AI', change: (c: AppContextValue) => { c.meta!.providers[0].available = false; } },
  ])('explains the $reason gate before generation', async ({ reason, change }) => {
    const { user } = await creator('agents', change);
    await user.click(screen.getByText('Create with AI'));
    const form = within(screen.getByRole('form', { name: 'Create agent with AI' }));
    expect(form.getByText(new RegExp(reason))).toBeTruthy();
    expect((form.getByRole('button', { name: 'Generate draft' }) as HTMLButtonElement).disabled).toBe(true);
  });

  test('skips an opted-out Utility provider when a later provider can generate', async () => {
    const { user } = await creator('agents', (c) => {
      c.settings.title_model = { copilot: 'none' };
      c.meta!.providers.push({ ...c.meta!.providers[0], name: 'second', display_name: 'Second provider' });
    });
    await user.click(screen.getByText('Create with AI'));
    await user.type(screen.getByLabelText('Describe your agent'), 'Review changes.');
    expect(screen.getByText(/Uses Second provider/)).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Generate draft' }) as HTMLButtonElement).disabled).toBe(false);
  });

  test('only offers available visible Copilot models and blocks a choice that disappears', async () => {
    const { user, context, update } = await creator('agents', (c) => { c.settings.hidden_models = { copilot: ['gpt-5-mini'] }; });
    await user.click(screen.getByRole('button', { name: 'Add agent' }));
    const form = within(screen.getByRole('form', { name: 'Add agent' }));
    expect(form.queryByRole('textbox', { name: 'Model (optional)' })).toBeNull();
    await user.click(form.getByRole('combobox', { name: 'Model (optional)' }));
    expect(screen.queryByRole('option', { name: 'Auto', exact: true })).toBeNull();
    expect(screen.queryByRole('option', { name: 'GPT-5 mini', exact: true })).toBeNull();
    expect(screen.queryByRole('option', { name: 'Qwen3 Coder', exact: true })).toBeNull();
    expect(screen.getByRole('option', { name: 'Inherit task model' })).toBeTruthy();
    await user.click(screen.getByRole('option', { name: 'GPT-5.6 Luna', exact: true }));
    context.meta!.providers[0].models = [];
    update();
    expect((await form.findByRole('alert')).textContent).toContain('no longer available');
    expect((form.getByRole('button', { name: 'Add agent' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(form.getByText('Advanced settings'));
    expect((form.getByRole('button', { name: 'Edit full document for advanced options' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(form.getByRole('combobox', { name: 'Model (optional)' }));
    await user.click(screen.getByRole('option', { name: 'Inherit task model' }));
    await waitFor(() => expect(form.queryByRole('alert')).toBeNull());
    expect((form.getByRole('button', { name: 'Add agent' }) as HTMLButtonElement).disabled).toBe(false);
  });
});
