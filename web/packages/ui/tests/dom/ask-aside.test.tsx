import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type AsideAnswer, type Command } from '../../src/api';
import { ASIDE_COMMAND, forgetAsides, withAside } from '../../src/components/AskAside';
import { ASIDE_FOLLOW_UPS, ASIDE_QUESTION_BYTES, asideQuestion } from '../../src/lib/aside';
import { composer, openTask, type User } from './render';

afterEach(() => { vi.restoreAllMocks(); forgetAsides(); });

const card = () => screen.queryByRole('dialog', { name: 'Ask aside' });

/** Types `/btw <question>` in the composer and presses Enter. */
async function btw(user: User, question: string) {
  await waitFor(() => expect(card()).toBeNull());
  await user.click(composer());
  await user.type(composer(), `/btw ${question}`);
  // Enter asks once the command list has loaded and Send reads as the aside.
  await screen.findByRole('button', { name: 'Ask aside' });
  await user.keyboard('{Enter}');
}

async function dismiss(user: User) {
  await user.keyboard('{Escape}');
  await waitFor(() => expect(card()).toBeNull());
}

describe('/btw', () => {
  test('is the first command and asks aside: the answer shows in the card and nothing goes to the Task', async () => {
    const ask = vi.spyOn(api, 'askAside');
    const prompt = vi.spyOn(api, 'prompt');
    const command = vi.spyOn(api, 'command');
    const { user } = await openTask('t3');
    await user.type(composer(), '/');
    const list = within(await screen.findByRole('listbox', { name: 'Commands' }));
    await list.findByRole('option', { name: /^\/review/ });
    expect(list.getAllByRole('option')[0].getAttribute('aria-label') ?? list.getAllByRole('option')[0].textContent).toMatch(/^\/btw/);
    await user.clear(composer());
    await btw(user, 'did the tests pass?');
    const shown = within(await screen.findByRole('dialog', { name: 'Ask aside' }));
    expect(shown.getByText('did the tests pass?')).toBeTruthy();
    expect(shown.getByText('Waiting for the answer…')).toBeTruthy();
    expect(await shown.findByText(/the doctor never sends the CPR probe/)).toBeTruthy();
    expect(shown.getByText('Not added to the Task')).toBeTruthy();
    expect(ask).toHaveBeenCalledTimes(1);
    expect(ask.mock.calls[0].slice(0, 2)).toEqual(['t3', 'did the tests pass?']);
    expect(prompt).not.toHaveBeenCalled();
    expect(command).not.toHaveBeenCalled();
    expect(composer().value).toBe('');
    await dismiss(user);
    expect(document.activeElement).toBe(composer());
  });

  test('keeps this page\'s asides and sends the earlier ones as context with a follow-up', async () => {
    const ask = vi.spyOn(api, 'askAside')
      .mockResolvedValueOnce({ text: 'First **answer**.' })
      .mockResolvedValueOnce({ text: 'Second answer.' });
    const { user } = await openTask('t3');
    await btw(user, 'first question');
    expect(await within(await screen.findByRole('dialog', { name: 'Ask aside' })).findByText('answer')).toBeTruthy();
    await dismiss(user);
    // Leaving the Task and coming back keeps its page history.
    history.pushState(null, '', '/#task=t4');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Bump GitHub Actions pins'));
    history.pushState(null, '', '/#task=t3');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Doctor: add terminal line'));
    await waitFor(() => expect(composer()?.disabled).toBe(false));
    await btw(user, 'second question');
    const shown = within(await screen.findByRole('dialog', { name: 'Ask aside' }));
    expect(await shown.findByText('Second answer.')).toBeTruthy();
    // Both asides, oldest first; the card shows only what was typed, never the wrapped question.
    expect(shown.getAllByRole('article').map((a) => a.textContent)).toEqual(['first questionFirst answer.', 'second questionSecond answer.']);
    expect(shown.queryByText(/Earlier by-the-way/)).toBeNull();
    expect(ask).toHaveBeenCalledTimes(2);
    expect(ask.mock.calls[1][1]).toBe('Earlier by-the-way questions in this Task and their answers:\nQ: first question\nA: First **answer**.\n\nNew question: second question');
  });

  test('closing the card drops a question still waiting and ignores its late answer; one question waits at a time', async () => {
    let deliver!: (a: AsideAnswer) => void;
    const ask = vi.spyOn(api, 'askAside').mockImplementation(() => new Promise((resolve) => { deliver = resolve; }));
    const { user } = await openTask('t3');
    await btw(user, 'slow question');
    const shown = within(await screen.findByRole('dialog', { name: 'Ask aside' }));
    expect(shown.getByText('Waiting for the answer…')).toBeTruthy();
    // The wait is words, not another spinner.
    expect(screen.getByRole('dialog', { name: 'Ask aside' }).querySelector('[aria-busy="true"]')).toBeNull();
    const signal = ask.mock.calls[0][2]!;
    await dismiss(user);
    expect(signal.aborted).toBe(true);
    await act(async () => deliver({ text: 'obsolete answer' }));
    ask.mockResolvedValue({ text: 'fresh answer' });
    await btw(user, 'next question');
    const next = within(await screen.findByRole('dialog', { name: 'Ask aside' }));
    expect(await next.findByText('fresh answer')).toBeTruthy();
    expect(next.queryByText('slow question')).toBeNull();
    expect(next.queryByText('obsolete answer')).toBeNull();
    // The dropped question is not context for the next one.
    expect(ask.mock.calls[1][1]).toBe('next question');
  });

  test('an error stays in the card, bounded, and is not context for the next aside', async () => {
    const ask = vi.spyOn(api, 'askAside')
      .mockRejectedValueOnce(new Error('Connected failure: ' + 'x'.repeat(2_000_000)))
      .mockResolvedValueOnce({ text: 'ok' });
    const { user } = await openTask('t3');
    await btw(user, 'q1');
    const shown = within(await screen.findByRole('dialog', { name: 'Ask aside' }));
    await waitFor(() => expect(shown.getByRole('status').textContent?.startsWith('Connected failure: ')).toBe(true));
    expect(shown.getByRole('status').textContent?.length).toBe(512);
    await dismiss(user);
    await btw(user, 'q2');
    expect(await within(await screen.findByRole('dialog', { name: 'Ask aside' })).findByText('ok')).toBeTruthy();
    expect(ask.mock.calls[1][1]).toBe('q2');
  });
});

describe('the /btw entry and its question', () => {
  const list: Command[] = [{ name: 'review', description: 'Review', kind: 'command', input_hint: '' }, { name: ASIDE_COMMAND, description: 'native', kind: 'command', input_hint: '' }];

  test('comes first in place of a native command of that name, and says why while the conversation is closed', () => {
    const open = withAside(list, true);
    expect(open.map((c) => c.name)).toEqual([ASIDE_COMMAND, 'review']);
    expect(open[0].disabled_reason).toBeUndefined();
    expect(withAside(list, true)).toBe(open);
    expect(withAside(list, false)[0].disabled_reason).toBe("Ask aside needs the Task's conversation open. Send a prompt to open it first.");
  });

  test('carries at most five earlier asides, newest kept, within the service byte limit', () => {
    const prior = Array.from({ length: 7 }, (_, n) => ({ question: `q${n}`, answer: `a${n}` }));
    const text = asideQuestion(prior, 'new');
    expect(ASIDE_FOLLOW_UPS).toBe(5);
    expect(text.match(/^Q: /gm)).toHaveLength(5);
    expect(text).not.toContain('Q: q1\n');
    expect(text).toContain('Q: q2\nA: a2');
    expect(text.endsWith('Q: q6\nA: a6\n\nNew question: new')).toBe(true);
    // Multi-byte text counts in UTF-8 bytes: the oldest go first until the whole question fits.
    const wide = 'é'.repeat(3000); // 6000 bytes
    const big = [{ question: 'old', answer: wide }, { question: 'mid', answer: wide }, { question: 'recent', answer: wide }];
    const fitted = asideQuestion(big, 'new');
    expect(new TextEncoder().encode(fitted).length).toBeLessThanOrEqual(ASIDE_QUESTION_BYTES);
    expect(fitted).not.toContain('Q: old');
    expect(fitted).toContain('Q: mid');
    expect(fitted).toContain('Q: recent');
    // Nothing fits beside a question near the limit: it goes alone.
    const long = 'x'.repeat(ASIDE_QUESTION_BYTES - 10);
    expect(asideQuestion(big, long)).toBe(long);
    expect(asideQuestion([], 'alone')).toBe('alone');
  });
});
