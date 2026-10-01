import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

describe('routines', () => {
  test('the project filter opens a project’s routines with their next run and last outcome', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Routines of unified-agent-manager' }));
    expect(await screen.findByRole('heading', { level: 1, name: /Routines · unified-agent-manager/ })).toBeTruthy();
    const card = within(await screen.findByRole('region', { name: 'Dependency check' }));
    expect(card.getByText(/Every weekday at 09:00/)).toBeTruthy();
    // The history under the card lists the runs again, folded away.
    expect(card.getAllByText('Finished').length).toBeGreaterThan(0);
    expect(window.location.hash).toBe('#routines=p1');
    const paused = within(screen.getByRole('region', { name: 'Flaky test sweep' }));
    expect(paused.getByText('Paused')).toBeTruthy();
    expect(paused.getByText(/paused, no next run/)).toBeTruthy();
  });

  test('Run now twice: the second run is skipped while the first runs', async () => {
    const { user } = renderApp('#routines=p1');
    const card = within(await screen.findByRole('region', { name: 'Dependency check' }));
    await user.click(card.getByRole('button', { name: 'Run now' }));
    await card.findAllByText('Running');
    await user.click(card.getByRole('button', { name: 'Run now' }));
    await waitFor(() => expect(card.getAllByText('Skipped').length).toBeGreaterThan(1));
    expect(card.getAllByText('still running').length).toBeGreaterThan(1);
  });

  test('a new routine is created from the form, Yolo with its warning', async () => {
    const { user } = renderApp('#routines=p1');
    await screen.findByRole('region', { name: 'Dependency check' });
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'New routine' }));
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'Nightly smoke');
    await user.type(dialog.getByRole('textbox', { name: 'What the agent should do' }), 'Run the smoke tests and report failures.');
    await user.click(dialog.getByRole('radio', { name: 'Yolo' }));
    expect(dialog.getByText(/allows every permission request without asking/)).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Create routine' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New routine' })).toBeNull());
    const card = within(await screen.findByRole('region', { name: 'Nightly smoke' }));
    expect(card.getByText('Yolo')).toBeTruthy();
    expect(card.getByText(/Every weekday at 09:00/)).toBeTruthy();
  });

  test('Delete asks first, then removes the routine', async () => {
    const { user } = renderApp('#routines=p1');
    const card = within(await screen.findByRole('region', { name: 'Flaky test sweep' }));
    await user.click(card.getByRole('button', { name: 'Delete' }));
    const confirm = within(await screen.findByRole('alertdialog', { name: 'Delete Flaky test sweep?' }));
    await user.click(confirm.getByRole('button', { name: 'Delete routine' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Flaky test sweep' })).toBeNull());
  });
});
