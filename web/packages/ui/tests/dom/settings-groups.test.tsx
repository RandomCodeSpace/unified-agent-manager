import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { MOTION_KEY, MOTION_NOTICED_KEY } from '../../src/lib/motion';
import { renderApp } from './render';

const rail = () => within(screen.getByRole('navigation', { name: 'Settings sections' }));

/** The text a control's aria-describedby points at. */
const description = (el: HTMLElement) => (el.getAttribute('aria-describedby') ?? '').split(' ').filter(Boolean).map((id) => document.getElementById(id)?.textContent).join(' ');

/** A browser whose OS asks for reduced motion, until the returned spy is restored. */
function reducedMotion() {
  const original = window.matchMedia.bind(window);
  return vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === '(prefers-reduced-motion: reduce)' ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
}

test('every rail group is named by its label, a label with spaces too', async () => {
  renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  const names = [...screen.getByRole('navigation', { name: 'Settings sections' }).querySelectorAll('[role="group"]')].map((g) => {
    // `aria-labelledby` is a list of ids: each one must exist.
    const parts = (g.getAttribute('aria-labelledby') ?? '').split(/\s+/).map((id) => document.getElementById(id)?.textContent);
    return parts.includes(undefined) ? undefined : parts.join(' ');
  });
  expect(names).toEqual(expect.arrayContaining([expect.stringMatching(/^Models & AI/), 'Agent toolkit', 'This browser']));
  expect(names.every(Boolean)).toBe(true);
});

test('Output styles is a page in its own rail group, and Find a setting finds it', async () => {
  const { user } = renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  const group = rail().getByRole('group', { name: 'Replies' });
  expect(within(group).getAllByRole('button').map((b) => b.textContent)).toEqual(['Output styles']);
  await user.click(within(group).getByRole('button', { name: 'Output styles' }));
  expect(await screen.findByRole('region', { name: 'Output styles' })).toBeTruthy();
  // Instructions no longer holds the styles card.
  await user.click(rail().getByRole('button', { name: 'Instructions', exact: true }));
  await screen.findByRole('region', { name: 'Commits & pull requests' });
  expect(screen.queryByRole('region', { name: 'Output styles' })).toBeNull();
  for (const words of ['output style', 'tone', 'simplified technical english']) {
    await user.clear(rail().getByRole('searchbox', { name: 'Find a setting' }));
    await user.type(rail().getByRole('searchbox', { name: 'Find a setting' }), words);
    expect(rail().getByRole('button', { name: 'Output styles' })).toBeTruthy();
    expect(rail().queryByRole('button', { name: 'Instructions', exact: true })).toBeNull();
  }
});

test('with the OS asking for reduced motion, Settings → Motion carries a dot until it is viewed, once', async () => {
  const media = reducedMotion();
  try {
    const { user } = renderApp('#settings');
    await screen.findByRole('heading', { level: 1, name: 'Settings' });
    expect(description(screen.getByRole('button', { name: 'Settings' }))).toContain('Your system asks for reduced motion');
    const page = rail().getByRole('button', { name: 'Appearance & alerts' });
    expect(description(page)).toBe('Your system asks for reduced motion; Motion can match it');
    await user.click(page);
    await waitFor(() => expect(page.getAttribute('aria-describedby')).toBeNull());
    expect(localStorage.getItem(MOTION_NOTICED_KEY)).toBe('1');
    // Motion stays Always on: the dot points at the choice, it does not make it.
    expect(localStorage.getItem(MOTION_KEY)).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Close settings' }));
    await waitFor(() => expect(description(screen.getByRole('button', { name: 'Settings' }))).not.toContain('reduced motion'));
  } finally {
    media.mockRestore();
  }
});

test('no Motion dot without the OS request', async () => {
  renderApp('#settings');
  await screen.findByRole('heading', { level: 1, name: 'Settings' });
  expect(rail().getByRole('button', { name: 'Appearance & alerts' }).getAttribute('aria-describedby')).toBeNull();
});

test('no Motion dot with Match system already chosen', async () => {
  localStorage.setItem(MOTION_KEY, 'system');
  const media = reducedMotion();
  try {
    renderApp('#settings');
    await screen.findByRole('heading', { level: 1, name: 'Settings' });
    expect(rail().getByRole('button', { name: 'Appearance & alerts' }).getAttribute('aria-describedby')).toBeNull();
  } finally {
    media.mockRestore();
  }
});
