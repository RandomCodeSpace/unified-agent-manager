// Planner UI fixes from an end-to-end run: the blocked mark, a proposed acceptance command, the
// card editor's fields, Check at HEAD, the Board's empty state and Paused marker, opening a
// proposal from the Inbox, the Approve dialog's effort, and the Release and split copy.
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api, type Card } from '../../src/api';
import { openMenu, renderApp, sidebar, type User } from './render';

/** Opens the planner from the sidebar and waits for unified-agent-manager's outline. */
async function openPlanner() {
  const rendered = renderApp();
  const side = await sidebar();
  await rendered.user.click(side.getByRole('button', { name: 'Planner' }));
  const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
  await tree.findByRole('treeitem', { name: /^#1 Faster first load of long transcripts/ });
  return { ...rendered, tree };
}

/** Opens notes-site's plan, which has no acceptance command. */
async function openNotes() {
  const rendered = renderApp('#planner=p3');
  const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
  await tree.findByRole('treeitem', { name: '#32 Accessible post template, Doing' });
  return { ...rendered, tree };
}

/** Picks `option` in the labelled select. */
async function pick(user: User, within_: ReturnType<typeof within>, name: string, option: string | RegExp) {
  await user.click(within_.getByRole('combobox', { name }));
  await user.click(await screen.findByRole('option', { name: option }));
}

/** Opens a card's detail from the Tree. */
async function openCard(user: User, tree: ReturnType<typeof within>, seq: number) {
  await user.click(tree.getByRole('treeitem', { name: new RegExp(`^#${seq} `) }));
  return within(await screen.findByLabelText(`Card #${seq}`));
}

/** Closes the card detail: below 1280px it is an overlay, and the Tree under it is hidden while it is open. */
async function closeCard(user: User) {
  await user.click(screen.getByRole('button', { name: 'Close card' }));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Close card' })).toBeNull());
}

/** Answers the requests `match` picks with `answer`; every other request goes to the mock. Call it after renderApp. */
function serviceReply(match: (url: string, method: string) => boolean, answer: (real: () => Promise<Response>) => Promise<Response>) {
  const real = window.fetch;
  return vi.spyOn(window, 'fetch').mockImplementation((input, init) => {
    const url = String(input instanceof Request ? input.url : input);
    return match(url, init?.method ?? 'GET') ? answer(() => real(input, init)) : real(input, init);
  });
}

const reply = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));

/**
 * notes-site's Board as an agent's plan: one proposed epic with one proposed subtask, nothing
 * confirmed, and with `cancelled` a confirmed subtask at the root that was cancelled.
 */
function proposalsOnly(cancelled = false) {
  return serviceReply(
    (url, method) => method === 'GET' && url.includes('/api/board?project_id=p3'),
    async (real) => {
      const data = await (await real()).json();
      const base = data.cards.find((c: Card) => c.seq === 35);
      const expires_at = new Date(Date.now() + 14 * 86_400_000).toISOString();
      const epic = { ...base, id: 'cp3-90', seq: 90, kind: 'epic', parent_id: null, rank: 99, title: 'Offline reading', status: 'planned', progress: { done: 0, total: 1, proposed: 1 }, confirmed: false, expires_at, pinned_sha: '' };
      const leaf = { ...base, id: 'cp3-91', seq: 91, parent_id: 'cp3-90', rank: 0, title: 'Cache posts', status: 'planned', confirmed: false, expires_at, pinned_sha: '' };
      const old = { ...base, id: 'cp3-92', seq: 92, parent_id: null, rank: 98, title: 'Print stylesheet', status: 'cancelled' };
      return reply(200, { ...data, cards: cancelled ? [epic, leaf, old] : [epic, leaf] });
    },
  );
}

describe('the blocked mark', () => {
  test('a card marked blocked says what clearing does, and Clear blocked mark clears it', async () => {
    const { user, tree } = await openPlanner();
    await act(() => api.planner.edit('cp1-22', { blocked: true }));
    const edit = vi.spyOn(api.planner, 'edit');
    try {
      const panel = await openCard(user, tree, 22);
      expect(panel.getAllByText('Marked blocked').length).toBeGreaterThan(0);
      expect(panel.getByText(/^Marked blocked: its Task can't finish it/)).toBeTruthy();
      await user.click(panel.getByRole('button', { name: 'Clear blocked mark' }));
      await waitFor(() => expect(edit).toHaveBeenCalledWith('cp1-22', { blocked: false }));
      await waitFor(() => expect(panel.queryByText('Marked blocked')).toBeNull());
      expect(panel.queryByRole('button', { name: 'Clear blocked mark' })).toBeNull();
      expect((await api.planner.board('p1')).cards.find((c) => c.seq === 22)!.blocked).toBe(false);
    } finally {
      edit.mockRestore();
    }
  });

  test('a story marked blocked says the mark holds nothing back there, and a done subtask has no note', async () => {
    const { user, tree } = await openPlanner();
    await act(() => api.planner.edit('cp1-19', { blocked: true }));
    await act(() => api.planner.edit('cp1-3', { blocked: true }));
    const story = await openCard(user, tree, 19);
    expect(story.getByText('Marked blocked: on a story the mark holds nothing back.')).toBeTruthy();
    expect(story.queryByText(/its Task can't finish it/)).toBeNull();
    expect(story.getByRole('button', { name: 'Clear blocked mark' })).toBeTruthy();
    await closeCard(user);
    const done = await openCard(user, tree, 3);
    expect(done.getAllByText('Marked blocked').length).toBeGreaterThan(0);
    expect(done.queryByText(/^Marked blocked:/)).toBeNull();
    expect(done.getByRole('button', { name: 'Clear blocked mark' })).toBeTruthy();
  });

  test('a row’s menu offers Clear blocked mark only while the card is marked', async () => {
    const { user } = await openPlanner();
    const items = async () => (await openMenu(user, 'Actions for #22')).getAllByRole('menuitem').map((i) => i.textContent);
    expect(await items()).not.toContain('Clear blocked mark');
    await user.keyboard('{Escape}');
    await act(() => api.planner.edit('cp1-22', { blocked: true }));
    expect(await items()).toContain('Clear blocked mark');
  });
});

describe('a proposed acceptance command', () => {
  test('a done request shows it in the Inbox and the evidence trail, and Apply makes it the subtask’s command', async () => {
    const { user, tree } = await openPlanner();
    const edit = vi.spyOn(api.planner, 'edit');
    try {
      await user.click(screen.getByRole('button', { name: /^Inbox, \d+ pending$/ }));
      const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
      const request = within(inbox.getByRole('article', { name: 'Done request on #8' }));
      expect(request.getByText('Proposed acceptance command')).toBeTruthy();
      // Line breaks and runs of spaces show as Apply stores them.
      expect(request.getByText('go test ./internal/web/...').classList.contains('whitespace-pre-wrap')).toBe(true);
      await user.click(request.getByRole('button', { name: 'Apply' }));
      await waitFor(() => expect(edit).toHaveBeenCalledWith('cp1-8', { accept_cmd: 'go test ./internal/web/...' }));
      // Applied, the row says the command is the subtask's now.
      expect(await request.findByText('In use')).toBeTruthy();
      expect(request.queryByRole('button', { name: 'Apply' })).toBeNull();
      await user.click(screen.getByRole('button', { name: 'Close inbox' }));
      const panel = await openCard(user, tree, 8);
      const trail = within(await panel.findByRole('region', { name: 'Evidence trail' }));
      const row = within(await trail.findByRole('article', { name: 'Done request on #8' }));
      expect(row.getByText('go test ./internal/web/...')).toBeTruthy();
      expect(row.getByText('In use')).toBeTruthy();
    } finally {
      edit.mockRestore();
    }
  });

  test('Apply shows it is working and leaves an open Reject form as it is', async () => {
    const { user } = await openPlanner();
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const spy = serviceReply((url, method) => method === 'PATCH' && url.endsWith('/api/board/cards/cp1-8'), async (real) => {
      await gate;
      return real();
    });
    try {
      await user.click(screen.getByRole('button', { name: /^Inbox, \d+ pending$/ }));
      const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
      const request = within(inbox.getByRole('article', { name: 'Done request on #8' }));
      await user.click(request.getByRole('button', { name: 'Reject' }));
      await user.type(request.getByRole('textbox', { name: 'Reason' }), 'Not yet');
      const apply = request.getByRole('button', { name: 'Apply' });
      await user.click(apply);
      await waitFor(() => expect(apply.getAttribute('aria-busy')).toBe('true'));
      release();
      expect(await request.findByText('In use')).toBeTruthy();
      expect((request.getByRole('textbox', { name: 'Reason' }) as HTMLInputElement).value).toBe('Not yet');
    } finally {
      release();
      spy.mockRestore();
    }
  });

  test('Apply shows the service’s refusal', async () => {
    const { user } = await openPlanner();
    const spy = serviceReply((url, method) => method === 'PATCH' && url.endsWith('/api/board/cards/cp1-8'), () => reply(400, { error: '#8 is cancelled; restore it first', code: 'invalid' }));
    try {
      await user.click(screen.getByRole('button', { name: /^Inbox, \d+ pending$/ }));
      const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
      const request = within(inbox.getByRole('article', { name: 'Done request on #8' }));
      await user.click(request.getByRole('button', { name: 'Apply' }));
      // The planner's notice, as for Accept and Reject; at this width the Inbox overlay hides it from the accessibility tree.
      expect((await screen.findByRole('alert', { hidden: true })).textContent).toContain('Could not apply the proposed acceptance command: #8 is cancelled; restore it first');
      expect(request.getByRole('button', { name: 'Apply' })).toBeTruthy();
    } finally {
      spy.mockRestore();
    }
  });
});

describe('the card editor', () => {
  test('the card panel shows the priority and edits description, effort, priority and labels', async () => {
    const { user, tree } = await openPlanner();
    const edit = vi.spyOn(api.planner, 'edit');
    try {
      expect((await openCard(user, tree, 1)).getByText('Priority High')).toBeTruthy();
      await closeCard(user);
      const panel = await openCard(user, tree, 15);
      expect(panel.getByText('Priority Low')).toBeTruthy();
      await user.click(panel.getByRole('button', { name: 'Edit card' }));
      const form = within(panel.getByRole('form', { name: 'Edit #15' }));
      await user.type(form.getByRole('textbox', { name: 'Description' }), 'Use the long fixture.');
      await pick(user, form, 'Effort', 'Effort M');
      await pick(user, form, 'Priority', 'Priority High');
      await user.type(form.getByRole('textbox', { name: 'Labels' }), 'tests  web');
      await user.click(form.getByRole('button', { name: 'Save' }));
      await waitFor(() =>
        expect(edit).toHaveBeenCalledWith('cp1-15', {
          desc: 'Use the long fixture.',
          effort: 'M',
          prio: 1,
          labels: ['tests', 'web'],
        }),
      );
      expect(await panel.findByText('Priority High')).toBeTruthy();
      expect(panel.getByText('Effort M')).toBeTruthy();
      expect(panel.getByText('tests')).toBeTruthy();
      expect(panel.getByText('Use the long fixture.')).toBeTruthy();
    } finally {
      edit.mockRestore();
    }
  });

  test('a save sends only the fields the owner changed, so an edit made meanwhile stays', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    await user.click(panel.getByRole('button', { name: 'Edit card' }));
    const form = within(panel.getByRole('form', { name: 'Edit #15' }));
    // An agent edits the card while the editor is open.
    await act(() => api.planner.edit('cp1-15', { win_condition: 'Agreed by an agent.', desc: 'Written by an agent.', prio: 2, labels: ['agent'] }));
    const edit = vi.spyOn(api.planner, 'edit');
    try {
      await user.type(form.getByRole('textbox', { name: 'Title' }), ' first');
      await user.click(form.getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(edit).toHaveBeenCalledWith('cp1-15', { title: 'Cover anchoring with a DOM test first' }));
      const saved = (await api.planner.board('p1')).cards.find((c) => c.seq === 15)!;
      expect([saved.win_condition, saved.desc, saved.prio, saved.labels]).toEqual(['Agreed by an agent.', 'Written by an agent.', 2, ['agent']]);
    } finally {
      edit.mockRestore();
    }
  });

  test('a save with nothing changed sends nothing and closes the editor', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    await user.click(panel.getByRole('button', { name: 'Edit card' }));
    const edit = vi.spyOn(api.planner, 'edit');
    try {
      await user.click(within(panel.getByRole('form', { name: 'Edit #15' })).getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(panel.queryByRole('form', { name: 'Edit #15' })).toBeNull());
      expect(edit).not.toHaveBeenCalled();
    } finally {
      edit.mockRestore();
    }
  });
});

describe('Check at HEAD', () => {
  test('a subtask with no acceptance command, its own or its project’s, has Check at HEAD off with why', async () => {
    const { user, tree } = await openNotes();
    const panel = await openCard(user, tree, 35);
    const check = (await panel.findByRole('button', { name: 'Check at HEAD' })) as HTMLButtonElement;
    await waitFor(() => expect(check.disabled).toBe(true));
    expect(panel.getByText("#35 has no acceptance command: set one under Owner only, or as the project's default.")).toBeTruthy();
    // Its own command turns it on.
    await act(() => api.planner.edit('cp3-35', { accept_cmd: 'npm test' }));
    await waitFor(() => expect(check.disabled).toBe(false));
  });

  test('a subtask that inherits its project’s command keeps Check at HEAD on', async () => {
    const project = vi.spyOn(api.planner, 'project');
    try {
      const { user, tree } = await openPlanner();
      const panel = await openCard(user, tree, 22);
      // The project's settings, read with the view, name its default command.
      await waitFor(() => expect(project).toHaveBeenCalledWith('p1'));
      await act(() => Promise.all(project.mock.results.map((r) => r.value)));
      expect(((await panel.findByRole('button', { name: 'Check at HEAD' })) as HTMLButtonElement).disabled).toBe(false);
    } finally {
      project.mockRestore();
    }
  });

  test('saving the project’s command in the Approve dialog turns Check at HEAD on', async () => {
    const { user, tree } = await openNotes();
    let panel = await openCard(user, tree, 35);
    await waitFor(() => expect((panel.getByRole('button', { name: 'Check at HEAD' }) as HTMLButtonElement).disabled).toBe(true));
    await closeCard(user);
    await user.click((await openMenu(user, 'Actions for #32')).getByRole('menuitem', { name: 'Approve and run…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Approve #32?' }));
    // The dialog reads the project's settings: none yet.
    await dialog.findByText('None');
    await user.click(dialog.getByRole('button', { name: 'Edit' }));
    await user.type(dialog.getByRole('textbox', { name: 'Project acceptance command' }), 'npm test');
    await user.click(dialog.getByRole('button', { name: 'Save' }));
    expect(await dialog.findByText('npm test')).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Approve #32?' })).toBeNull());
    panel = await openCard(user, tree, 35);
    expect((panel.getByRole('button', { name: 'Check at HEAD' }) as HTMLButtonElement).disabled).toBe(false);
  });
});

describe('the Board', () => {
  test('a plan of proposals only says they stay in the Tree, not that filters hide them', async () => {
    const { user } = renderApp('#planner=p3');
    const spy = proposalsOnly();
    try {
      const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
      await tree.findByRole('treeitem', { name: '+1 suggested' });
      await user.click(screen.getByRole('radio', { name: 'Board' }));
      expect(await screen.findByText('Only proposals so far: they stay in the Tree until you confirm them or approve their epic.')).toBeTruthy();
      expect(screen.queryByText('No subtasks match the filters.')).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  test('proposals beside subtasks the filters hide say the filters hide them', async () => {
    const { user } = renderApp('#planner=p3');
    const spy = proposalsOnly(true);
    try {
      const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
      await tree.findByRole('treeitem', { name: '+1 suggested' });
      await user.click(screen.getByRole('radio', { name: 'Board' }));
      expect(await screen.findByText('No subtasks match the filters.')).toBeTruthy();
      await user.click(screen.getByRole('switch', { name: 'Show cancelled' }));
      expect(await screen.findByRole('region', { name: 'Board' })).toBeTruthy();
    } finally {
      spy.mockRestore();
    }
  });

  test('filters that hide every subtask say so', async () => {
    const { user } = await openNotes();
    await act(() => api.planner.status('cp3-32', 'cancelled', 'Not this quarter.'));
    await user.click(screen.getByRole('radio', { name: 'Board' }));
    expect(await screen.findByText('No subtasks match the filters.')).toBeTruthy();
    await user.click(screen.getByRole('switch', { name: 'Show cancelled' }));
    expect(await screen.findByRole('region', { name: 'Board' })).toBeTruthy();
  });

  test('a paused subtask carries Paused on the Board, as in the Tree', async () => {
    const { user } = await openNotes();
    await act(() => api.planner.pause('cp3-35', true));
    await user.click(screen.getByRole('radio', { name: 'Board' }));
    const board = within(await screen.findByRole('region', { name: 'Board' }));
    const card = within(await board.findByRole('button', { name: '#35 Fail the build on an image without alt text' }));
    expect(card.getByText('Paused')).toBeTruthy();
  });
});

describe('opening a card from the Inbox', () => {
  test('a proposed epic in Plans to approve opens unfolded from +N suggested, selected in the Tree', async () => {
    const { user } = renderApp('#planner=p3');
    const spy = serviceReply(
      (url, method) => method === 'GET' && url.includes('/api/board?project_id=p3'),
      async (real) => {
        const data = await (await real()).json();
        const base = data.cards.find((c: Card) => c.seq === 32);
        const epic = { ...base, id: 'cp3-90', seq: 90, rank: 99, title: 'Offline reading', status: 'planned', progress: { done: 0, total: 0, proposed: 0 }, confirmed: false, expires_at: new Date(Date.now() + 14 * 86_400_000).toISOString(), pinned_sha: '' };
        return reply(200, { ...data, cards: [...data.cards, epic] });
      },
    );
    try {
      const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
      await tree.findByRole('treeitem', { name: '+1 suggested' });
      expect(tree.queryByRole('treeitem', { name: /^#90 / })).toBeNull();
      await user.click(screen.getByRole('button', { name: 'Inbox, 1 pending' }));
      const plans = within(await screen.findByRole('region', { name: 'Plans to approve' }));
      await user.click(plans.getByRole('button', { name: '#90 Offline reading' }));
      await screen.findByLabelText('Card #90');
      await closeCard(user);
      const row = await tree.findByRole('treeitem', { name: '#90 Offline reading, Planned' });
      expect(row.getAttribute('aria-selected')).toBe('true');
    } finally {
      spy.mockRestore();
    }
  });

  test('a request on a subtask under a folded story opens it unfolded', async () => {
    const { user, tree } = await openPlanner();
    await user.click(tree.getByRole('button', { name: 'Fold #2' }));
    await waitFor(() => expect(tree.queryByRole('treeitem', { name: /^#5 / })).toBeNull());
    await user.click(screen.getByRole('button', { name: /^Inbox, \d+ pending$/ }));
    const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
    await user.click(within(inbox.getByRole('article', { name: 'Done request on #5' })).getByRole('button', { name: /^#5 / }));
    await screen.findByLabelText('Card #5');
    await closeCard(user);
    expect((await tree.findByRole('treeitem', { name: /^#5 / })).getAttribute('aria-selected')).toBe('true');
  });
});

describe('the Approve dialog', () => {
  test('Effort reads Default after a switch to a model without effort levels', async () => {
    const { user } = await openNotes();
    await user.click((await openMenu(user, 'Actions for #32')).getByRole('menuitem', { name: 'Approve and run…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Approve #32?' }));
    await pick(user, dialog, 'Model', /GPT-5 mini/);
    await pick(user, dialog, 'Effort', 'Medium');
    expect(dialog.getByRole('combobox', { name: 'Effort' }).textContent).toBe('Medium');
    await pick(user, dialog, 'Model', /Kimi K3/);
    await waitFor(() => expect(dialog.getByRole('combobox', { name: 'Effort' }).textContent).toBe('Default'));
    expect(dialog.getByText('This model uses its default effort.')).toBeTruthy();
  });

  test('a switch to a model without context sizes runs at the default size', async () => {
    const { user } = await openNotes();
    await user.click((await openMenu(user, 'Actions for #32')).getByRole('menuitem', { name: 'Approve and run…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Approve #32?' }));
    // notes-site has no acceptance command, which Approve needs.
    await dialog.findByText('None');
    await user.click(dialog.getByRole('button', { name: 'Edit' }));
    await user.type(dialog.getByRole('textbox', { name: 'Project acceptance command' }), 'npm test');
    await user.click(dialog.getByRole('button', { name: 'Save' }));
    await dialog.findByText('npm test');
    await pick(user, dialog, 'Model', /Claude Haiku/);
    await pick(user, dialog, 'Context size', /Long context/);
    await pick(user, dialog, 'Model', /Kimi K3/);
    expect(dialog.getByRole('combobox', { name: 'Context size' }).textContent).toBe('Default');
    const approve = vi.spyOn(api.planner, 'approve');
    try {
      await user.click(dialog.getByRole('button', { name: 'Approve and run' }));
      await waitFor(() => expect(approve).toHaveBeenCalledWith('cp3-32', expect.objectContaining({ model: 'kimi-k3', context_size: 'default' })));
    } finally {
      approve.mockRestore();
    }
  });
});

describe('request and dialog copy', () => {
  test('Release says split and change requests stay', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 8);
    await user.click(panel.getByRole('button', { name: 'Release' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Release #8?' }));
    expect(dialog.getByText('The subtask goes back to To do and its Task stops holding it. Its pending done, cancel and blocked requests are withdrawn; split and change requests stay for you to decide.')).toBeTruthy();
  });

  test('a split request does not say the hold moves', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: /^Inbox, \d+ pending$/ }));
    const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
    const request = within(inbox.getByRole('article', { name: 'Split request on #28' }));
    expect(request.getByText('Adds 4 subtasks to #27 Changelog from merged pull requests right after #28, which is cancelled.')).toBeTruthy();
    expect(request.queryByText(/The hold moves/)).toBeNull();
  });
});

