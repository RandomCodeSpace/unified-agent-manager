import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { api, DEFAULT_COMMIT_PROMPT, DEFAULT_MR_PROMPT } from '../../src/api';
import { renderApp } from './render';

async function openPage() {
  const { user } = renderApp('#settings');
  const rail = within(await screen.findByRole('navigation', { name: 'Settings sections' }));
  await user.click(rail.getByRole('button', { name: 'Commits & pull requests' }));
  return user;
}

test.each([
  { name: 'Commit messages', fallback: DEFAULT_COMMIT_PROMPT, key: 'commit_prompt' },
  { name: 'Pull and merge requests', fallback: DEFAULT_MR_PROMPT, key: 'gitlab_mr_prompt' },
])('$name instructions show the default until saved, and reset to it', async ({ name, fallback, key }) => {
  const save = vi.spyOn(api, 'updateWebSettings');
  try {
    const user = await openPage();
    const prompt = await screen.findByRole<HTMLTextAreaElement>('textbox', { name });
    expect(prompt.value).toBe('');
    expect(prompt.placeholder).toBe(fallback);
    const form = within(prompt.closest('form')!);
    await user.type(prompt, '  Be brief.  ');
    await user.click(form.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ [key]: 'Be brief.' }));
    const reset = form.getByRole('button', { name: 'Reset to default' });
    await waitFor(() => expect(reset.hasAttribute('disabled') || reset.getAttribute('aria-disabled') === 'true').toBe(false));
    await user.click(reset);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ [key]: '' }));
    await waitFor(() => expect(prompt.value).toBe(''));
  } finally { save.mockRestore(); }
});

test('the co-author name appears once the switch is on, and an empty one leaves it to Copilot', async () => {
  const save = vi.spyOn(api, 'updateWebSettings');
  try {
    const user = await openPage();
    const toggle = await screen.findByRole('switch', { name: 'Co-author' });
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(screen.queryByRole('textbox', { name: 'Co-author name' })).toBeNull();
    await user.click(toggle);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ coauthor: { enabled: true, name: '' } }));
    const name = await screen.findByRole<HTMLInputElement>('textbox', { name: 'Co-author name' });
    await waitFor(() => expect(name.readOnly).toBe(false));
    await user.type(name, ' Ada <ada@example.com> ');
    await user.click(within(name.closest('form')!).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ coauthor: { enabled: true, name: 'Ada <ada@example.com>' } }));
    await waitFor(() => expect(toggle.hasAttribute('data-disabled')).toBe(false));
    await user.click(toggle);
    await waitFor(() => expect(save).toHaveBeenLastCalledWith({ coauthor: { enabled: false, name: 'Ada <ada@example.com>' } }));
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Co-author name' })).toBeNull());
  } finally { save.mockRestore(); }
});
