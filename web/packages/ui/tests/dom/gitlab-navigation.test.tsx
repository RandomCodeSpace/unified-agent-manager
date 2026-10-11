import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { renderApp } from './render';

afterEach(() => vi.restoreAllMocks());

const label = 'GitLab pipelines and merge requests';

test('the task header opens GitLab directly and closing or Escape returns focus', async () => {
  const read = vi.spyOn(api, 'gitlabPipeline').mockResolvedValue({ supported: true, auto_investigate: false, can_investigate: false });
  const { user } = renderApp('?gitidle#task=t3');
  const button = await screen.findByRole('button', { name: label });
  expect(button.textContent).toContain('GitLab');
  expect(read).not.toHaveBeenCalled();
  await user.click(button);
  const panel = within(await screen.findByRole('dialog', { name: 'GitLab' }));
  await waitFor(() => expect(read).toHaveBeenCalledWith('t3', expect.any(AbortSignal)));
  expect(screen.queryByRole('dialog', { name: 'Changes' })).toBeNull();
  await user.click(panel.getByRole('button', { name: 'Close GitLab' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'GitLab' })).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(button));
  await user.click(button);
  await screen.findByRole('dialog', { name: 'GitLab' });
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'GitLab' })).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(button));
});

test('the desktop inspector stays open while working in the conversation', async () => {
  const match = window.matchMedia.bind(window);
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => {
    const media = match(query);
    if (query === '(min-width: 1280px)') Object.defineProperty(media, 'matches', { value: true });
    return media;
  });
  vi.spyOn(api, 'gitlabPipeline').mockResolvedValue({ supported: true, auto_investigate: false, can_investigate: false });
  const { user } = renderApp('?gitidle#task=t3');
  const button = await screen.findByRole('button', { name: label });
  await user.click(button);
  await screen.findByRole('complementary', { name: 'GitLab' });
  await user.click(screen.getByRole('region', { name: 'Conversation' }));
  expect(button.getAttribute('aria-pressed')).toBe('true');
  expect(screen.getByRole('complementary', { name: 'GitLab' })).toBeTruthy();
});

test('GitLab remains available through the task menu when the header has little room', async () => {
  vi.spyOn(api, 'gitlabPipeline').mockResolvedValue({ supported: true, auto_investigate: false, can_investigate: false });
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return this.tagName === 'HEADER' ? 520 : 0;
  });
  const { user } = renderApp('?gitidle#task=t3');
  await screen.findByRole('region', { name: 'Conversation' });
  expect(screen.queryByRole('button', { name: label })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Task actions' }));
  await user.click(await screen.findByRole('menuitem', { name: label }));
  const panel = within(await screen.findByRole('dialog', { name: 'GitLab' }));
  await user.click(panel.getByRole('button', { name: 'Close GitLab' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'GitLab' })).toBeNull());
  await waitFor(() => expect(document.activeElement?.getAttribute('aria-label')).toBe('Task actions'));
});
