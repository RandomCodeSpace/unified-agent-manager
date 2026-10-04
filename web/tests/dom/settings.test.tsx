import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { SettingsView } from '../../src/components/Settings';
import { seed } from '../../src/mock/data';
import { install } from '../../src/mock/install';
import { renderApp, type User } from './render';

async function openSettings(tab = 'General', user?: User) {
  const rendered = renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  await rendered.user.click(screen.getByRole('button', { name: tab, exact: true }));
  await section(tab === 'General' ? 'Composer' : tab);
  return { ...rendered, user: user ?? rendered.user };
}

/** A settings card once its content (not the loading skeleton) is on screen. */
async function section(name: string) {
  const card = await waitFor(() => {
    const found = screen.getByRole('region', { name });
    expect(found.getAttribute('aria-busy')).toBeNull();
    return found;
  });
  return within(card);
}

describe('settings', () => {
  async function customModelSettings(change?: (context: AppContextValue) => void) {
    install();
    const fixture = seed();
    const context: AppContextValue = { meta: fixture.meta, metaError: null, loaded: true, settings: fixture.settings, dispatch: vi.fn(), narrow: false, hasNews: () => false, usage: null, refreshMeta: vi.fn() };
    context.settings.custom_models = [{ name: 'ollama', base_url: 'https://ollama.com/v1', api_key_env: 'UAM_BYOM_OLLAMA', model_id: 'deepseek-v4.1-flash', display_name: 'DeepSeek V4.1 Flash', key_present: true }];
    context.meta!.providers.find((p) => p.name === 'copilot')!.models.push({ id: 'ollama/deepseek-v4.1-flash', name: 'DeepSeek V4.1 Flash' });
    change?.(context);
    render(<AppContext.Provider value={context}><SettingsView onClose={() => {}} /></AppContext.Provider>);
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Models', exact: true }));
    return { user, context, models: await section('Models') };
  }

  test('custom models have their own provider group and keep the Copilot visibility contract', async () => {
    const { user, context, models } = await customModelSettings((context) => {
      context.settings.hidden_models = { copilot: ['old-native-model'], another: ['other-hidden-model'] };
    });
    const custom = within(models.getByRole('group', { name: 'ollama models' }));
    expect(custom.getByText('DeepSeek V4.1 Flash')).toBeTruthy();
    expect(models.getAllByRole('switch', { name: 'Show DeepSeek V4.1 Flash' })).toHaveLength(1);
    expect(within(models.getByRole('group', { name: 'GitHub Copilot models' })).queryByText('DeepSeek V4.1 Flash')).toBeNull();
    const save = vi.spyOn(api, 'updateWebSettings').mockResolvedValue(context.settings);
    try {
      await user.click(custom.getByRole('switch', { name: 'Show DeepSeek V4.1 Flash' }));
      await waitFor(() => expect(save).toHaveBeenCalledWith({ hidden_models: { copilot: ['old-native-model', 'ollama/deepseek-v4.1-flash'], another: ['other-hidden-model'] } }));
      expect(context.refreshMeta).toHaveBeenCalled();
    } finally {
      save.mockRestore();
    }
  });

  test.each(['stale', 'failed'] as const)('configured custom models remain visible with a %s catalog and missing credentials', async (state) => {
    const { models } = await customModelSettings((context) => {
      context.settings.custom_models![0].key_present = false;
      context.settings.hidden_models = { copilot: ['ollama/deepseek-v4.1-flash'] };
      if (state === 'failed') {
        context.meta = null;
        context.metaError = 'Catalog unavailable';
      } else {
        const copilot = context.meta!.providers.find((p) => p.name === 'copilot')!;
        copilot.models = copilot.models.filter((m) => m.id !== 'ollama/deepseek-v4.1-flash');
      }
    });
    const custom = within(models.getByRole('group', { name: 'ollama models' }));
    expect(custom.getByText('DeepSeek V4.1 Flash')).toBeTruthy();
    expect(custom.getByText('ollama/deepseek-v4.1-flash · not offered now')).toBeTruthy();
    expect(custom.getByText(/UAM_BYOM_OLLAMA is not set/)).toBeTruthy();
    expect(models.getAllByRole('switch', { name: /Show .*deepseek/i })).toHaveLength(1);
    const visibility = custom.getByRole('switch', { name: 'Show DeepSeek V4.1 Flash' });
    expect(visibility.getAttribute('aria-checked')).toBe('false');
    expect(visibility.getAttribute('aria-disabled')).not.toBe('true');
    if (state === 'failed') expect(models.getByRole('alert').textContent).toContain('Catalog unavailable');
  });

  test('Models contains model choices and Providers contains accounts and endpoints', async () => {
    const { user } = await openSettings('Models');
    expect(screen.getByRole('region', { name: 'Utility model' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Models', exact: true })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Add provider' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'GitHub Copilot' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Providers', exact: true }));
    expect((await section('Providers')).getByRole('button', { name: 'Add provider' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'GitHub Copilot' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Models', exact: true })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Utility model' })).toBeNull();
  });

  test('sections keep an unfinished provider draft when navigating away and back', async () => {
    const { user } = await openSettings('Providers');
    const models = await section('Providers');
    await user.click(models.getByRole('button', { name: 'Add provider' }));
    await user.type(models.getByRole('textbox', { name: 'Provider name' }), 'draft-provider');
    await user.click(screen.getByRole('button', { name: 'General', exact: true }));
    expect(screen.queryByRole('region', { name: 'Providers', exact: true })).toBeNull();
    expect(screen.getByRole('button', { name: 'General', exact: true }).getAttribute('aria-current')).toBe('page');
    await user.click(screen.getByRole('button', { name: 'Providers', exact: true }));
    expect((models.getByRole('textbox', { name: 'Provider name' }) as HTMLInputElement).value).toBe('draft-provider');
  });

  test('the Enter default switches between steer and queue, saved on the service', async () => {
    const { user } = await openSettings();
    const composer = await section('Composer');
    const steer = composer.getByRole('radio', { name: 'Send now' });
    expect(steer.getAttribute('aria-checked')).toBe('true');
    await user.click(composer.getByRole('radio', { name: 'After this turn' }));
    await waitFor(() => expect(composer.getByRole('radio', { name: 'After this turn' }).getAttribute('aria-checked')).toBe('true'));
    expect(composer.getByText(/Send now stays on Ctrl\+Enter/)).toBeTruthy();
  });

  test('new tasks start on the defaults shown, and a change is saved', async () => {
    const { user } = await openSettings();
    const tasks = await section('New tasks');
    const model = await tasks.findByRole('combobox', { name: 'Model' });
    expect(model.textContent).toContain('Claude Haiku 4.5');
    expect(tasks.getByText('Long context may cost more.')).toBeTruthy();
    await user.click(model);
    await user.click(await screen.findByRole('option', { name: /GPT-5\.6 Luna/ }));
    await waitFor(() => expect(tasks.getByRole('combobox', { name: 'Model' }).textContent).toContain('GPT-5.6 Luna'));
  });

  test('the compaction threshold is saved, and the default is stored as unset', async () => {
    const { user } = await openSettings();
    const stored = async () => (await (await fetch('/api/settings')).json()).compact_threshold;
    const tasks = await section('New tasks');
    const threshold = tasks.getByRole('combobox', { name: 'Compact the conversation when its context reaches' });
    expect(threshold.textContent).toContain('80% (default)');
    await user.click(threshold);
    await user.click(await screen.findByRole('option', { name: '60%' }));
    await waitFor(async () => expect(await stored()).toBe(60));
    expect(tasks.getByRole('combobox', { name: 'Compact the conversation when its context reaches' }).textContent).toContain('60%');
    await user.click(tasks.getByRole('combobox', { name: 'Compact the conversation when its context reaches' }));
    await user.click(await screen.findByRole('option', { name: '80% (default)' }));
    await waitFor(async () => expect(await stored()).toBeUndefined());
  });

  test('a setting the service refuses goes back to its old value and says why', async () => {
    const { user } = await openSettings('Models');
    const models = await section('Models');
    // The mock service knows no hidden_models setting, so hiding a model is refused.
    await user.click(models.getByRole('switch', { name: 'Show Kimi K3' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Could not save the setting: unknown setting "hidden_models"');
    await waitFor(() => expect(models.getByRole('switch', { name: 'Show Kimi K3' }).getAttribute('aria-checked')).toBe('true'));
    expect(models.queryByText('Hidden')).toBeNull();
  });

  test('custom providers load their model list and save the chosen ones', async () => {
    const { user } = await openSettings('Providers');
    const models = await section('Providers');
    expect(models.getByRole('region', { name: 'openrouter models' })).toBeTruthy();
    expect(models.getByText("UAM_BYOM_OPENROUTER is not set in the service's environment")).toBeTruthy();
    await user.click(models.getByRole('button', { name: 'Add provider' }));
    const form = within(models.getByRole('form', { name: 'Add a provider' }));
    await user.type(form.getByRole('textbox', { name: 'Provider name' }), 'ollama');
    await user.type(form.getByRole('textbox', { name: 'Base URL' }), 'https://ollama.com/v1');
    await user.type(form.getByRole('textbox', { name: 'API key variable' }), 'OLLAMA_KEY');
    await user.click(form.getByRole('button', { name: 'Load models' }));
    expect((await form.findByRole('alert')).textContent).toMatch(/API key variable must be named UAM_BYOM_<NAME>/);
    const key = form.getByRole('textbox', { name: 'API key variable' });
    await user.clear(key);
    await user.type(key, 'UAM_BYOM_OLLAMA');
    await user.click(form.getByRole('button', { name: 'Load models' }));
    const list = within(await form.findByRole('group', { name: 'Models to offer' }));
    await user.type(form.getByRole('searchbox', { name: 'Search models' }), 'gpt-oss');
    await waitFor(() => expect(list.getAllByRole('checkbox', { name: /^gpt-oss/ })).toHaveLength(2));
    await user.click(form.getByRole('button', { name: 'Select all' }));
    expect(form.getByText('2 selected')).toBeTruthy();
    await user.clear(form.getByRole('searchbox', { name: 'Search models' }));
    expect((list.getByRole('checkbox', { name: 'Enable vision for gemma3:27b' }) as HTMLInputElement).disabled).toBe(true);
    await user.click(list.getByRole('checkbox', { name: 'gemma3:27b' }));
    await user.click(list.getByRole('checkbox', { name: 'Enable vision for gemma3:27b' }));
    await user.type(form.getByRole('textbox', { name: 'Add a model ID the endpoint does not list' }), 'llama4:scout');
    await user.click(form.getByRole('button', { name: 'Add ID' }));
    await user.click(list.getByRole('checkbox', { name: 'Enable vision for llama4:scout' }));
    expect(form.getByText('4 selected')).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'Save provider' }));
    const saved = within(await models.findByRole('region', { name: 'ollama models' }));
    expect(saved.getByText('llama4:scout')).toBeTruthy();
    expect(saved.getByText('gpt-oss:120b')).toBeTruthy();
    const offered = (await api.webSettings()).custom_models!.filter((m) => m.name === 'ollama');
    expect(offered.filter((m) => m.vision).map((m) => m.model_id)).toEqual(['gemma3:27b', 'llama4:scout']);
    expect((await api.meta()).providers[0].models.find((m) => m.id === 'ollama/gemma3:27b')?.media).toEqual({ images: true, pdf: false });

    await user.click(saved.getByRole('button', { name: 'Edit', exact: true }));
    const edit = within(models.getByRole('form', { name: 'Edit provider ollama' }));
    expect((edit.getByRole('checkbox', { name: 'Enable vision for gemma3:27b' }) as HTMLInputElement).checked).toBe(true);
    expect((edit.getByRole('checkbox', { name: 'Enable vision for gpt-oss:120b' }) as HTMLInputElement).checked).toBe(false);
    await user.click(edit.getByRole('button', { name: 'Load models' }));
    await waitFor(() => expect(edit.getByRole('button', { name: 'Load models' }).hasAttribute('disabled')).toBe(false));
    expect((edit.getByRole('checkbox', { name: 'Enable vision for llama4:scout' }) as HTMLInputElement).checked).toBe(true);
    await user.click(edit.getByRole('checkbox', { name: 'Enable vision for gemma3:27b' }));
    await user.click(edit.getByRole('button', { name: 'Save provider' }));
    await waitFor(() => expect(models.queryByRole('form')).toBeNull());
    expect((await api.webSettings()).custom_models!.filter((m) => m.vision).map((m) => m.model_id)).toEqual(['llama4:scout']);
    expect((await api.meta()).providers[0].models.find((m) => m.id === 'ollama/gemma3:27b')?.media).toEqual({ images: false, pdf: false });
  });

  test('removing a custom provider is confirmed first', async () => {
    const { user } = await openSettings('Providers');
    const models = await section('Providers');
    await user.click(models.getByRole('button', { name: 'Remove provider openrouter' }));
    let confirm = await screen.findByRole('alertdialog', { name: 'Remove provider openrouter?' });
    expect(within(confirm).getByText(/Its one model leaves the model menus\./)).toBeTruthy();
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(models.getByRole('region', { name: 'openrouter models' })).toBeTruthy();
    await user.click(models.getByRole('button', { name: 'Remove provider openrouter' }));
    confirm = await screen.findByRole('alertdialog', { name: 'Remove provider openrouter?' });
    await user.click(within(confirm).getByRole('button', { name: 'Remove provider' }));
    await waitFor(() => expect(models.queryByRole('region', { name: 'openrouter models' })).toBeNull());
  });

  test.each(['deselect', 'rename'])('provider edits confirm removed model IDs before saving: %s', async (action) => {
    const { user } = await openSettings('Providers');
    const providers = await section('Providers');
    await user.click(within(providers.getByRole('region', { name: 'openrouter models' })).getByRole('button', { name: 'Edit' }));
    const form = within(providers.getByRole('form', { name: 'Edit provider openrouter' }));
    const before = (await api.webSettings()).custom_models!;
    if (action === 'deselect') await user.click(form.getByRole('button', { name: 'None' }));
    else {
      await user.clear(form.getByRole('textbox', { name: 'Provider name' }));
      await user.type(form.getByRole('textbox', { name: 'Provider name' }), 'renamed');
    }
    const save = vi.spyOn(api, 'updateWebSettings');
    try {
      await user.click(form.getByRole('button', { name: 'Save provider' }));
      let dialog = within(await screen.findByRole('alertdialog', { name: 'Remove models from this provider?' }));
      expect(dialog.getByText(`${before[0].name}/${before[0].model_id}`)).toBeTruthy();
      expect(save).not.toHaveBeenCalled();
      await user.click(dialog.getByRole('button', { name: 'Cancel' }));
      expect(save).not.toHaveBeenCalled();
      expect((await api.webSettings()).custom_models).toEqual(before);
      expect(form.getByText(action === 'deselect' ? '0 selected' : '1 selected')).toBeTruthy();
      await user.click(form.getByRole('button', { name: 'Save provider' }));
      dialog = within(await screen.findByRole('alertdialog', { name: 'Remove models from this provider?' }));
      await user.click(dialog.getByRole('button', { name: 'Save and remove models' }));
      await waitFor(() => expect(providers.queryByRole('form')).toBeNull());
      expect(save).toHaveBeenCalledTimes(1);
      expect(((await api.webSettings()).custom_models ?? []).some((model) => model.name === 'openrouter')).toBe(false);
    } finally { save.mockRestore(); }
  });

  test('editing a provider keeps its saved models selected', async () => {
    const { user } = await openSettings('Providers');
    const models = await section('Providers');
    await user.click(within(models.getByRole('region', { name: 'openrouter models' })).getByRole('button', { name: 'Edit' }));
    const form = within(models.getByRole('form', { name: 'Edit provider openrouter' }));
    expect(form.getByText('1 selected')).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'None' }));
    expect(form.getByText('0 selected')).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'Cancel' }));
    expect(models.queryByRole('form')).toBeNull();
  });

  test('the terminal switch and this browser\'s motion and activity settings', async () => {
    const { user } = await openSettings();
    const shell = await section('Shell access');
    await user.click(shell.getByRole('switch', { name: 'Terminal' }));
    await waitFor(() => expect(shell.getByRole('switch', { name: 'Terminal' }).getAttribute('aria-checked')).toBe('false'));
    await user.click(screen.getByRole('button', { name: 'This browser', exact: true }));
    const browser = await section('This browser');
    await user.click(browser.getByRole('radio', { name: 'Match system' }));
    expect(localStorage.getItem('uam.motion')).toBe('system');
    await user.click(browser.getByRole('radio', { name: 'Detailed' }));
    expect(localStorage.getItem('uam.activity')).toBe('detailed');
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Settings' })).toBeNull());
  });
});
