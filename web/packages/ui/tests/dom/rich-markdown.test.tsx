import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { Markdown } from '../../src/components/common';

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
