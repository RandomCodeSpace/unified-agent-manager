import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type Item, type SessionSummary, type TurnChanges, type TurnTiming } from '../../src/api';
import { ChangesSheet } from '../../src/components/Changes';
import { Transcript } from '../../src/components/Transcript';
import { SessionContext } from '../../src/components/common';
import { DetailVisibility } from '../../src/components/Details';

const at = '2026-10-09T12:00:00Z';
const items: Item[] = [
  { id: 'owner', kind: 'user', text: 'change it', time: at },
  { id: 'answer', kind: 'assistant', text: 'First reply.', time: at },
];
const timing: TurnTiming = { id: 'timing', user_item_id: 'owner', started_at: at, ended_at: at, state: 'completed', changes: { status: 'available', event_id: 'native-owner', files: 1, additions: 2, deletions: 1 } };
const record: TurnChanges = { timing_id: 'timing', ended_at: at, counts: timing.changes!, files: [{ path: '/work/old.txt', kind: 'modified', additions: 2, deletions: 1 }] };

function fixture(read = vi.fn().mockResolvedValue(record)) {
  const client = { ...api, turnChanges: read };
  const all = vi.fn();
  const view = (active = true, generation = 'one', visible = true, timings = [timing], value = items) => <ApiContext.Provider value={client}><SessionContext.Provider value="task"><DetailVisibility open={visible}>
    <Transcript sessionId="task" provider="copilot" workdir="/work" items={value} interactions={[]} subagents={[]} live={false} working={false} turnTimings={timings} onOpenAllChanges={all} readersActive={active} readerGeneration={generation} />
  </DetailVisibility></SessionContext.Provider></ApiContext.Provider>;
  return { read, all, view, ...render(view()) };
}

test('native turn facts load only when its foot opens and remain historical across later turns', async () => {
  const user = userEvent.setup();
  const f = fixture();
  const foot = screen.getByRole('button', { name: '1 file +2 −1' });
  expect(f.read).not.toHaveBeenCalled();
  const later: TurnTiming = { ...timing, id: 'later', user_item_id: 'next', changes: { status: 'available', event_id: 'native-next', files: 1, additions: 7, deletions: 5 } };
  f.rerender(f.view(true, 'one', true, [timing, later], [...items, { id: 'next', kind: 'user', text: 'again', time: at }, { id: 'last', kind: 'assistant', text: 'Later reply.', time: at }]));
  expect(f.read).not.toHaveBeenCalled();
  await user.click(foot);
  const dialog = await screen.findByRole('dialog', { name: "This turn's changes" });
  expect(await within(dialog).findByText('/work/old.txt')).toBeTruthy();
  expect(f.read).toHaveBeenCalledTimes(1);
  expect(f.read.mock.calls[0].slice(0, 2)).toEqual(['task', 'timing']);
  expect(dialog.textContent).toContain('As this turn left it');
  expect(dialog.textContent).toContain('historical facts');
  expect(dialog.className).not.toContain('border');
  await user.click(within(dialog).getByRole('button', { name: 'Close' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(foot));
});

test('the reply menu exposes the readonly historical reader and separate current All changes', async () => {
  const user = userEvent.setup();
  const f = fixture();
  await user.click(screen.getByRole('button', { name: 'Turn actions' }));
  const menu = await screen.findByRole('menu');
  expect(within(menu).getByRole('menuitem', { name: "This turn's changes" })).toBeTruthy();
  expect(within(menu).queryByRole('menuitem', { name: /Rewind|Branch|Edit and resend/ })).toBeNull();
  await user.click(within(menu).getByRole('menuitem', { name: 'All changes' }));
  await waitFor(() => expect(f.all).toHaveBeenCalledTimes(1));
  expect(f.read).not.toHaveBeenCalled();
});

test('unavailable native facts never display guessed zero or fetch historical rows', async () => {
  const user = userEvent.setup();
  const f = fixture();
  f.rerender(f.view(true, 'one', true, [{ ...timing, changes: { status: 'busy' } }]));
  expect(screen.queryByRole('button', { name: /0 files/ })).toBeNull();
  await user.click(screen.getByRole('button', { name: 'Changes unavailable' }));
  const dialog = await screen.findByRole('dialog');
  expect(dialog.textContent).toContain('still busy');
  expect(f.read).not.toHaveBeenCalled();
});

test.each(['inactive', 'generation', 'fold', 'unmount'])('a %s reader drops its rows and cancels a late read', async kind => {
  let resolve!: (rows: TurnChanges) => void;
  const read = vi.fn<(task: string, timing: string, signal?: AbortSignal) => Promise<TurnChanges>>(() => new Promise<TurnChanges>(r => { resolve = r; }));
  const f = fixture(read);
  await userEvent.setup().click(screen.getByRole('button', { name: '1 file +2 −1' }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  const signal = read.mock.calls[0][2]!;
  if (kind === 'unmount') f.unmount();
  else f.rerender(f.view(kind !== 'inactive', kind === 'generation' ? 'two' : 'one', kind !== 'fold'));
  await waitFor(() => expect(signal.aborted).toBe(true));
  resolve(record);
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(screen.queryByText('/work/old.txt')).toBeNull();
});

test('wrong immutable timing identity is rejected and legacy replies stay unchanged', async () => {
  const f = fixture(vi.fn().mockResolvedValue({ ...record, timing_id: 'wrong' }));
  await userEvent.setup().click(screen.getByRole('button', { name: '1 file +2 −1' }));
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'The Task changed. Close this reader and open it again.');
  f.rerender(f.view(true, 'two', true, [{ ...timing, changes: undefined }]));
  expect(screen.queryByRole('button', { name: /Changes unavailable|1 file/ })).toBeNull();
  expect(screen.getByText('First reply.')).toBeTruthy();
});


test('All changes starts the current workspace scope even when the Task default is native session', async () => {
  const changes = vi.fn().mockResolvedValue({ scope: 'workspace', supported: true, label: 'Whole working tree', files: [] });
  const client = { ...api, changes };
  const session = { id: 'task', provider: 'copilot', project_id: 'project', workdir: '/work', state: 'completed', stage: '', capabilities: { session_diff: true } } as SessionSummary;
  render(<ApiContext.Provider value={client}><ChangesSheet session={session} projectName="Project" changes={null} changesError={null} isDefaultPending={() => false} inline={false} open active onChanges={() => {}} onClose={() => {}} onClosed={() => {}} turn={{ files: [], latest: false, at: 1, scope: 'workspace' }} /></ApiContext.Provider>);
  await waitFor(() => expect(changes).toHaveBeenCalledTimes(1));
  expect(changes.mock.calls[0].slice(0, 2)).toEqual(['task', 'workspace']);
  expect(await screen.findByText('No uncommitted changes.')).toBeTruthy();
});
