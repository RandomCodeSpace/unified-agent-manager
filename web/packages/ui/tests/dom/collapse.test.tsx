import { act, render } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { Collapse } from '../../src/components/ui/collapse';

afterEach(() => vi.useRealTimers());

const box = (container: HTMLElement) => container.firstElementChild as HTMLElement;

test('a close keeps the room while the content fades, then steps to 0fr once and reports', () => {
  vi.useFakeTimers();
  const onClosed = vi.fn();
  const { container, rerender } = render(<Collapse open onClosed={onClosed}>Body</Collapse>);
  rerender(<Collapse open={false} onClosed={onClosed}>Body</Collapse>);
  // Under reduced motion the motion-reduce variant snaps the track at once.
  expect(box(container).className).toContain('grid-rows-[1fr] motion-reduce:grid-rows-[0fr]');
  expect(box(container).firstElementChild?.className).toContain('opacity-0 transition-opacity duration-100');
  act(() => vi.advanceTimersByTime(100));
  expect(onClosed).not.toHaveBeenCalled();
  act(() => vi.advanceTimersByTime(20));
  expect(box(container).className).not.toContain('grid-rows-[1fr]');
  expect(box(container).className).toContain('grid-rows-[0fr]');
  expect(onClosed).toHaveBeenCalledTimes(1);
});

test('reopening during the fade keeps the room and never reports closed', () => {
  vi.useFakeTimers();
  const onClosed = vi.fn();
  const { container, rerender } = render(<Collapse open onClosed={onClosed}>Body</Collapse>);
  rerender(<Collapse open={false} onClosed={onClosed}>Body</Collapse>);
  act(() => vi.advanceTimersByTime(50));
  rerender(<Collapse open onClosed={onClosed}>Body</Collapse>);
  act(() => vi.advanceTimersByTime(500));
  expect(box(container).className).toContain('grid-rows-[1fr]');
  expect(box(container).className).not.toContain('grid-rows-[0fr]');
  expect(onClosed).not.toHaveBeenCalled();
});
