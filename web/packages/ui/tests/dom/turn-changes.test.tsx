import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type Item, type TurnChanges, type TurnTiming } from '../../src/api';
import { Transcript } from '../../src/components/Transcript';
import { SessionContext } from '../../src/components/common';
import { LiftedRow } from '../../src/components/ui/reader';
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
    <Transcript sessionId="task" provider="copilot" workdir="/work" items={value} interactions={[]} subagents={[]} live={false} working={false} turnTimings={timings} onOpenThreadChanges={all} readersActive={active} readerGeneration={generation} />
  </DetailVisibility></SessionContext.Provider></ApiContext.Provider>;
  return { read, all, view, ...render(view()) };
}

test('native turn facts load only when its Changed chip opens and remain historical across later turns', async () => {
  const user = userEvent.setup();
  const f = fixture();
  const foot = screen.getByRole('button', { name: 'Changed 1 file +2 −1' });
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

test('the reply menu opens the readonly historical reader, which leads on to the Thread\'s current changes', async () => {
  const user = userEvent.setup();
  const f = fixture();
  await user.click(screen.getByRole('button', { name: 'Turn actions' }));
  const menu = await screen.findByRole('menu');
  expect(within(menu).queryByRole('menuitem', { name: /All changes|Rewind|Branch|Edit and resend/ })).toBeNull();
  await user.click(within(menu).getByRole('menuitem', { name: "This turn's changes" }));
  await user.click(await within(await screen.findByRole('dialog')).findByRole('button', { name: 'Open current changes' }));
  await waitFor(() => expect(f.all).toHaveBeenCalledTimes(1));
});

test('one reply menu carries the turn changes and Branch from here', async () => {
  const user = userEvent.setup();
  const branch = vi.fn();
  render(<ApiContext.Provider value={{ ...api, turnChanges: vi.fn() }}><SessionContext.Provider value="task"><DetailVisibility open>
    <Transcript sessionId="task" workdir="/work" items={items} interactions={[]} subagents={[]} live={false} working={false} turnTimings={[timing]} onOpenThreadChanges={vi.fn()} onBranch={branch} />
  </DetailVisibility></SessionContext.Provider></ApiContext.Provider>);
  const triggers = screen.getAllByRole('button', { name: 'Turn actions' });
  expect(triggers).toHaveLength(1);
  await user.click(triggers[0]);
  const menu = await screen.findByRole('menu');
  expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(["This turn's changes", 'Branch from here']);
  await user.click(within(menu).getByRole('menuitem', { name: 'Branch from here' }));
  expect(branch).toHaveBeenCalledWith('owner', triggers[0]);
});

test('a turn that changed no file shows no changes in its outcome bar or its menu', async () => {
  render(<ApiContext.Provider value={{ ...api, turnChanges: vi.fn() }}><SessionContext.Provider value="task"><DetailVisibility open>
    <Transcript sessionId="task" workdir="/work" items={items} interactions={[]} subagents={[]} live={false} working={false} turnTimings={[{ ...timing, changes: { status: 'available', files: 0, additions: 0, deletions: 0 } }]} onOpenThreadChanges={vi.fn()} />
  </DetailVisibility></SessionContext.Provider></ApiContext.Provider>);
  expect(screen.queryByRole('button', { name: /0 files/ })).toBeNull();
  // Nothing to offer: the menu itself is gone.
  expect(screen.queryByRole('button', { name: 'Turn actions' })).toBeNull();
});

test('unavailable native facts never display guessed zero or fetch historical rows', async () => {
  const user = userEvent.setup();
  const f = fixture();
  f.rerender(f.view(true, 'one', true, [{ ...timing, changes: { status: 'busy' } }]));
  expect(screen.queryByRole('button', { name: /0 files/ })).toBeNull();
  // No chip and no "unavailable" noise in the card; the reason stays one step away in the turn's menu.
  expect(screen.queryByRole('button', { name: /^Changed/ })).toBeNull();
  expect(document.body.textContent).not.toContain('Changes unavailable');
  await user.click(screen.getByRole('button', { name: 'Turn actions' }));
  await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: "This turn's changes" }));
  const dialog = await screen.findByRole('dialog');
  expect(dialog.textContent).toContain('still busy');
  expect(f.read).not.toHaveBeenCalled();
});

test.each(['inactive', 'generation', 'fold', 'unmount'])('a %s reader drops its rows and cancels a late read', async kind => {
  let resolve!: (rows: TurnChanges) => void;
  const read = vi.fn<(task: string, timing: string, signal?: AbortSignal) => Promise<TurnChanges>>(() => new Promise<TurnChanges>(r => { resolve = r; }));
  const f = fixture(read);
  await userEvent.setup().click(screen.getByRole('button', { name: 'Changed 1 file +2 −1' }));
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
  await userEvent.setup().click(screen.getByRole('button', { name: 'Changed 1 file +2 −1' }));
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'The Thread changed. Close this reader and open it again.');
  f.rerender(f.view(true, 'two', true, [{ ...timing, changes: undefined }]));
  expect(screen.queryByRole('button', { name: /Changes unavailable|1 file/ })).toBeNull();
  expect(screen.getByText('First reply.')).toBeTruthy();
});


test('the lifted copy of a foot chip keeps the small type its row gives it, so the chip is not cut', () => {
  const foot = document.createElement('div');
  foot.style.fontSize = '10px';
  foot.style.lineHeight = '10px';
  foot.style.color = 'rgb(1, 2, 3)';
  const chip = foot.appendChild(document.createElement('button'));
  chip.textContent = '1 file +1 −0';
  document.body.append(foot);
  const { container } = render(<LiftedRow row={chip} />);
  const host = container.firstElementChild as HTMLElement;
  expect(host.textContent).toBe('1 file +1 −0');
  expect([host.style.fontSize, host.style.lineHeight, host.style.color]).toEqual(['10px', '10px', 'rgb(1, 2, 3)']);
  foot.remove();
});
