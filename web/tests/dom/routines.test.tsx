import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

describe('routines', () => {
  test('the project filter opens a project’s routines with their next run and last outcome', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Routines of unified-agent-manager' }));
    expect(await screen.findByRole('heading', { level: 1, name: 'Routines' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Project: unified-agent-manager' })).toBeTruthy();
    const card = within(await screen.findByRole('region', { name: 'Dependency check' }));
    // Filtered to one Project: another Project's routines are not listed.
    expect(screen.queryByRole('region', { name: 'Broken link check' })).toBeNull();
    expect(card.getByText(/Every weekday at 09:00/)).toBeTruthy();
    // The history under the card lists the runs again, folded away.
    expect(card.getAllByText('Finished').length).toBeGreaterThan(0);
    expect(window.location.hash).toBe('#routines=p1');
    const paused = within(screen.getByRole('region', { name: 'Flaky test sweep' }));
    expect(paused.getByText('Paused')).toBeTruthy();
    expect(paused.getByText(/paused, no next run/)).toBeTruthy();
  });

  // Whether a firing runs or is skipped is the service's rule (routines_test.go); the mock stands in for it. This checks the
  // card: Run now posts, and each run the reply records shows at once with its outcome and reason.
  test('Run now shows each recorded run on the card, a skip with its reason', async () => {
    const { user } = renderApp('#routines=p1');
    const card = within(await screen.findByRole('region', { name: 'Dependency check' }));
    await user.click(card.getByRole('button', { name: 'Run now' }));
    await card.findAllByText('Running');
    await user.click(card.getByRole('button', { name: 'Run now' }));
    await waitFor(() => expect(card.getAllByText('Skipped').length).toBeGreaterThan(1));
    expect(card.getAllByText('still running').length).toBeGreaterThan(1);
  });

  test('a new routine is Yolo with autopilot unless chosen otherwise, with the risk spelled out', async () => {
    const { user } = renderApp('#routines=p1');
    await screen.findByRole('region', { name: 'Dependency check' });
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'New routine' }));
    expect((dialog.getByRole('radio', { name: 'Yolo with autopilot' }) as HTMLElement).getAttribute('aria-checked')).toBe('true');
    expect(dialog.getByText(/keeps working until the task is done or the time limit stops it/)).toBeTruthy();
    expect(dialog.getByText(/acts without asking while nobody is watching/)).toBeTruthy();
    await user.click(dialog.getByRole('radio', { name: 'Safe' }));
    expect(dialog.getByText(/waits for you/)).toBeTruthy();
    expect(dialog.queryByText(/acts without asking/)).toBeNull();
    await user.click(dialog.getByRole('radio', { name: 'Yolo with autopilot' }));
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'Nightly smoke');
    await user.type(dialog.getByRole('textbox', { name: 'What the agent should do' }), 'Run the smoke tests and report failures.');
    await user.click(dialog.getByRole('button', { name: 'Create routine' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New routine' })).toBeNull());
    const card = within(await screen.findByRole('region', { name: 'Nightly smoke' }));
    expect(card.getByText('Yolo with autopilot')).toBeTruthy();
    expect(card.getByText(/· Yolo with autopilot ·/)).toBeTruthy();
    expect(card.getByText(/Every weekday at 09:00/)).toBeTruthy();
  });

  test('a routine saved before autopilot keeps its mode, shown on its card and in its form', async () => {
    const { user } = renderApp('#routines=p1');
    const safe = within(await screen.findByRole('region', { name: 'Dependency check' }));
    expect(safe.getByText(/· Safe ·/)).toBeTruthy();
    const yolo = within(screen.getByRole('region', { name: 'Flaky test sweep' }));
    expect(yolo.getByText('Yolo')).toBeTruthy();
    expect(yolo.queryByText(/autopilot/)).toBeNull();
    await user.click(yolo.getByRole('button', { name: 'Edit' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Edit routine' }));
    expect((dialog.getByRole('radio', { name: 'Yolo' }) as HTMLElement).getAttribute('aria-checked')).toBe('true');
    expect(dialog.getByText(/for one turn/)).toBeTruthy();
  });

  test('the sidebar’s Routines lists every project’s routines under its name, and closes again', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    const button = side.getByRole('button', { name: 'Routines' });
    await user.click(button);
    expect(await screen.findByRole('heading', { level: 1, name: 'Routines' })).toBeTruthy();
    expect(window.location.hash).toBe('#routines');
    expect(button.getAttribute('aria-pressed')).toBe('true');
    const uam = within(await screen.findByRole('region', { name: 'unified-agent-manager' }));
    expect(uam.getByRole('heading', { level: 3, name: 'Dependency check' })).toBeTruthy();
    expect(uam.getByRole('region', { name: 'Flaky test sweep' })).toBeTruthy();
    const notes = within(screen.getByRole('region', { name: 'notes-site' }));
    expect(notes.getByRole('region', { name: 'Broken link check' })).toBeTruthy();
    // A Project without routines gets no heading.
    expect(screen.queryByRole('region', { name: 'dotfiles' })).toBeNull();
    await user.click(button);
    await waitFor(() => expect(screen.queryByRole('heading', { level: 1, name: 'Routines' })).toBeNull());
    expect(button.getAttribute('aria-pressed')).toBe('false');
  });

  test('the collapsed rail has Routines too', async () => {
    localStorage.setItem('uam.sidebar', 'false');
    const { user } = renderApp();
    const rail = within(await screen.findByRole('navigation', { name: 'Sidebar' }));
    await user.click(await rail.findByRole('button', { name: 'Routines' }));
    expect(await screen.findByRole('region', { name: 'notes-site' })).toBeTruthy();
    expect(window.location.hash).toBe('#routines');
  });

  test('the project filter narrows the list, kept in the URL', async () => {
    const { user } = renderApp('#routines=all');
    await screen.findByRole('region', { name: 'Broken link check' });
    await user.click(screen.getByRole('button', { name: 'Project: All projects' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /notes-site/ }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Dependency check' })).toBeNull());
    expect(screen.getByRole('region', { name: 'Broken link check' })).toBeTruthy();
    expect(window.location.hash).toBe('#routines=p3');
    await user.click(screen.getByRole('button', { name: 'Project: notes-site' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /All projects/ }));
    expect(await screen.findByRole('region', { name: 'Dependency check' })).toBeTruthy();
    expect(window.location.hash).toBe('#routines');
  });

  test('a project’s own New routine creates it there, with no project to choose', async () => {
    const { user } = renderApp('#routines');
    await screen.findByRole('region', { name: 'notes-site' });
    await user.click(screen.getByRole('button', { name: 'New routine in notes-site' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'New routine' }));
    expect(dialog.getByText('Each run starts a task in notes-site.')).toBeTruthy();
    expect(dialog.queryByRole('combobox', { name: 'Project' })).toBeNull();
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'Spell check');
    await user.type(dialog.getByRole('textbox', { name: 'What the agent should do' }), 'Check the spelling of every page.');
    await user.click(dialog.getByRole('button', { name: 'Create routine' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New routine' })).toBeNull());
    expect(await within(screen.getByRole('region', { name: 'notes-site' })).findByRole('region', { name: 'Spell check' })).toBeTruthy();
  });

  test('with no routines anywhere, the view says what one is and New routine asks for the project', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    for (const id of ['r1', 'r2', 'r3']) await fetch(`/api/routines/${id}`, { method: 'DELETE' });
    await user.click(side.getByRole('button', { name: 'Routines' }));
    expect(await screen.findByRole('heading', { name: 'No routines yet' })).toBeTruthy();
    expect(screen.getByText(/A routine starts a task in a project on a schedule/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'New routine' }));
    expect(dialog.getByRole('combobox', { name: 'Project' })).toBeTruthy();
  });

  test('a #routines link opened while the page is open shows the routines', async () => {
    renderApp();
    await sidebar();
    window.location.hash = '#routines=p3';
    expect(await screen.findByRole('button', { name: 'Project: notes-site' })).toBeTruthy();
    expect(await screen.findByRole('region', { name: 'Broken link check' })).toBeTruthy();
  });

  test('Edit project → Routines opens that project’s routines', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Edit notes-site' }));
    const edit = within(await screen.findByRole('dialog', { name: /Edit project/ }));
    await user.click(edit.getByRole('button', { name: 'Routines' }));
    expect(await screen.findByRole('button', { name: 'Project: notes-site' })).toBeTruthy();
    expect(await screen.findByRole('region', { name: 'Broken link check' })).toBeTruthy();
    expect(window.location.hash).toBe('#routines=p3');
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
