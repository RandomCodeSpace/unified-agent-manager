import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Item } from '../../src/api';
import { Markdown } from '../../src/components/common';
import { PlannerContext, type PlannerContextValue } from '../../src/components/planner/context';
import { ToolRow } from '../../src/components/Transcript';
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

  test('a tool row menu expands it and copies its command', async () => {
    const { user } = await openTask('t14');
    const head = log().getAllByRole('button', { name: /activity of this turn/ })[0];
    await user.click(head);
    const menu = await openMenu(user, 'Actions for bash ls ~/projects/sky-dodge');
    await user.click(menu.getByRole('menuitem', { name: 'Copy command' }));
    const again = await openMenu(user, 'Actions for bash ls ~/projects/sky-dodge');
    await user.click(again.getByRole('menuitem', { name: 'Expand' }));
    await waitFor(() => expect(conversation().textContent).toContain('{"command":"ls ~/projects/sky-dodge"}'));
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

  test('subagent rows open their transcript from the conversation', async () => {
    const { user } = await openTask('t8');
    // The two running subagents stand in the conversation with a way into their transcripts.
    const open = await log().findAllByRole('button', { name: 'Open' });
    expect(open).toHaveLength(2);
    await user.click(open[0]);
    expect(await screen.findByRole('dialog', { name: /^Subagent / })).toBeTruthy();
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
  test('scrolling up a long compact task reads earlier pages', async () => {
    await openTask('t15');
    expect(await screen.findByText('Scroll up for earlier messages')).toBeTruthy();
    expect(log().queryByText('Turn 138: check pkg138 for unused exports.')).toBeNull();
    fireEvent.wheel(conversation(), { deltaY: -120 });
    expect(await log().findByText('Turn 138: check pkg138 for unused exports.')).toBeTruthy();
    fireEvent.keyDown(conversation(), { key: 'PageUp' });
    expect(await log().findByText('Turn 126: check pkg126 for unused exports.')).toBeTruthy();
  });

  test('the parent call of an older subagent is found by paging back', async () => {
    const { user } = await openTask('t15');
    await user.click(screen.getByRole('button', { name: 'Subagents, 1 or more' }));
    const panel = within(await screen.findByRole('dialog', { name: 'Subagents' }));
    await user.click(panel.getByRole('button', { name: /Completed/ }));
    const menu = await openMenu(user, 'Actions for subagent Audit the remaining packages');
    await user.click(menu.getByRole('menuitem', { name: 'Show where it was spawned' }));
    await waitFor(() => expect(document.getElementById('item-h-audit')?.classList.contains('animate-flash')).toBe(true));
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
