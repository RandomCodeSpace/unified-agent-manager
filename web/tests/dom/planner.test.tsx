import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { copyStyles, popMode } from '../../src/components/planner/PopOut';
import { openMenu, openTask, renderApp, sidebar, type User } from './render';

const header = () => screen.getByRole('heading', { level: 1 });

/** Opens the planner from the sidebar and waits for unified-agent-manager's outline. */
async function openPlanner() {
  const rendered = renderApp();
  const side = await sidebar();
  await rendered.user.click(side.getByRole('button', { name: 'Planner' }));
  await waitFor(() => expect(header().textContent).toBe('Planner'));
  const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
  await tree.findByRole('treeitem', { name: /^#1 Faster first load of long transcripts/ });
  return { ...rendered, tree };
}

describe('planner', () => {
  afterEach(() => {
    delete window.documentPictureInPicture;
  });

  test('a project row in the filter opens that project’s plan, kept in the URL', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
    await user.click(await screen.findByRole('button', { name: 'Plan notes-site' }));
    const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    expect(await tree.findByRole('treeitem', { name: '#32 Accessible post template, Doing' })).toBeTruthy();
    expect(window.location.hash).toBe('#planner=p3');
  });

  test('the Project picker lists every Project by badge and name, one without git disabled with why, and Unassigned last', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Project: unified-agent-manager' }));
    const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
    const options = list.getAllByRole('option');
    expect(options.map((o) => o.getAttribute('aria-current') === 'true')).toEqual([true, false, false, false]);
    expect(options.at(-1)!.getAttribute('aria-label')).toBe('Unassigned, 1 card');
    const dotfiles = list.getByRole('option', { name: /^dotfiles/ });
    expect(dotfiles.getAttribute('aria-disabled')).toBe('true');
    expect(within(dotfiles).getByText('Not a git repository, so it has no plan.')).toBeTruthy();
    await user.click(dotfiles);
    expect(screen.getByRole('listbox', { name: 'Projects' })).toBeTruthy();
    // The search box keeps focus; Enter picks the highlighted match.
    await user.type(screen.getByRole('combobox', { name: 'Search projects' }), 'notes');
    expect(list.getAllByRole('option').map((o) => o.textContent)).toEqual(['NSnotes-site']);
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('listbox', { name: 'Projects' })).toBeNull());
    expect(screen.getByRole('button', { name: 'Project: notes-site' })).toBeTruthy();
    const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    expect(await tree.findByRole('treeitem', { name: '#32 Accessible post template, Doing' })).toBeTruthy();
  });

  test('the Tree folds suggestions behind +N suggested, and Confirm makes one part of the plan', async () => {
    const { user, tree } = await openPlanner();
    expect(tree.queryByRole('treeitem', { name: /^#11 / })).toBeNull();
    const [suggested] = tree.getAllByRole('treeitem', { name: '+1 suggested' });
    await user.click(suggested);
    await tree.findByRole('treeitem', { name: /^#11 Benchmark snapshot size per Task count/ });
    await user.click(tree.getByRole('button', { name: 'Confirm Benchmark snapshot size per Task count' }));
    // Confirmed, it joins #7's children and the story's count, and its suggestion row goes.
    await waitFor(() => expect(tree.queryByRole('button', { name: 'Confirm Benchmark snapshot size per Task count' })).toBeNull());
    expect(tree.getByRole('treeitem', { name: /^#11 Benchmark snapshot size per Task count, Planned/ })).toBeTruthy();
    expect(tree.getByRole('treeitem', { name: /^#7 Trim the snapshot payload/ }).textContent).toContain('0/3');
  });

  test('confirming a card inside a suggestion confirms its unconfirmed parents too', async () => {
    const { user, tree } = await openPlanner();
    // #16 is a suggested story under #1, and #17 its suggested subtask.
    await user.click(tree.getAllByRole('treeitem', { name: '+1 suggested' })[1]);
    await tree.findByRole('treeitem', { name: /^#17 Split the diagram renderer/ });
    await user.click(tree.getByRole('button', { name: 'Confirm Split the diagram renderer into its own chunk' }));
    await waitFor(() => expect(tree.queryByRole('button', { name: 'Confirm Lazy-load the diagram renderer' })).toBeNull());
    expect(tree.queryByRole('button', { name: 'Confirm Split the diagram renderer into its own chunk' })).toBeNull();
    // Both are part of the plan now, and #1 counts #17.
    expect(tree.getByRole('treeitem', { name: /^#16 Lazy-load the diagram renderer, Planned/ })).toBeTruthy();
    expect(tree.getByRole('treeitem', { name: /^#1 Faster first load of long transcripts/ }).textContent).toContain('4/10');
  });

  test('an epic an agent proposed folds into +N suggested at the root, and the epic filter shows it to confirm or dismiss', async () => {
    const { user } = renderApp('#planner=p3');
    const spy = serviceReply(
      (url, method) => method === 'GET' && url.includes('/api/board?project_id=p3'),
      async (real) => {
        const data = await (await real()).json();
        const base = data.cards.find((c: { seq: number }) => c.seq === 32);
        const epic = { ...base, id: 'cp3-90', seq: 90, rank: 99, title: 'Offline reading', win_condition: '', status: 'planned', progress: { done: 0, total: 0, proposed: 0 }, confirmed: false, expires_at: new Date(Date.now() + 14 * 86_400_000).toISOString(), pinned_sha: '' };
        return reply(200, { ...data, cards: [...data.cards, epic] });
      },
    );
    try {
      const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
      await tree.findByRole('treeitem', { name: '#32 Accessible post template, Doing' });
      expect(tree.queryByRole('treeitem', { name: /^#90 / })).toBeNull();
      await user.click(tree.getByRole('treeitem', { name: '+1 suggested' }));
      expect(await tree.findByRole('treeitem', { name: '#90 Offline reading, Planned' })).toBeTruthy();
      // Filtered to it, the Tree shows the proposal itself, with its badge, Confirm and Dismiss.
      await pick(user, within(document.body), 'Epic', /^#90 /);
      const filtered = within(await screen.findByRole('tree', { name: 'Plan outline' }));
      const row = within(await filtered.findByRole('treeitem', { name: '#90 Offline reading, Planned' }));
      expect(row.getByText('Suggested')).toBeTruthy();
      expect(row.getByRole('button', { name: 'Confirm Offline reading' })).toBeTruthy();
      expect(row.getByRole('button', { name: 'Dismiss Offline reading' })).toBeTruthy();
      expect(filtered.queryByRole('treeitem', { name: /^#32 / })).toBeNull();
      expect(filtered.queryByRole('treeitem', { name: '+1 suggested' })).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  test('the epic filter lists no cancelled epic, except the one it is set to', async () => {
    const { user } = renderApp('#planner=p3');
    const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    await tree.findByRole('treeitem', { name: '#32 Accessible post template, Doing' });
    const add = async (form: string, title: string, button: string) => {
      const f = within(await screen.findByRole('form', { name: form }));
      await user.type(f.getByRole('textbox', { name: 'Title' }), title);
      await user.click(f.getByRole('button', { name: button }));
    };
    await user.click(screen.getByRole('button', { name: 'New epic' }));
    await add('New epic', 'Offline mode', 'Add epic');
    await user.click(within(await tree.findByRole('treeitem', { name: '#37 Offline mode, Planned' })).getByRole('button', { name: 'Add a subtask to #37' }));
    await add('New subtask in #37', 'Cache posts', 'Add subtask');
    await tree.findByRole('treeitem', { name: '#38 Cache posts, Planned' });
    await pick(user, within(document.body), 'Epic', /^#37 /);
    const panel = await openCard(user, tree, 37);
    await user.click(panel.getByRole('button', { name: 'Cancel' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Cancel #37?' }));
    await user.type(dialog.getByRole('textbox', { name: 'Why (required)' }), 'Not this quarter.');
    await user.click(dialog.getByRole('button', { name: 'Cancel card' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Cancel #37?' })).toBeNull());
    await closeCard(user);
    // Cancelled, #37 stays listed while the filter is set to it, so the filter never names nothing.
    const filter = screen.getByRole('combobox', { name: 'Epic' });
    expect(filter.textContent).toContain('#37 Offline mode');
    await user.click(filter);
    expect(await screen.findByRole('option', { name: /^#37 / })).toBeTruthy();
    await user.click(screen.getByRole('option', { name: 'All epics' }));
    await waitFor(() => expect(screen.queryByRole('option', { name: 'All epics' })).toBeNull());
    // Set to all epics, the filter lists only the live ones.
    await user.click(screen.getByRole('combobox', { name: 'Epic' }));
    expect(await screen.findByRole('option', { name: /^#32 / })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /^#37 / })).toBeNull();
  });

  test('accepting a split under a story adds siblings after the subtask, cancels it and moves its hold', async () => {
    const { user, tree } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Inbox, 6 pending' }));
    const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
    const request = within(inbox.getByRole('article', { name: 'Split request on #28' }));
    // The given child first, then #28's three checklist items; the ticked one is accepted with the split.
    expect(request.getByText('Adds 4 subtasks to #27 Changelog from merged pull requests right after #28, which is cancelled. The hold moves to the first pending one.')).toBeTruthy();
    const listed = request.getAllByRole('listitem').map((li) => li.textContent);
    expect(listed[0]).toContain('Read merged pull requests since the last tag');
    expect(listed[1]).toContain('Parse the commit type (ticked: accepted with the split)');
    await user.click(request.getByRole('button', { name: 'Accept' }));
    await waitFor(() => expect(inbox.queryByRole('article', { name: 'Split request on #28' })).toBeNull());
    await user.click(screen.getByRole('button', { name: 'Close inbox' }));
    await waitFor(() => expect(tree.queryByRole('treeitem', { name: /^#28 / })).toBeNull());
    const rows = tree.getAllByRole('treeitem').map((r) => r.getAttribute('aria-label') ?? r.textContent ?? '');
    const at = rows.findIndex((r) => r.startsWith('#27 '));
    expect(rows.slice(at + 1, at + 7)).toEqual([
      '#37 Read merged pull requests since the last tag, Doing',
      '#38 Parse the commit type, Done',
      '#39 Group feat and fix, Planned',
      '#40 Fold chores into one line, Planned',
      '#29 Write the upgrade notes section, Planned',
      '#30 Link each entry to its pull request, Planned',
    ]);
    // The hold moved with the Task: #37 carries its chip.
    expect(within(tree.getByRole('treeitem', { name: /^#37 / })).getByRole('button', { name: /^Open task / })).toBeTruthy();
  });

  test('clicking a card opens its detail; the view switch keeps the selection', async () => {
    const { user, tree } = await openPlanner();
    await user.click(tree.getByRole('treeitem', { name: /^#6 Measure first paint/ }));
    // An overlay below 1280px, inline beside the view above it.
    const panel = within(await screen.findByLabelText('Card #6'));
    expect(await panel.findByRole('region', { name: 'Blocker links' })).toBeTruthy();
    await user.click(panel.getByRole('button', { name: 'Close card' }));
    await waitFor(() => expect(screen.queryByLabelText('Card #6')).toBeNull());
    await user.click(screen.getByRole('radio', { name: 'Board' }));
    const board = within(await screen.findByRole('region', { name: 'Board' }));
    expect(board.getByRole('button', { name: '#6 Measure first paint on a 5,000-item Task' }).getAttribute('aria-pressed')).toBe('true');
  });

  test('Check at HEAD waits for its job, then shows the run from the job’s last frame', async () => {
    const { user, tree } = await openPlanner();
    await user.click(tree.getByRole('treeitem', { name: /^#6 Measure first paint/ }));
    const panel = within(await screen.findByLabelText('Card #6'));
    await user.click(panel.getByRole('button', { name: 'Check at HEAD' }));
    // The route answers with the job at once; the run lands later, in a board_job frame.
    expect(await panel.findByText('Checking at HEAD…')).toBeTruthy();
    expect(panel.queryByText(/exited/)).toBeNull();
    const run = await panel.findByText(/exited 1 at/, undefined, { timeout: 3000 });
    expect(run.textContent).toMatch(/^make test exited 1 at /);
    expect(panel.getByText(/--- FAIL: TestRelease/)).toBeTruthy();
    expect(panel.queryByText('Checking at HEAD…')).toBeNull();
    // A check job is not a suggestion: the card never says it is suggesting.
    expect(panel.queryByText('Suggesting…')).toBeNull();
  });

  test('on the Board a held subtask’s Task chip opens that Task', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('radio', { name: 'Board' }));
    const board = within(await screen.findByRole('region', { name: 'Board' }));
    const lane = within(board.getByRole('region', { name: 'Trim the snapshot payload lane' }));
    await user.click(lane.getByRole('button', { name: 'Open task Fix re-attach redraw regression' }));
    await waitFor(() => expect(header().textContent).toBe('Fix re-attach redraw regression'));
    expect(window.location.hash).toBe('#task=t1');
  });

  test('the Inbox asks a reason before rejecting, and a decided request leaves it', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Inbox, 6 pending' }));
    const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
    const request = within(inbox.getByRole('article', { name: 'Change request on #9' }));
    await user.click(request.getByRole('button', { name: 'Reject' }));
    const form = within(request.getByRole('form', { name: 'Reject the request' }));
    const confirm = form.getByRole('button', { name: 'Reject request' }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    await user.type(form.getByRole('textbox', { name: 'Reason' }), 'Recent Tasks are out of scope here.');
    expect(confirm.disabled).toBe(false);
    await user.click(confirm);
    await waitFor(() => expect(inbox.queryByRole('article', { name: 'Change request on #9' })).toBeNull());
    expect(screen.getByText('5 pending')).toBeTruthy();
    await waitFor(() => expect(document.title).toBe('(12) UAM'));
  });

  test('a cancelled card opened from a link turns Show cancelled on, so the Tree lists what it selects', async () => {
    const { user, tree } = await openPlanner();
    await act(() => api.planner.status('cp1-20', 'cancelled', 'The release goes without it'));
    await waitFor(() => expect(tree.queryByRole('treeitem', { name: /^#20 / })).toBeNull());
    const card = await openCard(user, tree, 21);
    await user.click(card.getByRole('button', { name: /^#20 / }));
    expect(await screen.findByLabelText('Card #20')).toBeTruthy();
    await closeCard(user);
    expect(screen.getByRole('switch', { name: 'Show cancelled' }).getAttribute('aria-checked')).toBe('true');
    expect(tree.getByRole('treeitem', { name: /^#20 / }).getAttribute('aria-selected')).toBe('true');
  });

  test('the pop-out is a floating panel on the page, with no separate window where the API is missing', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    const pop = within(await screen.findByRole('region', { name: 'Planner pop-out' }));
    expect(pop.getByRole('radio', { name: 'Tree' }).getAttribute('aria-checked')).toBe('true');
    expect(pop.queryByRole('button', { name: 'Open in a separate window' })).toBeNull();
    await user.click(pop.getByRole('radio', { name: /Inbox/ }));
    expect(await pop.findByRole('list', { name: 'Pending requests' })).toBeTruthy();
    await user.click(pop.getByRole('button', { name: 'Close the pop-out' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull());
  });

  test('with Picture-in-Picture the view renders into the window, styled by links', async () => {
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    // A data: URL, so the test environment fetches nothing.
    link.href = 'data:text/css,';
    document.head.append(link);
    // A frame stands in for the window: a second document of the page's origin.
    const frame = document.createElement('iframe');
    document.body.append(frame);
    const pipDoc = frame.contentDocument!;
    const listeners = new Set<() => void>();
    const pip = {
      document: pipDoc,
      closed: false,
      close() {
        this.closed = true;
        for (const l of listeners) l();
      },
      addEventListener: (_: string, l: () => void) => listeners.add(l),
      removeEventListener: (_: string, l: () => void) => listeners.delete(l),
    };
    const requestWindow = vi.fn(async () => pip as unknown as Window);
    window.documentPictureInPicture = { requestWindow };
    try {
      const { user } = await openPlanner();
      // The floating panel first, never a window on its own; the window is the panel's own button.
      await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
      const float = within(await screen.findByRole('region', { name: 'Planner pop-out' }));
      expect(requestWindow).not.toHaveBeenCalled();
      await user.click(float.getByRole('button', { name: 'Open in a separate window' }));
      await waitFor(() => expect(requestWindow).toHaveBeenCalledWith({ width: 520, height: 680 }));
      const inWindow = within(pipDoc.body);
      expect(await inWindow.findByRole('treeitem', { name: /^#1 Faster first load/ })).toBeTruthy();
      expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
      // Menus would open in the page, not the window: the window's rows offer none.
      expect(inWindow.queryByRole('button', { name: /^Actions for / })).toBeNull();
      expect([...pipDoc.head.querySelectorAll('link[rel="stylesheet"]')].map((l) => (l as HTMLLinkElement).href)).toContain(link.href);
      expect(pipDoc.querySelector('style')).toBeNull();
      // The window's own close ends the pop-out.
      await user.click(inWindow.getByRole('button', { name: 'Close the pop-out' }));
      await waitFor(() => expect(pipDoc.body.textContent).toBe(''));
    } finally {
      link.remove();
      frame.remove();
    }
  });

  test('a phone, or a browser without the API, has no separate window', () => {
    const api = { requestWindow: async () => window };
    const media = (matches: boolean) => (() => ({ matches })) as unknown as Window['matchMedia'];
    expect(popMode({ documentPictureInPicture: api, matchMedia: media(false) } as unknown as Window)).toBe('pip');
    expect(popMode({ documentPictureInPicture: api, matchMedia: media(true) } as unknown as Window)).toBe('float');
    expect(popMode({ matchMedia: media(false) } as unknown as Window)).toBe('float');
    const to = document.implementation.createHTMLDocument('');
    copyStyles(document, to);
    expect(to.title).toBe('UAM planner');
  });

  test('settling a Task that holds a subtask asks what happens to it', async () => {
    const { user } = renderApp('#task=t15');
    await waitFor(() => expect(header().textContent).toBe('Remove unused exports across packages'));
    await user.click(await screen.findByRole('button', { name: 'Task actions' }));
    await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Settle' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Settle “Remove unused exports across packages”?' }));
    await user.click(dialog.getByRole('radio', { name: 'Cancel' }));
    const settle = dialog.getByRole('button', { name: 'Settle' }) as HTMLButtonElement;
    expect(settle.disabled).toBe(true);
    await user.type(dialog.getByRole('textbox', { name: 'Comment for #5' }), 'Superseded by the window rewrite.');
    await user.click(settle);
    expect(await screen.findByText('Settled. Reopen this task to continue the same conversation.')).toBeTruthy();
  });

  test('Settings turns the planner off, and the import reports what it copied', async () => {
    const { user } = renderApp('#settings');
    const planner = within(await screen.findByRole('region', { name: 'Planner' }));
    const form = within(planner.getByRole('form', { name: 'Import from kb' }));
    await user.type(form.getByRole('textbox', { name: 'Import from kb' }), '/home/dev/.local/share/kb');
    await user.click(form.getByRole('button', { name: 'Import' }));
    expect((await form.findByRole('status')).textContent).toContain('3 imported, 0 already here, 3 to Unassigned');
    await user.click(planner.getByRole('switch', { name: 'Planner' }));
    await waitFor(() => expect(planner.getByRole('switch', { name: 'Planner' }).getAttribute('aria-checked')).toBe('false'));
    expect(planner.queryByRole('form', { name: 'Import from kb' })).toBeNull();
    // Off, the planner leaves the sidebar and its requests the Needs-you count.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Planner' })).toBeNull());
    await waitFor(() => expect(document.title).toBe('(7) UAM'));
  });
});

/** What the planner shows in one state of the setting: Settings' Planner card, the sidebar entry, Plan in the Project menu, and the Needs-you count. */
async function plannerSurface(user: User) {
  const side = await sidebar();
  await screen.findByRole('region', { name: 'Shell access' });
  const settingsCard = screen.queryByRole('region', { name: 'Planner' });
  const entry = side.queryByRole('button', { name: 'Planner' });
  await user.click(side.getByRole('button', { name: 'Project filter: all projects' }));
  await screen.findByRole('button', { name: 'Edit unified-agent-manager' });
  const plan = screen.queryAllByRole('button', { name: /^Plan / }).length;
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Edit unified-agent-manager' })).toBeNull());
  return { settingsCard, entry, plan };
}

/** Settles t15 (it holds #5 while the planner is on) from the header menu; returns the body of the first settle request. */
async function settleT15(user: User) {
  const side = await sidebar();
  // The row itself, not its hover Settle.
  const row = side.getAllByRole('button', { name: /Remove unused exports across packages/ }).find((b) => !b.getAttribute('aria-label')?.startsWith('Settle'));
  await user.click(row!);
  await waitFor(() => expect(header().textContent).toBe('Remove unused exports across packages'));
  const fetchSpy = vi.spyOn(window, 'fetch');
  try {
    await user.click(await screen.findByRole('button', { name: 'Task actions' }));
    await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Settle' }));
    const call = await waitFor(() => {
      const found = fetchSpy.mock.calls.find(([url]) => String(url).endsWith('/api/sessions/t15/settle'));
      expect(found).toBeTruthy();
      return found!;
    });
    return call[1]?.body;
  } finally {
    fetchSpy.mockRestore();
  }
}

const settleWait = () => new Promise((resolve) => setTimeout(resolve, 300));

describe('the planner setting', () => {
  test('unknown to the service: no planner anywhere, and Settle is the plain call it always was', async () => {
    const { user } = renderApp('?planner=unset#settings');
    const { settingsCard, entry, plan } = await plannerSurface(user);
    expect(settingsCard).toBeNull();
    expect(entry).toBeNull();
    expect(plan).toBe(0);
    await settleWait();
    expect(document.title).toBe('(7) UAM');
    // The request Settle always sent, and the Task settles with no question about holds.
    expect(await settleT15(user)).toBe('{}');
    expect(await screen.findByText('Settled. Reopen this task to continue the same conversation.')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('off: only the Settings switch, and Settle stays the plain call', async () => {
    const { user } = renderApp('?planner=off#settings');
    const { settingsCard, entry, plan } = await plannerSurface(user);
    expect(settingsCard).toBeTruthy();
    const card = within(settingsCard!);
    expect(card.getByRole('switch', { name: 'Planner' }).getAttribute('aria-checked')).toBe('false');
    expect(card.queryByRole('form', { name: 'Import from kb' })).toBeNull();
    expect(entry).toBeNull();
    expect(plan).toBe(0);
    await settleWait();
    expect(document.title).toBe('(7) UAM');
    expect(await settleT15(user)).toBe('{}');
    expect(await screen.findByText('Settled. Reopen this task to continue the same conversation.')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('on: the switch, the import, the sidebar entry, Plan per git project and pending requests in the count', async () => {
    const { user } = renderApp('#settings');
    const { settingsCard, entry, plan } = await plannerSurface(user);
    const card = within(settingsCard!);
    expect(card.getByRole('switch', { name: 'Planner' }).getAttribute('aria-checked')).toBe('true');
    expect(card.getByRole('form', { name: 'Import from kb' })).toBeTruthy();
    expect(entry).toBeTruthy();
    // dotfiles has no git: two of the three Projects can be planned.
    expect(plan).toBe(2);
    await waitFor(() => expect(document.title).toBe('(13) UAM'));
    // Settle asks first: t15 holds a subtask.
    expect(await settleT15(user)).toBe('{}');
    expect(await screen.findByRole('dialog', { name: 'Settle “Remove unused exports across packages”?' })).toBeTruthy();
  });
});

describe('the map', () => {
  test('opens at scale 1 from the roots; Fit zooms out no further than the limit', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('radio', { name: 'Map' }));
    const map = await screen.findByRole('group', { name: /Plan map/ });
    const layer = map.firstElementChild as HTMLElement;
    await waitFor(() => expect(layer.style.transform).toBe('translate(24px, 24px) scale(1)'));
    expect(within(map).getByRole('button', { name: /^#1 · Epic · Doing/ })).toBeTruthy();
    // The test environment lays nothing out (a 0 × 0 viewport): Fit stops at the 0.4 limit.
    await user.click(screen.getByRole('button', { name: 'Fit the plan' }));
    expect(layer.style.transform).toMatch(/scale\(0\.4\)$/);
  });

  test('Tab goes parents first, and a focused node pans into view without scrolling the viewport', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('radio', { name: 'Map' }));
    const map = await screen.findByRole('group', { name: /Plan map/ });
    const layer = map.firstElementChild as HTMLElement;
    await waitFor(() => expect(layer.style.transform).toBe('translate(24px, 24px) scale(1)'));
    expect(map.classList.contains('overflow-clip')).toBe(true);
    act(() => map.focus());
    const seen: string[] = [];
    for (let i = 0; i < 3; i++) {
      await user.tab();
      seen.push(document.activeElement!.getAttribute('aria-label')!.split(' · ').slice(0, 2).join(' · '));
    }
    expect(seen).toEqual(['#1 · Epic', '#2 · Story', '#3 · Subtask']);
    // The test environment's viewport is 0 × 0, so every node is out of view: focus pans the layer to it.
    await waitFor(() => expect(layer.style.transform).not.toBe('translate(24px, 24px) scale(1)'));
    expect(map.scrollTop).toBe(0);
    expect(map.scrollLeft).toBe(0);
  });
});

/** Picks `option` in the labelled select. */
async function pick(user: User, within_: ReturnType<typeof within>, name: string, option: string | RegExp) {
  await user.click(within_.getByRole('combobox', { name }));
  await user.click(await screen.findByRole('option', { name: option }));
}

/** Picks a Board in the Planner header's Project picker. */
async function pickProject(user: User, option: RegExp) {
  await user.click(screen.getByRole('button', { name: /^Project: / }));
  await user.click(await within(await screen.findByRole('listbox', { name: 'Projects' })).findByRole('option', { name: option }));
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

const labels = (tree: ReturnType<typeof within>) => tree.getAllByRole('treeitem').map((r) => r.getAttribute('aria-label') ?? '');

describe('owner authoring', () => {
  test('an empty plan offers New epic; containers add stories and subtasks, and the root a subtask', async () => {
    const { user } = renderApp();
    const side = await sidebar();
    await api.createProject({ dir: '/home/user/projects/empty-plan' });
    await user.click(side.getByRole('button', { name: 'Planner' }));
    await waitFor(() => expect(header().textContent).toBe('Planner'));
    await pickProject(user, /^empty-plan/);
    expect(await screen.findByText('Nothing is planned for empty-plan yet.')).toBeTruthy();
    // The header's New epic, and the empty state's.
    await user.click(screen.getAllByRole('button', { name: 'New epic' }).at(-1)!);
    const add = async (form: string, title: string, button: string) => {
      const f = within(await screen.findByRole('form', { name: form }));
      await user.type(f.getByRole('textbox', { name: 'Title' }), title);
      await user.click(f.getByRole('button', { name: button }));
    };
    await add('New epic', 'Offline mode', 'Add epic');
    const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    const epic = await tree.findByRole('treeitem', { name: '#37 Offline mode, Planned' });
    await user.click(within(epic).getByRole('button', { name: 'Add a story to #37' }));
    await add('New story in #37', 'Queue sends while offline', 'Add story');
    const story = await tree.findByRole('treeitem', { name: '#38 Queue sends while offline, Planned' });
    await user.click(within(story).getByRole('button', { name: 'Add a subtask to #38' }));
    await add('New subtask in #38', 'Persist the send queue', 'Add subtask');
    await tree.findByRole('treeitem', { name: /^#39 Persist the send queue/ });
    await user.click(screen.getByRole('button', { name: 'Add a subtask at the root' }));
    await add('New subtask', 'Audit the service worker cache', 'Add subtask');
    await tree.findByRole('treeitem', { name: /^#40 Audit the service worker cache/ });
    expect(tree.getAllByRole('treeitem').map((r) => `${r.getAttribute('aria-level')} ${r.getAttribute('aria-label')?.split(' ')[0]}`)).toEqual(['1 #37', '2 #38', '3 #39', '1 #40']);
  });

  test('New epic in the header opens its form in the Tree from any view', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('radio', { name: 'Board' }));
    await screen.findByRole('region', { name: 'Board' });
    await user.click(screen.getByRole('button', { name: 'New epic' }));
    const form = within(await screen.findByRole('form', { name: 'New epic' }));
    expect(screen.getByRole('radio', { name: 'Tree' }).getAttribute('aria-checked')).toBe('true');
    await user.type(form.getByRole('textbox', { name: 'Title' }), 'Offline mode{Enter}');
    const tree = within(screen.getByRole('tree', { name: 'Plan outline' }));
    expect((await tree.findByRole('treeitem', { name: '#37 Offline mode, Planned' })).getAttribute('aria-level')).toBe('1');
  });

  test('Mark done shows what is still open, and Finish anyway closes it', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    await user.click(panel.getByRole('button', { name: 'Mark done' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Mark #15 done?' }));
    const submit = dialog.getByRole('button', { name: 'Mark done' }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    await user.type(dialog.getByRole('textbox', { name: 'Comment (required)' }), 'The window tests cover it.');
    await user.click(submit);
    const alert = within(await dialog.findByRole('alert'));
    expect(alert.getByText('Open checklist items')).toBeTruthy();
    expect(alert.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Scroll to the middle', 'Land an older page']);
    await user.click(dialog.getByRole('button', { name: 'Finish anyway' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Mark #15 done?' })).toBeNull());
    await closeCard(user);
    expect(await tree.findByRole('treeitem', { name: '#15 Cover anchoring with a DOM test, Done' })).toBeTruthy();
  });

  test('Back to To do moves a done subtask back through the status API, with an optional comment', async () => {
    const { user, tree } = await openPlanner();
    const status = vi.spyOn(api.planner, 'status');
    try {
      const panel = await openCard(user, tree, 4);
      expect(panel.queryByRole('button', { name: 'Mark done' })).toBeNull();
      await user.click(panel.getByRole('button', { name: 'Back to To do' }));
      const dialog = within(await screen.findByRole('dialog', { name: 'Move #4 back to To do?' }));
      await user.type(dialog.getByRole('textbox', { name: 'Comment (optional)' }), 'A cold start still misses the cache.');
      await user.click(dialog.getByRole('button', { name: 'Back to To do' }));
      await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Move #4 back to To do?' })).toBeNull());
      expect(status).toHaveBeenCalledWith('cp1-4', 'todo', 'A cold start still misses the cache.');
      await closeCard(user);
      expect(await tree.findByRole('treeitem', { name: '#4 Cache archive pages in IndexedDB, To do' })).toBeTruthy();
    } finally {
      status.mockRestore();
    }
  });

  test('the blocker picker offers only open siblings of one kind, suggestions included, and the link names a suggestion', async () => {
    const { user, tree } = await openPlanner();
    const link = vi.spyOn(api.planner, 'link');
    try {
      // #9 sits under story #7 with #8 (in progress, and already its blocker), #10 (cancelled) and the suggestion #11.
      const panel = await openCard(user, tree, 9);
      await user.click(panel.getByRole('combobox', { name: 'Add a blocker' }));
      expect((await screen.findAllByRole('option')).map((o) => o.textContent)).toEqual(['#11 Benchmark snapshot size per Task count (suggested)']);
      await user.click(screen.getByRole('option', { name: '#11 Benchmark snapshot size per Task count (suggested)' }));
      await user.click(panel.getByRole('button', { name: 'Link' }));
      expect(link).toHaveBeenCalledWith('cp1-11', 'cp1-9');
      const row = await panel.findByRole('button', { name: '#11 Benchmark snapshot size per Task count' });
      expect(row.closest('li')!.textContent).toContain('Suggested');
      expect(panel.getByRole('button', { name: 'Remove the link to #11' })).toBeTruthy();
      // A blocker in another story is refused by the service, which names the containers to link instead.
      await expect(api.planner.link('cp1-31', 'cp1-9')).rejects.toThrow(/#31 can't block #9/);
    } finally {
      link.mockRestore();
    }
  });

  test('a subtask in progress keeps its plan: no edit, links or checklist text, with the reason', async () => {
    const { user, tree } = await openPlanner();
    // #8 is held by a Task.
    const panel = await openCard(user, tree, 8);
    expect(panel.getByText('In progress: release it to change the plan.')).toBeTruthy();
    expect(panel.queryByRole('combobox', { name: 'Add a blocker' })).toBeNull();
    expect(panel.queryByRole('button', { name: /^Remove the link/ })).toBeNull();
    expect(panel.queryByRole('button', { name: /^Edit/ })).toBeNull();
    await closeCard(user);
    const menu = await openMenu(user, 'Actions for #8');
    expect(menu.queryByRole('menuitem', { name: 'Move to…' })).toBeNull();
    expect(menu.queryByRole('menuitem', { name: 'Split' })).toBeNull();
    expect(menu.getByRole('menuitem', { name: 'Edit' }).getAttribute('aria-disabled')).toBe('true');
    expect(menu.getByText('In progress: release it to change the plan.')).toBeTruthy();
  });

  test('a story with a started subtask under it offers Move to… switched off, naming the subtask, as the service refuses the move', async () => {
    const { user, tree } = await openPlanner();
    const busy = "Can't move while #8 under it is in progress: release it first.";
    // Story #7 holds #8, which a Task holds.
    await expect(api.planner.move('cp1-7', null)).rejects.toThrow(busy);
    const panel = await openCard(user, tree, 7);
    const move = panel.getByRole('button', { name: 'Move to…' });
    expect(move.hasAttribute('disabled') || move.getAttribute('aria-disabled') === 'true').toBe(true);
    expect(move.getAttribute('aria-describedby')).toBe('planner-action-move-reason');
    expect(panel.getByText(busy)).toBeTruthy();
    // Other actions stay as they are; Cancel with a running subtask is the owner's to take.
    expect(panel.getByRole('button', { name: 'Cancel' }).hasAttribute('disabled')).toBe(false);
    await closeCard(user);
    let menu = await openMenu(user, 'Actions for #7');
    expect(menu.getByRole('menuitem', { name: 'Move to…' }).getAttribute('aria-disabled')).toBe('true');
    expect(menu.getByText(busy)).toBeTruthy();
    await user.keyboard('{Escape}');
    // A done subtask holds its story in place too: story #12 has done #13 and #14 beside open #15.
    menu = await openMenu(user, 'Actions for #12');
    expect(menu.getByRole('menuitem', { name: 'Move to…' }).getAttribute('aria-disabled')).toBe('true');
    expect(menu.getByText("Can't move while #13 under it is done: move it back to To do first.")).toBeTruthy();
    await user.keyboard('{Escape}');
    // Released, #8 holds #7 in place no longer.
    await act(() => api.planner.release('cp1-8', ''));
    menu = await openMenu(user, 'Actions for #7');
    await waitFor(() => expect(menu.getByRole('menuitem', { name: 'Move to…' }).getAttribute('aria-disabled')).not.toBe('true'));
    expect(menu.queryByText(busy)).toBeNull();
  });

  test('launching a suggestion asks first, naming what it confirms and the suggestions it still waits on', async () => {
    const { user, tree } = await openPlanner();
    // The service refuses a launch that would confirm suggestions unless it says to.
    await expect(api.planner.launch('cp1-17')).rejects.toThrow(/#17, #16 must be confirmed/);
    // #16 is a suggested story under #1 and #17 its suggested subtask; a suggested sibling, #37, blocks #17.
    await act(() => api.planner.suggest('cp1-16', { brief: '', document: '', max: 1 }));
    await user.click(tree.getAllByRole('treeitem', { name: '+1 suggested' })[1]);
    await tree.findByRole('treeitem', { name: /^#37 Write the failing test first/ }, { timeout: 4000 });
    await act(() => api.planner.link('cp1-37', 'cp1-17'));
    const launch = vi.spyOn(api.planner, 'launch');
    try {
      const panel = await openCard(user, tree, 17);
      await user.click(panel.getByRole('button', { name: 'Launch' }));
      const dialog = within(await screen.findByRole('dialog', { name: 'Confirm and launch #17?' }));
      expect(launch).not.toHaveBeenCalled();
      expect(within(dialog.getByRole('list', { name: 'Launching confirms' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
        '#17 Split the diagram renderer into its own chunk · Subtask',
        '#16 Lazy-load the diagram renderer · Story',
      ]);
      expect(within(dialog.getByRole('list', { name: 'Still waits on' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['#37 Write the failing test first · Subtask']);
      await user.click(dialog.getByRole('button', { name: 'Confirm and launch' }));
      await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Confirm and launch #17?' })).toBeNull());
      expect(launch).toHaveBeenCalledWith('cp1-17', { confirm: true });
      await closeCard(user);
      expect(await tree.findByRole('treeitem', { name: /^#17 Split the diagram renderer into its own chunk, Doing/ })).toBeTruthy();
      expect(tree.getByRole('treeitem', { name: /^#16 Lazy-load the diagram renderer, Doing/ })).toBeTruthy();
      // The blocker stays a suggestion, folded under #16.
      expect(tree.queryByRole('treeitem', { name: /^#37 / })).toBeNull();
    } finally {
      launch.mockRestore();
    }
  });

  test('the launch step marks a suggested blocker it waits on through a parent, as the finishing guard names it', async () => {
    const { user, tree } = await openPlanner();
    // A suggested story #37 beside #16 under #1 blocks #16, so #17 under #16 waits on it too.
    await act(() => api.planner.suggest('cp1-1', { brief: '', document: '', max: 1 }));
    await user.click(await tree.findByRole('treeitem', { name: '+2 suggested' }, { timeout: 4000 }));
    await tree.findByRole('treeitem', { name: /^#37 Harden the retry path/ });
    await act(() => api.planner.link('cp1-37', 'cp1-16'));
    const panel = await openCard(user, tree, 17);
    await user.click(panel.getByRole('button', { name: 'Launch' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Confirm and launch #17?' }));
    expect(within(dialog.getByRole('list', { name: 'Still waits on' })).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['#37 Harden the retry path · Story (via its story #16)']);
  });

  test('a done subtask under a cancelled card offers no Back to To do, which the service refuses there', async () => {
    const { user, tree } = await openPlanner();
    // #12 holds done #13 and #14; cancelling it cancels only its open #15.
    await act(() => api.planner.status('cp1-12', 'cancelled', 'Anchoring moved to the new list.'));
    await user.click(screen.getByRole('switch', { name: 'Show cancelled' }));
    await waitFor(() => expect(tree.getByRole('treeitem', { name: /^#12 .*, Cancelled$/ })).toBeTruthy());
    const panel = await openCard(user, tree, 13);
    expect(within(panel.getByRole('navigation', { name: 'Card path' })).getByRole('button', { name: /^#12 / })).toBeTruthy();
    expect(panel.queryByRole('button', { name: 'Back to To do' })).toBeNull();
    await closeCard(user);
    expect(itemsOf(await openMenu(user, 'Actions for #13'))).not.toContain('Back to To do');
    await user.keyboard('{Escape}');
    await expect(api.planner.status('cp1-13', 'todo', '')).rejects.toThrow(/#13 is under cancelled #12; restore that first/);
  });

  test('Move to… puts a subtask last under another story', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 22);
    await user.click(panel.getByRole('button', { name: 'Move to…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Move #22' }));
    await pick(user, dialog, 'Move to', '#12 Keep the transcript steady while pages land (in #1)');
    await user.click(dialog.getByRole('button', { name: 'Move' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Move #22' })).toBeNull());
    await waitFor(() => expect(within(panel.getByRole('navigation', { name: 'Card path' })).getByRole('button', { name: /^#12 / })).toBeTruthy());
    await closeCard(user);
    const rows = labels(tree);
    expect(rows[rows.findIndex((r) => r.startsWith('#15 ')) + 1]).toBe('#22 Set up the macOS signing step, To do');
  });

  test('Split under a story adds the subtasks right after it and cancels it', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 26);
    await user.click(panel.getByRole('button', { name: 'Split' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Split #26' }));
    await user.type(dialog.getByRole('textbox', { name: 'Subtask 1 title' }), 'Check the signature before unpacking');
    await user.click(dialog.getByRole('button', { name: 'Another subtask' }));
    await user.type(dialog.getByRole('textbox', { name: 'Subtask 2 title' }), 'Fail closed on a missing signature');
    expect(dialog.getByText('Adds 2 subtasks to #23 Sign release binaries right after #26, which is cancelled.')).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Split' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Split #26' })).toBeNull());
    expect(await panel.findByText('Cancelled')).toBeTruthy();
    await closeCard(user);
    await waitFor(() => expect(tree.queryByRole('treeitem', { name: /^#26 / })).toBeNull());
    const rows = labels(tree);
    const at = rows.findIndex((r) => r.startsWith('#25 '));
    expect(rows.slice(at + 1, at + 3)).toEqual(['#37 Check the signature before unpacking, Planned', '#38 Fail closed on a missing signature, Planned']);
  });

  test('an Unassigned card moves into a project, and its plan opens on it', async () => {
    const { user } = await openPlanner();
    await pickProject(user, /^Unassigned, 1 card/);
    const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    const panel = await openCard(user, tree, 36);
    await user.click(panel.getByRole('button', { name: 'Move' }));
    // The card opens in its new plan, no longer read-only.
    expect(await panel.findByRole('button', { name: 'Mark done' })).toBeTruthy();
    await closeCard(user);
    expect(screen.getByRole('button', { name: 'Project: unified-agent-manager' })).toBeTruthy();
    const plan = within(screen.getByRole('tree', { name: 'Plan outline' }));
    expect(await plan.findByRole('treeitem', { name: /^#36 Investigate the flaky clipboard test on Firefox/ })).toBeTruthy();
    // Empty now and not shown, Unassigned leaves the picker.
    await user.click(screen.getByRole('button', { name: 'Project: unified-agent-manager' }));
    const list = within(await screen.findByRole('listbox', { name: 'Projects' }));
    expect(list.queryByRole('option', { name: /^Unassigned/ })).toBeNull();
  });

  test('a card update keeps what the owner is typing in its owner fields', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 5);
    // Queried each time: a remounted form would be a new element.
    const paths = () => (panel.getByRole('textbox', { name: 'Paths' }) as HTMLTextAreaElement).value;
    await user.type(panel.getByRole('textbox', { name: 'Paths' }), '{Enter}web/src/lib/historyWindow.ts');
    const typed = 'web/src/lib/historyCache.ts\nweb/src/lib/historyWindow.ts';
    // An agent-side tick lands as a new revision of the card.
    await user.click(panel.getByRole('checkbox', { name: 'Keep the live tail' }));
    expect(await panel.findByRole('region', { name: 'Checklist 2/3' })).toBeTruthy();
    expect(paths()).toBe(typed);
    // A stored value that changes under an untouched field shows in it; the typed field keeps its text.
    await api.planner.edit('cp1-5', { accept_cmd: 'make test-web' });
    await waitFor(() => expect(panel.getByRole('radio', { name: 'Command' }).getAttribute('aria-checked')).toBe('true'));
    expect((panel.getByRole('textbox', { name: 'Command' }) as HTMLInputElement).value).toBe('make test-web');
    expect(paths()).toBe(typed);
  });

  test('the checklist adds an item, also to a card without one, renames one and removes one', async () => {
    const { user, tree } = await openPlanner();
    // #9 has no checklist yet: the group offers only the add row.
    let panel = await openCard(user, tree, 9);
    const empty = within(panel.getByRole('region', { name: 'Checklist' }));
    expect(empty.queryByRole('listitem')).toBeNull();
    expect((empty.getByRole('button', { name: 'Add item' }) as HTMLButtonElement).disabled).toBe(true);
    await user.type(empty.getByRole('textbox', { name: 'Add an item' }), 'Drop previews from closed Tasks{Enter}');
    const one = within(await panel.findByRole('region', { name: 'Checklist 0/1' }));
    expect(one.getByRole('checkbox', { name: 'Drop previews from closed Tasks' })).toBeTruthy();
    await waitFor(() => expect((one.getByRole('textbox', { name: 'Add an item' }) as HTMLInputElement).value).toBe(''));
    await closeCard(user);

    panel = await openCard(user, tree, 15);
    // Click the text to rename it; Enter saves and hands focus back to the item.
    await user.click(panel.getByRole('button', { name: 'Edit Scroll to the middle' }));
    const field = panel.getByRole('textbox', { name: 'Item text' });
    expect(document.activeElement).toBe(field);
    await user.clear(field);
    await user.type(field, 'Scroll to the middle row{Enter}');
    const renamed = await panel.findByRole('button', { name: 'Edit Scroll to the middle row' });
    await waitFor(() => expect(document.activeElement).toBe(renamed));
    // Esc keeps the old text.
    await user.click(renamed);
    await user.type(panel.getByRole('textbox', { name: 'Item text' }), ' and back{Escape}');
    expect(panel.queryByRole('textbox', { name: 'Item text' })).toBeNull();
    expect(panel.getByRole('button', { name: 'Edit Scroll to the middle row' })).toBeTruthy();
    // Leaving the field saves too.
    await user.click(panel.getByRole('button', { name: 'Edit Land an older page' }));
    await user.type(panel.getByRole('textbox', { name: 'Item text' }), ' above it');
    await user.tab();
    expect(await panel.findByRole('button', { name: 'Edit Land an older page above it' })).toBeTruthy();
    // × removes an item at once.
    await user.click(panel.getByRole('button', { name: 'Remove Scroll to the middle row' }));
    const left = within(await panel.findByRole('region', { name: 'Checklist 0/1' }));
    expect(left.getAllByRole('checkbox').map((x) => x.getAttribute('aria-label'))).toEqual(['Land an older page above it']);
  });

  test('a checklist item emptied is refused in place, with no request', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    const fetchSpy = vi.spyOn(window, 'fetch');
    try {
      await user.click(panel.getByRole('button', { name: 'Edit Land an older page' }));
      const field = panel.getByRole('textbox', { name: 'Item text' });
      await user.clear(field);
      await user.type(field, '   {Enter}');
      expect(within(panel.getByRole('region', { name: 'Checklist 0/2' })).getByRole('alert').textContent).toBe('An item needs text. Esc keeps the old one, or remove the item.');
      expect(field.getAttribute('aria-invalid')).toBe('true');
      expect(document.activeElement).toBe(field);
      expect(fetchSpy.mock.calls.filter(([, init]) => init?.method === 'PATCH')).toHaveLength(0);
      // Typing clears the error; Esc leaves the item as it was.
      await user.type(field, 'x');
      expect(panel.queryByRole('alert')).toBeNull();
      await user.keyboard('{Escape}');
      expect(panel.getByRole('button', { name: 'Edit Land an older page' })).toBeTruthy();
    } finally {
      fetchSpy.mockRestore();
    }
  });

  test('the checklist is read-only on a cancelled or Unassigned card, and waits while a save runs', async () => {
    const { user, tree } = await openPlanner();
    // A cancelled card's items show, but nothing edits them until it is restored.
    await user.click(screen.getByRole('switch', { name: 'Show cancelled' }));
    let panel = await openCard(user, tree, 10);
    const cancelled = within(panel.getByRole('region', { name: 'Checklist 1/2' }));
    expect(cancelled.getAllByRole('checkbox').every((x) => (x as HTMLInputElement).disabled)).toBe(true);
    expect(cancelled.queryByRole('button')).toBeNull();
    expect(cancelled.queryByRole('textbox')).toBeNull();
    await closeCard(user);

    // While a save runs, the other controls wait for it.
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const spy = serviceReply(
      (url, method) => method === 'PATCH' && url.includes('/api/board/cards/'),
      async (real) => {
        await gate;
        return real();
      },
    );
    try {
      panel = await openCard(user, tree, 15);
      await user.type(panel.getByRole('textbox', { name: 'Add an item' }), 'Assert the row');
      await user.click(panel.getByRole('button', { name: 'Remove Scroll to the middle' }));
      expect((panel.getByRole('button', { name: 'Remove Land an older page' }) as HTMLButtonElement).disabled).toBe(true);
      expect((panel.getByRole('button', { name: 'Edit Land an older page' }) as HTMLButtonElement).disabled).toBe(true);
      expect((panel.getByRole('checkbox', { name: 'Land an older page' }) as HTMLInputElement).disabled).toBe(true);
      expect((panel.getByRole('button', { name: 'Add item' }) as HTMLButtonElement).disabled).toBe(true);
      release();
      expect(await panel.findByRole('region', { name: 'Checklist 0/1' })).toBeTruthy();
      await waitFor(() => expect((panel.getByRole('button', { name: 'Add item' }) as HTMLButtonElement).disabled).toBe(false));
    } finally {
      spy.mockRestore();
    }
    await closeCard(user);

    // An Unassigned card has no checklist to show and none to add.
    await pickProject(user, /^Unassigned, 1 card/);
    const unassigned = within(await screen.findByRole('tree', { name: 'Plan outline' }));
    panel = await openCard(user, unassigned, 36);
    expect(panel.queryByRole('region', { name: /^Checklist/ })).toBeNull();
  });

  test('a card’s description renders as Markdown', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 1);
    const desc = panel.getByRole('region', { name: 'Description' });
    expect(within(desc).getAllByRole('listitem')).toHaveLength(3);
    expect(desc.querySelector('code')?.textContent).toBe('historyCache');
    expect(desc.querySelector('strong')?.textContent).toBe('steady');
  });
});

describe('the planner’s frames and streams', () => {
  test('in Picture-in-Picture the Tree moves focus on the window’s own frames', async () => {
    const frame = document.createElement('iframe');
    document.body.append(frame);
    const pipDoc = frame.contentDocument!;
    const listeners = new Set<() => void>();
    const pip = {
      document: pipDoc,
      closed: false,
      close() {
        this.closed = true;
        for (const l of listeners) l();
      },
      addEventListener: (_: string, l: () => void) => listeners.add(l),
      removeEventListener: (_: string, l: () => void) => listeners.delete(l),
    };
    window.documentPictureInPicture = { requestWindow: async () => pip as unknown as Window };
    const own = vi.spyOn(frame.contentWindow!, 'requestAnimationFrame');
    try {
      const { user } = await openPlanner();
      await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
      await user.click(within(await screen.findByRole('region', { name: 'Planner pop-out' })).getByRole('button', { name: 'Open in a separate window' }));
      const row = await within(pipDoc.body).findByRole('treeitem', { name: /^#1 Faster first load/ });
      fireEvent.keyDown(row, { key: 'ArrowDown' });
      expect(own).toHaveBeenCalled();
      await user.click(within(pipDoc.body).getByRole('button', { name: 'Close the pop-out' }));
      await waitFor(() => expect(pipDoc.body.textContent).toBe(''));
    } finally {
      own.mockRestore();
      frame.remove();
    }
  });

  test('a new stream fetches no Board whose revision it reports unchanged', async () => {
    const { user } = await openPlanner();
    const side = await sidebar();
    const fetchSpy = vi.spyOn(window, 'fetch');
    try {
      const row = side.getAllByRole('button', { name: /Fix re-attach redraw regression/ }).find((b) => !b.getAttribute('aria-label')?.startsWith('Settle'));
      await user.click(row!);
      await screen.findByRole('region', { name: 'Conversation' });
      await settleWait();
      expect(fetchSpy.mock.calls.map(([url]) => String(url)).filter((u) => u.includes('/api/board?'))).toEqual([]);
    } finally {
      fetchSpy.mockRestore();
    }
  });
});

/**
 * Answers the requests `match` picks with `answer`, a reply shaped as internal/web sends it (it
 * may start from the mock's own, `real`); every other request goes to the mock. Call it after
 * renderApp, which installs the mock.
 */
function serviceReply(match: (url: string, method: string) => boolean, answer: (real: () => Promise<Response>) => Promise<Response>) {
  const real = window.fetch;
  return vi.spyOn(window, 'fetch').mockImplementation((input, init) => {
    const url = String(input instanceof Request ? input.url : input);
    return match(url, init?.method ?? 'GET') ? answer(() => real(input, init)) : real(input, init);
  });
}

const reply = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));

describe('parity with the service', () => {
  test('Mark done names the open blockers the refusal lists by #seq', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 9);
    await user.click(panel.getByRole('button', { name: 'Mark done' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Mark #9 done?' }));
    await user.type(dialog.getByRole('textbox', { name: 'Comment (required)' }), 'Previews moved to the detail stream.');
    await user.click(dialog.getByRole('button', { name: 'Mark done' }));
    const alert = within(await dialog.findByRole('alert'));
    expect(alert.getByText('Open blockers')).toBeTruthy();
    expect(alert.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['#8 Drop unused fields from the task summary']);
    expect(dialog.getByRole('button', { name: 'Finish anyway' })).toBeTruthy();
  });

  test('a planner database that cannot open says so where the plan would be', async () => {
    renderApp('#planner=p1');
    const spy = serviceReply((url) => url.includes('/api/board?project_id=p1'), () => reply(503, { error: 'the planner database could not be opened; see the service log', code: 'planner_unavailable' }));
    try {
      expect((await screen.findByRole('alert')).textContent).toContain('Could not load the plan: the planner database could not be opened; see the service log');
    } finally {
      spy.mockRestore();
    }
  });

  test('a project without git shows why it has no plan, and offers no authoring', async () => {
    renderApp('#planner=p2');
    expect(await screen.findByText('dotfiles is not a git repository, so it has no plan.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'New epic' })).toBeNull();
  });

  test('a rejection the Task did not hear offers Release in its inbox row', async () => {
    const { user, tree } = await openPlanner();
    // The Task is Active, so the hold stays; sending it the reason failed (steered false).
    const spy = serviceReply(
      (url, method) => method === 'POST' && url.endsWith('/reject'),
      async (real) => {
        const r = await real();
        return reply(r.status, { ...(await r.json()), steered: false });
      },
    );
    try {
      await user.click(screen.getByRole('button', { name: 'Inbox, 6 pending' }));
      const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
      const request = within(inbox.getByRole('article', { name: 'Done request on #8' }));
      await user.click(request.getByRole('button', { name: 'Reject' }));
      await user.type(request.getByRole('textbox', { name: 'Reason' }), 'The fields are still read by the Task list.');
      await user.click(request.getByRole('button', { name: 'Reject request' }));
      expect(await request.findByText('The Task did not get the reason and still holds #8.')).toBeTruthy();
      await user.click(request.getByRole('button', { name: 'Release #8' }));
      await waitFor(() => expect(inbox.queryByRole('article', { name: 'Done request on #8' })).toBeNull());
      await user.click(screen.getByRole('button', { name: 'Close inbox' }));
      expect(await tree.findByRole('treeitem', { name: '#8 Drop unused fields from the task summary, To do' })).toBeTruthy();
    } finally {
      spy.mockRestore();
    }
  });

  test('a heard rejection leaves the inbox', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Inbox, 6 pending' }));
    const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
    const request = within(inbox.getByRole('article', { name: 'Done request on #8' }));
    await user.click(request.getByRole('button', { name: 'Reject' }));
    await user.type(request.getByRole('textbox', { name: 'Reason' }), 'The fields are still read by the Task list.');
    await user.click(request.getByRole('button', { name: 'Reject request' }));
    await waitFor(() => expect(inbox.queryByRole('article', { name: 'Done request on #8' })).toBeNull());
  });

  test('the inbox shows a partial transcript, the proposed patch and every flag', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Inbox, 6 pending' }));
    const inbox = within(await screen.findByRole('list', { name: 'Pending requests' }));
    expect(within(inbox.getByRole('article', { name: 'Done request on #8' })).getByText('Touched files may be incomplete')).toBeTruthy();
    expect(within(inbox.getByRole('article', { name: 'Done request on #5' })).queryByText('Touched files may be incomplete')).toBeNull();
    const change = within(inbox.getByRole('article', { name: 'Change request on #9' }));
    expect(change.getByText('Send subagent previews only for recent Tasks')).toBeTruthy();
    expect(change.getByText('win condition')).toBeTruthy();
  });

  test('the card panel shows a partial transcript in its evidence trail', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 8);
    const trail = within(await panel.findByRole('region', { name: 'Evidence trail' }));
    expect(await trail.findByText('Touched files may be incomplete')).toBeTruthy();
  });

  test('the evidence trail tells a request accepted automatically from the owner’s, and keeps its evidence', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 4);
    const trail = within(await panel.findByRole('region', { name: 'Evidence trail' }));
    const request = within(await trail.findByRole('article', { name: 'Done request on #4' }));
    expect(request.getByText('Accepted automatically')).toBeTruthy();
    expect(request.getByText('make test')).toBeTruthy();
    expect(request.getByText('exit 0')).toBeTruthy();
    expect(request.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(await panel.findByText('Accepted automatically: the acceptance command passed')).toBeTruthy();
  });

  test('Purge reports how many cards went', async () => {
    const { user } = await openPlanner();
    const menu = await openMenu(user, 'Planner actions');
    await user.click(menu.getByRole('menuitem', { name: 'Purge cancelled (1)' }));
    const dialog = within(await screen.findByRole('alertdialog', { name: 'Purge 1 cancelled card?' }));
    await user.click(dialog.getByRole('button', { name: 'Purge' }));
    expect(await screen.findByText('Purged 1 card.')).toBeTruthy();
  });

  test('without the import route the import stays, disabled, and says why', async () => {
    const { user } = renderApp('#settings');
    const spy = serviceReply((url, method) => method === 'POST' && url.endsWith('/api/board/import'), () => reply(404, { error: 'not found' }));
    try {
      const planner = within(await screen.findByRole('region', { name: 'Planner' }));
      const form = within(planner.getByRole('form', { name: 'Import from kb' }));
      await user.type(form.getByRole('textbox', { name: 'Import from kb' }), '/home/dev/.local/share/kb');
      await user.click(form.getByRole('button', { name: 'Import' }));
      expect(await form.findByText('This service cannot import yet; an update adds it.')).toBeTruthy();
      expect((form.getByRole('textbox', { name: 'Import from kb' }) as HTMLInputElement).disabled).toBe(true);
      expect((form.getByRole('button', { name: 'Import' }) as HTMLButtonElement).disabled).toBe(true);
    } finally {
      spy.mockRestore();
    }
  });

  test('an import refusal shows the service’s reason', async () => {
    const { user } = renderApp('#settings');
    const spy = serviceReply((url, method) => method === 'POST' && url.endsWith('/api/board/import'), () => reply(409, { error: 'the source database is being written; try again', code: 'import_busy' }));
    try {
      const planner = within(await screen.findByRole('region', { name: 'Planner' }));
      const form = within(planner.getByRole('form', { name: 'Import from kb' }));
      await user.type(form.getByRole('textbox', { name: 'Import from kb' }), '/home/dev/.local/share/kb');
      await user.click(form.getByRole('button', { name: 'Import' }));
      expect((await form.findByRole('alert')).textContent).toBe('Could not import: the source board changed while it was copied; try again in a moment');
    } finally {
      spy.mockRestore();
    }
  });
});

/** The item labels of the open menu. */
const itemsOf = (menu: ReturnType<typeof within>) => menu.getAllByRole('menuitem').map((i) => i.textContent);

describe('card menus', () => {
  const SUBTASK = ['Open', 'Edit', 'Launch', 'Mark done', 'Check at HEAD', 'Move to…', 'Split', 'Cancel'];

  test('a Tree row’s … button and its context menu hold the same actions, which open the card panel’s dialogs', async () => {
    const { user, tree } = await openPlanner();
    expect(itemsOf(await openMenu(user, 'Actions for #22'))).toEqual(SUBTASK);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
    fireEvent.contextMenu(tree.getByRole('treeitem', { name: /^#22 / }));
    const context = within(await screen.findByRole('menu'));
    expect(itemsOf(context)).toEqual(SUBTASK);
    await user.click(context.getByRole('menuitem', { name: 'Move to…' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Move #22' }));
    await pick(user, dialog, 'Move to', '#12 Keep the transcript steady while pages land (in #1)');
    await user.click(dialog.getByRole('button', { name: 'Move' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Move #22' })).toBeNull());
    const rows = labels(tree);
    expect(rows[rows.findIndex((r) => r.startsWith('#15 ')) + 1]).toBe('#22 Set up the macOS signing step, To do');
  });

  test('a container’s menu adds under it and edits in place; an Unassigned card’s Edit says why it is off', async () => {
    const { user, tree } = await openPlanner();
    const menu = await openMenu(user, 'Actions for #2');
    expect(itemsOf(menu)).toEqual(['Open', 'Add subtask', 'Edit', 'Do whole story', 'Plan with agent', 'Suggest subtasks', 'Move to…', 'Cancel']);
    await user.click(menu.getByRole('menuitem', { name: 'Add subtask' }));
    expect(await screen.findByRole('form', { name: 'New subtask in #2' })).toBeTruthy();
    await user.keyboard('{Escape}');
    await user.click(await openMenu(user, 'Actions for #7').then((m) => m.getByRole('menuitem', { name: 'Edit' })));
    expect(await tree.findByRole('form', { name: 'Edit #7' })).toBeTruthy();
    await user.keyboard('{Escape}');
    await pickProject(user, /^Unassigned/);
    const unassigned = await openMenu(user, 'Actions for #36');
    expect(itemsOf(unassigned)).toEqual(['Open', 'Edit']);
    expect(unassigned.getByRole('menuitem', { name: 'Edit' }).getAttribute('aria-disabled')).toBe('true');
    expect(unassigned.getByText('An Unassigned card is read-only until it moves into a Project.')).toBeTruthy();
  });

  test('a Board card’s … button and context menu hold its actions; Edit opens the editor on the card', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('radio', { name: 'Board' }));
    const board = within(await screen.findByRole('region', { name: 'Board' }));
    fireEvent.contextMenu(board.getByRole('button', { name: '#22 Set up the macOS signing step' }));
    expect(itemsOf(within(await screen.findByRole('menu')))).toEqual(SUBTASK);
    await user.keyboard('{Escape}');
    const menu = await openMenu(user, 'Actions for #22');
    expect(itemsOf(menu)).toEqual(SUBTASK);
    await user.click(menu.getByRole('menuitem', { name: 'Edit' }));
    const form = within(await board.findByRole('form', { name: 'Edit #22' }));
    const title = form.getByRole('textbox', { name: 'Title' });
    await user.clear(title);
    await user.type(title, 'Sign the macOS build{Enter}');
    expect(await board.findByRole('button', { name: '#22 Sign the macOS build' })).toBeTruthy();
  });
});

describe('the floating pop-out', () => {
  const panel = () => screen.findByRole('region', { name: 'Planner pop-out' });
  const tab = () => screen.findByRole('button', { name: /^Show the planner/ });
  /** The Task's panel is the tab alone. */
  const folded = async () => {
    expect(await tab()).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
  };
  const box = (el: HTMLElement) => ['--fp-x', '--fp-y', '--fp-w', '--fp-h'].map((k) => el.style.getPropertyValue(k));

  test('Maximise fills the viewport and Restore returns the box; a double-click on the header does the same', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    const el = await panel();
    const pop = within(el);
    const restored = box(el);
    expect(restored[2]).toBe('520px');
    await user.click(pop.getByRole('button', { name: 'Maximise the pop-out' }));
    expect(box(el)).toEqual(['8px', '8px', `${window.innerWidth - 16}px`, `${window.innerHeight - 16}px`]);
    await user.click(pop.getByRole('button', { name: 'Restore the pop-out' }));
    expect(box(el)).toEqual(restored);
    await user.dblClick(pop.getByText('unified-agent-manager'));
    expect(pop.getByRole('button', { name: 'Restore the pop-out' })).toBeTruthy();
    await user.dblClick(pop.getByText('unified-agent-manager'));
    expect(box(el)).toEqual(restored);
  });

  test('Hide folds it into a tab with the pending count; the tab, by click or Enter, brings it back; focus follows', async () => {
    const { user, tree } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    within(await panel()).getByRole('button', { name: 'Hide the pop-out' }).focus();
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull());
    // Focus on the control that went moves to what took its place: the tab, then the panel's grip.
    const tab = screen.getByRole('button', { name: 'Show the planner, 6 pending' });
    await waitFor(() => expect(document.activeElement).toBe(tab));
    await user.keyboard('{Enter}');
    const pop = within(await panel());
    await waitFor(() => expect(document.activeElement).toBe(pop.getByRole('button', { name: 'Move the pop-out (arrow keys)' })));
    await user.click(pop.getByRole('button', { name: 'Hide the pop-out' }));
    await user.click(await screen.findByRole('button', { name: 'Show the planner, 6 pending' }));
    expect(await panel()).toBeTruthy();
    // Focus elsewhere stays put.
    await user.click(within(await panel()).getByRole('button', { name: 'Hide the pop-out' }));
    const row = tree.getByRole('treeitem', { name: /^#1 / });
    row.focus();
    fireEvent.click(await screen.findByRole('button', { name: 'Show the planner, 6 pending' }));
    await panel();
    expect(document.activeElement).toBe(row);
  });

  test('closing a pop-out over another Project’s Task keeps the Planner’s Board, selection and filter', async () => {
    const { user, tree } = await openPlanner();
    await pick(user, within(document.body), 'Epic', /^#1 /);
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    await panel();
    await openCard(user, tree, 2);
    await closeCard(user);
    // The row, not its hover actions (Settle).
    await user.click((await sidebar()).getAllByRole('button', { name: /Accessibility pass on the post template/ })[0]);
    await waitFor(() => expect(header().textContent).toMatch(/^Accessibility pass on the post template/));
    expect(within(await panel()).getByText('unified-agent-manager')).toBeTruthy();
    await user.click(within(await panel()).getByRole('button', { name: 'Close the pop-out' }));
    // This Task's panel takes its place, as the tab for this visit.
    expect(await screen.findByRole('button', { name: /^Show the planner/ })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
    await user.click((await sidebar()).getByRole('button', { name: 'Planner' }));
    await waitFor(() => expect(header().textContent).toBe('Planner'));
    expect(screen.getByRole('button', { name: 'Project: unified-agent-manager' })).toBeTruthy();
    expect(screen.getByRole('combobox', { name: 'Epic' }).textContent).toContain('#1 Faster first load');
    const plan = within(screen.getByRole('tree', { name: 'Plan outline' }));
    expect(plan.getByRole('treeitem', { name: /^#2 / }).getAttribute('aria-selected')).toBe('true');
  });

  test('a card opened from a Task’s panel shows in the Planner, clearing an epic filter that would hide it', async () => {
    const { user } = await openPlanner();
    await pick(user, within(document.body), 'Epic', /^#18 /);
    await user.click((await sidebar()).getAllByRole('button', { name: /Fix re-attach redraw regression/ })[0]);
    await waitFor(() => expect(header().textContent).toBe('Fix re-attach redraw regression'));
    await user.click(await tab());
    await user.click(await within(await panel()).findByRole('treeitem', { name: /^#2 / }));
    expect(await screen.findByLabelText('Card #2')).toBeTruthy();
    await closeCard(user);
    expect(screen.getByRole('combobox', { name: 'Epic' }).textContent).toContain('All epics');
    const plan = within(screen.getByRole('tree', { name: 'Plan outline' }));
    expect(plan.getByRole('treeitem', { name: /^#2 / }).getAttribute('aria-selected')).toBe('true');
  });

  test('a Task’s panel moved into a separate window shows its Board, and the Planner switches to that Board', async () => {
    const frame = document.createElement('iframe');
    document.body.append(frame);
    const pipDoc = frame.contentDocument!;
    const listeners = new Set<() => void>();
    const pip = {
      document: pipDoc,
      closed: false,
      close() {
        this.closed = true;
        for (const l of listeners) l();
      },
      addEventListener: (_: string, l: () => void) => listeners.add(l),
      removeEventListener: (_: string, l: () => void) => listeners.delete(l),
    };
    window.documentPictureInPicture = { requestWindow: async () => pip as unknown as Window };
    try {
      const { user } = await openPlanner();
      await user.click((await sidebar()).getAllByRole('button', { name: /Accessibility pass on the post template/ })[0]);
      await waitFor(() => expect(header().textContent).toMatch(/^Accessibility pass on the post template/));
      await user.click(await tab());
      await user.click(within(await panel()).getByRole('button', { name: 'Open in a separate window' }));
      expect(await within(pipDoc.body).findByRole('treeitem', { name: '#32 Accessible post template, Doing' })).toBeTruthy();
      expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
      await user.click(within(pipDoc.body).getByRole('button', { name: 'Close the pop-out' }));
      await waitFor(() => expect(pipDoc.body.textContent).toBe(''));
      // Closed over the Task: its panel stays the tab for the rest of the visit.
      expect(await screen.findByRole('button', { name: /^Show the planner/ })).toBeTruthy();
      await user.click((await sidebar()).getByRole('button', { name: 'Planner' }));
      await waitFor(() => expect(header().textContent).toBe('Planner'));
      expect(screen.getByRole('button', { name: 'Project: notes-site' })).toBeTruthy();
    } finally {
      delete window.documentPictureInPicture;
      frame.remove();
    }
  });

  test('a pop-out from the Planner opens expanded and follows the Planner’s Board, whatever an earlier version stored', async () => {
    localStorage.setItem('uam.plannerHidden', JSON.stringify({ p1: true, p3: true }));
    const { user } = await openPlanner();
    // The earlier version's key is removed once the planner mounts.
    expect(localStorage.getItem('uam.plannerHidden')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    await panel();
    await pickProject(user, /^notes-site/);
    expect(await within(await panel()).findByRole('treeitem', { name: '#32 Accessible post template, Doing' })).toBeTruthy();
  });

  test('a git Project’s Task starts as the tab alone, over live cards and a Show an earlier version stored', async () => {
    localStorage.setItem('uam.plannerHidden', JSON.stringify({ p1: false }));
    await openTask('t1');
    await folded();
    // Decided once for the visit: nothing opens it later.
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
  });

  test('opened, then left for another Task and come back to, it starts as the tab again; Hide folds it for the visit', async () => {
    const { user } = await openTask('t1');
    await user.click(await tab());
    await user.click(within(await panel()).getByRole('button', { name: 'Hide the pop-out' }));
    await folded();
    await user.click(await tab());
    expect(within(await panel()).getByText('unified-agent-manager')).toBeTruthy();
    window.location.hash = '#task=t8';
    await waitFor(() => expect(header().textContent).toMatch(/^Accessibility pass on the post template/));
    await folded();
    window.location.hash = '#task=t1';
    await waitFor(() => expect(header().textContent).toBe('Fix re-attach redraw regression'));
    await folded();
  });

  test('over a git Project’s Task its tab opens that Task’s Board, and it never shows over the Planner', async () => {
    const { user } = await openTask('t8');
    await user.click(await tab());
    const pop = within(await panel());
    expect(pop.getByText('notes-site')).toBeTruthy();
    expect(await pop.findByRole('treeitem', { name: '#32 Accessible post template, Doing' })).toBeTruthy();
    // Only folds: no close for a panel nobody opened.
    expect(pop.queryByRole('button', { name: 'Close the pop-out' })).toBeNull();
    await user.click((await sidebar()).getByRole('button', { name: 'Planner' }));
    await waitFor(() => expect(header().textContent).toBe('Planner'));
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Project: notes-site' })).toBeTruthy();
    // The one popped out here stays across the way back to a Task.
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    await panel();
    await user.click((await sidebar()).getByRole('button', { name: /Fix re-attach redraw regression/ }));
    await waitFor(() => expect(header().textContent).toBe('Fix re-attach redraw regression'));
    expect(within(await panel()).getByText('notes-site')).toBeTruthy();
  });

  test('a Task without git shows none, and one whose Board is empty starts as the tab and stays one as cards land', async () => {
    const { user } = await openTask('t6');
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Show the planner/ })).toBeNull();
    const project = await api.createProject({ dir: '/home/user/projects/empty-plan' });
    const task = await api.createSession({ project_id: project.id, provider: 'copilot', request_id: 'empty-plan-task' });
    window.location.hash = `#task=${task.id}`;
    const tab = await screen.findByRole('button', { name: 'Show the planner' });
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
    // Decided once for the visit: a first card (a planning Task's) does not open it over the conversation.
    await act(() => api.planner.create({ project_id: project.id, kind: 'epic', parent_id: null, title: 'Plan the empty project', win_condition: 'It has a plan' }));
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(screen.queryByRole('region', { name: 'Planner pop-out' })).toBeNull();
    await user.click(tab);
    const pop = within(await panel());
    expect(pop.getByText('empty-plan')).toBeTruthy();
    expect(await pop.findByRole('treeitem', { name: /Plan the empty project/ })).toBeTruthy();
  });

  test('junk stored under an earlier version’s key or the box’s leaves the tab and the panel working', async () => {
    for (const junk of ['null', '[true]', '{"p1":"yes"}', '{']) {
      localStorage.setItem('uam.plannerHidden', junk);
      localStorage.setItem('uam.plannerBox', junk);
      const { user, unmount } = await openTask('t1');
      await user.click(await tab());
      expect(box(await panel())[2]).toBe('520px');
      unmount();
    }
  });

  test('the box lasts across a reload, and a Show does not: the next visit starts as the tab', async () => {
    const first = await openTask('t1');
    await first.user.click(await tab());
    const grip = within(await panel()).getByRole('button', { name: 'Move the pop-out (arrow keys)' });
    grip.focus();
    await first.user.keyboard('{ArrowLeft}{ArrowDown}');
    const moved = box(await panel());
    expect(JSON.parse(localStorage.getItem('uam.plannerBox')!)).toMatchObject({ x: parseInt(moved[0]), y: parseInt(moved[1]) });
    first.unmount();

    const second = await openTask('t1');
    await folded();
    await second.user.click(await tab());
    expect(box(await panel())).toEqual(moved);
  });
});
