import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { ApiError, api, type Item, type TurnTiming, type TurnTodos } from '../../src/api';
import { Markdown } from '../../src/components/common';
import { EChart } from '../../src/components/EChart';
import { chartOption } from '../../src/lib/chart';
import { ToolRow, Transcript, replyEnds } from '../../src/components/Transcript';
import { saveDensity } from '../../src/lib/density';
import { composer, log, openMenu, openTask } from './render';

afterEach(() => saveDensity('compact'));

const conversation = () => screen.getByRole('region', { name: 'Conversation' });

describe('messages', () => {
  test.each(['compact', 'detailed'] as const)('skill invocation notices stay visible and ordered in %s turns', (density) => {
    saveDensity(density);
    const items: Item[] = [
      { id: 'u', kind: 'user', time: '2026-10-09T00:00:00Z', text: 'Review the change' },
      { id: 's1', kind: 'notice', time: '2026-10-09T00:00:01Z', text: 'Skill: ` uam `' },
      { id: 's2', kind: 'notice', time: '2026-10-09T00:00:02Z', text: 'Skill: ` uam `' },
      { id: 's3', kind: 'notice', time: '2026-10-09T00:00:03Z', text: 'Skill: `` my_skill*[docs](https://example.test)<b>` ``' },
      { id: 'a', kind: 'assistant', time: '2026-10-09T00:00:04Z', text: 'Reviewed.' },
    ];
    const view = render(<Transcript sessionId="s" items={items} interactions={[]} subagents={[]} live working={false} provider="copilot" workdir="/w" liveCard />);
    const label = (text: string) => (_: string, el: Element | null) => el?.tagName === 'P' && el.textContent === text;
    const repeated = view.getAllByText(label('Skill: uam'));
    expect(repeated).toHaveLength(2);
    expect(repeated[0].compareDocumentPosition(repeated[1]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const named = view.getByText(label('Skill: my_skill*[docs](https://example.test)<b>`'));
    expect(named.closest('.text-caption')).toBeTruthy();
    expect(repeated[1].compareDocumentPosition(named) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(named.compareDocumentPosition(view.getByText('Reviewed.')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(view.queryByRole('link')).toBeNull();
    expect(view.queryByRole('button', { name: 'Show full message' })).toBeNull();
  });

  test('user and assistant messages render as Markdown with code, links and attachments', async () => {
    await openTask('t3');
    const view = log();
    expect(view.getByText('Add a line to', { exact: false })).toBeTruthy();
    // Inline code and a fenced block from the assistant's answer.
    expect(view.getAllByText('uam doctor').some((el) => el.tagName === 'CODE')).toBe(true);
    expect(conversation().textContent).toContain('terminal  Windows Terminal · wide glyphs');
    // A user message's uploads: an image thumbnail, a text file and one no longer stored.
    expect(view.getByRole('button', { name: 'Open dumb-terminal.png' })).toBeTruthy();
    expect(view.getByText('doctor-output.txt')).toBeTruthy();
    expect(view.getByText('earlier-run.png')).toBeTruthy();
  });

  test('a message ends in its foot: the copy glyph and its clock time, the full date in the tooltip', async () => {
    await openTask('t3');
    const bubble = log().getByText('Add a line to', { exact: false }).closest('[data-history-anchor]') as HTMLElement;
    const time = within(bubble).getByRole('time') as HTMLTimeElement;
    const at = new Date(time.dateTime);
    expect(time.textContent).toBe(at.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }));
    expect(time.title).toBe(at.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }));
    // The foot holds the copy button beside it, under the bubble.
    const foot = time.parentElement!;
    expect(within(foot).getByRole('button', { name: 'Copy message' })).toBeTruthy();
    expect(bubble.querySelector('.bg-bubble')!.compareDocumentPosition(foot) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Hover only, out of the flow: the row sits on the gap under the block and fades in with the pointer over the block.
    expect(foot.className).toContain('absolute');
    // The stamp keeps its size through the class merge, in the faint tone.
    expect(foot.className).toContain('text-stamp');
    expect(foot.className).toContain('text-faint');
    expect(foot.className).toContain('opacity-0');
    expect(foot.className).toContain('group-hover/copy:opacity-100');
  });

  test("the turn's last reply carries its tokens out and generation speed after the time", async () => {
    await openTask('t3');
    // The assistant's anchor is the text block; its foot is the block's sibling inside the copyable wrapper.
    const reply = log().getByText('Done.', { exact: false }).closest('[data-history-anchor]')!.parentElement as HTMLElement;
    const foot = within(reply).getByRole('time').parentElement!;
    // 1,860 tokens out over 41.2 s of generation (mock t3).
    expect(foot.textContent).toContain('1.9K tokens');
    expect(foot.textContent).toContain('45 tok/s');
    expect(foot.querySelector('[title*="48,210 tokens in"]')).toBeTruthy();
    // The user's bubble carries the time alone.
    const bubble = log().getByText('Add a line to', { exact: false }).closest('[data-history-anchor]') as HTMLElement;
    expect(within(bubble).getByRole('time').parentElement!.textContent).not.toContain('tokens');
  });

  test('only the last reply of an ended turn has a foot: the end time and the turn\'s tokens; earlier replies and a working turn have none', () => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const items: Item[] = [
      { id: 'u1', kind: 'user', time: at(0), text: 'Hello' },
      { id: 'a1', kind: 'assistant', time: at(2), text: 'Looking.' },
      { id: 'a2', kind: 'assistant', time: at(5), text: 'Found it.' },
      { id: 'u2', kind: 'user', time: at(10), text: 'Fix it' },
      { id: 'a3', kind: 'assistant', time: at(12), text: 'On it.' },
      { id: 'a4', kind: 'assistant', time: at(15), text: 'Patching.' },
    ];
    const turnTimings = [
      { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(6), state: 'completed' as const, input_tokens: 1000, output_tokens: 940, generation_ms: 4000 },
      { id: 't2', user_item_id: 'u2', started_at: at(10), state: 'working' as const, input_tokens: 2000, output_tokens: 610, generation_ms: 3000 },
    ];
    const foot = (view: ReturnType<typeof render>, text: string) => within(view.getByText(text).closest('[data-history-anchor]')!.parentElement as HTMLElement).queryByRole('time')?.parentElement?.textContent ?? null;
    const view = render(<Transcript sessionId="s" items={items} turnTimings={turnTimings} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" liveCard />);
    // The ended turn: one foot, under its last reply, with the time the turn ended and its count.
    expect(foot(view, 'Looking.')).toBeNull();
    expect(foot(view, 'Found it.')).toContain(`${new Date(at(6)).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })} · 940 tokens · 235 tok/s`);
    // The working turn: no foot under either reply, however many tokens have been counted so far.
    expect(foot(view, 'On it.')).toBeNull();
    expect(foot(view, 'Patching.')).toBeNull();
    // Once it ends, the foot lands under its last reply with the whole turn's count.
    view.rerender(<Transcript sessionId="s" items={items} turnTimings={[turnTimings[0], { ...turnTimings[1], ended_at: at(16), state: 'completed' as const, output_tokens: 1210, generation_ms: 5000 }]} interactions={[]} subagents={[]} live working={false} provider="copilot" workdir="/w" liveCard />);
    expect(foot(view, 'On it.')).toBeNull();
    expect(foot(view, 'Patching.')).toContain('1.2K tokens · 242 tok/s');
  });

  test("an ended turn's foot keeps its identity while the next reply streams, so that reply's row is not drawn again", () => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const items: Item[] = [
      { id: 'u1', kind: 'user', time: at(0), text: 'Hello' },
      { id: 'a1', kind: 'assistant', time: at(2), text: 'Found it.' },
      { id: 'u2', kind: 'user', time: at(10), text: 'Fix it' },
      { id: 'a2', kind: 'assistant', time: at(12), text: 'Patching' },
    ];
    const timings = [
      { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(6), state: 'completed' as const, output_tokens: 940 },
      { id: 't2', user_item_id: 'u2', started_at: at(10), state: 'working' as const },
    ];
    const before = replyEnds(items, timings, true);
    const streamed = replyEnds([...items.slice(0, -1), { ...items[3], text: 'Patching the test' }], timings, true);
    expect(streamed?.get('a1')).toBe(before?.get('a1'));
    expect(streamed?.has('a2')).toBe(false);
    // A new timing for the turn is a new foot.
    expect(replyEnds(items, [{ ...timings[0], output_tokens: 1000 }, timings[1]], true)?.get('a1')).not.toBe(before?.get('a1'));
  });

  test("a turn that changed the todo list names it in its reply's foot, kept in view, and opens the list as the turn left it", async () => {
    const user = userEvent.setup();
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const items: Item[] = [
      { id: 'u1', kind: 'user', time: at(0), text: 'Plan the site' },
      { id: 'a1', kind: 'assistant', time: at(5), text: 'Planned.' },
    ];
    const timing: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(6), state: 'completed', output_tokens: 940, todo: { done: 1, total: 4, blocked: 1, in_progress: 1, pending: 1, open: 2 } };
    const snap: TurnTodos = {
      timing_id: 't1',
      ended_at: at(6),
      counts: timing.todo!,
      todos: [
        { id: 'z', title: 'Sign in to the registry', status: 'blocked', note: 'No token on this machine' },
        { id: 'y', title: 'Build the index page', status: 'in_progress', agent_id: 'sub-1', agent: 'Index page writer' },
        { id: 'p', title: 'Write the README', status: 'pending' },
        { id: 'w', title: 'Plan the pages', status: 'done' },
      ],
    };
    const turnTodos = vi.fn(async () => snap);
    const draw = (timings: TurnTiming[], sessionId = 'todo-task') => (
      <ApiContext.Provider value={{ ...api, turnTodos }}>
        <Transcript sessionId={sessionId} items={items} turnTimings={timings} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" liveCard />
      </ApiContext.Provider>
    );
    const view = render(draw([timing]));
    const button = screen.getByRole('button', { name: 'Todo at the end of this turn: 1 of 4 done, 1 in progress, 1 blocked, 1 to do' });
    expect(button.getAttribute('aria-haspopup')).toBe('dialog');
    expect(button.textContent).toBe('Todo 1/4·1 in progress·1 blocked·1 to do');
    // Each stage in its colour behind its glyph; a phone drops the words, never the glyphs.
    for (const [word, tone] of [['in progress', 'text-accent'], ['blocked', 'text-warning'], ['to do', 'text-muted']]) {
      const words = within(button).getByText(word);
      expect(words.className).toContain('max-sm:hidden');
      expect(words.parentElement!.className).toContain(tone);
      expect(words.parentElement!.querySelector('svg')).toBeTruthy();
    }
    // The words beside the tokens, and the foot stays in view without a hover.
    const foot = button.parentElement!;
    expect(foot.textContent).toContain('940 tokens · Todo 1/4');
    expect(foot.className).toContain('opacity-100');

    await user.click(button);
    const reader = await screen.findByRole('dialog', { name: 'Todo at the end of this turn' });
    expect(button.getAttribute('aria-controls')).toBe(reader.id);
    await waitFor(() => expect(document.activeElement).toBe(within(reader).getByRole('heading', { name: 'Todo' })));
    const clock = new Date(at(6)).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
    expect(await within(reader).findByText(`As this turn left it, ${clock} · 2 left open`)).toBeTruthy();
    expect(turnTodos).toHaveBeenCalledWith('todo-task', 't1', expect.any(AbortSignal));
    expect(within(reader).getByRole('region', { name: 'Blocked' }).textContent).toBe('Blocked1Sign in to the registryNo token on this machine, Blocked');
    // The live reader's stages, in its order.
    expect(within(reader).getAllByRole('region').map((r) => r.getAttribute('aria-label'))).toEqual(['In progress', 'Blocked', 'To do', 'Done']);
    expect(within(reader).getByRole('region', { name: 'In progress' }).textContent).toBe('In progress1Build the index page, by Index page writer, In progress');
    expect(within(reader).getByRole('region', { name: 'To do' }).textContent).toBe('To do1Write the README, To do');
    expect(within(reader).getByRole('region', { name: 'Done' }).textContent).toBe('Done1Plan the pages, Done');
    expect(within(reader).getByText('Kept by uam when the turn ended')).toBeTruthy();
    expect(within(reader).getByText('Esc')).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.activeElement).toBe(button);

    // Opened again, even from a new foot, it reads the copy kept in memory.
    view.rerender(draw([{ ...timing }]));
    await user.click(screen.getByRole('button', { name: /^Todo at the end of this turn/ }));
    expect(await screen.findByText('Kept by uam when the turn ended')).toBeTruthy();
    expect(turnTodos).toHaveBeenCalledTimes(1);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    // A list no longer kept says so; a turn that did not change the list has nothing in its foot.
    turnTodos.mockRejectedValueOnce(new ApiError(404, 'no todo list was kept for that turn'));
    view.rerender(draw([timing], 'other-task'));
    await user.click(screen.getByRole('button', { name: /^Todo at the end of this turn/ }));
    expect(await screen.findByText('This turn’s list is no longer kept')).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    // Counts kept before uam split in progress from to do: the open rows stay one count.
    view.rerender(draw([{ ...timing, todo: { done: 1, total: 4, blocked: 1, open: 2 } }]));
    expect(screen.getByRole('button', { name: 'Todo at the end of this turn: 1 of 4 done, 1 blocked, 2 left open' }).textContent).toBe('Todo 1/4·1 blocked·2 open');
    view.rerender(draw([{ ...timing, todo: undefined }]));
    expect(screen.queryByRole('button', { name: /^Todo at the end of this turn/ })).toBeNull();
    expect(screen.getByText('Planned.').closest('[data-history-anchor]')!.parentElement!.textContent).not.toContain('Todo');
  });

  test("a phone opens a turn's list as a sheet with a close button", async () => {
    const happy = (window as unknown as { happyDOM: { setViewport: (v: { width: number; height: number }) => void } }).happyDOM;
    happy.setViewport({ width: 390, height: 844 });
    try {
      const user = userEvent.setup();
      const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
      const timing: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(6), state: 'completed', todo: { done: 2, total: 2 } };
      const turnTodos = vi.fn(async (): Promise<TurnTodos> => ({ timing_id: 't1', ended_at: at(6), counts: { done: 2, total: 2 }, todos: [{ id: 'a', title: 'One', status: 'done' }, { id: 'b', title: 'Two', status: 'done' }] }));
      render(
        <ApiContext.Provider value={{ ...api, turnTodos }}>
          <Transcript sessionId="phone-task" items={[{ id: 'u1', kind: 'user', time: at(0), text: 'Go' }, { id: 'a1', kind: 'assistant', time: at(5), text: 'Went.' }]} turnTimings={[timing]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" liveCard />
        </ApiContext.Provider>,
      );
      await user.click(screen.getByRole('button', { name: 'Todo at the end of this turn: 2 of 2 done' }));
      const sheet = await screen.findByRole('dialog', { name: 'Todo at the end of this turn' });
      expect(await within(sheet).findByText(/^As this turn left it, .* · all done$/)).toBeTruthy();
      expect(within(sheet).queryByText('Esc')).toBeNull();
      await user.click(within(sheet).getByRole('button', { name: 'Close' }));
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    } finally {
      happy.setViewport({ width: 1024, height: 768 });
    }
  });

  test('a line break typed with Shift+Enter stays a line break in the sent message', async () => {
    const { user } = await openTask('t3');
    await user.type(composer(), 'first line{Shift>}{Enter}{/Shift}second line');
    await user.keyboard('{Enter}');
    const first = await log().findByText(/^first line/);
    expect(first.tagName).toBe('P');
    expect(first.querySelectorAll('br')).toHaveLength(1);
    expect(first.textContent).toBe('first line\nsecond line');
  });

  test('an agent reply keeps CommonMark: a single newline is not a line break', () => {
    const view = render(<Markdown text={'first line\nsecond line'} />);
    const p = view.container.querySelector('p');
    expect(p?.querySelector('br')).toBeNull();
    expect(p?.textContent).toBe('first line\nsecond line');
  });

  test('links open outside, and images in the project open in the viewer', async () => {
    const { user } = await openTask('t1');
    const link = log().getByRole('link', { name: 'original report' });
    expect(link.getAttribute('href')).toBe('https://example.com/issue/56.png');
    expect(link.getAttribute('target')).toBe('_blank');
    await user.click(log().getByRole('button', { name: 'Open attach-flow.png' }));
    const viewer = await screen.findByRole('dialog', { name: /attach-flow\.png/ });
    await user.click(within(viewer).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test('copying a message confirms it', async () => {
    const { user } = await openTask('t10');
    const copy = log().getAllByRole('button', { name: 'Copy message' })[0];
    await user.click(copy);
    expect(await log().findByRole('button', { name: 'Copied' })).toBeTruthy();
  });

  test('a message held shortened reads its whole text on request', async () => {
    const { user } = await openTask('t15');
    const show = await log().findAllByRole('button', { name: 'Show full message' });
    expect(log().getAllByText(/Shortened here to save memory\./)).toHaveLength(2);
    await user.click(show[0]);
    expect(await log().findByText('Loading the full message…')).toBeTruthy();
    await waitFor(() => expect(conversation().textContent?.includes('build: pkg150 compiled in')).toBe(true));
    expect(log().getAllByText(/Shortened here to save memory\./)).toHaveLength(1);
  });

  test('a failed turn shows the provider\'s notice once, without a second failure line', async () => {
    await openTask('t4');
    expect(log().getByText('Error: provider process exited (code 1).')).toBeTruthy();
    expect(screen.queryByText(/^Turn failed/)).toBeNull();
  });

  test('a task another task started names that task at the start of its conversation', async () => {
    await openTask('t4');
    expect(log().getByText('Started by another task, Doctor: add terminal line.')).toBeTruthy();
  });
});

describe('activity', () => {
  test('a compact turn folds its work into one line that opens the timeline', async () => {
    const { user } = await openTask('t3');
    const head = log().getAllByRole('button', { name: /activity of this turn/ })[0];
    expect(head.getAttribute('aria-expanded')).toBe('false');
    expect(head.textContent).toMatch(/1 thought/);
    await user.click(head);
    await waitFor(() => expect(head.getAttribute('aria-expanded')).toBe('true'));
    const edit = await log().findByRole('button', { name: /^edit.*cmd\/doctor\.go.*done$/ });
    await user.click(edit);
    await waitFor(() => expect(edit.getAttribute('aria-expanded')).toBe('true'));
    expect(conversation().textContent).toContain('printRow("terminal", term.Describe())');
    const thought = log().getByRole('button', { name: 'Thought' });
    await user.click(thought);
    await waitFor(() => expect(thought.getAttribute('aria-expanded')).toBe('true'));
    expect(conversation().textContent).toContain('the terminal probe already exposes');
    await user.click(log().getByRole('button', { name: 'Collapse' }));
    await waitFor(() => expect(head.getAttribute('aria-expanded')).toBe('false'));
  });

  test('a live compact turn shows its current step unfolded, then folds it into the turn line', async () => {
    const { user } = await openTask('t3');
    const heads = () => log().queryAllByRole('button', { name: /activity of this turn/ });
    const earlier = heads().length;
    await user.type(composer(), 'Run the tests please');
    await user.keyboard('{Enter}');
    // The thought streams at the foot; the new turn's line has nothing counted yet, so it draws no bare chevron.
    await waitFor(() => expect(conversation().textContent).toContain('a small, safe change'), { timeout: 5000 });
    expect(heads()).toHaveLength(earlier);
    const head = () => heads().at(-1)!;
    // The running call is its tool row with the tail of its output; the finished thought is counted.
    const running = await log().findByRole('button', { name: /^bash.*go test \.\/\.\.\. -count=1.*running$/ }, { timeout: 5000 });
    // The finished thought is the first count: the line appears, closed.
    expect(heads()).toHaveLength(earlier + 1);
    expect(head().getAttribute('aria-expanded')).toBe('false');
    expect(head().textContent).toMatch(/^1 thought/);
    await waitFor(() => expect(running.closest('.animate-rise')?.textContent).toContain('internal/agentapi'), { timeout: 3000 });
    // Once it completes it leaves the foot and the turn line counts it; nothing of it stays outside.
    await waitFor(() => expect(head().textContent).toMatch(/1 thought · 1 command/), { timeout: 6000 });
    expect(log().queryByRole('button', { name: /^bash.*go test \.\/\.\.\. -count=1/ })).toBeNull();
    expect(conversation().textContent).not.toContain('a small, safe change');
  }, 20000);

  test('a tool row menu expands it and copies its command, not the JSON around it', async () => {
    const { user } = await openTask('t14');
    // After `openTask`: its user-event setup puts its own clipboard in place.
    const copy = vi.spyOn(navigator.clipboard, 'writeText');
    const head = log().getAllByRole('button', { name: /activity of this turn/ })[0];
    await user.click(head);
    const menu = await openMenu(user, 'Actions for bash ls ~/projects/sky-dodge');
    await user.click(menu.getByRole('menuitem', { name: 'Copy command' }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('ls ~/projects/sky-dodge'));
    copy.mockRestore();
    const again = await openMenu(user, 'Actions for bash ls ~/projects/sky-dodge');
    await user.click(again.getByRole('menuitem', { name: 'Expand' }));
    // The input reads as the command; Raw shows it as recorded.
    const raw = await log().findByRole('button', { name: 'Raw' });
    expect(raw.closest('.group\\/code')?.textContent).toContain('commandRawls ~/projects/sky-dodge');
    await user.click(raw);
    await waitFor(() => expect(conversation().textContent).toContain('{"command":"ls ~/projects/sky-dodge"}'));
  });

  test.each(['detailed', 'compact'] as const)('a call a stopped turn left running is not the running step while the next turn starts (%s)', (density) => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const bash = (id: string, s: number): Item => ({ id, kind: 'tool', time: at(s), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'seq 1 60' }) } });
    const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Count to 60' }, bash('c1', 2)];
    const stopped: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(5), state: 'cancelled' };
    const draw = (turnTimings: TurnTiming[], working: boolean, list = items) => <Transcript sessionId="s" items={list} turnTimings={turnTimings} interactions={[]} subagents={[]} live={working} working={working} provider="copilot" workdir="/w" density={density} footVerb={false} liveCard />;
    const ring = () => view.container.querySelector('.animate-spin');
    const view = render(draw([stopped], false));
    expect(ring()).toBeNull();
    // The Task works before the next turn's timing comes, then before its message lands.
    view.rerender(draw([stopped], true));
    expect(ring()).toBeNull();
    expect(view.container.textContent).not.toContain('Running');
    view.rerender(draw([stopped, { id: 't2', started_at: at(10), state: 'working' }], true));
    expect(ring()).toBeNull();
    // The next turn's own call is its running step.
    view.rerender(draw([stopped, { id: 't2', user_item_id: 'u2', started_at: at(10), state: 'working' }], true, [...items, { id: 'u2', kind: 'user', time: at(10), text: 'Again' }, bash('c2', 12)]));
    expect(ring()).toBeTruthy();
  });

  test.each(['detailed', 'compact'] as const)('a stopped turn keeps its end and its reply foot while the next turn starts (%s)', (density) => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const bash = (id: string, s: number): Item => ({ id, kind: 'tool', time: at(s), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'seq 1 60' }) } });
    const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Count to 60' }, { id: 'a1', kind: 'assistant', time: at(2), text: 'Counting.' }, bash('c1', 3)];
    const stopped: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(5), state: 'cancelled', output_tokens: 940, generation_ms: 4000 };
    const draw = (turnTimings: TurnTiming[], working: boolean, list = items) => <Transcript sessionId="s" items={list} turnTimings={turnTimings} interactions={[]} subagents={[]} live={working} working={working} provider="copilot" workdir="/w" density={density} footVerb={false} liveCard />;
    const view = render(draw([stopped], false));
    const ends = () => [view.container.textContent?.includes('Took 5s'), within(view.getByText('Counting.').closest('[data-history-anchor]')!.parentElement as HTMLElement).queryByRole('time')?.parentElement?.textContent?.includes('940 tokens')];
    expect(ends()).toEqual([true, true]);
    // The Task works before the next turn's timing comes, then before its message lands: the stopped turn stays as it ended.
    view.rerender(draw([stopped], true));
    expect(ends()).toEqual([true, true]);
    view.rerender(draw([stopped, { id: 't2', started_at: at(10), state: 'working' }], true));
    expect(ends()).toEqual([true, true]);
    // A row begun after the end is a turn's that came without a message: it runs.
    view.rerender(draw([stopped, { id: 't2', started_at: at(10), state: 'working' }], true, [...items, bash('c2', 12)]));
    expect(view.container.textContent).not.toContain('Took 5s');
  });

  test.each(['detailed', 'compact'] as const)('a call a turn the service cut off left running is not the running step while the next turn starts; a dropped stream keeps it (%s)', (density) => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const bash = (id: string, s: number): Item => ({ id, kind: 'tool', time: at(s), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'seq 1 60' }) } });
    const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Count to 60' }, bash('c1', 2)];
    // A restart or close cut the turn off: the service marks it unknown, without an end.
    const cut: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), state: 'unknown' };
    const draw = (turnTimings: TurnTiming[], working: boolean, list = items, connected = true) => <Transcript sessionId="s" items={list} turnTimings={turnTimings} interactions={[]} subagents={[]} live={working} working={working} connected={connected} provider="copilot" workdir="/w" density={density} footVerb={false} liveCard />;
    const ring = () => view.container.querySelector('.animate-spin');
    const view = render(draw([cut], false));
    expect(ring()).toBeNull();
    // You reopen the Task with a message: it works before the next turn's timing comes, then before its message lands.
    view.rerender(draw([cut], true));
    expect(ring()).toBeNull();
    expect(view.container.textContent).not.toContain('Running');
    view.rerender(draw([cut, { id: 't2', started_at: at(10), state: 'working' }], true));
    expect(ring()).toBeNull();
    // A row begun since the next turn started is a turn's that came without a message: it runs.
    view.rerender(draw([cut, { id: 't2', started_at: at(10), state: 'working' }], true, [...items, bash('c2', 12)]));
    expect(ring()).toBeTruthy();
    // The page lost its stream and marked the running turn unknown itself: its call may still run.
    view.rerender(draw([cut], true, items, false));
    expect(ring()).toBeTruthy();
  });

  test('a turn that ended with nothing in it says there was no reply', () => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const items: Item[] = [
      { id: 'u1', kind: 'user', time: at(0), text: 'Hello' },
      { id: 'a1', kind: 'assistant', time: at(3), text: 'Hi.' },
      { id: 'u2', kind: 'user', time: at(10), text: '/skil' },
    ];
    const turnTimings = [
      { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(4), state: 'completed' as const },
      { id: 't2', user_item_id: 'u2', started_at: at(10), ended_at: at(13), state: 'completed' as const },
    ];
    const view = render(<Transcript sessionId="s" items={items} turnTimings={turnTimings} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" liveCard />);
    expect(view.getByText('Took 4s')).toBeTruthy();
    expect(view.getByText('Took 3s · No reply')).toBeTruthy();
  });

  test('a tool row that runs no command offers its input to copy, decoded', async () => {
    const copy = vi.spyOn(navigator.clipboard, 'writeText');
    const item: Item = { id: 'p1', kind: 'tool', time: new Date().toISOString(), tool: { name: 'apply_patch', status: 'completed', input: JSON.stringify('*** Begin Patch\n*** Add File: web/a.html\n+<!doctype html>\n*** End Patch\n') } };
    const { getByRole } = render(<ToolRow item={item} live={false} />);
    expect(getByRole('button', { name: /^apply_patch.*web\/a\.html.*done$/ })).toBeTruthy();
    fireEvent.click(getByRole('button', { name: 'Actions for apply_patch web/a.html' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Copy input' }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('*** Begin Patch\n*** Add File: web/a.html\n+<!doctype html>\n*** End Patch\n'));
    copy.mockRestore();
  });

  test('detailed density keeps one row per run of tool calls', async () => {
    saveDensity('detailed');
    const { user } = await openTask('t3');
    expect(log().queryByRole('button', { name: /activity of this turn/ })).toBeNull();
    const run = await log().findByRole('button', { name: /^Thought, ran 1 command and used 1 tool/ });
    await user.click(run);
    await waitFor(() => expect(run.getAttribute('aria-expanded')).toBe('true'));
    // Inside, consecutive calls share one disclosure.
    await user.click(await log().findByRole('button', { name: 'Ran 1 command and used 1 tool' }));
    expect(await log().findByRole('button', { name: /^bash.*go test \.\/cmd\/\.\.\..*done$/ })).toBeTruthy();
  });

  test('subagents in use sit at the foot in one-line rows; a row opens its transcript beside it, one at a time', async () => {
    const { user } = await openTask('t8');
    // The failed one first, then the running ones (each followed by what it spawned), then the done one.
    const live = within(await log().findByRole('region', { name: 'Subagents at work' }));
    const rows = live.getAllByRole('button', { name: /, (failed|running|completed)/ });
    expect(rows.map((row) => row.getAttribute('aria-label')?.split(',')[0])).toEqual(['Run the accessibility linter', 'Survey templates for missing alt text and labels', 'Check contrast of the theme tokens', 'Verify store callers', 'Check the heading order']);
    // A failed row says why under it; the head counts them with their tokens.
    expect(live.getByText('axe-core is not installed in this project.')).toBeTruthy();
    expect(live.getByText(/^5 subagents · [\d.]+M tokens$/)).toBeTruthy();
    // Nothing of them stands at their calls, and the turn line does not count them.
    expect(log().queryByRole('button', { name: 'Open' })).toBeNull();
    expect(log().queryByRole('button', { name: /activity of this turn/ })).toBeNull();
    await user.click(rows[1]);
    let panel = within(await screen.findByRole('dialog', { name: 'Subagent transcript' }));
    expect(await panel.findByRole('region', { name: 'Transcript of Survey templates for missing alt text and labels' })).toBeTruthy();
    // Nothing to type: subagents take no follow-up.
    expect(panel.queryByRole('textbox')).toBeNull();
    await user.click(panel.getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Subagent transcript' })).toBeNull());
    await user.click(rows[2]);
    panel = within(await screen.findByRole('dialog', { name: 'Subagent transcript' }));
    expect(await panel.findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Transcript of Survey templates for missing alt text and labels' })).toBeNull();
  });
});

describe('requests', () => {
  test('a permission card is decided in place and the turn goes on', async () => {
    const { user } = await openTask('t14');
    const card = within(screen.getByRole('group', { name: 'Run a shell command outside the project' }));
    expect(card.getByText('chmod 0644 ~/sky-dodge.png')).toBeTruthy();
    await user.click(card.getByRole('button', { name: 'Allow once' }));
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Run a shell command outside the project' })).toBeNull());
    expect(await screen.findByRole('button', { name: 'Stop turn' })).toBeTruthy();
  });

  test('a question takes chosen options and typed text', async () => {
    const { user } = await openTask('t2');
    const card = within(screen.getByRole('group', { name: 'Which terminals should the explanation cover?' }));
    const answer = card.getByRole('button', { name: 'Answer' });
    expect(answer).toHaveProperty('disabled', true);
    await user.click(card.getByRole('checkbox', { name: 'GNOME Terminal' }));
    await user.click(card.getByRole('checkbox', { name: 'tmux inside either' }));
    await user.click(card.getByRole('checkbox', { name: 'GNOME Terminal' }));
    const texts = card.getAllByRole('textbox', { name: 'Your answer' });
    await user.type(texts[1], 'Mention the glyph set');
    expect(answer).toHaveProperty('disabled', false);
    await user.click(answer);
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Which terminals should the explanation cover?' })).toBeNull());
  });

  test('a pending permission card lists its options', async () => {
    await openTask('t6');
    const card = within(screen.getByRole('group', { name: 'Run a shell command outside the project' }));
    expect(card.getByText('Needs permission')).toBeTruthy();
    expect(card.getByRole('button', { name: 'Deny' })).toBeTruthy();
  });
});

describe('history', () => {
  test.each([false, true])('scrolling up a long compact task reads earlier pages (over chart: %s)', async overChart => {
    await openTask('t15');
    expect(await screen.findByText('Scroll up for earlier messages')).toBeTruthy();
    expect(log().queryByText('Turn 138: check pkg138 for unused exports.')).toBeNull();
    let target = conversation();
    if (overChart) {
      // A real chart's native wheel guard must not hide input from the history loader.
      const container = conversation().appendChild(document.createElement('div'));
      const chart = { title: 'History chart', kind: 'line' as const, labels: ['a', 'b'], series: [{ name: 'values', values: [2, 8] }] };
      render(<EChart option={chartOption(chart, { width: 480, height: 240 })} width={480} height={240} label="History chart" />, { container });
      target = screen.getByRole('img', { name: 'History chart' });
      await waitFor(() => expect(target.querySelector('svg')).toBeTruthy());
      const historyRead = vi.spyOn(api, 'history');
      try {
        const zoom = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -120 });
        // Happy DOM omits wheel modifier fields, so set the actual browser field explicitly.
        Object.defineProperty(zoom, 'ctrlKey', { value: true });
        fireEvent(target, zoom);
        expect(historyRead).not.toHaveBeenCalled();
      } finally { historyRead.mockRestore(); }
    }
    fireEvent.wheel(target, { deltaY: -120 });
    expect(await log().findByText('Turn 138: check pkg138 for unused exports.')).toBeTruthy();
    fireEvent.keyDown(conversation(), { key: 'PageUp' });
    expect(await log().findByText('Turn 126: check pkg126 for unused exports.')).toBeTruthy();
  });

  test('a subagent picked from the header index is found in its reply, its list opened, its transcript open', async () => {
    const { user } = await openTask('t15');
    await user.click(screen.getByRole('button', { name: 'Subagents, 1 or more' }));
    const index = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(index.getByRole('button', { name: /^Audit the remaining packages/ }));
    await waitFor(() => expect(document.getElementById('item-h-audit')?.classList.contains('animate-flash')).toBe(true));
    // The open transcript is modal, so what is behind it is hidden from the accessibility tree.
    expect(screen.getByRole('button', { name: /^1 subagent · [\d.]+M tokens · 1 done/, hidden: true }).getAttribute('aria-expanded')).toBe('true');
    // Picked from the index, its transcript opens beside the row it landed on.
    expect(await screen.findByRole('region', { name: 'Transcript of Audit the remaining packages' })).toBeTruthy();
  });
});
