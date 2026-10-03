// The UAM wordmark sits beside the brand mark, and the tab title says "UAM - <what the pane shows>".
import { render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { Login } from '../../src/components/Login';
import { renderApp, sidebar } from './render';

describe('the UAM wordmark', () => {
  test('the sidebar toggle and Home show the mark with UAM; the collapsed rail shows the mark alone', async () => {
    const { user } = renderApp('?planner=unset');
    const side = await sidebar();
    const hide = side.getByRole('button', { name: 'Hide sidebar' });
    expect(hide.querySelector('svg')).toBeTruthy();
    expect(hide.textContent).toBe('UAM');
    // The planner's button is not the brand.
    expect(screen.queryByRole('button', { name: 'Planner' })?.textContent ?? '').not.toContain('UAM');
    const home = within(await screen.findByRole('region', { name: 'Home' }));
    const brand = home.getByText('UAM');
    expect(brand.parentElement!.querySelector('svg')).toBeTruthy();
    await user.click(hide);
    const rail = within(await screen.findByRole('navigation', { name: 'Sidebar' }));
    const show = rail.getByRole('button', { name: /^Show sidebar/ });
    expect(show.querySelector('svg')).toBeTruthy();
    expect(show.textContent).not.toContain('UAM');
  });

  test('the sign-in page shows the mark with UAM', () => {
    render(<Login onLoggedIn={() => {}} />);
    const heading = screen.getByRole('heading', { level: 1, name: 'Sign in to UAM' });
    const brand = heading.previousElementSibling!;
    expect(brand.querySelector('svg')).toBeTruthy();
    expect(brand.textContent).toBe('UAM');
  });
});

describe('the tab title', () => {
  test('is UAM on the home screen and UAM - <name> for an open Task, after the Needs you count', async () => {
    const { user } = renderApp('?planner=unset');
    const side = await sidebar();
    await waitFor(() => expect(document.title).toBe('(7) UAM'));
    await user.click(side.getByRole('button', { name: /, Doctor: add terminal line/ }));
    await waitFor(() => expect(document.title).toBe('(7) UAM - Doctor: add terminal line'));
  });

  test('a Task still waiting for a title, and a new Task, read UAM - New task', async () => {
    const { user } = renderApp('?planner=unset#task=t7');
    const side = await sidebar();
    await waitFor(() => expect(document.title).toBe('(7) UAM - New task'));
    await user.click(side.getByRole('button', { name: /, Doctor: add terminal line/ }));
    await waitFor(() => expect(document.title).toBe('(7) UAM - Doctor: add terminal line'));
    await user.click(side.getByRole('button', { name: 'New task' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /notes-site/ }));
    await waitFor(() => expect(document.title).toBe('(7) UAM - New task'));
  });

  test('names Settings, Routines and the planner', async () => {
    const { user } = renderApp('#settings');
    await sidebar();
    await waitFor(() => expect(document.title).toBe('(13) UAM - Settings'));
    await user.click(screen.getByRole('button', { name: 'Settings' }));
    await waitFor(() => expect(document.title).toBe('(13) UAM'));
    await user.click(screen.getByRole('button', { name: 'Routines' }));
    await waitFor(() => expect(document.title).toBe('(13) UAM - Routines'));
    await user.click(screen.getByRole('button', { name: 'Planner' }));
    await waitFor(() => expect(document.title).toBe('(13) UAM - Planner'));
  });
});
