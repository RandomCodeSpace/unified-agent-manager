import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { expect, test, vi } from 'vitest';
import { ItemContext, Markdown, ReplyContext, type ReplyTools } from '../../src/components/common';
import { planKey } from '../../src/lib/richMarkdown';

/** A reply's Markdown as the transcript renders it: in its item, with the Thread's reply tools. */
const inReply = (tools: ReplyTools, children: ReactNode, id = 'r1') => (
  <ReplyContext.Provider value={tools}>
    <ItemContext.Provider value={{ id, text: '' }}>{children}</ItemContext.Provider>
  </ReplyContext.Provider>
);

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

const QUESTION = '> [!QUESTION]\n> Which database should the cache use?\n>\n> - Postgres\n> - SQLite';

test('a question on the latest reply answers in place: a choice pressed twice sends it with the question quoted', async () => {
  const user = userEvent.setup();
  const send = vi.fn(async () => null);
  render(inReply({ latest: 'r1', send }, <Markdown text={QUESTION} />));
  const note = screen.getByRole('note');
  expect(note.dataset.callout).toBe('question');
  const choices = within(note).getByRole('group', { name: 'Choices' });
  expect(within(choices).getAllByRole('button').map((b) => b.textContent)).toEqual(['Postgres', 'SQLite']);
  await user.click(within(choices).getByRole('button', { name: 'SQLite' }));
  expect(within(choices).getByRole('button', { name: 'SQLite' }).getAttribute('aria-pressed')).toBe('true');
  expect(send).not.toHaveBeenCalled();
  await user.click(within(choices).getByRole('button', { name: 'SQLite' }));
  expect(send).toHaveBeenCalledWith('> Which database should the cache use?\n\nSQLite');
});

test('a blocker takes typed words; an error stays beside the field', async () => {
  const user = userEvent.setup();
  const send = vi.fn(async () => 'The thread did not take the answer.');
  render(inReply({ latest: 'r1', send }, <Markdown text={'> [!BLOCKER]\n> No token for the registry.'} />));
  const note = screen.getByRole('note');
  expect(note.dataset.callout).toBe('blocker');
  expect(within(note).queryByRole('group')).toBeNull();
  await user.type(within(note).getByRole('textbox', { name: 'Answer' }), 'Use the read-only token{Enter}');
  expect(send).toHaveBeenCalledWith('> No token for the registry.\n\nUse the read-only token');
  await waitFor(() => expect(within(note).getByRole('alert').textContent).toBe('The thread did not take the answer.'));
  expect((within(note).getByRole('textbox', { name: 'Answer' }) as HTMLInputElement).value).toBe('Use the read-only token');
});

test('a question on an older reply, or a read-only Thread, shows its choices as a plain list', () => {
  const { container } = render(inReply({ latest: 'r2', send: vi.fn() }, <Markdown text={QUESTION} />));
  expect(screen.queryByRole('textbox')).toBeNull();
  expect([...container.querySelectorAll('.md-choices-plain li')].map((li) => li.textContent)).toEqual(['Postgres', 'SQLite']);
  const { container: locked } = render(inReply({ latest: 'r1' }, <Markdown text={QUESTION} />));
  expect(locked.querySelectorAll('.md-choices-plain li')).toHaveLength(2);
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

test('a reply that ends by asking answers in place too, drawn as prose; a closing list after the question gives its choices', async () => {
  const user = userEvent.setup();
  const send = vi.fn(async () => null);
  const { container } = render(inReply({ latest: 'r1', send }, <Markdown text={'Fixed the header.\n\nShould I also fix the footer?'} />));
  expect(screen.queryByRole('note')).toBeNull();
  expect(container.querySelector('.md-ask p')?.textContent).toBe('Should I also fix the footer?');
  await user.type(screen.getByRole('textbox', { name: 'Answer' }), 'Yes{Enter}');
  expect(send).toHaveBeenCalledWith('> Should I also fix the footer?\n\nYes');
  render(inReply({ latest: 'r1', send }, <Markdown text={'Which one should I keep?\n\n- The old cache\n- The new cache'} />));
  expect(within(screen.getByRole('group', { name: 'Choices' })).getAllByRole('button').map((b) => b.textContent)).toEqual(['The old cache', 'The new cache']);
});

test('a question that is not the reply\'s end, or a closing list without a question before it, stays plain', () => {
  const { container } = render(inReply({ latest: 'r1', send: vi.fn() }, <Markdown text={'Is this right?\n\nI think so.'} />));
  expect(container.querySelector('.md-ask')).toBeNull();
  const { container: list } = render(inReply({ latest: 'r1', send: vi.fn() }, <Markdown text={'Done:\n\n- a\n- b'} />));
  expect(list.querySelector('.md-ask')).toBeNull();
  expect(screen.queryByRole('textbox')).toBeNull();
});
