import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { copyStyles, popMode } from '../../src/components/planner/PopOut';
import { renderApp, sidebar } from './render';

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

  test('without Picture-in-Picture the pop-out is a floating panel on the page', async () => {
    const { user } = await openPlanner();
    await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
    const pop = within(await screen.findByRole('dialog', { name: 'Planner pop-out' }));
    expect(pop.getByRole('radio', { name: 'Tree' }).getAttribute('aria-checked')).toBe('true');
    await user.click(pop.getByRole('radio', { name: /Inbox/ }));
    expect(await pop.findByRole('list', { name: 'Pending requests' })).toBeTruthy();
    await user.click(pop.getByRole('button', { name: 'Close the pop-out' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Planner pop-out' })).toBeNull());
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
      await user.click(screen.getByRole('button', { name: 'Pop out the tree' }));
      await waitFor(() => expect(requestWindow).toHaveBeenCalledOnce());
      const inWindow = within(pipDoc.body);
      expect(await inWindow.findByRole('treeitem', { name: /^#1 Faster first load/ })).toBeTruthy();
      expect(screen.queryByRole('dialog', { name: 'Planner pop-out' })).toBeNull();
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

  test('a phone, or a browser without the API, gets the floating panel', () => {
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
