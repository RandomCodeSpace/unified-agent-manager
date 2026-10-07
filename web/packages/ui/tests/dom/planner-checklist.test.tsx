// Removing a checklist item: × in the Planner's card panel and in a Task's Plan panel sends the
// card's checklist without it through the owner's PATCH, a refusal shows in the planner's notice,
// and × stays on the row while its text is being renamed.
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { openTask, renderApp, sidebar, type User } from './render';

/** Opens the planner from the sidebar and waits for unified-agent-manager's outline. */
async function openPlanner() {
  const rendered = renderApp();
  const side = await sidebar();
  await rendered.user.click(side.getByRole('button', { name: 'Planner' }));
  const tree = within(await screen.findByRole('tree', { name: 'Plan outline' }));
  await tree.findByRole('treeitem', { name: /^#1 Faster first load of long transcripts/ });
  return { ...rendered, tree };
}

/** Opens a card's detail from the Tree. */
async function openCard(user: User, tree: ReturnType<typeof within>, seq: number) {
  await user.click(tree.getByRole('treeitem', { name: new RegExp(`^#${seq} `) }));
  return within(await screen.findByLabelText(`Card #${seq}`));
}

/** The card PATCHes sent while `fetch` was spied on, as URL and parsed body. */
function patches(fetch: ReturnType<typeof vi.spyOn<Window, 'fetch'>>) {
  return fetch.mock.calls
    .filter(([, init]) => init?.method === 'PATCH')
    .map(([input, init]) => ({ url: String(input instanceof Request ? input.url : input), body: JSON.parse(String(init?.body)) as unknown }));
}

/** The checklist items' texts, in order. */
const texts = (region: ReturnType<typeof within>) => region.queryAllByRole('checkbox').map((x) => x.getAttribute('aria-label'));

describe('removing a checklist item', () => {
  test('× in the card panel sends the checklist without the item, and the item goes', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    const fetch = vi.spyOn(window, 'fetch');
    try {
      await user.click(panel.getByRole('button', { name: 'Remove Scroll to the middle' }));
      const left = within(await panel.findByRole('region', { name: 'Checklist 0/1' }));
      expect(texts(left)).toEqual(['Land an older page']);
      const sent = patches(fetch);
      expect(sent).toHaveLength(1);
      expect(sent[0].url).toMatch(/\/api\/board\/cards\/cp1-15$/);
      expect(sent[0].body).toEqual({ checklist: [{ text: 'Land an older page', done: false }] });
    } finally {
      fetch.mockRestore();
    }
    expect((await api.planner.board('p1')).cards.find((c) => c.seq === 15)!.checklist).toEqual([{ text: 'Land an older page', done: false }]);
  });

  test('a refusal shows in the planner’s notice, and the item stays', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    const real = window.fetch;
    const spy = vi.spyOn(window, 'fetch').mockImplementation((input, init) =>
      init?.method === 'PATCH'
        ? Promise.resolve(new Response(JSON.stringify({ error: 'Release #15 first: it is in progress, and a card in progress keeps its plan', code: 'in_progress' }), { status: 409, headers: { 'Content-Type': 'application/json' } }))
        : real(input, init),
    );
    try {
      await user.click(panel.getByRole('button', { name: 'Remove Scroll to the middle' }));
      // At this width the card panel is an overlay, which hides the notice from the accessibility tree.
      expect((await screen.findByRole('alert', { hidden: true })).textContent).toContain('Could not save the checklist: Release #15 first: it is in progress, and a card in progress keeps its plan');
      expect(texts(within(panel.getByRole('region', { name: 'Checklist 0/2' })))).toEqual(['Scroll to the middle', 'Land an older page']);
      expect((panel.getByRole('button', { name: 'Remove Scroll to the middle' }) as HTMLButtonElement).disabled).toBe(false);
    } finally {
      spy.mockRestore();
    }
  });

  test('× stays on an item being renamed: it removes the item, typed text and all, and an emptied one too', async () => {
    const { user, tree } = await openPlanner();
    const panel = await openCard(user, tree, 15);
    const fetch = vi.spyOn(window, 'fetch');
    try {
      // A changed text is not saved first: the item goes as it was stored.
      await user.click(panel.getByRole('button', { name: 'Edit Scroll to the middle' }));
      await user.type(panel.getByRole('textbox', { name: 'Item text' }), ' row');
      await user.click(panel.getByRole('button', { name: 'Remove Scroll to the middle' }));
      const left = within(await panel.findByRole('region', { name: 'Checklist 0/1' }));
      expect(texts(left)).toEqual(['Land an older page']);
      expect(panel.queryByRole('textbox', { name: 'Item text' })).toBeNull();
      expect(patches(fetch).map((p) => p.body)).toEqual([{ checklist: [{ text: 'Land an older page', done: false }] }]);

      // An emptied item is refused in place, and × next to it removes it, the last one included.
      await user.click(left.getByRole('button', { name: 'Edit Land an older page' }));
      await user.clear(panel.getByRole('textbox', { name: 'Item text' }));
      await user.keyboard('{Enter}');
      expect(panel.getByRole('alert').textContent).toBe('An item needs text. Esc keeps the old one, or remove the item.');
      await user.click(panel.getByRole('button', { name: 'Remove Land an older page' }));
      const empty = within(await panel.findByRole('region', { name: 'Checklist' }));
      expect(texts(empty)).toEqual([]);
      expect(panel.queryByRole('textbox', { name: 'Item text' })).toBeNull();
      expect(patches(fetch).map((p) => p.body)).toEqual([{ checklist: [{ text: 'Land an older page', done: false }] }, { checklist: [] }]);
    } finally {
      fetch.mockRestore();
    }
  });

  test('× in a Task’s Plan panel removes an item of a subtask not started', async () => {
    const { user } = await openTask('t21');
    await act(() => api.planner.edit('cp1-26', { checklist: [{ text: 'Fetch the signature', done: true }, { text: 'Refuse a mismatch', done: false }] }));
    await waitFor(() => expect(screen.queryByRole('button', { name: /^This task works on/, hidden: true })).toBeTruthy());
    await user.click(screen.getByRole('button', { name: /^This task works on/, hidden: true }));
    const panel = within(await screen.findByRole('dialog', { name: 'Plan' }));
    await user.click(within(panel.getByRole('list', { name: 'Plan outline' })).getByRole('button', { name: /^#26 Verify signatures in the install script/ }));
    const details = within(panel.getByRole('region', { name: '#26 details' }));
    const edit = vi.spyOn(api.planner, 'edit');
    try {
      await user.click(details.getByRole('button', { name: 'Remove Fetch the signature' }));
      await waitFor(() => expect(edit).toHaveBeenCalledWith('cp1-26', { checklist: [{ text: 'Refuse a mismatch', done: false }] }));
      expect(texts(within(await details.findByRole('region', { name: 'Checklist 0/1' })))).toEqual(['Refuse a mismatch']);
    } finally {
      edit.mockRestore();
    }
  });
});
