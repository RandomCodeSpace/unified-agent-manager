import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { expect, test, vi } from 'vitest';
import { Markdown, ReplyContext, type ReplyTools } from '../../src/components/common';
import { planKey } from '../../src/lib/richMarkdown';

/** Markdown with the Thread's reply tools, as the transcript renders it. */
const inReply = (tools: ReplyTools, children: ReactNode) => <ReplyContext.Provider value={tools}>{children}</ReplyContext.Provider>;

test('a GitHub alert renders as a titled callout without its marker', () => {
  const { container } = render(<Markdown text={'> [!WARNING]\n> The cache is stale.'} />);
  const note = screen.getByRole('note');
  expect(note.dataset.callout).toBe('warning');
  expect(note.textContent?.replace(/\s+/g, ' ').trim()).toBe('Warning The cache is stale.');
  expect(container.querySelector('blockquote')).toBeNull();
});

test('a plain quote and an unknown alert kind stay quotes', () => {
  const { container } = render(<Markdown text={'> just a quote\n\n> [!DANGER] nope'} />);
  expect(screen.queryByRole('note')).toBeNull();
  expect(container.querySelectorAll('blockquote')).toHaveLength(2);
  expect(container.textContent).toContain('[!DANGER] nope');
});

test('a list of bold labels becomes a label/value grid; short figures become tiles', () => {
  const { container } = render(<Markdown text={'- **Branch:** `main`\n- **Status**: green, see [CI](https://example.test)\n\nNext\n\n- **Tests:** 742\n- **Coverage:** 81%\n- **Time:** 1.2 s'} />);
  const [facts, tiles] = container.querySelectorAll('dl');
  expect(facts.className).toBe('md-facts');
  expect([...facts.querySelectorAll('dt')].map(d => d.textContent)).toEqual(['Branch', 'Status']);
  expect([...facts.querySelectorAll('dd')].map(d => d.textContent)).toEqual(['main', 'green, see CI']);
  expect(tiles.className).toBe('md-facts md-tiles');
  expect([...tiles.querySelectorAll('dd')].map(d => d.textContent)).toEqual(['742', '81%', '1.2 s']);
  expect(container.querySelector('ul')).toBeNull();
});

test('a list where one item lacks a bold label stays an ordinary list', () => {
  const { container } = render(<Markdown text={'- **Branch:** main\n- plain item'} />);
  expect(container.querySelector('dl')).toBeNull();
  expect(container.querySelectorAll('li')).toHaveLength(2);
});

test('a numbered list of bold leads renders as steps', () => {
  const { container } = render(<Markdown text={'1. **Build** the binary\n2. **Deploy** it'} />);
  expect(container.querySelector('ol')?.className).toBe('md-steps');
  const { container: other } = render(<Markdown text={'1. **Build** it\n2. deploy'} />);
  expect(other.querySelector('ol')?.className).toBeFalsy();
});

test('a task list shows how many items are done', () => {
  render(<Markdown text={'- [x] write\n- [x] test\n- [ ] ship'} />);
  const bar = screen.getByRole('progressbar', { name: '2 of 3 done' });
  expect(bar.getAttribute('value')).toBe('2');
  expect(bar.getAttribute('max')).toBe('3');
});

test('rich blocks wait for a streaming block to finish', () => {
  const text = '> [!NOTE]\n> soon';
  const { rerender } = render(<Markdown text={text} streaming />);
  expect(screen.queryByRole('note')).toBeNull();
  rerender(<Markdown text={text} />);
  expect(screen.getByRole('note').dataset.callout).toBe('note');
});

test('label paragraphs in a row become one facts grid, also after streaming split the text', () => {
  const text = 'Intro.\n\n**Contents:** a playground.\n\n**Git**: on `main`.\n\n**Risks:** the CSV header changed.\n\nAfter.';
  for (const streamed of [false, true]) {
    const { container, rerender, unmount } = render(<Markdown text={text} streaming={streamed} />);
    if (streamed) rerender(<Markdown text={text} />);
    const grids = container.querySelectorAll('dl.md-facts');
    expect(grids).toHaveLength(1);
    expect([...grids[0].querySelectorAll('dt')].map(d => d.textContent)).toEqual(['Contents', 'Git', 'Risks']);
    expect(grids[0].querySelectorAll('dd')[1].textContent).toBe('on main.');
    expect([...container.querySelectorAll('.md > p, .md p')].map(p => p.textContent)).toEqual(['Intro.', 'After.']);
    unmount();
  }
});

test('one label paragraph, or one without a value, stays a paragraph', () => {
  const { container } = render(<Markdown text={'**Note:** only one.\n\n**Already done:**\n\n- a'} />);
  expect(container.querySelector('dl')).toBeNull();
});

test('one paragraph of label lines becomes facts; short figures become tiles', () => {
  const { container } = render(<Markdown text={'**Tracked files:** 5\n**Python files:** 2\n**Lines of Python:** 62\n\nThen **Branch:** main\nand more.'} />);
  const grids = container.querySelectorAll('dl');
  expect(grids).toHaveLength(1);
  expect(grids[0].className).toBe('md-facts md-tiles');
  expect([...grids[0].querySelectorAll('dd')].map(d => d.textContent)).toEqual(['5', '2', '62']);
  expect(container.textContent).toContain('Then Branch: main');
});

test('a question or blocker is a callout like the others: its options stay a list, nothing to answer in place', () => {
  const { container } = render(<Markdown text={'> [!QUESTION]\n> Which database should the cache use?\n>\n> - Postgres\n> - SQLite\n\n> [!BLOCKER]\n> No token for the registry.'} />);
  expect(screen.getAllByRole('note').map((n) => n.dataset.callout)).toEqual(['question', 'blocker']);
  expect([...container.querySelectorAll('[data-callout="question"] li')].map((li) => li.textContent)).toEqual(['Postgres', 'SQLite']);
  expect(screen.queryByRole('textbox')).toBeNull();
  expect(screen.queryByRole('button')).toBeNull();
});

test('a reply\'s plan follows the todo list: items whose title a row has take its state, the others keep their own', () => {
  const plan = new Map([[planKey('Bump the SDK'), 'done' as const], [planKey('Run the suites'), 'in_progress' as const]]);
  const { container } = render(inReply({ plan }, <Markdown text={'- [ ] Bump the SDK\n- [ ] Run the suites\n- [x] Write notes\n- [ ] Deploy'} />));
  expect([...container.querySelectorAll('li')].map((li) => [li.dataset.state, li.dataset.live ?? ''])).toEqual([['done', 'true'], ['in_progress', 'true'], ['done', ''], ['pending', '']]);
  expect(container.querySelector('input')).toBeNull();
  expect(container.querySelector('.md-progress')?.textContent).toBe('2 of 4 done· from the todo list');
  const { container: steps } = render(inReply({ plan }, <Markdown text={'1. **Bump the SDK.** then tidy\n2. **Deploy.** to dev'} />));
  expect([...steps.querySelectorAll('li')].map((li) => li.dataset.state ?? '')).toEqual(['done', '']);
  expect(steps.querySelector('.md-progress')?.textContent).toBe('1 of 2 done· from the todo list');
});


test('a list whose every item opens with ✅ ❌ ⚠️ is a status list: failures first, a count line, each mark a glyph', () => {
  const { container } = render(<Markdown text={'- ✅ Go tests\n- ❌ Sonar gate\n- ⚠️ Not tried live\n- ✔ ESLint'} />);
  const list = container.querySelector('ul.md-status')!;
  expect([...list.querySelectorAll('li')].map((li) => [li.dataset.status, li.textContent])).toEqual([
    ['fail', 'Sonar gate, failed'],
    ['warn', 'Not tried live, warning'],
    ['ok', 'Go tests, passed'],
    ['ok', 'ESLint, passed'],
  ]);
  expect(container.querySelector('.md-counts')?.textContent).toBe('1 failed · 1 warning · 2 passed');
  // One plain item keeps the list as written.
  const { container: plain } = render(<Markdown text={'- ✅ done\n- plain'} />);
  expect(plain.querySelector('.md-status')).toBeNull();
  expect(plain.textContent).toContain('✅ done');
});

test('a list of severity labels is grouped most severe first with a count line; the label leaves the text', () => {
  const { container } = render(<Markdown text={'- **Medium:** retry has no cap\n- **High:** token in the log\n- **Low**: typo\n- **P1:** race on close'} />);
  const rows = [...container.querySelectorAll('ul.md-severity > li')];
  expect(rows.map((li) => [li.dataset.severity, li.querySelector('.md-level')?.textContent, li.lastElementChild?.textContent])).toEqual([
    ['high', 'High:', 'token in the log'],
    ['high', 'P1:', 'race on close'],
    ['medium', 'Medium:', 'retry has no cap'],
    ['low', 'Low:', 'typo'],
  ]);
  expect(container.querySelector('.md-counts')?.textContent).toBe('2 high · 1 medium · 1 low');
  expect(container.querySelector('dl')).toBeNull();
});

test('a list of file paths is file rows; "(+a −d)" becomes counts and leaves the text', () => {
  const { container } = render(<Markdown text={'- `internal/web/turn_activity.go` (+4 −6)\n- `web/src/lib/todos.ts` (+18 -3): newest first\n- `DESIGN.md`'} />);
  const rows = [...container.querySelectorAll('ul.md-files > li')];
  expect(rows).toHaveLength(3);
  expect(rows.map((li) => li.querySelector('.md-file-counts')?.textContent ?? '')).toEqual(['+4−6', '+18−3', '']);
  expect(rows[1].textContent).toContain('todos.ts: newest first');
  expect(rows[0].textContent).not.toContain('(+4');
  const { container: other } = render(<Markdown text={'- `npm ci` first\n- `src/a.ts`'} />);
  expect(other.querySelector('.md-files')).toBeNull();
});

test('a fact whose value reads "old → new" shows the change, with the signed delta for figures in one unit', () => {
  const { container } = render(<Markdown text={'- **SDK:** v1.0.26 → v1.0.28\n- **Bundle:** 14.6 MB → 19.6 MB\n- **Cold load:** 1.9 s -> 1.4 s\n- **Owner:** the web team'} />);
  const dds = [...container.querySelectorAll('dd')];
  expect(dds.map((dd) => [dd.querySelector('del')?.textContent ?? '', dd.querySelector('strong')?.textContent ?? '', dd.querySelector('.md-delta')?.textContent ?? ''])).toEqual([
    ['v1.0.26', 'v1.0.28', ''],
    ['14.6 MB', '19.6 MB', '+5.0 MB'],
    ['1.9 s', '1.4 s', '−0.5 s'],
    ['', '', ''],
  ]);
  expect(dds[3].textContent).toBe('the web team');
});

test('a shell fence written as a session is a row per command, each copying alone; the block copies the commands', async () => {
  const user = userEvent.setup();
  const writeText = vi.fn(async () => {});
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
  const { container } = render(<Markdown text={'```sh\n$ make build\n$ go test ./internal/web/\nok  internal/web  65.9s\n```'} />);
  const rows = [...container.querySelectorAll('.md-cmd')];
  expect(rows.map((r) => r.querySelector('code')?.textContent)).toEqual(['$make build', '$go test ./internal/web/']);
  expect(rows[1].querySelector('.md-cmd-out')?.textContent).toBe('ok  internal/web  65.9s');
  await user.click(screen.getByRole('button', { name: 'Copy go test ./internal/web/' }));
  expect(writeText).toHaveBeenLastCalledWith('go test ./internal/web/');
  const { container: plain } = render(<Markdown text={'```sh\nmake build\n```'} />);
  expect(plain.querySelector('.md-cmds')).toBeNull();
});

test('a table column of pass/fail words or marks shows toned labels; the words stay, a bare mark keeps its word for screen readers', () => {
  const { container } = render(<Markdown text={'| Suite | Result | Mark |\n|---|---|---|\n| web | pass | ✅ |\n| go | fail | ❌ |'} />);
  const cells = [...container.querySelectorAll('tbody tr')].map((tr) => [...tr.querySelectorAll('td')].slice(1).map((td) => [td.querySelector('svg') ? 'glyph' : '', td.textContent]));
  expect(cells).toEqual([[['glyph', 'pass'], ['glyph', 'pass']], [['glyph', 'fail'], ['glyph', 'fail']]]);
});

test('a value with several changes stays as written; a figure with a Unicode minus still makes a tile', () => {
  const { container } = render(<Markdown text={'- **React:** 18.2.0 → 19.0.0, bundle 142 KB → 118 KB\n- **Vite:** 5.2.0 → 6.0.1\n\nStats:\n\n- **Tests:** 742\n- **Build time:** 38 s\n- **Bundle delta:** −45 KB'} />);
  const [changes, stats] = container.querySelectorAll('dl');
  const dds = [...changes.querySelectorAll('dd')];
  expect(dds[0].querySelector('del')).toBeNull();
  expect(dds[0].textContent).toBe('18.2.0 → 19.0.0, bundle 142 KB → 118 KB');
  expect(dds[1].querySelector('del')?.textContent).toBe('5.2.0');
  expect(stats.className).toBe('md-facts md-tiles');
});

test('a list whose every item opens with a time is a timeline: the time on the rail, a gap of over an hour marked', () => {
  const { container } = render(<Markdown text={'- 06:58 #708 merged\n- **07:04** — dev on `c2934028`\n- 09:11: v0.21.12 published'} />);
  const items = [...container.querySelectorAll('ul.md-timeline > li')] as HTMLElement[];
  expect(items.map((li) => [li.querySelector('time')?.textContent, li.lastElementChild?.textContent, li.dataset.gap ?? ''])).toEqual([
    ['06:58', '#708 merged', ''],
    ['07:04', 'dev on c2934028', 'true'],
    ['09:11', 'v0.21.12 published', ''],
  ]);
  const { container: mixed } = render(<Markdown text={'- 06:58 merged\n- then deployed\n- 1:5 ratio'} />);
  expect(mixed.querySelector('.md-timeline')).toBeNull();
});

test('Pros and Cons lists sit side by side, also after streaming split the text', () => {
  const text = 'Options:\n\n**Pros**\n- no new dependency\n- deterministic\n\n**Cons:**\n- needs exact titles';
  for (const streamed of [false, true]) {
    const { container, rerender, unmount } = render(<Markdown text={text} streaming={streamed} />);
    if (streamed) rerender(<Markdown text={text} />);
    const columns = [...container.querySelectorAll('.md-pair > div')];
    expect(columns.map((c) => [c.querySelector('.md-pair-label')?.textContent, [...c.querySelectorAll('li')].map((li) => li.textContent)])).toEqual([
      ['Pros', ['no new dependency', 'deterministic']],
      ['Cons:', ['needs exact titles']],
    ]);
    unmount();
  }
  const { container } = render(<Markdown text={'**Pros**\n- a\n\n**Risks**\n- b'} />);
  expect(container.querySelector('.md-pair')).toBeNull();
});

test('a reply that opens with **Result:** sets its gist apart; the same label later, or as one of several label lines, does not', () => {
  const text = '**Result:** v0.21.12 is out and on dev.\n\nDetails follow.\n\n**Summary:** not the opening.';
  for (const streamed of [false, true]) {
    const { container, rerender, unmount } = render(<Markdown text={text} streaming={streamed} />);
    if (streamed) rerender(<Markdown text={text} />);
    const leads = container.querySelectorAll('.md-lead');
    expect(leads).toHaveLength(1);
    expect([...leads[0].querySelectorAll('p')].map((p) => p.textContent)).toEqual(['Result', 'v0.21.12 is out and on dev.']);
    unmount();
  }
  const { container } = render(<Markdown text={'**Result:** green\n**Tests:** 742'} />);
  expect(container.querySelector('.md-lead')).toBeNull();
  expect(container.querySelector('dl.md-facts')).not.toBeNull();
});

test('a # title with three or more ## sections gets a section index; a click brings the section into view', async () => {
  const scrolled: string[] = [];
  vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(function (this: HTMLElement) { scrolled.push(this.textContent ?? ''); });
  const text = '**Result:** shipped.\n\n# Release `v0.21.12`\n\n## Changes\n\nA.\n\n```md\n## Not a section\n```\n\n## Verification\n\nB.\n\n## Open items\n\nC.';
  for (const streamed of [false, true]) {
    const { rerender, unmount } = render(<Markdown text={text} streaming={streamed} />);
    if (streamed) rerender(<Markdown text={text} />);
    const index = screen.getByRole('list', { name: 'Sections' });
    expect(index.closest('.md-outline')?.querySelector('h1')?.textContent).toBe('Release v0.21.12');
    const buttons = within(index).getAllByRole('button');
    expect(buttons.map((b) => b.textContent)).toEqual(['01Changes', '02Verification', '03Open items']);
    await userEvent.click(buttons[1]);
    expect(scrolled.at(-1)).toBe('Verification');
    unmount();
  }
  for (const other of ['# Title\n\n## One\n\n## Two', '## Intro\n\n# Title\n\n## One\n\n## Two\n\n## Three']) {
    const { container } = render(<Markdown text={other} />);
    expect(container.querySelector('.md-outline')).toBeNull();
  }
  vi.restoreAllMocks();
});

test('a bold line alone over a list is its head: toned when it says what was or was not verified', () => {
  const { container } = render(<Markdown text={'**Verified**\n- Go tests\n\n**Not verified:**\n- Windows\n\n**Next steps**\n- Release\n\n**Unverified:** ran nothing\n\n**Pros**\n- a\n\n**Cons**\n- b'} />);
  expect([...container.querySelectorAll('p.md-head')].map((p) => [p.textContent, (p as HTMLElement).dataset.tone ?? ''])).toEqual([
    ['Verified', 'ok'],
    ['Not verified:', 'warn'],
    ['Next steps', ''],
  ]);
  expect(container.querySelector('p.md-head svg')).not.toBeNull();
  expect(container.textContent).toContain('Unverified: ran nothing');
  expect(container.querySelector('.md-pair')).not.toBeNull();
});

test('**Label** — value items are facts; a hyphen is not a separator', () => {
  const { container } = render(<Markdown text={'- **`add`** — Reject duplicate SKUs\n- **`remove`** – Keep the audit row\n\nThen\n\n- **a** - one\n- **b** - two'} />);
  const grid = container.querySelector('dl.md-facts')!;
  expect([...grid.querySelectorAll('dt')].map((d) => d.textContent)).toEqual(['add', 'remove']);
  expect([...grid.querySelectorAll('dd')].map((d) => d.textContent)).toEqual(['Reject duplicate SKUs', 'Keep the audit row']);
  expect(container.querySelectorAll('dl')).toHaveLength(1);
});

test('paragraphs numbered **1.** **2.** in bold are steps, also after streaming split the text; a gap leaves them as written', () => {
  const text = '**1. `Inventory.value` — lines 34–36**\nSums floats.\n\n**2. `remove()` — line 52**\nDrops the audit row.\n\nAfter.';
  for (const streamed of [false, true]) {
    const { container, rerender, unmount } = render(<Markdown text={text} streaming={streamed} />);
    if (streamed) rerender(<Markdown text={text} />);
    const items = [...container.querySelectorAll('ol.md-steps > li')] as HTMLElement[];
    expect(items.map((li) => li.textContent?.replace(/\s+/g, ' ').trim())).toEqual(['Inventory.value — lines 34–36 Sums floats.', 'remove() — line 52 Drops the audit row.']);
    expect(container.querySelector('.md > p:last-child')?.textContent).toBe('After.');
    unmount();
  }
  const { container } = render(<Markdown text={'**1. One**\n\n**3. Three**'} />);
  expect(container.querySelector('ol')).toBeNull();
});

test('choices are drawn like steps with their letter, never tied to the todo list', () => {
  const plan = new Map([[planKey('Retry with luna'), 'done' as const]]);
  const { container } = render(inReply({ plan }, <Markdown text={'**To unblock, pick one:**\n\n1. **Retry with luna** — cheaper\n2. **Split the file** — two PRs'} />));
  const list = container.querySelector('ol.md-options')!;
  expect([...list.querySelectorAll('li')].map((li) => [li.dataset.option, li.dataset.state ?? '', li.dataset.plan ?? ''])).toEqual([['A', '', ''], ['B', '', '']]);
  expect(container.querySelector('.md-progress')).toBeNull();
  expect(container.querySelector('button, [role="radio"]')).toBeNull();
  const { container: lettered } = render(<Markdown text={'- **A · Ledger:** quiet\n- **B · Cards:** loud'} />);
  expect(lettered.querySelector('dl')).toBeNull();
  expect([...lettered.querySelectorAll('ul.md-options > li')].map((li) => [li.dataset.option, li.textContent])).toEqual([['A', 'Ledger: quiet'], ['B', 'Cards: loud']]);
});

test('a line with counts of two kinds gets a count line, failures first; one kind does not', () => {
  const { container } = render(<Markdown text={'Ran the suite: 979 passed, 3 skipped, 1 failed.\n\nAll 12 passed.'} />);
  expect([...container.querySelectorAll('.md-counts')].map((p) => p.textContent)).toEqual(['1 failed · 3 skipped · 979 passed']);
  expect(container.querySelector('.md-counts .text-error')?.textContent).toBe('1 failed');
});

test('the mouse over inline code highlights every identical inline code span in the message', async () => {
  const set = vi.fn(), del = vi.fn();
  vi.stubGlobal('CSS', { highlights: { set, delete: del } });
  vi.stubGlobal('Highlight', class { ranges: Range[]; constructor(...ranges: Range[]) { this.ranges = ranges; } });
  const { container } = render(<Markdown text={'Merged as `a20a1947`.\n\nDev runs `a20a1947`, not `e444bfe3`.'} />);
  const [first] = [...container.querySelectorAll('code')];
  await userEvent.hover(first);
  expect(set).toHaveBeenCalledWith('echo', expect.objectContaining({ ranges: [expect.any(Range), expect.any(Range)] }));
  await userEvent.unhover(first);
  expect(del).toHaveBeenCalledWith('echo');
  vi.unstubAllGlobals();
});
