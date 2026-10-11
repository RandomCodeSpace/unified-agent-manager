import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useRef, useState } from 'react';
import { beforeEach, expect, test, vi } from 'vitest';
import { WritingAssistance } from '../../src/components/WritingAssistance';
import { checkWriting, type WritingIssue } from '../../src/lib/writing-checker';
import { replaceWritingRange } from '../../src/lib/writing-edit';

vi.mock('../../src/lib/writing-checker', () => ({ checkWriting: vi.fn(), MAX_WRITING_CHECK_LENGTH: 12_000 }));

const SAMPLE = 'There is two checks.';
const grammar: WritingIssue = { start: 0, end: 8, original: 'There is', replacements: ['There are'], kind: 'grammar', message: 'Use a plural verb with a plural subject.' };
const checker = vi.mocked(checkWriting);

function Harness({ initial = SAMPLE, scope = 'draft', disabled = false, suspended = false }: { initial?: string; scope?: string; disabled?: boolean; suspended?: boolean }) {
  const [text, setText] = useState(initial);
  const [target, setTarget] = useState<HTMLSpanElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  return <form onSubmit={event => { event.preventDefault(); throw new Error('A correction submitted the draft'); }}>
    <div className="relative">
      <textarea aria-label="Message" ref={textareaRef} value={text} disabled={disabled} onChange={event => setText(event.currentTarget.value)} />
      <WritingAssistance text={text} textareaRef={textareaRef} scope={scope} disabled={disabled} suspended={suspended} toolbarTarget={target} onChange={setText} />
    </div>
    <span ref={setTarget} />
  </form>;
}

const input = () => screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;
const review = () => screen.getByRole('button', { name: /^Spelling and grammar/ });
const checked = () => screen.findByRole('button', { name: 'Spelling and grammar: 1 suggestions' });
const pause = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 750)); });

beforeEach(() => { checker.mockReset(); checker.mockResolvedValue([grammar]); });

test('checking leaves typing alone; explicit Apply changes one range and offers fallback Undo', async () => {
  render(<Harness />);
  const user = userEvent.setup();
  input().focus();
  input().setSelectionRange(SAMPLE.length, SAMPLE.length);
  await checked();
  expect(document.activeElement).toBe(input());
  expect(input().value).toBe(SAMPLE);
  expect(input().selectionStart).toBe(SAMPLE.length);
  await user.click(review());
  expect(within(await screen.findByRole('dialog')).getByText(grammar.message)).toBeTruthy();
  await user.click(screen.getByRole('button', { name: 'Apply' }));
  expect(input().value).toBe('There are two checks.');
  expect(input().selectionStart).toBe(SAMPLE.length + 1);
  expect(document.activeElement).toBe(input());
  await user.click(review());
  await user.click(await screen.findByRole('button', { name: 'Undo correction' }));
  expect(input().value).toBe(SAMPLE);
  expect(input().selectionStart).toBe(SAMPLE.length);
});

test('typing after a fallback correction removes its Undo instead of replacing newer input', async () => {
  render(<Harness />);
  const user = userEvent.setup();
  await checked();
  await user.click(review());
  await user.click(screen.getByRole('button', { name: 'Apply' }));
  fireEvent.change(input(), { target: { value: 'There are two checks. More text.' } });
  await user.click(review());
  expect(screen.queryByRole('button', { name: 'Undo correction' })).toBeNull();
  expect(input().value).toBe('There are two checks. More text.');
});

test('Keep original survives checking the draft again but belongs only to this buffer', async () => {
  const { rerender } = render(<Harness />);
  const user = userEvent.setup();
  await checked();
  await user.click(review());
  await user.click(screen.getByRole('button', { name: 'Keep original' }));
  expect(input().value).toBe(SAMPLE);
  expect(review().getAttribute('aria-label')).toBe('Spelling and grammar');
  fireEvent.change(input(), { target: { value: SAMPLE + ' More.' } });
  await pause();
  expect(review().getAttribute('aria-label')).toBe('Spelling and grammar');
  rerender(<Harness scope="answer-2" />);
  await checked();
});

test('old checks cannot reappear after the draft changes A to B to A', async () => {
  let oldResult!: (issues: WritingIssue[]) => void;
  checker.mockImplementationOnce(() => new Promise(resolve => { oldResult = resolve; }));
  render(<Harness />);
  await waitFor(() => expect(checker).toHaveBeenCalledTimes(1));
  fireEvent.change(input(), { target: { value: 'Different text.' } });
  fireEvent.change(input(), { target: { value: SAMPLE } });
  await act(async () => oldResult([grammar]));
  expect(document.querySelector('[data-writing-issue]')).toBeNull();
  await checked();
  expect(checker).toHaveBeenCalledTimes(2);
});

test('composition, pickers and disabled input suspend assistance and close its popup', async () => {
  const { rerender } = render(<Harness />);
  fireEvent.compositionStart(input());
  await pause();
  expect(checker).not.toHaveBeenCalled();
  fireEvent.compositionEnd(input());
  await checked();
  const user = userEvent.setup();
  await user.click(review());
  rerender(<Harness suspended />);
  expect(review().hasAttribute('disabled')).toBe(true);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(document.querySelector('[data-writing-issue]')).toBeNull();
  rerender(<Harness disabled />);
  expect(review().hasAttribute('disabled')).toBe(true);
});

test('custom checking holds native spellcheck off between keystrokes and restores it after failure', async () => {
  render(<Harness />);
  await checked();
  expect(input().getAttribute('spellcheck')).toBe('false');
  checker.mockRejectedValueOnce(new Error('Worker unavailable'));
  fireEvent.change(input(), { target: { value: SAMPLE + ' More.' } });
  expect(input().getAttribute('spellcheck')).toBe('false');
  await waitFor(() => expect(input().getAttribute('spellcheck')).toBeNull());
  await userEvent.setup().click(review());
  expect(await screen.findByText(/checks are unavailable/)).toBeTruthy();
  expect(input().value).toBe(SAMPLE + ' More.');
});

test('long drafts keep their value and show the checking limit only when reviewed', async () => {
  render(<Harness initial={'x'.repeat(12_001)} />);
  await pause();
  expect(checker).not.toHaveBeenCalled();
  await userEvent.setup().click(review());
  expect(await screen.findByText(/support drafts up to 12,000 characters/)).toBeTruthy();
  expect(input().value.length).toBe(12_001);
});

test('the keyboard review button opens an actionable popup and Escape restores the typing selection', async () => {
  render(<Harness />);
  const user = userEvent.setup();
  await checked();
  input().setSelectionRange(12, 12);
  review().focus();
  await user.keyboard('{Enter}');
  const apply = await screen.findByRole('button', { name: 'Apply' });
  await waitFor(() => expect(document.activeElement).toBe(apply));
  await user.keyboard('{Escape}');
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(document.activeElement).toBe(input());
  expect(input().selectionStart).toBe(12);
});

test('Escape from the textarea closes writing review before composer history or edit handlers', async () => {
  const handler = vi.fn();
  render(<div onKeyDown={handler}><Harness /></div>);
  const user = userEvent.setup();
  await checked();
  const rectangles = vi.spyOn(Range.prototype, 'getClientRects').mockReturnValue([new DOMRect(10, 10, 60, 20)] as unknown as DOMRectList);
  try {
    input().focus();
    input().setSelectionRange(12, 12);
    fireEvent.pointerDown(input(), { button: 0, clientX: 20, clientY: 20 });
    // Native pointerdown moves the caret before click; activating the underline restores it.
    input().setSelectionRange(2, 2);
    fireEvent.click(input(), { detail: 1, clientX: 20, clientY: 20 });
    expect(await screen.findByRole('dialog')).toBeTruthy();
    expect(document.activeElement).toBe(input());
    expect(input().selectionStart).toBe(12);
    await user.keyboard('{Escape}');
    expect(handler).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(input().selectionStart).toBe(12);
  } finally { rectangles.mockRestore(); }
});

test('replacement refuses stale or disabled drafts and uses a native insertion when available', () => {
  const textarea = document.createElement('textarea');
  textarea.value = SAMPLE;
  document.body.append(textarea);
  try {
    expect(replaceWritingRange(textarea, 'stale draft', 0, 8, 'There are')).toBeNull();
    textarea.disabled = true;
    expect(replaceWritingRange(textarea, SAMPLE, 0, 8, 'There are')).toBeNull();
    textarea.disabled = false;
    const native = vi.fn(() => { textarea.setRangeText('There are', textarea.selectionStart, textarea.selectionEnd, 'end'); return true; });
    Object.defineProperty(document, 'execCommand', { configurable: true, value: native });
    expect(replaceWritingRange(textarea, SAMPLE, 0, 8, 'There are')?.native).toBe(true);
    expect(native).toHaveBeenCalledWith('insertText', false, 'There are');
  } finally {
    delete (document as unknown as { execCommand?: unknown }).execCommand;
    textarea.remove();
  }
});
