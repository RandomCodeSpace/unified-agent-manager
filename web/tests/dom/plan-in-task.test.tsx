// The plan inside a Task (ADR 0005 §10): the story strip under the header, the Plan panel in the
// side-panel slot with its Outline and per-level Graph, card links that stay in the Task, adding a
// Task with no card to a story, and a header whose title keeps its room beside open panels.
import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { headerRoom } from '../../src/components/Task';
import { openMenu, openTask, renderApp, type User } from './render';

/** The story strip's button: the Task's place in its story, or the offer to add it to one. */
const strip = () => screen.queryByRole('button', { name: /^This task (works on|is not part of a story)/, hidden: true });

async function openPlan(user: User) {
  await waitFor(() => expect(strip()).toBeTruthy());
  await user.click(strip()!);
  // Below 1280px the panel is the overlay sheet, a dialog.
  return within(await screen.findByRole('dialog', { name: 'Plan' }));
}

describe('the story strip', () => {
  test('a Task working on a subtask shows its story, progress, card and what it waits for, and no next subtask while each waits', async () => {
    await openTask('t21');
    await waitFor(() => expect(strip()).toBeTruthy());
    const line = strip()!;
    expect(line.getAttribute('aria-label')).toBe('This task works on #25 Sign archives and publish signatures in Release automation › Sign release binaries. Show the plan');
    expect(line.textContent).toContain('Release automation ›');
    expect(line.textContent).toContain('Sign release binaries');
    // #23 has #24 (done), #25, #26 and the proposal #31: proposals count, and are named.
    expect(line.textContent).toContain('1/4 done · 1 proposed');
    expect(line.textContent).toContain('This task #25');
    // No next: #26 waits on #31, and every subtask here on the story's blocker #19.
    expect(line.textContent).not.toContain('next #');
    // The story waits for #19 (its own blocker); the epic for #1.
    expect(line.textContent).toContain('waits for Cross-compile the release matrix (through its story) +1');
  });

  test('a Task with no card offers to add it to a story', async () => {
    await openTask('t20');
    await waitFor(() => expect(strip()).toBeTruthy());
    expect(strip()!.textContent).toBe('Not part of a storyAdd to a story');
  });

  test('a Project with no plan shows no strip and no Plan button', async () => {
    await openTask('t21');
    const project = await api.createProject({ dir: '/home/user/projects/empty-plan' });
    const task = await api.createSession({ project_id: project.id, provider: 'copilot', request_id: 'no-plan', prompt: 'Look around' });
    await act(async () => {
      window.location.hash = `#task=${task.id}`;
    });
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).not.toBe('Sign the release archives'));
    await screen.findByRole('region', { name: 'Conversation' });
    expect(strip()).toBeNull();
    expect(screen.queryByRole('button', { name: 'Plan' })).toBeNull();
  });

  test('no Task carries the Planner pop-out any more', async () => {
    await openTask('t21');
    await waitFor(() => expect(strip()).toBeTruthy());
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Show the planner/ })).toBeNull();
  });
});

describe('the Plan panel', () => {
  test('the strip opens it on the Task’s card: its story open, its details in place, and the header button pressed', async () => {
    const { user } = await openTask('t21');
    const panel = await openPlan(user);
    expect(screen.getByRole('button', { name: 'Plan', hidden: true }).getAttribute('aria-pressed')).toBe('true');
    const here = within(panel.getByRole('region', { name: 'This task works on' }));
    expect(here.getByText('Release automation › Sign release binaries')).toBeTruthy();
    expect(here.getByText(/Waits for/).textContent).toContain('#19 Cross-compile the release matrix (through its story)');
    const outline = within(panel.getByRole('list', { name: 'Plan outline' }));
    expect(outline.getByRole('button', { name: /^#23 Sign release binaries/ }).textContent).toContain('1/4 · 1 proposed');
    const mine = outline.getByRole('button', { name: '#25 Sign archives and publish signatures, In progress, this task' });
    expect(mine.getAttribute('aria-expanded')).toBe('true');
    const details = within(panel.getByRole('region', { name: '#25 details' }));
    expect(details.getByText(/Every release archive has a .sig file/)).toBeTruthy();
    expect(details.getByText('In progress: release it to change the plan.')).toBeTruthy();
    expect(details.getByRole('region', { name: 'Checklist 1/3' })).toBeTruthy();
    // A started subtask keeps its plan: no Edit, no Launch.
    expect(details.queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(details.queryByRole('button', { name: 'Launch' })).toBeNull();
    // Another card opens in place, with its dependencies at its own level and through its story.
    await user.click(outline.getByRole('button', { name: /^#26 Verify signatures in the install script/ }));
    const other = within(panel.getByRole('region', { name: '#26 details' }));
    const deps = within(other.getByRole('region', { name: 'Dependencies' }));
    expect(deps.getByText('Waits for')).toBeTruthy();
    expect(deps.getByText('Waits for, through its story #23')).toBeTruthy();
    expect(other.getByRole('button', { name: 'Edit' })).toBeTruthy();
    expect(other.getByRole('button', { name: 'Launch' })).toBeTruthy();
    expect(panel.queryByRole('region', { name: '#25 details' })).toBeNull();
    await user.click(panel.getByRole('button', { name: 'Close plan' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Plan' })).toBeNull());
    expect(screen.getByRole('button', { name: 'Plan' }).getAttribute('aria-pressed')).toBe('false');
  });

  test('a proposal opens with Discard, and launching it goes through the confirm step', async () => {
    const { user } = await openTask('t21');
    const panel = await openPlan(user);
    const outline = within(panel.getByRole('list', { name: 'Plan outline' }));
    await user.click(outline.getByRole('button', { name: /^#31 Publish SHA-256 checksums beside the archives, Planned, proposed/ }));
    const details = within(panel.getByRole('region', { name: '#31 details' }));
    expect(details.getByText('A proposal: editing and linking keep it one; launching it confirms it.')).toBeTruthy();
    const dismiss = vi.spyOn(api.planner, 'dismiss');
    try {
      await user.click(details.getByRole('button', { name: 'Launch' }));
      const dialog = within(await screen.findByRole('dialog', { name: 'Confirm and launch #31?' }));
      await user.click(dialog.getByRole('button', { name: 'Keep' }));
      await user.click(details.getByRole('button', { name: 'Discard' }));
      expect(dismiss).toHaveBeenCalledWith('cp1-31');
    } finally {
      dismiss.mockRestore();
    }
  });

  test('the Graph shows one level at a time, from this Task’s story up to the Project and down again', async () => {
    const { user } = await openTask('t21');
    const panel = await openPlan(user);
    await user.click(panel.getByRole('radio', { name: 'Graph' }));
    const crumbs = within(panel.getByRole('navigation', { name: 'Plan level' }));
    expect(crumbs.getByText('#23 Sign release binaries').getAttribute('aria-current')).toBe('location');
    expect(panel.getByText(/This whole story waits for/).textContent).toContain('#19 Cross-compile the release matrix');
    const graph = within(panel.getByRole('group', { name: 'Dependencies between the subtasks' }));
    expect(graph.getByRole('button', { name: /^#25 Sign archives and publish signatures, In progress, this task/ })).toBeTruthy();
    expect(panel.getByText(/^\d+ subtasks ·/).textContent).toContain('1/4 done · 1 proposed');
    // Plain SVG: no HTML inside the drawing.
    expect(panel.getByRole('group', { name: 'Dependencies between the subtasks' }).querySelector('foreignObject')).toBeNull();
    // A subtask opens its details under the graph.
    await user.click(graph.getByRole('button', { name: /^#26 Verify signatures/ }));
    expect(panel.getByRole('region', { name: '#26 details' })).toBeTruthy();
    // Up to the Project: its epics, #18 waiting for #1.
    await user.click(crumbs.getByRole('button', { name: 'unified-agent-manager' }));
    const epics = within(panel.getByRole('group', { name: 'Dependencies between the epics' }));
    expect(epics.getAllByRole('button').map((b) => b.getAttribute('aria-label')?.split(',')[0])).toEqual(['#1 Faster first load of long transcripts', '#18 Release automation']);
    // A container opens its level.
    await user.click(epics.getByRole('button', { name: /^#18 Release automation/ }));
    expect(within(panel.getByRole('navigation', { name: 'Plan level' })).getByText('#18 Release automation').getAttribute('aria-current')).toBe('location');
    expect(panel.getByRole('group', { name: 'Dependencies between the stories' })).toBeTruthy();
    expect(within(panel.getByRole('group', { name: 'Dependencies between the stories' })).getByRole('button', { name: /^#23 Sign release binaries, 1\/4 done · 1 proposed\. Show its subtasks$/ })).toBeTruthy();
    expect(panel.getByText(/This whole epic waits for/).textContent).toContain('#1 Faster first load of long transcripts');
  });
});

describe('card links inside a Task', () => {
  test('a transcript card chip opens the card in the Plan panel and the Task stays', async () => {
    const { user } = await openTask('t21');
    await waitFor(() => expect(strip()).toBeTruthy());
    await user.click(await screen.findByRole('button', { name: /4 tools/ }));
    await user.click(await screen.findByRole('button', { name: /^#26 Verify signatures in the install script$/ }));
    const panel = within(await screen.findByRole('dialog', { name: 'Plan' }));
    expect(await panel.findByRole('region', { name: '#26 details' })).toBeTruthy();
    expect(window.location.hash).toBe('#task=t21');
    expect(screen.getByRole('region', { name: 'Conversation', hidden: true })).toBeTruthy();
    expect(screen.queryByRole('tree', { name: 'Plan outline' })).toBeNull();
  });
});

describe('adding a Task to a story', () => {
  afterEach(() => vi.restoreAllMocks());

  test('the strip’s offer adds the Task under a story, asking first under a proposal', async () => {
    const { user } = await openTask('t20');
    const attach = vi.spyOn(api.planner, 'attach');
    const panel = await openPlan(user);
    const box = within(panel.getByRole('region', { name: 'Add this task to a story' }));
    await user.click(box.getByRole('combobox', { name: 'Story' }));
    await user.click(await screen.findByRole('option', { name: 'Faster first load of long transcripts › #16 Lazy-load the diagram renderer (proposed)' }));
    expect((box.getByRole('textbox', { name: 'New subtask title' }) as HTMLInputElement).value).toBe('Replay focus events on re-attach');
    await user.click(box.getByRole('button', { name: 'Add to story' }));
    const dialog = within(await screen.findByRole('alertdialog', { name: 'Confirm and add this task?' }));
    expect(within(dialog.getByRole('list', { name: 'Adding confirms' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['#16 Lazy-load the diagram renderer']);
    await user.click(dialog.getByRole('button', { name: 'Confirm and add' }));
    expect(attach).toHaveBeenCalledWith('cp1-16', { task_id: 't20', title: 'Replay focus events on re-attach', confirm: true });
    const here = within(await panel.findByRole('region', { name: 'This task works on' }));
    expect(here.getByText('Faster first load of long transcripts › Lazy-load the diagram renderer')).toBeTruthy();
    await waitFor(() => expect(strip()!.textContent).toContain('This task #'));
    expect(strip()!.textContent).toContain('Lazy-load the diagram renderer');
  });

  test('a confirmed story adds the Task at once, to a subtask not started yet', async () => {
    const { user } = await openTask('t20');
    const attach = vi.spyOn(api.planner, 'attach');
    const panel = await openPlan(user);
    const box = within(panel.getByRole('region', { name: 'Add this task to a story' }));
    await user.click(box.getByRole('combobox', { name: 'Story' }));
    await user.click(await screen.findByRole('option', { name: 'Release automation › #27 Changelog from merged pull requests' }));
    await user.click(box.getByRole('combobox', { name: 'Work on' }));
    await user.click(await screen.findByRole('option', { name: '#30 Link each entry to its pull request' }));
    expect(box.queryByRole('textbox', { name: 'New subtask title' })).toBeNull();
    await user.click(box.getByRole('button', { name: 'Add to story' }));
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(attach).toHaveBeenCalledWith('cp1-30', { task_id: 't20', title: undefined, confirm: false });
    await waitFor(() => expect(strip()!.textContent).toContain('This task #30'));
    // The outline opens the story it joined, with the card's details.
    expect(await panel.findByRole('region', { name: '#30 details' })).toBeTruthy();
    expect(panel.getByRole('button', { name: /^#30 Link each entry to its pull request, .*this task$/ })).toBeTruthy();
  });
});

describe('the Task header', () => {
  test('narrow, it drops the button labels, then folds Files and Terminal into the menu; the title keeps its room', async () => {
    expect(headerRoom(0)).toBe('wide');
    expect(headerRoom(1100)).toBe('wide');
    expect(headerRoom(720)).toBe('snug');
    expect(headerRoom(520)).toBe('tight');
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return this.tagName === 'HEADER' ? 520 : 0;
    });
    try {
      const { user } = renderApp('#task=t21');
      await screen.findByRole('region', { name: 'Conversation' });
      const header = screen.getByRole('heading', { level: 1 }).closest('header')!;
      expect(screen.getByRole('heading', { level: 1 }).parentElement!.className).toContain('min-w-28');
      expect(within(header).queryByRole('button', { name: 'Browse files' })).toBeNull();
      expect(within(header).getByRole('button', { name: /^Open changes/ }).textContent).not.toContain('Changes');
      const menu = await openMenu(user, 'Task actions');
      expect(menu.getByRole('menuitem', { name: 'Browse files' })).toBeTruthy();
    } finally {
      width.mockRestore();
    }
  });
});
