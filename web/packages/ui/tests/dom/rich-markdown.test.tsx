import { render, screen } from '@testing-library/react';
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
