import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, onTestFinished, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { ApiError, api, type Item, type SessionDetail, type SnapshotData, type TurnTiming, type TurnTodos } from '../../src/api';
import { Markdown } from '../../src/components/common';
import { EChart } from '../../src/components/EChart';
import { chartOption } from '../../src/lib/chart';
import { ToolRow, Transcript, replyEnds } from '../../src/components/Transcript';
import { TaskActionsContext, type TaskActions } from '../../src/components/taskActions';
import { VisualBoundary } from '../../src/components/VisualBoundary';
import { composer, log, openMenu, openSubagentIndex, openTask, renderApp } from './render';

const conversation = () => screen.getByRole('region', { name: 'Conversation' });
/** The head of the turn `el` is in: its card's first row. */
const headOf = (el: Element) => el.closest('[data-reply]')!.querySelector('[data-history-anchor^="turn-head-"]') as HTMLElement;
/** The card of the turn `el` is in: its head row, then its outcome row. */
const cardOf = (el: Element) => headOf(el).parentElement as HTMLElement;
/** The ended turn's outcome in its card (copy, the turn's menu, its chips), if the card has one. */
const outcomeOf = (el: Element) => (within(cardOf(el)).queryByRole('button', { name: 'Copy message' }) ? cardOf(el) : null);
/** A foot of the reply block `el` is in, if it has one: replies have none, the turn card copies them. */
const footOf = (el: Element) => el.closest('.group\\/copy')?.querySelector(':scope > [data-foot]') ?? null;

describe('messages', () => {
  const notes = 'uam thread: Thread "Fix \\"login\\"" (child-1), which you started, ended its turn: completed. Files changed: 2. Its last reply:\nRaw child reply\n\nwith a second paragraph\n\nuam thread: Thread "Docs" (child-2), which you started, was settled.\nuam trail: Thread "Docs" (child-2) edited a.go 3 min ago and is still running. Re-read it first.\nuam budget: this Thread is close to the spending limit its owner set.';
  const drawing = 'The user edited docs/b.drawio since your version (v1 → v2):\n~ node "API" renamed "Gateway"\nNow 3 nodes, 2 edges. Build on the user\'s version; uam_diagram_read for the full diagram.';
  // The journal hands notices back with line breaks as spaces.
  test.each([['as sent', (s: string) => s], ['from the journal', (s: string) => s.replaceAll('\n', ' ')]])('uam notes link the thread and leave replies and drawing changes to the agent (%s)', (_, form) => {
    const select = vi.fn();
    const items: Item[] = [
      { id: 'u', kind: 'user', time: '2026-10-09T00:00:00Z', text: 'Split the work' },
      { id: 'n', kind: 'notice', time: '2026-10-09T00:00:01Z', text: form(notes) },
      { id: 'd', kind: 'notice', time: '2026-10-09T00:00:02Z', text: form(drawing) },
    ];
    const view = render(
      <TaskActionsContext.Provider value={{ select } as unknown as TaskActions}>
        <Transcript sessionId="s" items={items} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />
      </TaskActionsContext.Provider>,
    );
    expect(view.queryByText(/Raw child reply|second paragraph|Re-read it first|renamed|uam_diagram_read/)).toBeNull();
    expect(view.getByText('ended its turn: completed. Files changed: 2.', { exact: false })).toBeTruthy();
    expect(view.getByText('was settled.', { exact: false })).toBeTruthy();
    expect(view.getByText('3 min ago and is still running.', { exact: false }).textContent).toBe('Thread Docs edited a.go 3 min ago and is still running.');
    expect(view.getByText(/^uam budget: this Thread is close/)).toBeTruthy();
    expect(view.getByText('You edited', { exact: false }).textContent).toBe('You edited docs/b.drawio (v1 → v2).');
    fireEvent.click(view.getByRole('button', { name: 'Fix "login"' }));
    for (const docs of view.getAllByRole('button', { name: 'Docs' })) fireEvent.click(docs);
    expect(select.mock.calls).toEqual([['child-1'], ['child-2'], ['child-2']]);
  });

  test.each([['as sent', (s: string) => s], ['from the journal', (s: string) => s.replaceAll('\n', ' ')]])('a uam memory note is one line that opens to what uam pinned (%s)', (_, form) => {
    const memory = 'uam memory: your conversation was compacted, and uam pinned what its summary may have dropped. Earlier turns stay searchable word for word.\n\nNotes quoted from the conversation:\n- constraint: "Never touch the vendored folder"';
    const items: Item[] = [
      { id: 'u', kind: 'user', time: '2026-10-09T00:00:00Z', text: 'Go on' },
      { id: 'n', kind: 'notice', time: '2026-10-09T00:00:01Z', text: form(`${memory}\nuam budget: this Thread is close to the spending limit its owner set.`) },
    ];
    const view = render(<Transcript sessionId="s" items={items} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    const summary = view.getByText('Your conversation was compacted, and uam pinned what its summary may have dropped.');
    expect(summary.tagName).toBe('SUMMARY');
    expect(summary.closest('details')!.textContent).toContain('constraint: "Never touch the vendored folder"');
    expect(view.getByText(/^uam budget: this Thread is close/)).toBeTruthy();
  });

  test('skill invocation notices stay visible and ordered in turns', () => {
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

  test('native completion decisions remain visible in history', () => {
    const items: Item[] = [
      { id: 'u', kind: 'user', text: 'Fix the parser', time: '2026-10-09T10:00:00Z' },
      { id: 'a', kind: 'assistant', text: 'Fixed the parser.', time: '2026-10-09T10:00:01Z' },
      ...(['accepted', 'rejected', 'blocked', 'unknown'] as const).map((decision, index): Item => ({
        id: `receipt-${index}`, kind: 'notice', time: `2026-10-09T10:00:0${index + 2}Z`,
        text: `${decision === 'unknown' ? 'Completion decision unknown' : `Completion ${decision}`} · Summary: \` Native summary ${index} \` · Reason: \` Exact native reason ${index} \` · Blocker kind: \` permission \` · Blocker reason: \` user_denied \` · Resumable: ${index % 2 ? 'yes' : 'no'}`,
        completion: { decision, user_item_id: 'u', summary: `Native summary ${index}`, reason: `Exact native reason ${index}`, blocker: { kind: 'permission', reason: 'user_denied', resumable: index % 2 === 1 } },
        compact: { has_text: false, has_reasoning: false },
      })),
    ];
    const view = render(<Transcript sessionId="completion-task" items={items} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    expect(view.getByText('Fixed the parser.')).toBeTruthy();
    const rows = items.slice(2).map((item, index) => {
      const row = view.getByText(`Native summary ${index}`).closest('p')!;
      expect(within(row).getByText(`Native summary ${index}`).tagName).toBe('CODE');
      expect(within(row).getByText(`Exact native reason ${index}`).tagName).toBe('CODE');
      expect(within(row).getByText('permission').tagName).toBe('CODE');
      expect(within(row).getByText('user_denied').tagName).toBe('CODE');
      expect(row.textContent).toContain(`Resumable: ${item.completion!.blocker!.resumable ? 'yes' : 'no'}`);
      return row;
    });
    expect(rows).toHaveLength(4);
    for (let index = 1; index < rows.length; index++) {
      expect(rows[index - 1].compareDocumentPosition(rows[index]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
    expect(view.queryByText('Loading the full message…')).toBeNull();
  });

  test("a turn's last reply shows receipts only for claims its record proves wrong", () => {
    const timing: TurnTiming = { id: 't', user_item_id: 'u', started_at: '2026-10-09T00:00:00Z', ended_at: '2026-10-09T00:00:09Z', state: 'completed' };
    const base: Item[] = [
      { id: 'u', kind: 'user', time: '2026-10-09T00:00:00Z', text: 'Fix it and test it' },
      { id: 'e', kind: 'tool', time: '2026-10-09T00:00:01Z', tool: { name: 'edit', status: 'completed', input: JSON.stringify({ path: 'internal/web/charts.go' }) } },
    ];
    const green: Item = { id: 'b', kind: 'tool', time: '2026-10-09T00:00:02Z', tool: { name: 'bash', status: 'completed', input: JSON.stringify({ command: 'go test ./internal/web' }), exit_code: 0, output: 'ok' } };
    const reply = (text: string): Item => ({ id: 'a', kind: 'assistant', time: '2026-10-09T00:00:08Z', text });
    const view = render(<Transcript sessionId="s" items={[...base, green, reply('Changed `charts.go` and ran `go test ./internal/web`; all tests pass.')]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" turnTimings={[timing]} />);
    // Every claim holds: nothing.
    expect(view.queryByText(/match the record/)).toBeNull();
    expect(view.queryByRole('list', { name: 'Receipts' })).toBeNull();
    // No test ran this turn: an earlier turn's result restated is not a contradiction, so still nothing.
    view.rerender(<Transcript sessionId="s" items={[...base, reply('Changed `charts.go`; all tests pass.')]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" turnTimings={[timing]} />);
    expect(view.queryByRole('list', { name: 'Receipts' })).toBeNull();
    // The test run failed: that claim alone is stamped.
    const red: Item = { ...green, tool: { ...green.tool!, status: 'failed', exit_code: 1, output: 'FAIL' } };
    view.rerender(<Transcript sessionId="s" items={[...base, red, reply('Changed `charts.go`; all tests pass.')]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" turnTimings={[timing]} />);
    const stamps = within(view.getByRole('list', { name: 'Receipts' })).getAllByRole('listitem').map((li) => li.textContent);
    expect(stamps).toEqual(['all tests passlast test run exit 1']);
    // A reply that makes no claim gets nothing.
    view.rerender(<Transcript sessionId="s" items={[...base, reply('Done.')]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" turnTimings={[timing]} />);
    expect(view.queryByRole('list', { name: 'Receipts' })).toBeNull();
    expect(view.queryByText(/claims? match/)).toBeNull();
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
    // The stamp keeps its size through the class merge, in the muted tone (faint is under AA for words).
    expect(foot.className).toContain('text-stamp');
    expect(foot.className).toContain('text-muted');
    expect(foot.className).toContain('opacity-0');
    expect(foot.className).toContain('group-hover/copy:opacity-100');
  });

  test("the turn's head carries when it ended, its time split, its tokens out and generation speed", async () => {
    await openTask('t3');
    const head = headOf(log().getByText('Done.', { exact: false }));
    expect(within(head).getByRole('time')).toBeTruthy();
    // 1,860 tokens out over 41.2 s of generation (mock t3), the model's own time first in the split.
    expect(head.textContent).toContain('1.9K tokens · 45 tok/s');
    expect(head.textContent).toContain('(model 41s');
    expect(head.querySelector('[title*="48,210 tokens in"]')).toBeTruthy();
    // The user's bubble carries the time alone.
    const bubble = log().getByText('Add a line to', { exact: false }).closest('[data-history-anchor]') as HTMLElement;
    expect(within(bubble).getByRole('time').parentElement!.textContent).not.toContain('tokens');
  });

  test("an ended turn's card holds its outcome and copy, no reply has a foot; the head says when the turn ended and its tokens, or that it works with its tokens so far", () => {
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
    const view = render(<Transcript sessionId="s" items={items} turnTimings={turnTimings} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" liveCard />);
    const head = (text: string) => headOf(view.getByText(text)).textContent;
    // The ended turn: its head says when it ended and its count, and copies its last reply; no reply has a foot.
    expect(footOf(view.getByText('Looking.'))).toBeNull();
    expect(footOf(view.getByText('Found it.'))).toBeNull();
    expect(outcomeOf(view.getByText('Found it.'))).toBeTruthy();
    expect(head('Found it.')).toContain(new Date(at(6)).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }));
    expect(head('Found it.')).toContain('Took 6s (model 4s)');
    expect(head('Found it.')).toContain('940 tokens · 235 tok/s');
    // The working turn: no outcome yet; its head works on with the tokens counted so far, live.
    expect(outcomeOf(view.getByText('Patching.'))).toBeNull();
    expect(head('Patching.')).toMatch(/^Working/);
    expect(head('Patching.')).toContain('610 tokens · 203 tok/s');
    expect(within(headOf(view.getByText('Patching.'))).queryByRole('button', { name: /tokens/ })).toBeNull();
    // Once it ends, its card takes the outcome and the head has the whole turn's count.
    view.rerender(<Transcript sessionId="s" items={items} turnTimings={[turnTimings[0], { ...turnTimings[1], ended_at: at(16), state: 'completed' as const, output_tokens: 1210, generation_ms: 5000 }]} interactions={[]} subagents={[]} live working={false} provider="copilot" workdir="/w" liveCard />);
    expect(footOf(view.getByText('Patching.'))).toBeNull();
    expect(outcomeOf(view.getByText('Patching.'))).toBeTruthy();
    expect(head('Patching.')).toContain('1.2K tokens · 242 tok/s');
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

  test("a turn that changed the todo list shows it as blocks in its card and opens the list as the turn left it", async () => {
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
    // "Todo", then a block per stage: its glyph and count in its tone, its words for a screen reader.
    expect(button.parentElement!.firstElementChild!.textContent).toBe('Todo');
    const block = (title: string) => within(button).getByTitle(title);
    for (const [title, tone] of [['1 done', 'bg-success-wash'], ['1 blocked', 'bg-warning-wash'], ['1 to do', 'bg-tint-well']]) {
      expect(block(title).className).toContain(tone);
      expect(block(title).querySelector('svg')).toBeTruthy();
    }
    // The turn has ended, so nothing is in progress any more: that block keeps its glyph without the accent.
    expect(block('1 in progress').className).not.toContain('accent');
    expect(block('1 in progress').querySelector('svg')).toBeTruthy();
    // In the turn card's outcome row, in the flow and always in view.
    const row = button.closest('[data-outcome]')!;
    expect(row.className).not.toMatch(/absolute|opacity-0/);

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
    expect(within(screen.getByRole('button', { name: 'Todo at the end of this turn: 1 of 4 done, 1 blocked, 2 left open' })).getByTitle('2 open').textContent).toBe('2 open2 open');
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

  test('HTML comments are never shown; other raw HTML stays as text', () => {
    const view = render(<Markdown text={'<!-- a note to the writer -->\n\nText <!-- inline --> after.\n\n<b>raw</b>'} />);
    expect(view.container.textContent).not.toContain('<!--');
    expect(view.container.textContent).not.toContain('a note to the writer');
    expect(view.container.textContent).toContain('Text  after.');
    expect(view.container.textContent).toContain('<b>raw</b>');
  });

  test('an https image loads in place with no referrer and links to itself; an http one stays a link card', () => {
    const view = render(<Markdown text={'![chart](https://example.com/a.png)\n\n![old](http://example.com/b.png)'} />);
    const img = view.getByRole('img', { name: 'chart' });
    expect(img.getAttribute('src')).toBe('https://example.com/a.png');
    expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(img.closest('a')?.getAttribute('href')).toBe('https://example.com/a.png');
    expect(view.queryByRole('img', { name: 'old' })).toBeNull();
    expect(view.getByRole('link', { name: /old/ }).getAttribute('href')).toBe('http://example.com/b.png');
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

  test('a tool image without a name takes the image file its call named', () => {
    const item: Item = { id: 'v1', kind: 'tool', time: '2026-10-05T10:00:00Z', tool: { name: 'view', status: 'completed', input: '{"path":"/tmp/repo/e2e-red.png"}' }, images: [{ id: 'img1', mime: 'image/png', size: 178 }] };
    const row = render(<ToolRow item={item} live={false} sessionId="s" />);
    expect(row.getByRole('img', { name: 'e2e-red.png' })).toBeTruthy();
    expect(row.getByRole('button', { name: 'Open e2e-red.png' })).toBeTruthy();
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
    expect(log().getByText('Started by another thread, Doctor: add terminal line.')).toBeTruthy();
  });
});

describe('activity', () => {
  test('tool progress shows its latest plain status before its body loads', () => {
    const call: Item = { id: 'call', kind: 'tool', time: '2026-10-09T00:00:01Z', compact: { has_reasoning: false, has_text: false }, tool: { name: 'mcp_fetch', status: 'running', has_input: true, has_output: false, progress: '**Reading** https://example.test' } };
    const prompt: Item = { id: 'u', kind: 'user', time: '2026-10-09T00:00:00Z', text: 'Fetch the file' };
    const draw = (item: Item, live = true) => <Transcript sessionId="s" items={[prompt, item]} interactions={[]} subagents={[]} live={live} working={live} provider="copilot" workdir="/w" liveCard />;
    const view = render(draw(call));
    const progress = () => view.getByRole('status', { name: 'Tool progress' });
    expect(progress().textContent).toContain('**Reading** https://example.test');
    expect(view.queryByRole('link')).toBeNull();
    expect(view.queryByText('No details yet.')).toBeNull();
    view.rerender(draw({ ...call, tool: { ...call.tool!, progress: 'Waiting for file' } }));
    expect(progress().textContent).toContain('Waiting for file');
    expect(view.queryByText('**Reading** https://example.test')).toBeNull();
    view.rerender(draw({ ...call, tool: { ...call.tool!, status: 'completed', progress: 'Stale' } }));
    expect(view.queryByRole('status', { name: 'Tool progress' })).toBeNull();
    view.rerender(draw(call, false));
    expect(view.queryByRole('status', { name: 'Tool progress' })).toBeNull();
  });

  test('a saved Detailed preference still uses the turn timeline and its tool details', async () => {
    localStorage.setItem('uam.activity', 'detailed');
    const { user } = await openTask('t3');
    expect(conversation().querySelector('[data-activity]')).toBeNull();
    const head = log().getAllByRole('button', { name: /activity of this turn/ })[0];
    expect(head.getAttribute('aria-expanded')).toBe('false');
    expect(head.textContent).toMatch(/1 thought/);
    await user.click(head);
    await waitFor(() => expect(head.getAttribute('aria-expanded')).toBe('true'));
    const edit = await log().findByRole('button', { name: /^Edited doctor\.go/ });
    await user.click(edit);
    await waitFor(() => expect(edit.getAttribute('aria-expanded')).toBe('true'));
    expect(conversation().textContent).toContain('printRow("terminal", term.Describe())');
    const thought = log().getByRole('button', { name: /^Thought/ });
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
    const head = () => heads().at(-1)!;
    // The running call is its tool row alone at the foot (its output is the strip's peek); the finished thought is counted.
    const running = await log().findByRole('button', { name: /^bash.*go test \.\/\.\.\. -count=1.*running$/ }, { timeout: 8000 });
    // The streaming thought never stood at the foot; finished, it is the first count: the line appears, closed.
    expect(conversation().textContent).not.toContain('a small, safe change');
    expect(heads()).toHaveLength(earlier + 1);
    expect(head().getAttribute('aria-expanded')).toBe('false');
    expect(head().textContent).toMatch(/^1 thought/);
    await new Promise((r) => setTimeout(r, 1500));
    expect(running.closest('.animate-rise')?.textContent).not.toContain('internal/agentapi');
    expect(log().queryByRole('group', { name: 'Live output, last lines' })).toBeNull();
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
    // Over 20 steps the earlier ones wait behind one press.
    await user.click(await log().findByRole('button', { name: /^\d+ earlier steps · Show all \d+ calls$/ }));
    const step = await log().findByRole('button', { name: /^Ran ls ~\/projects\/sky-dodge/ });
    const menu = await openMenu(user, 'Actions for bash ls ~/projects/sky-dodge');
    await user.click(menu.getByRole('menuitem', { name: 'Copy command' }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('ls ~/projects/sky-dodge'));
    copy.mockRestore();
    await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
    fireEvent.contextMenu(step);
    await user.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Expand' }));
    // The raw view reads the input as the command, never the JSON around it.
    await waitFor(() => expect(step.getAttribute('aria-expanded')).toBe('true'));
    expect(conversation().textContent).toContain('$ ls ~/projects/sky-dodge');
    expect(conversation().textContent).not.toContain('{"command":"ls ~/projects/sky-dodge"}');
  });

  test('a call a stopped turn left running is not the running step while the next turn starts', () => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const bash = (id: string, s: number): Item => ({ id, kind: 'tool', time: at(s), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'seq 1 60' }) } });
    const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Count to 60' }, bash('c1', 2)];
    const stopped: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(5), state: 'cancelled' };
    const draw = (turnTimings: TurnTiming[], working: boolean, list = items) => <Transcript sessionId="s" items={list} turnTimings={turnTimings} interactions={[]} subagents={[]} live={working} working={working} provider="copilot" workdir="/w" liveCard />;
    const ring = () => view.container.querySelector('[data-step-mark]');
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

  test('a call waiting to start is still in its row and turns only as the step in progress', () => {
    const item: Item = { id: 'c1', kind: 'tool', time: '2026-10-05T10:00:02Z', tool: { name: 'bash', status: 'pending', input: JSON.stringify({ command: 'ls' }) } };
    const row = render(<ToolRow item={item} live sessionId="s" />);
    expect(row.container.querySelector('[data-step-mark]')).toBeNull();
    expect(row.container.querySelector('.lucide-circle-dashed')).not.toBeNull();
    row.unmount();
    const items: Item[] = [{ id: 'u1', kind: 'user', time: '2026-10-05T10:00:00Z', text: 'List' }, item];
    const view = render(<Transcript sessionId="s" items={items} turnTimings={[{ id: 't1', user_item_id: 'u1', started_at: '2026-10-05T10:00:00Z', state: 'working' }]} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" liveCard />);
    expect(view.container.querySelectorAll('[data-step-mark]')).toHaveLength(1);
  });

  test('a stopped turn keeps its end and its outcome while the next turn starts', () => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const bash = (id: string, s: number): Item => ({ id, kind: 'tool', time: at(s), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'seq 1 60' }) } });
    const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Count to 60' }, { id: 'a1', kind: 'assistant', time: at(2), text: 'Counting.' }, bash('c1', 3)];
    const stopped: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(5), state: 'cancelled', output_tokens: 940, generation_ms: 4000 };
    const draw = (turnTimings: TurnTiming[], working: boolean, list = items) => <Transcript sessionId="s" items={list} turnTimings={turnTimings} interactions={[]} subagents={[]} live={working} working={working} provider="copilot" workdir="/w" liveCard />;
    const view = render(draw([stopped], false));
    const ends = () => [view.container.textContent?.includes('Took 5s'), !!outcomeOf(view.getByText('Counting.')) && headOf(view.getByText('Counting.')).textContent?.includes('940 tokens')];
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

  test('a call a turn the service cut off left running is not the running step while the next turn starts; a dropped stream keeps it', () => {
    const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
    const bash = (id: string, s: number): Item => ({ id, kind: 'tool', time: at(s), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'seq 1 60' }) } });
    const items: Item[] = [{ id: 'u1', kind: 'user', time: at(0), text: 'Count to 60' }, bash('c1', 2)];
    // A restart or close cut the turn off: the service marks it unknown, without an end.
    const cut: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), state: 'unknown' };
    const draw = (turnTimings: TurnTiming[], working: boolean, list = items, connected = true) => <Transcript sessionId="s" items={list} turnTimings={turnTimings} interactions={[]} subagents={[]} live={working} working={working} connected={connected} provider="copilot" workdir="/w" liveCard />;
    const ring = () => view.container.querySelector('[data-step-mark]');
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
    expect(view.container.textContent).toContain('Took 4s');
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

  test('a step ends in its result: its test counts, exit code or failure as a block, its edits as line counts', () => {
    const run = (id: string, tests?: { passed: number; failed: number }, exit = tests?.failed ? 1 : 0, status: 'completed' | 'failed' = 'completed'): Item => ({ id, kind: 'tool', time: new Date().toISOString(), tool: { name: 'bash', status, exit_code: exit, input: JSON.stringify({ command: 'go test ./...' }), ...(tests && { tests }) } });
    const failing = render(<ToolRow item={run('b1', { passed: 12, failed: 2 })} live={false} />);
    const counts = failing.getByTitle('12 passed · 2 failed');
    expect(counts.className).toContain('bg-error-wash');
    // A phone keeps the failures alone.
    expect(counts.querySelector('.max-sm\\:hidden')?.textContent).toBe('12 passed · ');
    expect(failing.queryByText(/\d+\/\d+/)).toBeNull();
    failing.unmount();
    const passing = render(<ToolRow item={run('b2', { passed: 15, failed: 0 })} live={false} />);
    expect(passing.getByTitle('15 passed').className).toContain('bg-success-wash');
    passing.unmount();
    const none = render(<ToolRow item={run('b4', { passed: 0, failed: 3 })} live={false} />);
    expect(none.getByTitle('3 failed')).toBeTruthy();
    none.unmount();
    const plain = render(<ToolRow item={run('b3')} live={false} />);
    expect(plain.queryByText(/passed/)).toBeNull();
    expect(plain.getByTitle('exit 0').className).toContain('bg-success-wash');
    plain.unmount();
    const broke = render(<ToolRow item={run('b5', undefined, 2, 'failed')} live={false} />);
    expect(broke.getByTitle('exit 2').className).toContain('bg-error-wash');
    broke.unmount();
    const fetch: Item = { id: 'f1', kind: 'tool', time: new Date().toISOString(), tool: { name: 'web_fetch', status: 'failed', input: JSON.stringify({ url: 'https://example.test' }) } };
    const lost = render(<ToolRow item={fetch} live={false} />);
    expect(lost.getByTitle('failed').className).toContain('bg-error-wash');
    lost.unmount();
    const edit: Item = { id: 'e1', kind: 'tool', time: new Date().toISOString(), tool: { name: 'edit', status: 'completed', edit_event_id: 'ev', input: JSON.stringify({ path: 'a.go' }), file_edits: [{ path: 'a.go', kind: 'modified', additions: 12, deletions: 3, diff_status: 'available' }] } };
    // Its file rows carry the counts, so the step row shows none: one count per file.
    const edited = render(<ToolRow item={edit} live={false} />);
    expect(within(edited.getByRole('button', { name: /^edit/ })).queryByText('+12')).toBeNull();
    expect(edited.getAllByText('+12')).toHaveLength(1);
    edited.unmount();
    // Counts without file rows stand on the step row.
    const bare = render(<ToolRow item={{ ...edit, tool: { ...edit.tool!, edit_event_id: undefined } }} live={false} />);
    expect(within(bare.getByRole('button', { name: /^edit/ })).getByText('+12').parentElement!.textContent).toBe('+12 −3');
    bare.unmount();
  });

  test('a visual that throws while drawing leaves its fallback, not a broken transcript', () => {
    const quiet = vi.spyOn(console, 'error').mockImplementation(() => {});
    const Boom = () => { throw new Error('bad strip'); };
    const view = render(<VisualBoundary fallback={<span>plain row</span>}><Boom /></VisualBoundary>);
    expect(view.getByText('plain row')).toBeTruthy();
    quiet.mockRestore();
  });

  test('subagents in use sit at the foot in plain one-line rows under their chip; a row opens the strip\'s sheet on it', async () => {
    const { user } = await openTask('t8');
    // The running ones first (each followed by what it spawned), then the failed one, then the done one.
    const live = within(await log().findByRole('region', { name: 'Subagents at work' }));
    const rows = live.getAllByRole('button', { name: /, (failed|running|completed)/ });
    expect(rows.map((row) => row.getAttribute('aria-label')?.split(',')[0])).toEqual(['Survey templates for missing alt text and labels', 'Check contrast of the theme tokens', 'Verify store callers', 'Run the accessibility linter', 'Check the heading order']);
    // A failed row's caption is why; the chip above counts them with their tokens.
    expect(live.getByText('axe-core is not installed in this project.')).toBeTruthy();
    expect(live.getByRole('button', { name: /^5 subagents · [\d.]+M tokens · 1 done · 1 failed · 3 running/ })).toBeTruthy();
    // Nothing of them stands at their calls, and the turn line does not count them.
    expect(log().queryByRole('button', { name: 'Open' })).toBeNull();
    expect(log().queryByRole('button', { name: /activity of this turn/ })).toBeNull();
    await user.click(rows[0]);
    let sheet = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    expect(await sheet.findByRole('region', { name: 'Transcript of Survey templates for missing alt text and labels' })).toBeTruthy();
    // Nothing to type: subagents take no follow-up.
    expect(sheet.queryByRole('textbox')).toBeNull();
    await user.click(sheet.getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await user.click(rows[1]);
    sheet = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    expect(await sheet.findByRole('region', { name: 'Transcript of Check contrast of the theme tokens' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Transcript of Survey templates for missing alt text and labels' })).toBeNull();
  });
});

describe('turn card', () => {
  const at = (s: number) => `2026-10-05T10:00:${String(s).padStart(2, '0')}Z`;
  const edit = (id: string, s: number, path: string): Item => ({ id, kind: 'tool', time: at(s), ended_at: at(s + 1), tool: { name: 'edit', status: 'completed', path, input: JSON.stringify({ path }) } });
  const items: Item[] = [
    { id: 'u1', kind: 'user', time: at(0), text: 'Fix both files' },
    edit('e1', 1, '/w/internal/secret-a.go'),
    edit('e2', 3, '/w/internal/secret-b.go'),
    { id: 'a1', kind: 'assistant', time: at(8), text: 'Fixed both.' },
  ];
  const ended: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), ended_at: at(9), state: 'completed', output_tokens: 940, generation_ms: 4000, paused_ms: 1000 };

  test("the turn's card holds one Changed total chip, never a file's name, with copy and the turn's actions; its counts leave the files to the chip", () => {
    const view = render(<Transcript sessionId="s" items={items} turnTimings={[{ ...ended, changes: { status: 'available', event_id: 'ev', files: 2, additions: 52, deletions: 3 } }]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" onOpenChanges={vi.fn()} onOpenAllChanges={vi.fn()} />);
    const bar = outcomeOf(view.getByText('Fixed both.')) as HTMLElement;
    // In the flow, always in view and wrapping: never over the next message or past the edge.
    const row = bar.querySelector('[data-outcome]') as HTMLElement;
    expect(row.className).not.toMatch(/\babsolute\b|opacity-0|whitespace-nowrap|\bh-6\b/);
    expect(row.className).toContain('flex-wrap');
    expect(within(bar).getAllByRole('button', { name: /^Changed/ }).map((b) => b.getAttribute('aria-label'))).toEqual(['Changed 2 files +52 −3']);
    // The counts were only "2 files changed": they stay, the one way to the steps.
    expect(within(headOf(view.getByText('Fixed both.'))).getByRole('button', { name: /activity of this turn/ }).textContent).toContain('2 files changed');
    expect(within(bar).getByRole('button', { name: 'Copy message' })).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Turn actions' })).toBeTruthy();
    // No file's name anywhere in the card's head, not even in a tooltip; no separate changed-files line or GitLab label.
    expect(bar.textContent).not.toContain('secret');
    expect([...bar.querySelectorAll('[title]')].map((el) => el.getAttribute('title')).join(' ')).not.toContain('secret');
    expect(view.queryByRole('button', { name: 'View changes' })).toBeNull();
    expect(view.container.textContent).not.toMatch(/Changed 2 files|GitLab|Changes unavailable/);
  });

  test("a turn whose changes were not captured, or changed nothing, shows no chip and no stripe under its reply", () => {
    const view = render(<Transcript sessionId="s" items={[items[0], items[3]]} turnTimings={[{ ...ended, changes: { status: 'unavailable', event_id: 'ev' } }]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" onOpenAllChanges={vi.fn()} />);
    expect(view.container.textContent).not.toMatch(/Changes unavailable|Changed/);
    expect(cardOf(view.getByText('Fixed both.')).querySelector('[data-outcome]')!.childElementCount).toBe(0);
    expect(footOf(view.getByText('Fixed both.'))).toBeNull();
  });

  test("without captured counts the chip counts the files the turn's calls named and opens Changes on them", async () => {
    const user = userEvent.setup();
    const open = vi.fn();
    const view = render(<Transcript sessionId="s" items={items} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" onOpenChanges={open} liveCard />);
    const chip = within(outcomeOf(view.getByText('Fixed both.')) as HTMLElement).getByRole('button', { name: 'Changed 2 files' });
    expect(chip.textContent).toBe('2 files');
    await user.click(chip);
    expect(open).toHaveBeenCalledWith({ files: ['/w/internal/secret-a.go', '/w/internal/secret-b.go'], latest: true });
  });

  test("without a capture the chip takes its line counts from the edit calls' own patches, when every one recorded them", () => {
    const patched = (item: Item, additions: number, deletions: number): Item => ({ ...item, tool: { ...item.tool!, edit_event_id: `ev-${item.id}`, file_edits: [{ path: item.tool!.path!.slice(3), kind: 'modified', additions, deletions, diff_status: 'available' }] } });
    const draw = (list: Item[]) => <Transcript sessionId="s" items={list} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" onOpenChanges={vi.fn()} />;
    const view = render(draw([items[0], patched(items[1], 12, 3), patched(items[2], 40, 0), items[3]]));
    const chip = within(outcomeOf(view.getByText('Fixed both.')) as HTMLElement).getByRole('button', { name: 'Changed 2 files +52 −3' });
    expect(within(chip).getByText('+52').className).toContain('text-success');
    expect(within(chip).getByText('−3').className).toContain('text-error');
    // One call without recorded counts: the files alone, no guessed lines.
    view.rerender(draw([items[0], patched(items[1], 12, 3), items[2], items[3]]));
    expect(within(outcomeOf(view.getByText('Fixed both.')) as HTMLElement).getByRole('button', { name: 'Changed 2 files' })).toBeTruthy();
  });

  test('a todo block with nothing in it is left out', () => {
    const view = render(<Transcript sessionId="s" items={[items[0], items[3]]} turnTimings={[{ ...ended, todo: { done: 0, total: 2, pending: 2, open: 2 } }]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    const todo = view.getByRole('button', { name: /^Todo at the end of this turn/ });
    expect(within(todo).queryByTitle('0 done')).toBeNull();
    expect(within(todo).getByTitle('2 to do')).toBeTruthy();
  });

  test("a turn with no reply ending it yet has its edits' chip in its card; an ended turn without an outcome has copy alone", () => {
    const view = render(<Transcript sessionId="s" items={items.slice(0, 3)} turnTimings={[{ id: 't1', user_item_id: 'u1', started_at: at(0), state: 'working' }]} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" onOpenChanges={vi.fn()} liveCard />);
    const card = view.container.querySelector('[data-history-anchor^="turn-head-"]')!.parentElement as HTMLElement;
    expect(within(card).getByRole('button', { name: 'Changed 2 files' })).toBeTruthy();
    expect(within(card).queryByRole('button', { name: 'Copy message' })).toBeNull();
    view.rerender(<Transcript sessionId="s" items={[items[0], items[3]]} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" liveCard />);
    const bar = outcomeOf(view.getByText('Fixed both.')) as HTMLElement;
    expect(within(headOf(view.getByText('Fixed both.'))).getAllByRole('button').map((b) => b.getAttribute('aria-label')).filter(Boolean)).toEqual(['Copy message']);
    expect(bar.querySelector('[data-outcome]')!.childElementCount).toBe(0);
  });

  test("an ended turn's head splits its time: the model's own, its tool calls' and the time it waited on the user", () => {
    const view = render(<Transcript sessionId="s" items={items} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    const head = headOf(view.getByText('Fixed both.'));
    // Two one-second edits, less the second the turn waited.
    expect(head.textContent).toContain('Took 8s (model 4s · local 1s · waiting on you 1s)');
    // The counts are the toggle, at the head's end.
    expect(within(head).getByRole('button', { name: /activity of this turn/ }).textContent).toBe('2 files changed activity of this turn');
  });

  test("a running turn's head says it works, for how long, its tokens so far and its failures, never the running command", () => {
    const failed: Item = { id: 'x1', kind: 'tool', time: at(2), ended_at: at(3), tool: { name: 'web_fetch', status: 'failed', input: JSON.stringify({ url: 'https://example.test' }) } };
    const running: Item = { id: 'r1', kind: 'tool', time: at(4), tool: { name: 'bash', status: 'running', input: JSON.stringify({ command: 'go test ./internal/vterm' }) } };
    const view = render(<Transcript sessionId="s" items={[items[0], failed, running]} turnTimings={[{ id: 't1', user_item_id: 'u1', started_at: at(0), state: 'working', output_tokens: 4100, generation_ms: 79000 }]} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" liveCard />);
    const head = view.container.querySelector('[data-history-anchor^="turn-head-"]') as HTMLElement;
    expect(head.textContent).toMatch(/^Working/);
    expect(head.textContent).toContain('4.1K tokens · 52 tok/s');
    expect(within(head).getByText('1 failed').className).toContain('text-error');
    expect(head.textContent).not.toContain('go test');
    // One step mark: the running step's dots, at the foot.
    expect(view.container.querySelectorAll('[data-step-mark]')).toHaveLength(1);
  });

  test("a turn's end folds once: its timing ending before the state flips brings no Working row under its Took, and the next start, the queue's included, keeps its own", async () => {
    renderApp('#task=t3');
    const Source = window.EventSource;
    onTestFinished(() => { window.EventSource = Source; });
    let stream: EventSource | undefined;
    let original: SnapshotData | undefined;
    window.EventSource = class extends Source {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        if (String(url).startsWith('/api/events?')) this.addEventListener('snapshot', (event) => {
          stream = event.target as EventSource;
          original ??= JSON.parse((event as MessageEvent).data) as SnapshotData;
        });
      }
    };
    await waitFor(() => expect(original?.session?.id).toBe('t3'));
    const base = original!.session!;
    let seq = 1_000_000;
    const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString();
    const prompt: Item = { id: 'u-next', kind: 'user', time: ago(5), text: 'Run the tests once more.' };
    const reply: Item = { id: 'a-next', kind: 'assistant', time: ago(1), text: 'Tests pass.' };
    // As the service sends them: the state and the turn's timing come apart, at the start and at the end.
    const send = (state: SessionDetail['state'], timings: TurnTiming[], items: Item[]) => act(() => stream!.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify({
      ...original!, seq: ++seq,
      sessions: original!.sessions.map((s) => (s.id === 't3' ? { ...s, state } : s)),
      session: { ...base, state, items: [...base.items, ...items], turn_timings: timings },
    }) })));
    const working = () => log().queryAllByText('Working').length;
    const prior = base.turn_timings ?? [];
    // The start: working before its timing comes, the last timing still the previous turn's, ended. The Working row holds the slot.
    send('working', prior, [prompt]);
    await waitFor(() => expect(working()).toBe(1));
    const running: TurnTiming = { id: 'tt-next', user_item_id: 'u-next', started_at: ago(5), state: 'working' };
    send('working', [...prior, running], [prompt, reply]);
    await waitFor(() => expect(working()).toBe(1));
    // The end: the timing ends while the state still says working. The head says Took and nothing under it works.
    const ended: TurnTiming = { ...running, ended_at: ago(0), state: 'completed' };
    send('working', [...prior, ended], [prompt, reply]);
    await waitFor(() => expect(log().getAllByText('Took')).toHaveLength(prior.length + 1));
    expect(working()).toBe(0);
    // The queue's next prompt, its turn begun with the state never seen turning: its Working row holds the slot until its timing comes.
    const queued: Item = { id: 'u-queued', kind: 'user', time: ago(0), text: 'And run the linter.' };
    send('working', [...prior, ended], [prompt, reply, queued]);
    await waitFor(() => expect(working()).toBe(1));
    send('completed', [...prior, ended], [prompt, reply, queued]);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Stop turn' })).toBeNull());
    expect(working()).toBe(0);
  });

  test("a running turn's clock, on its card alone, leaves out approval and question waits, also after a remount", () => {
    vi.useFakeTimers();
    onTestFinished(() => vi.useRealTimers());
    const start = Date.parse(at(0));
    vi.setSystemTime(start + 5000);
    const working: TurnTiming = { id: 't1', user_item_id: 'u1', started_at: at(0), state: 'working' };
    const draw = (timing: TurnTiming) => <Transcript sessionId="s" items={[items[0]]} turnTimings={[timing]} interactions={[]} subagents={[]} live working provider="copilot" workdir="/w" liveCard />;
    const view = render(draw(working));
    const clock = () => view.container.textContent!.match(/Working\s*(\S+)/)?.[1];
    act(() => vi.advanceTimersByTime(0));
    expect(clock()).toBe('5s');
    view.rerender(draw({ ...working, paused_at: new Date(start + 5000).toISOString() }));
    act(() => vi.advanceTimersByTime(60_000));
    expect(clock()).toBe('5s');
    view.rerender(draw({ ...working, paused_ms: 60_000 }));
    act(() => vi.advanceTimersByTime(1000));
    expect(clock()).toBe('6s');
    view.unmount();
    const again = render(draw({ ...working, paused_ms: 60_000 }));
    act(() => vi.advanceTimersByTime(0));
    expect(again.container.textContent).toContain('Working 6s');
  });

  test('a step reads its result words apart for a screen reader, and a non-zero exit is a failure', () => {
    const vet: Item = { id: 'v1', kind: 'tool', time: at(1), ended_at: at(2), tool: { name: 'bash', status: 'completed', exit_code: 1, input: JSON.stringify({ command: 'go vet ./...' }), output: 'redraw.go:88:2: unreachable code' } };
    const view = render(<Transcript sessionId="s" items={[items[0], vet, items[3]]} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    fireEvent.click(view.getByRole('button', { name: /activity of this turn/ }));
    // The dots are not read; a comma parts the words (the test DOM pads inline boundaries with a space).
    const name = (n: string) => n.replaceAll(/\s+,/g, ',') === 'Ran go vet ./..., exit 1, redraw.go:88:2: unreachable code';
    expect(view.getByRole('button', { name })).toBeTruthy();
  });

  test('"Show all N calls" puts focus on the first step it showed', async () => {
    const user = userEvent.setup();
    const many: Item[] = Array.from({ length: 30 }, (_, i) => ({ id: `m${i}`, kind: 'tool', time: at(1), tool: { name: i % 2 ? 'view' : 'bash', status: 'completed', input: JSON.stringify(i % 2 ? { path: `f${i}.go` } : { command: `echo ${i}` }) } }));
    const view = render(<Transcript sessionId="s" items={[items[0], ...many, items[3]]} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    await user.click(view.getByRole('button', { name: /activity of this turn/ }));
    expect(view.queryByRole('button', { name: /^Read f1\.go/ })).toBeNull();
    await user.click(view.getByRole('button', { name: '17 earlier steps · Show all 30 calls' }));
    await waitFor(() => expect(document.activeElement).toBe(view.getByRole('button', { name: /^Read f1\.go/ })));
  });

  test('open steps are one clock-stamped timeline: a run of one kind folds, its duration on the row, its calls indented without a clock', async () => {
    const user = userEvent.setup();
    const view = render(<Transcript sessionId="s" items={items} turnTimings={[ended]} interactions={[]} subagents={[]} live={false} working={false} provider="copilot" workdir="/w" />);
    const head = view.getByRole('button', { name: /activity of this turn/ });
    // The turn line keeps counts only; durations live on the rows.
    expect(head.textContent).not.toMatch(/\(/);
    await user.click(head);
    const fold = await view.findByRole('button', { name: /^Edited 2 files/ });
    const row = fold.closest('[role="listitem"]')!;
    // Its clock (the full date in its tooltip), and its span from the first call's start to the last one's end.
    expect(row.firstElementChild!.getAttribute('title')).toBeTruthy();
    expect(row.children[3].textContent).toBe('3s');
    expect(view.queryByRole('button', { name: /^Edited secret-b\.go/ })).toBeNull();
    await user.click(fold);
    const call = await view.findByRole('button', { name: /^Edited secret-b\.go/ });
    // Per-step file names stay, behind the head; an unfolded call has its duration and no clock.
    const callRow = call.closest('[role="listitem"]')!;
    expect(callRow.textContent).toContain('1s');
    expect(callRow.querySelector('[title]')).toBeNull();
    // A call opens onto its raw input.
    await user.click(call);
    await waitFor(() => expect(call.getAttribute('aria-expanded')).toBe('true'));
    expect(callRow.textContent).toContain('secret-b.go');
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

  test('a subagent picked from the strip\'s list opens in the sheet; "Spawned" finds where in its reply, paging the history in', async () => {
    const { user } = await openTask('t15');
    const index = await openSubagentIndex(user);
    await user.click(index.getByRole('button', { name: /^Audit the remaining packages/ }));
    expect(await index.findByRole('region', { name: 'Transcript of Audit the remaining packages' })).toBeTruthy();
    await user.click(index.getByRole('button', { name: 'Show where it was spawned' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    // Its reply's chip holds its call: the conversation lands there.
    const chip = await screen.findByRole('button', { name: /^1 subagent · [\d.]+M tokens · 1 done/ });
    await waitFor(() => expect(chip.classList.contains('animate-flash')).toBe(true));
    expect(chip.dataset.subagentItems).toContain('h-audit');
  });
});
