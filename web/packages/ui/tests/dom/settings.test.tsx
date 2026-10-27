import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { SettingsView } from '../../src/components/Settings';
import { seed } from '../../src/mock/data';
import { install } from '../../src/mock/install';
import { renderApp, type User } from './render';

/** Opens Settings on a page of its rail and waits for one of the page's cards (the page's namesake by default). */
async function openSettings(page = 'New threads', card = page, user?: User) {
  const rendered = renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  await rendered.user.click(page_(page));
  await section(card);
  return { ...rendered, user: user ?? rendered.user };
}

/** A page's button in the Settings rail. */
const page_ = (name: string) => within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name, exact: true });

/** A wide screen (the rail beside the page) until the returned spy is restored. */
function wide() {
  const original = window.matchMedia.bind(window);
  return vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === '(min-width: 768px)' ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
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
  test('Find a setting marks the first match current, and Enter opens it', async () => {
    const media = wide();
    try {
      const { user } = await openSettings();
      const find = screen.getByRole('searchbox', { name: 'Find a setting' });
      await user.type(find, 'theme');
      await waitFor(() => expect(page_('Appearance & alerts').getAttribute('aria-current')).toBe('page'));
      await user.keyboard('{Enter}');
      expect(find).toHaveProperty('value', '');
      await section('Appearance & alerts');
      expect(page_('Appearance & alerts').getAttribute('aria-current')).toBe('page');
      expect(page_('New threads').getAttribute('aria-current')).toBeNull();
    } finally {
      media.mockRestore();
    }
  });

  test('a page mounts on its first visit and stays mounted after it; Background AI from the start', async () => {
    const media = wide();
    try {
      const { user } = await openSettings();
      expect(document.getElementById('background-ai-title')).not.toBeNull();
      expect(document.getElementById('browser-title')).toBeNull();
      await user.click(page_('Appearance & alerts'));
      await section('Appearance & alerts');
      await user.click(page_('New threads'));
      await section('New threads');
      expect(document.getElementById('browser-title')).not.toBeNull();
    } finally {
      media.mockRestore();
    }
  });

  test('project location saves a normalized path and clearing restores home', async () => {
    const { user } = await openSettings('Projects & worktrees', 'Folders');
    const folders = await section('Folders');
    const input = folders.getByRole('textbox', { name: 'Default project location' });
    expect(folders.queryByRole('button', { name: 'Save project location' })).toBeNull();
    await user.type(input, '/home/user/projects/');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(input).toHaveProperty('value', '/home/user/projects'));
    expect((await api.listDirs()).path).toBe('/home/user/projects');
    await user.clear(input);
    await user.keyboard('{Enter}');
    await waitFor(async () => expect((await api.listDirs()).path).toBe('/home/user'));
  });

  test('project location saves the chosen folder immediately, including a newly created folder', async () => {
    const { user } = await openSettings('Projects & worktrees', 'Folders');
    const card = await section('Folders');
    const input = card.getByRole('textbox', { name: 'Default project location' });
    const browse = card.getByRole('button', { name: 'Browse' });
    await user.click(browse);
    const folders = within(await card.findByRole('listbox', { name: 'Folders' }));
    await user.dblClick(await folders.findByRole('option', { name: /^projects/ }));
    await folders.findByRole('option', { name: /^archive/ });
    await user.click(card.getByRole('button', { name: 'New folder' }));
    await user.type(await card.findByRole('textbox', { name: 'New folder name' }), 'client-work{Enter}');
    expect((await folders.findByRole('option', { name: 'client-work' })).getAttribute('aria-selected')).toBe('true');
    expect(input).toHaveProperty('value', '');
    expect((await api.listDirs()).path).toBe('/home/user');
    await user.click(card.getByRole('button', { name: 'Use this folder' }));
    expect(input).toHaveProperty('value', '/home/user/projects/client-work');
    expect(document.activeElement).toBe(input);
    expect(card.queryByRole('button', { name: 'Save project location' })).toBeNull();
    await waitFor(async () => expect((await api.listDirs()).path).toBe('/home/user/projects/client-work'));
    await waitFor(() => expect(document.getElementById('default-project-dir-picker')).toBeNull());
    await user.click(browse);
    await card.findByText('No folders here');
    expect(card.getByRole('button', { name: 'client-work' }).getAttribute('aria-current')).toBe('location');
    await user.keyboard('{Escape}');
    expect(browse.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(browse);
    expect(input).toHaveProperty('value', '/home/user/projects/client-work');
  });

  test('project location: typing a name past the listing cap asks the server and selects the folder', async () => {
    const original = api.listDirs;
    // The cap left archive out of the projects folder's listing.
    const list = vi.spyOn(api, 'listDirs').mockImplementation(async (path, hidden, filter) => {
      const listing = await original(path, hidden, filter);
      return filter || !path?.endsWith('/projects') ? listing : { ...listing, entries: listing.entries.filter((e) => e.name !== 'archive'), truncated: true };
    });
    try {
      const { user } = await openSettings('Projects & worktrees', 'Folders');
      const card = await section('Folders');
      await user.click(card.getByRole('button', { name: 'Browse' }));
      const folders = within(await card.findByRole('listbox', { name: 'Folders' }));
      await user.dblClick(await folders.findByRole('option', { name: /^projects/ }));
      expect(await card.findByText('Showing the first 1,000. Filter folders to search them all.')).toBeTruthy();
      expect(folders.queryByRole('option', { name: /^archive/ })).toBeNull();
      await user.keyboard('arch');
      expect(await folders.findByRole('option', { name: /^archive/ })).toBeTruthy();
      expect(list).toHaveBeenLastCalledWith('/home/user/projects', false, 'arch');
      expect(card.getByRole('textbox', { name: 'Filter folders' })).toHaveProperty('value', 'arch');
    } finally {
      list.mockRestore();
    }
  });

  test('project location keeps a refused folder choice for retry without replacing the saved default', async () => {
    const { user } = await openSettings('Projects & worktrees', 'Folders');
    const card = await section('Folders');
    const input = card.getByRole('textbox', { name: 'Default project location' });
    await user.click(card.getByRole('button', { name: 'Browse' }));
    const folders = within(await card.findByRole('listbox', { name: 'Folders' }));
    await user.click(await folders.findByRole('option', { name: /^projects/ }));
    const update = vi.spyOn(api, 'updateWebSettings').mockRejectedValueOnce(new Error('Folder is no longer available'));
    try {
      await user.click(card.getByRole('button', { name: 'Use this folder' }));
      expect((await screen.findByRole('alert')).textContent).toContain('Folder is no longer available');
      expect(update).toHaveBeenCalledExactlyOnceWith({ default_project_dir: '/home/user/projects' });
      expect(input).toHaveProperty('value', '/home/user/projects');
      expect(document.activeElement).toBe(input);
      expect((await api.listDirs()).path).toBe('/home/user');
      await user.keyboard('{Enter}');
      await waitFor(async () => expect((await api.listDirs()).path).toBe('/home/user/projects'));
    } finally { update.mockRestore(); }
  });

  test('project location keeps a refused draft and the previously saved location', async () => {
    const { user } = await openSettings('Projects & worktrees', 'Folders');
    const folders = await section('Folders');
    const input = folders.getByRole('textbox', { name: 'Default project location' });
    await user.type(input, 'relative/projects');
    await user.keyboard('{Enter}');
    expect((await screen.findByRole('alert')).textContent).toContain('must be an absolute path');
    expect(input).toHaveProperty('value', 'relative/projects');
    expect((await api.listDirs()).path).toBe('/home/user');
  });

  test('Log out lives in UAM and access and returns to sign in', async () => {
    const auth = vi.spyOn(api, 'auth').mockResolvedValue({ authenticated: true, required: true });
    const logout = vi.spyOn(api, 'logout').mockResolvedValue(undefined);
    try {
      const { user } = await openSettings('UAM and access', 'Access');
      expect(within(screen.getByRole('navigation', { name: 'Threads' })).queryByRole('button', { name: 'Log out' })).toBeNull();
      expect((await section('Access')).getByRole('button', { name: 'Log out' })).toBeTruthy();
      await user.click(page_('Custom providers'));
      expect(screen.queryByRole('button', { name: 'Log out' })).toBeNull();
      await user.click(page_('UAM and access'));
      await user.click(screen.getByRole('button', { name: 'Log out' }));
      expect(await screen.findByRole('heading', { name: 'Sign in to UAM' })).toBeTruthy();
      expect(logout).toHaveBeenCalledTimes(1);
    } finally { auth.mockRestore(); logout.mockRestore(); }
  });

  test('UAM and access omits Log out when the server does not require sign in', async () => {
    await openSettings('UAM and access', 'Access');
    expect(screen.queryByRole('button', { name: 'Log out' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Session', exact: true })).toBeNull();
  });

  async function customModelSettings(change?: (context: AppContextValue) => void) {
    install();
    const fixture = seed();
    const context: AppContextValue = { meta: fixture.meta, metaError: null, loaded: true, settings: fixture.settings, dispatch: vi.fn(), narrow: false, usage: null, refreshMeta: vi.fn() };
    context.settings.custom_models = [{ name: 'ollama', base_url: 'https://ollama.com/v1', api_key_env: 'UAM_BYOM_OLLAMA', model_id: 'deepseek-v4.1-flash', display_name: 'DeepSeek V4.1 Flash', key_present: true }];
    context.meta!.providers.find((p) => p.name === 'copilot')!.models.push({ id: 'ollama/deepseek-v4.1-flash', name: 'DeepSeek V4.1 Flash' });
    change?.(context);
    render(<AppContext.Provider value={context}><SettingsView onClose={() => {}} /></AppContext.Provider>);
    const user = userEvent.setup();
    await user.click(page_('Models'));
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

  test('Subagent models shows only for a provider that can limit them', async () => {
    await customModelSettings();
    expect(screen.queryByRole('region', { name: 'Subagent models' })).toBeNull();
  });

  /** The Models tab with Copilot able to limit subagents to `allowed`, and the PATCH spied. */
  async function subagentSettings(allowed?: string[], change?: (context: AppContextValue) => void) {
    const { user, context } = await customModelSettings((context) => {
      context.meta!.providers.find((p) => p.name === 'copilot')!.capabilities.subagent_models = true;
      if (allowed) context.settings.subagent_models = { copilot: allowed };
      change?.(context);
    });
    const save = vi.spyOn(api, 'updateWebSettings').mockResolvedValue(context.settings);
    return { user, save, subagents: await section('Subagent models') };
  }

  test('adding a subagent model saves the list in order, offering only visible models not listed yet', async () => {
    const { user, save, subagents } = await subagentSettings(['gpt-5-mini'], (context) => { context.settings.hidden_models = { copilot: ['kimi-k3'] }; });
    try {
      expect(subagents.getByText('Fallback')).toBeTruthy();
      await user.click(subagents.getByRole('combobox', { name: 'Add a GitHub Copilot subagent model' }));
      expect(screen.queryByRole('option', { name: 'Kimi K3' })).toBeNull();
      expect(screen.queryByRole('option', { name: 'GPT-5 mini' })).toBeNull();
      await user.click(await screen.findByRole('option', { name: 'DeepSeek V4.1 Flash' }));
      await waitFor(() => expect(save).toHaveBeenCalledWith({ subagent_models: { copilot: ['gpt-5-mini', 'ollama/deepseek-v4.1-flash'] } }));
    } finally { save.mockRestore(); }
  });

  test('Make fallback moves a subagent model to the front, and an ID no longer offered keeps its row', async () => {
    const { user, save, subagents } = await subagentSettings(['gpt-5-mini', 'retired-model', 'kimi-k3']);
    try {
      expect(subagents.getByText('Not offered now')).toBeTruthy();
      expect(subagents.queryByRole('button', { name: 'Make GPT-5 mini the fallback' })).toBeNull();
      await user.click(subagents.getByRole('button', { name: 'Make Kimi K3 the fallback' }));
      await waitFor(() => expect(save).toHaveBeenCalledWith({ subagent_models: { copilot: ['kimi-k3', 'gpt-5-mini', 'retired-model'] } }));
    } finally { save.mockRestore(); }
  });

  test('removing the last subagent model lifts the limit', async () => {
    const { user, save, subagents } = await subagentSettings(['retired-model']);
    try {
      await user.click(subagents.getByRole('button', { name: 'Remove retired-model from subagent models' }));
      await waitFor(() => expect(save).toHaveBeenCalledWith({ subagent_models: { copilot: [] } }));
    } finally { save.mockRestore(); }
  });

  test('each page shows only its own cards, with where they are saved', async () => {
    const { user } = await openSettings('Models');
    expect(screen.getByRole('region', { name: 'Models', exact: true })).toBeTruthy();
    // On a wide screen the rail names the page; a server-saved page has no line of its own.
    expect(screen.queryByText(/The models the menus offer/)).toBeNull();
    expect(screen.queryByText(/Saved on/)).toBeNull();
    expect(screen.queryByRole('region', { name: 'Utility model' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add provider' })).toBeNull();
    await user.click(page_('Background AI'));
    expect(await section('Utility model')).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Background AI' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Models', exact: true })).toBeNull();
    await user.click(page_('GitHub Copilot'));
    expect(screen.getByRole('region', { name: 'GitHub Copilot' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Add provider' })).toBeNull();
    await user.click(page_('Custom providers'));
    expect((await section('Custom providers')).getByRole('button', { name: 'Add provider' })).toBeTruthy();
    await user.click(page_('Appearance & alerts'));
    expect(screen.getByText(/Saved in this browser only/)).toBeTruthy();
    expect(page_('Appearance & alerts').getAttribute('aria-current')).toBe('page');
  });

  test('Find a setting lists the pages that hold the words, with the way back to each', async () => {
    const { user } = await openSettings();
    const rail = within(screen.getByRole('navigation', { name: 'Settings sections' }));
    await user.type(rail.getByRole('searchbox', { name: 'Find a setting' }), 'api key');
    expect(rail.queryByRole('button', { name: 'New threads', exact: true })).toBeNull();
    expect((await section('Custom providers')).getByRole('button', { name: 'Add provider' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'New threads' })).toBeNull();
    await user.clear(rail.getByRole('searchbox', { name: 'Find a setting' }));
    await user.type(rail.getByRole('searchbox', { name: 'Find a setting' }), 'dark theme');
    expect(await section('Appearance & alerts')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: /This browser.*Appearance & alerts/ }));
    expect((rail.getByRole('searchbox', { name: 'Find a setting' }) as HTMLInputElement).value).toBe('');
    expect(page_('Appearance & alerts').getAttribute('aria-current')).toBe('page');
    await user.type(rail.getByRole('searchbox', { name: 'Find a setting' }), 'nothing like this');
    expect(screen.getAllByText('No setting matches “nothing like this”.').length).toBeGreaterThan(0);
    await user.keyboard('{Escape}');
    expect((rail.getByRole('searchbox', { name: 'Find a setting' }) as HTMLInputElement).value).toBe('');
    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeTruthy();
  });

  test('the rail marks the pages that need the user, said in words', async () => {
    await openSettings();
    // The mock's openrouter key is not set, and its Background AI used all 40 calls today.
    await waitFor(() => expect(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: 'Background AI', description: 'Background AI is paused for today' })).toBeTruthy());
    expect(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: 'Custom providers', description: 'API key missing: openrouter' })).toBeTruthy();
    expect(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: 'Conversation', description: '' })).toBeTruthy();
  });

  test('sections keep an unfinished provider draft when navigating away and back', async () => {
    const { user } = await openSettings('Custom providers');
    const models = await section('Custom providers');
    await user.click(models.getByRole('button', { name: 'Add provider' }));
    await user.type(models.getByRole('textbox', { name: 'Provider name' }), 'draft-provider');
    await user.click(page_('Conversation'));
    expect(screen.queryByRole('region', { name: 'Custom providers', exact: true })).toBeNull();
    expect(page_('Conversation').getAttribute('aria-current')).toBe('page');
    await user.click(page_('Custom providers'));
    expect((models.getByRole('textbox', { name: 'Provider name' }) as HTMLInputElement).value).toBe('draft-provider');
  });

  test('the inline provider form takes focus, cancels on Escape and returns focus to what opened it', async () => {
    const { user } = await openSettings('Custom providers');
    const providers = await section('Custom providers');
    const add = providers.getByRole('button', { name: 'Add provider' });
    await user.click(add);
    const name = providers.getByRole('textbox', { name: 'Provider name' }) as HTMLInputElement;
    await waitFor(() => expect(document.activeElement).toBe(name));
    expect(name.placeholder).toBe('my-provider');
    expect((providers.getByRole('textbox', { name: 'Base URL' }) as HTMLInputElement).placeholder).toBe('https://api.example.com/v1');
    expect(providers.getByText('Save or cancel this form to edit another provider.')).toBeTruthy();
    await user.keyboard('{Escape}');
    expect(providers.queryByRole('form', { name: 'Add a provider' })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(providers.getByRole('button', { name: 'Add provider' })));
    const edit = providers.getAllByRole('button', { name: 'Edit', exact: true })[0];
    await user.click(edit);
    await user.click(providers.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(document.activeElement).toBe(edit));
  });

  test('models read in name order within each provider', async () => {
    await openSettings('Models');
    const copilot = within((await section('Models')).getByRole('group', { name: 'GitHub Copilot models' }));
    const names = copilot.getAllByRole('switch').map((s) => s.getAttribute('aria-label')!.replace(/^Show /, ''));
    expect(names).toEqual([...names].sort(new Intl.Collator('en', { numeric: true, sensitivity: 'base' }).compare));
  });

  test('the Enter default switches between steer and queue, saved on the service', async () => {
    const { user } = await openSettings('Conversation', 'Composer');
    const composer = await section('Composer');
    const steer = composer.getByRole('radio', { name: 'Send now' });
    expect(steer.getAttribute('aria-checked')).toBe('true');
    await user.click(composer.getByRole('radio', { name: 'After this turn' }));
    await waitFor(() => expect(composer.getByRole('radio', { name: 'After this turn' }).getAttribute('aria-checked')).toBe('true'));
    expect(composer.getByText(/Send now stays on Ctrl\+Enter/)).toBeTruthy();
  });

  test('new tasks start on the defaults shown, and a change is saved', async () => {
    const { user } = await openSettings();
    const tasks = await section('New threads');
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
    const tasks = await section('New threads');
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
    const { user } = await openSettings('Custom providers');
    const models = await section('Custom providers');
    expect(models.getByRole('region', { name: 'openrouter models' })).toBeTruthy();
    expect(models.getByText("UAM_BYOM_OPENROUTER is not set in the service's environment")).toBeTruthy();
    await user.click(models.getByRole('button', { name: 'Add provider' }));
    const form = within(models.getByRole('form', { name: 'Add a provider' }));
    await user.type(form.getByRole('textbox', { name: 'Provider name' }), 'ollama');
    await user.type(form.getByRole('textbox', { name: 'Base URL' }), 'https://ollama.com/v1');
    await user.click(form.getByRole('radio', { name: 'Environment variable' }));
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

  test('custom providers accept a direct key, keep it private on edit, and can switch to an environment variable', async () => {
    const { user } = await openSettings('Custom providers');
    const models = await section('Custom providers');
    await user.click(models.getByRole('button', { name: 'Add provider' }));
    let form = within(models.getByRole('form', { name: 'Add a provider' }));
    await user.type(form.getByRole('textbox', { name: 'Provider name' }), 'direct');
    await user.type(form.getByRole('textbox', { name: 'Base URL' }), 'https://llm.example/v1');
    const key = form.getByLabelText('API key') as HTMLInputElement;
    expect(key.type).toBe('password');
    await user.type(key, 'fixture-only-secret');
    await user.click(form.getByRole('button', { name: 'Load models' }));
    await user.click(await form.findByRole('checkbox', { name: 'gpt-oss:120b' }));
    await user.click(form.getByRole('button', { name: 'Save provider' }));
    let saved = within(await models.findByRole('region', { name: 'direct models' }));
    expect(saved.getByText('API key saved')).toBeTruthy();
    expect(JSON.stringify(await api.webSettings())).not.toContain('fixture-only-secret');
    await user.click(saved.getByRole('button', { name: 'Edit', exact: true }));
    form = within(models.getByRole('form', { name: 'Edit provider direct' }));
    expect((form.getByLabelText('API key') as HTMLInputElement).value).toBe('');
    expect((form.getByLabelText('API key') as HTMLInputElement).required).toBe(false);
    await user.click(form.getByRole('button', { name: 'Load models' }));
    await waitFor(() => expect(form.getByRole('button', { name: 'Load models' }).hasAttribute('disabled')).toBe(false));
    await user.click(form.getByRole('button', { name: 'Save provider' }));
    await waitFor(() => expect(models.queryByRole('form')).toBeNull());
    saved = within(models.getByRole('region', { name: 'direct models' }));
    expect(saved.getByText('API key saved')).toBeTruthy();
    await user.click(saved.getByRole('button', { name: 'Edit', exact: true }));
    form = within(models.getByRole('form', { name: 'Edit provider direct' }));
    await user.click(form.getByRole('radio', { name: 'Environment variable' }));
    expect(form.queryByLabelText('API key')).toBeNull();
    await user.type(form.getByRole('textbox', { name: 'API key variable' }), 'UAM_BYOM_DIRECT');
    await user.click(form.getByRole('button', { name: 'Save provider' }));
    await waitFor(() => expect(models.queryByRole('form')).toBeNull());
    expect(models.getByText("UAM_BYOM_DIRECT is not set in the service's environment")).toBeTruthy();
  });

  test('removing a custom provider is confirmed first', async () => {
    const { user } = await openSettings('Custom providers');
    const models = await section('Custom providers');
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
    const { user } = await openSettings('Custom providers');
    const providers = await section('Custom providers');
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
    const { user } = await openSettings('Custom providers');
    const models = await section('Custom providers');
    await user.click(within(models.getByRole('region', { name: 'openrouter models' })).getByRole('button', { name: 'Edit' }));
    const form = within(models.getByRole('form', { name: 'Edit provider openrouter' }));
    expect(form.getByText('1 selected')).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'None' }));
    expect(form.getByText('0 selected')).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'Cancel' }));
    expect(models.queryByRole('form')).toBeNull();
  });

  test('the terminal switch and this browser\'s theme and motion without an activity selector', async () => {
    const { user } = await openSettings('UAM and access', 'Access');
    const shell = await section('Access');
    await user.click(shell.getByRole('switch', { name: 'Terminal' }));
    await waitFor(() => expect(shell.getByRole('switch', { name: 'Terminal' }).getAttribute('aria-checked')).toBe('false'));
    await user.click(page_('Appearance & alerts'));
    const browser = await section('Appearance & alerts');
    const theme = within(browser.getByRole('radiogroup', { name: 'Theme' }));
    expect(theme.getByRole('radio', { name: 'Match system' }).getAttribute('aria-checked')).toBe('true');
    await user.click(theme.getByRole('radio', { name: 'Dark' }));
    expect(localStorage.getItem('uam.theme')).toBe('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    await user.click(theme.getByRole('radio', { name: 'Light' }));
    expect(document.documentElement.dataset.theme).toBe('light');
    await user.click(within(browser.getByRole('radiogroup', { name: 'Motion' })).getByRole('radio', { name: 'Match system' }));
    expect(localStorage.getItem('uam.motion')).toBe('system');
    // How todo dependencies are drawn: rails until the graph is chosen.
    const graph = within(browser.getByRole('radiogroup', { name: 'Todo dependencies' }));
    expect(graph.getByRole('radio', { name: 'Rails' }).getAttribute('aria-checked')).toBe('true');
    await user.click(graph.getByRole('radio', { name: 'Graph' }));
    expect(localStorage.getItem('uam.todoGraph')).toBe('graph');
    expect(browser.queryByRole('radiogroup', { name: 'Activity' })).toBeNull();
    expect(browser.queryByRole('radio', { name: 'Detailed' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Settings' })).toBeNull());
  });
});
