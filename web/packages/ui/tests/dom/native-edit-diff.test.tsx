import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type Item, type ItemDiffData, type SessionDetail } from '../../src/api';
import { DetailsProvider, DetailVisibility } from '../../src/components/Details';
import { ToolRow } from '../../src/components/Transcript';
import { EditDiffCache, EDIT_DIFF_BYTES, EDIT_DIFF_LIMIT } from '../../src/lib/edit-diff-cache';

const path = '/work/file.txt';
const patch = 'diff --git a/work/file.txt b/work/file.txt\n--- a/work/file.txt\n+++ b/work/file.txt\n@@ -1 +1 @@\n-before\n+recorded-first\n';
const row: Item = { id: 'tool', agent_id: 'agent', kind: 'tool', time: '2026-10-09T00:00:00Z', tool: { name: 'edit', status: 'failed', edit_event_id: 'completion', file_edits: [{ path, kind: 'edit', additions: 1, deletions: 1, diff_status: 'available' }] } };
const session = { id: 'task', epoch: 'epoch', items: [], interactions: [], subagents: [] } as unknown as SessionDetail;
const response = (text = patch): ItemDiffData => ({ session_id: 'task', epoch: 'epoch', agent_id: 'agent', item_id: 'tool', event_id: 'completion', path, status: 'available', patch: text });
function harness(read = vi.fn(async () => response())) {
  const body = vi.fn(api.itemBody);
  const client = { ...api, itemDiff: read, itemBody: body };
  function View({ active = true, generation = 0, visible = true, mounted = true, item = row }: { active?: boolean; generation?: number; visible?: boolean; mounted?: boolean; item?: Item }) {
    return <ApiContext.Provider value={client}><DetailsProvider session={session} active={active} generation={generation} versions={{}} onAuthLost={() => {}}><DetailVisibility open={visible}>{mounted && <ToolRow item={item} live={false} sessionId="task" />}</DetailVisibility></DetailsProvider></ApiContext.Provider>;
  }
  return { View, read, body };
}
const editButton = () => screen.getByRole('button', { name: /file.txt.*edit diff/ });

test('a failed committed edit fetches only its exact recorded patch when opened and releases the viewer on close', async () => {
  const { View, read, body } = harness();
  const view = render(<View />);
  expect(read).not.toHaveBeenCalled();
  expect(editButton().textContent).toContain('+1');
  await userEvent.click(editButton());
  await screen.findByRole('table');
  expect(read).toHaveBeenCalledWith('task', 'tool', 'agent', 'completion', path, expect.any(AbortSignal));
  expect(body).not.toHaveBeenCalled();
  expect(screen.getByRole('table').textContent).toContain('recorded-first');
  expect(screen.getByRole('table').parentElement?.style.contentVisibility).toBe('auto');
  await userEvent.click(editButton());
  expect(screen.queryByRole('table')).toBeNull();
  await userEvent.click(editButton());
  await screen.findByRole('table');
  expect(read).toHaveBeenCalledTimes(1);
  view.rerender(<View mounted={false} />);
  expect(screen.queryByRole('table')).toBeNull();
  view.rerender(<View />);
  await screen.findByRole('table');
  expect(read).toHaveBeenCalledTimes(1);
  // Native patch text is absent from both storage systems' synchronous keys.
  expect(localStorage.length).toBe(0); expect(sessionStorage.length).toBe(0);
});

test('unsupported native detail keeps unknown counts and never requests a substitute patch', async () => {
  const { View, read } = harness();
  const item = { ...row, tool: { ...row.tool!, file_edits: [{ path, kind: 'edit', diff_status: 'binary' }] } };
  render(<View item={item} />);
  expect(editButton().textContent).toContain('counts unavailable');
  await userEvent.click(editButton());
  expect(screen.getByRole('status').textContent).toContain('binary edit');
  expect(read).not.toHaveBeenCalled();
});

test('failed reads retry and reject a response for another agent or event', async () => {
  const read = vi.fn().mockResolvedValueOnce({ ...response(), event_id: 'other' }).mockResolvedValueOnce(response());
  const { View } = harness(read);
  render(<View />);
  await userEvent.click(editButton());
  await screen.findByRole('alert');
  expect(screen.queryByRole('table')).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByRole('table');
  expect(read).toHaveBeenCalledTimes(2);
});

test('inactivity, a folded parent, and a new history generation release readers and cached viewers', async () => {
  const { View, read } = harness();
  const view = render(<View />);
  fireEvent.click(editButton()); await screen.findByRole('table');
  view.rerender(<View visible={false} />);
  expect(screen.queryByRole('table')).toBeNull();
  view.rerender(<View active={false} />);
  expect(screen.queryByRole('table')).toBeNull();
  view.rerender(<View />); await screen.findByRole('table');
  expect(read).toHaveBeenCalledTimes(2);
  view.rerender(<View generation={1} />); await screen.findByRole('table');
  expect(read).toHaveBeenCalledTimes(3);
});

test('an inline patch renders at most 400 rows until Show all and supports keyboard disclosure', async () => {
  const lines = Array.from({ length: 450 }, (_, i) => `+line-${i}`).join('\n');
  const large = `--- /dev/null\n+++ b/work/file.txt\n@@ -0,0 +1,450 @@\n${lines}\n`;
  const { View } = harness(vi.fn(async () => response(large)));
  render(<View />);
  editButton().focus(); await userEvent.keyboard('{Enter}');
  const table = await screen.findByRole('table');
  expect(within(table).getAllByRole('row')).toHaveLength(400);
  await userEvent.click(screen.getByRole('button', { name: 'Show all 451 lines' }));
  expect(within(table).getAllByRole('row')).toHaveLength(451);
  expect(editButton().getAttribute('aria-expanded')).toBe('true');
});

test('the independent cache evicts by entries and retained bytes and ignores late canceled reads', async () => {
  const cache = new EditDiffCache();
  for (let i = 0; i <= EDIT_DIFF_LIMIT; i++) cache.load(String(i), async () => response());
  await waitFor(() => expect(cache.get(String(EDIT_DIFF_LIMIT)).status).toBe('loaded'));
  expect(cache.get('0').status).toBe('unloaded');
  cache.clear();
  const text = 'x'.repeat(Math.floor(EDIT_DIFF_BYTES / 3));
  cache.load('a', async () => response(text));
  await waitFor(() => expect(cache.get('a').status).toBe('loaded'));
  cache.load('b', async () => response(text));
  await waitFor(() => expect(cache.get('b').status).toBe('loaded'));
  expect(cache.get('a').status).toBe('unloaded');
  let resolve!: (value: ItemDiffData) => void;
  let signal!: AbortSignal;
  cache.load('late', value => { signal = value; return new Promise(done => { resolve = done; }); });
  cache.clear(); expect(signal.aborted).toBe(true);
  await act(async () => { resolve(response()); await Promise.resolve(); });
  expect(cache.get('late').status).toBe('unloaded');
});


test('an oversized read error is bounded, copied and charged against the same cache byte budget', async () => {
  const cache = new EditDiffCache();
  const text = 'x'.repeat(Math.floor((EDIT_DIFF_BYTES - 1024) / 2));
  cache.load('retained', async () => response(text));
  await waitFor(() => expect(cache.get('retained').status).toBe('loaded'));
  cache.load('error', async () => { throw new Error('E'.repeat(EDIT_DIFF_BYTES)); });
  await waitFor(() => expect(cache.get('error').status).toBe('error'));
  const state = cache.get('error');
  expect(state.status === 'error' && state.error).toBe('E'.repeat(512));
  expect(cache.get('retained').status).toBe('unloaded');
  cache.clear();
});
