import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, type User } from './render';

async function openSettings(user?: User) {
  const rendered = renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  await section('Models');
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
  test('the Enter default switches between steer and queue, saved on the service', async () => {
    const { user } = await openSettings();
    const composer = await section('Composer');
    const steer = composer.getByRole('radio', { name: 'Steer' });
    expect(steer.getAttribute('aria-checked')).toBe('true');
    await user.click(composer.getByRole('radio', { name: 'Queue' }));
    await waitFor(() => expect(composer.getByRole('radio', { name: 'Queue' }).getAttribute('aria-checked')).toBe('true'));
    expect(composer.getByText(/Steer stays on Ctrl\+Enter/)).toBeTruthy();
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

  test('a setting the service refuses goes back to its old value and says why', async () => {
    const { user } = await openSettings();
    const models = await section('Models');
    // The mock service knows no hidden_models setting, so hiding a model is refused.
    await user.click(models.getByRole('switch', { name: 'Show Kimi K3' }));
    expect((await screen.findByRole('alert')).textContent).toBe('Could not save the setting: unknown setting "hidden_models"');
    await waitFor(() => expect(models.getByRole('switch', { name: 'Show Kimi K3' }).getAttribute('aria-checked')).toBe('true'));
    expect(models.queryByText('Hidden')).toBeNull();
  });

  test('custom providers load their model list and save the chosen ones', async () => {
    const { user } = await openSettings();
    const models = await section('Models');
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
    await waitFor(() => expect(list.getAllByRole('checkbox')).toHaveLength(2));
    await user.click(form.getByRole('button', { name: 'Select all' }));
    expect(form.getByText('2 selected')).toBeTruthy();
    await user.clear(form.getByRole('searchbox', { name: 'Search models' }));
    await user.click(list.getByRole('checkbox', { name: 'gemma3:27b' }));
    await user.type(form.getByRole('textbox', { name: 'Add a model ID the endpoint does not list' }), 'llama4:scout');
    await user.click(form.getByRole('button', { name: 'Add ID' }));
    expect(form.getByText('4 selected')).toBeTruthy();
    await user.click(form.getByRole('button', { name: 'Save provider' }));
    const saved = within(await models.findByRole('region', { name: 'ollama models' }));
    expect(saved.getByText('llama4:scout')).toBeTruthy();
    expect(saved.getByText('gpt-oss:120b')).toBeTruthy();
  });

  test('removing a custom provider is confirmed first', async () => {
    const { user } = await openSettings();
    const models = await section('Models');
    await user.click(models.getByRole('button', { name: 'Remove provider openrouter' }));
    const confirm = await screen.findByRole('alertdialog', { name: 'Remove provider openrouter?' });
    expect(within(confirm).getByText(/Its one model leaves the model menus\./)).toBeTruthy();
    await user.click(within(confirm).getByRole('button', { name: 'Remove provider' }));
    await waitFor(() => expect(models.queryByRole('region', { name: 'openrouter models' })).toBeNull());
  });

  test('editing a provider keeps its saved models selected', async () => {
    const { user } = await openSettings();
    const models = await section('Models');
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
    const browser = await section('This browser');
    await user.click(browser.getByRole('radio', { name: 'Match system' }));
    expect(localStorage.getItem('uam.motion')).toBe('system');
    await user.click(browser.getByRole('radio', { name: 'Detailed' }));
    expect(localStorage.getItem('uam.activity')).toBe('detailed');
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Settings' })).toBeNull());
  });
});
