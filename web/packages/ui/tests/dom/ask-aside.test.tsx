import { act, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api, type AsideAnswer, type Command } from '../../src/api';
import { ASIDE_COMMAND, forgetAsides, keepAside, withAside } from '../../src/components/AskAside';
import { Transcript } from '../../src/components/Transcript';
import { ASIDE_FOLLOW_UPS, ASIDE_QUESTION_BYTES, asideQuestion, asideTurn } from '../../src/lib/aside';
import { composer, openTask, type User } from './render';

afterEach(() => { vi.restoreAllMocks(); forgetAsides(); });

const TASK = 'Asides in this Task';
const TURN = 'Asides in this turn';
const reader = (name = TASK) => screen.queryByRole('dialog', { name });
/** t3's three kept asides (src/mock/asides.ts), oldest first. */
const KEPT = ['Will the new row also show in uam doctor --json?', 'Where does term.Describe() get the glyph set from?', 'Would anything change for an SSH session without a TTY?'];
const questions = (dialog: HTMLElement) => within(dialog).getAllByRole('article').map((a) => a.querySelector('p')?.textContent);

/** Types `/btw <question>` (or a bare `/btw`) in the composer and presses Enter. */
async function btw(user: User, question = '') {
  await waitFor(() => expect(reader()).toBeNull());
  await user.click(composer());
  // A bare `/btw ` past the command list: Enter on `/btw` alone picks the command first, as any does.
  await user.type(composer(), `/btw ${question}`);
  // Enter acts once the command list has loaded and Send reads as the aside.
  await screen.findByRole('button', { name: question ? 'Ask aside' : 'Show asides' });
  await user.keyboard('{Enter}');
}

async function dismiss(user: User, name = TASK) {
  await user.keyboard('{Escape}');
  await waitFor(() => expect(reader(name)).toBeNull());
}

/** The asides chips in reply and message feet, with the transcript item each foot belongs to. */
function chips() {
  return screen.queryAllByRole('button', { name: /in this turn/ }).map((chip) => ({ chip, item: (chip.closest('[data-history-anchor]') ?? chip.parentElement?.parentElement?.querySelector('[data-history-anchor]'))?.getAttribute('data-history-anchor') }));
}

describe('/btw', () => {
  test('is the first command and asks aside: the answer lands in the Task\'s asides and nothing goes to the agent', async () => {
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
    const dialog = await screen.findByRole('dialog', { name: TASK });
    const shown = within(dialog);
    expect(shown.getByText('did the tests pass?')).toBeTruthy();
    expect(shown.getByText('Waiting for the answer…')).toBeTruthy();
    // The wait is words here; the ring is the chip's alone.
    expect(dialog.querySelector('.animate-spin')).toBeNull();
    expect(await shown.findByText(/TestDoctorDumbTerminal/)).toBeTruthy();
    expect(shown.getByText('Never sent to the agent')).toBeTruthy();
    expect(questions(dialog)).toEqual([...KEPT, 'did the tests pass?']);
    expect(ask).toHaveBeenCalledTimes(1);
    expect(ask.mock.calls[0][0]).toBe('t3');
    expect(ask.mock.calls[0][2]).toBe('did the tests pass?');
    expect(prompt).not.toHaveBeenCalled();
    expect(command).not.toHaveBeenCalled();
    expect(composer().value).toBe('');
    await dismiss(user);
    expect(document.activeElement).toBe(composer());
    // Kept with the latest turn: its reply's chip counts it.
    await waitFor(() => expect(chips().find((c) => c.item === 'i6')?.chip.textContent).toBe('btw2'));
  });

  test('chips each turn with its asides, and the chip lists them oldest first', async () => {
    const { user } = await openTask('t3');
    await waitFor(() => expect(chips().map((c) => [c.item, c.chip.textContent])).toEqual([['i4', 'btw2'], ['i6', 'btw1']]));
    const [first] = chips();
    expect(first.chip.getAttribute('aria-label')).toBe('2 asides in this turn');
    await user.click(first.chip);
    const dialog = await screen.findByRole('dialog', { name: TURN });
    expect(questions(dialog)).toEqual(KEPT.slice(0, 2));
    expect(within(dialog).getByText('while the agent worked')).toBeTruthy();
    expect(within(dialog).getByText('after the turn ended')).toBeTruthy();
    expect(within(dialog).getByText(/^From the CPR probe/)).toBeTruthy();
    await dismiss(user, TURN);
  });

  test('a turn without a reply foot yet carries its chip in its message\'s foot', async () => {
    const at = new Date().toISOString();
    keepAside(api, 'live', { id: 'k1', question: 'still going?', answer: 'Yes.', asked_at: at, answered_at: at, working: true, turn: 'u1' });
    const items = [{ id: 'u1', kind: 'user' as const, text: 'run the suite', time: at }, { id: 'a1', kind: 'assistant' as const, text: 'Running it.', time: at }];
    const view = render(<Transcript sessionId="live" provider="copilot" workdir="/w" items={items} interactions={[]} subagents={[]} live working />);
    await waitFor(() => expect(chips().map((c) => [c.item, c.chip.textContent])).toEqual([['u1', 'btw1']]));
    // Once the turn ends, its reply's foot takes the chip.
    view.rerender(<Transcript sessionId="live" provider="copilot" workdir="/w" items={items} interactions={[]} subagents={[]} live={false} working={false} />);
    await waitFor(() => expect(chips().map((c) => c.item)).toEqual(['a1']));
  });

  test('a bare /btw opens the Task\'s asides without asking', async () => {
    const ask = vi.spyOn(api, 'askAside');
    const { user } = await openTask('t3');
    await btw(user);
    const dialog = await screen.findByRole('dialog', { name: TASK });
    await waitFor(() => expect(questions(dialog)).toEqual(KEPT));
    expect(within(dialog).getByText(/^3 asides, oldest first/)).toBeTruthy();
    expect(ask).not.toHaveBeenCalled();
    await dismiss(user);
  });

  test('a follow-up carries the kept asides, also after a reload', async () => {
    const ask = vi.spyOn(api, 'askAside');
    const { user } = await openTask('t3');
    await btw(user, 'first question');
    expect(await within(await screen.findByRole('dialog', { name: TASK })).findByText(/TestDoctorDumbTerminal/)).toBeTruthy();
    await dismiss(user);
    // A reload forgets the page; the Task kept the aside.
    forgetAsides();
    history.pushState(null, '', '/#task=t4');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Bump GitHub Actions pins'));
    history.pushState(null, '', '/#task=t3');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Doctor: add terminal line'));
    await waitFor(() => expect(composer()?.disabled).toBe(false));
    await waitFor(() => expect(chips().find((c) => c.item === 'i6')?.chip.textContent).toBe('btw2'));
    await btw(user, 'second question');
    const dialog = await screen.findByRole('dialog', { name: TASK });
    await waitFor(() => expect(questions(dialog)).toEqual([...KEPT, 'first question', 'second question']));
    // The reader shows only what was typed, never the wrapped question.
    expect(within(dialog).queryByText(/Earlier by-the-way/)).toBeNull();
    expect(ask).toHaveBeenCalledTimes(2);
    const followUp = ask.mock.calls[1][1];
    expect(followUp.startsWith('Earlier by-the-way questions in this Task and their answers:\nQ: Will the new row')).toBe(true);
    expect(followUp).toContain('Q: first question\nA: Yes. `TestDoctorDumbTerminal`');
    expect(followUp.endsWith('\n\nNew question: second question')).toBe(true);
    expect(ask.mock.calls[1][2]).toBe('second question');
  });

  test('a question still waiting rings its turn\'s chip and goes on after the reader closes; leaving the Task drops it', async () => {
    let deliver!: (a: AsideAnswer) => void;
    const ask = vi.spyOn(api, 'askAside').mockImplementation(() => new Promise((resolve) => { deliver = resolve; }));
    const { user } = await openTask('t3');
    await btw(user, 'slow question');
    await screen.findByRole('dialog', { name: TASK });
    await dismiss(user);
    const signal = ask.mock.calls[0][3]!;
    expect(signal.aborted).toBe(false);
    const chip = chips().find((c) => c.item === 'i6')!.chip;
    expect(chip.getAttribute('aria-label')).toBe('2 asides in this turn, one waiting for its answer');
    expect(chip.querySelectorAll('.animate-spin')).toHaveLength(1);
    // One question waits at a time; the history still opens.
    await user.type(composer(), '/btw another{Enter}');
    expect(ask).toHaveBeenCalledTimes(1);
    expect(composer().value).toBe('/btw another');
    await user.clear(composer());
    history.pushState(null, '', '/#task=t4');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(signal.aborted).toBe(true));
    await act(async () => deliver({ text: 'obsolete answer' }));
    history.pushState(null, '', '/#task=t3');
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    await waitFor(() => expect(composer()?.disabled).toBe(false));
    await waitFor(() => expect(chips().find((c) => c.item === 'i6')?.chip.textContent).toBe('btw1'));
    await btw(user);
    const dialog = await screen.findByRole('dialog', { name: TASK });
    expect(questions(dialog)).toEqual(KEPT);
    expect(within(dialog).queryByText('obsolete answer')).toBeNull();
  });

  test('an error stays in the reader, bounded, and is not kept or context for the next aside', async () => {
    const ask = vi.spyOn(api, 'askAside')
      .mockRejectedValueOnce(new Error('Connected failure: ' + 'x'.repeat(2_000_000)))
      .mockResolvedValueOnce({ text: 'ok' });
    const { user } = await openTask('t3');
    await btw(user, 'q1');
    const shown = within(await screen.findByRole('dialog', { name: TASK }));
    await waitFor(() => expect(shown.getByRole('status').textContent?.startsWith('Connected failure: ')).toBe(true));
    expect(shown.getByRole('status').textContent?.length).toBe(512);
    await dismiss(user);
    await btw(user, 'q2');
    expect(await within(await screen.findByRole('dialog', { name: TASK })).findByText('ok')).toBeTruthy();
    expect(ask.mock.calls[1][1]).not.toContain('q1');
    expect(ask.mock.calls[1][2]).toBe('q2');
  });
});

describe('the /btw entry and its question', () => {
  const list: Command[] = [{ name: 'review', description: 'Review', kind: 'command', input_hint: '' }, { name: ASIDE_COMMAND, description: 'native', kind: 'command', input_hint: '' }];

  test('comes first in place of a native command of that name, takes no required question, and says why while the conversation is closed', () => {
    const open = withAside(list, true);
    expect(open.map((c) => c.name)).toEqual([ASIDE_COMMAND, 'review']);
    expect(open[0].disabled_reason).toBeUndefined();
    expect(open[0].input_required).toBeFalsy();
    expect(open[0].input_hint).toBe('question');
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

  test('belongs to the latest message the owner sent, not a steer or a subagent\'s', () => {
    const at = '2026-10-10T00:00:00Z';
    expect(asideTurn([])).toBe('');
    expect(asideTurn([
      { id: 'u1', kind: 'user', time: at },
      { id: 'a1', kind: 'assistant', time: at },
      { id: 'u2', kind: 'user', time: at },
      { id: 's1', kind: 'user', time: at, delivery: 'steer' },
      { id: 'c1', kind: 'user', time: at, agent_id: 'helper' },
    ])).toBe('u2');
  });
});
