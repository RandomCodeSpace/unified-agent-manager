import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type Item } from '../../src/api';
import { Markdown } from '../../src/components/common';
import { EChart } from '../../src/components/EChart';
import { chartOption } from '../../src/lib/chart';
import { PlannerContext, type PlannerContextValue } from '../../src/components/planner/context';
import { ToolRow, Transcript } from '../../src/components/Transcript';
import { saveDensity } from '../../src/lib/density';
import { composer, log, openMenu, openTask } from './render';

afterEach(() => saveDensity('compact'));

const conversation = () => screen.getByRole('region', { name: 'Conversation' });

describe('messages', () => {
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
    // Hover only: the row keeps its height and fades in with the pointer over the block.
    expect(foot.className).toContain('opacity-0');
    expect(foot.className).toContain('group-hover/copy:opacity-100');
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

describe('planner', () => {
  const item: Item = {
    id: 'board-1', kind: 'tool', time: '2026-09-29T12:00:00Z',
    tool: { name: 'board_get', status: 'completed', display_arg: '#12', board_card: { id: 'c1', seq: 12, kind: 'subtask', title: 'Make it', status: 'doing' } },
  };

  test('a planner tool call names its card beside its row, and the name opens the card', () => {
    const openCard = vi.fn();
    const planner = { enabled: true, openCard } as unknown as PlannerContextValue;
    const view = render(<PlannerContext.Provider value={planner}><ToolRow item={item} live={false} /></PlannerContext.Provider>);
    fireEvent.click(view.getByRole('button', { name: '#12 Make it' }));
    expect(openCard).toHaveBeenCalledWith('c1');
    // The chip sits outside the row's toggle, so opening the card leaves the row folded.
    expect(view.getByRole('button', { name: /^board_get/ }).getAttribute('aria-expanded')).toBe('false');
  });

  test('the card name is plain text while the planner is off or absent, and a failed call has none', () => {
    const planner = { enabled: false, openCard: vi.fn() } as unknown as PlannerContextValue;
    const view = render(<PlannerContext.Provider value={planner}><ToolRow item={item} live={false} /></PlannerContext.Provider>);
    expect(view.getByTitle('#12 Make it').tagName).toBe('SPAN');
    expect(view.queryByRole('button', { name: '#12 Make it' })).toBeNull();
    view.rerender(<ToolRow item={item} live={false} />);
    expect(view.getByTitle('#12 Make it').textContent).toBe('#12 Make it');
    expect(view.queryByRole('button', { name: '#12 Make it' })).toBeNull();
    view.rerender(<ToolRow item={{ ...item, tool: { name: 'board_get', status: 'failed', display_arg: '#12' } }} live={false} />);
    expect(view.queryByTitle('#12 Make it')).toBeNull();
  });
});
