// A question that no longer waits is one compact card: the question and what it got, at most
// two lines, that opens onto the full question, every choice and the whole answer.
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test } from 'vitest';
import type { Interaction, ToolCall } from '../../src/api';
import { QuestionBlock } from '../../src/components/Transcript';
import { questionOf } from '../../src/lib/transcript';

const ask = (question: string, choices: string[], extra: Partial<ToolCall> = {}): ToolCall => ({ name: 'ask_user', title: 'Ask user', status: 'completed', input: JSON.stringify({ question, choices }), ...extra });
const draw = (tool: ToolCall | undefined, interaction?: Interaction, live = false) => render(<QuestionBlock id="q" asked={questionOf(tool, interaction, live)!} />);
const card = () => within(screen.getByRole('region', { name: 'Question' }));
const toggle = () => card().getByRole('button', { expanded: false });

describe('a settled question', () => {
  test('waiting, it shows the question and every choice in full, with nothing to open', () => {
    draw(ask('Which port?', ['8000', '8080'], { status: 'running' }), undefined, true);
    expect(card().getByText('Waiting for your answer')).toBeTruthy();
    expect(card().getByText('8000')).toBeTruthy();
    expect(card().getByText('8080')).toBeTruthy();
    expect(card().queryByRole('button')).toBeNull();
  });

  test('a chosen option reads "You chose" with its label; the button names the whole question and answer', () => {
    draw(ask('Which port should the local server use?', ['8000', '8080'], { output: 'User selected: 8000' }));
    expect(toggle().getAttribute('aria-label')).toBe('Question: Which port should the local server use? You chose: 8000');
    expect(card().getByText('You chose')).toBeTruthy();
    expect(card().getByText('8000')).toBeTruthy();
    expect(card().getByTitle('Which port should the local server use?')).toBeTruthy();
    // The other choice waits behind the toggle.
    expect(card().queryByText('8080')).toBeNull();
  });

  test('typed text reads "You wrote" with the text, whole in its tooltip', () => {
    const long = 'Only the viewport, at 1280 by 800, so the game fills the frame';
    draw(ask('Whole page or viewport?', ['Whole page', 'Viewport only'], { output: `User responded: ${long}` }));
    expect(card().getByText('You wrote')).toBeTruthy();
    expect(card().getByTitle(long).textContent).toBe(long);
    expect(card().queryByText('You chose')).toBeNull();
  });

  test('several chosen options read as their labels joined', () => {
    const ix: Interaction = { id: 'i', kind: 'question', title: 'Question', state: 'answered', resolution: 'Linux, macOS', time: '', questions: [{ text: 'Which platforms?', choices: ['Linux', 'macOS', 'Windows'], multiple: true, custom: true }] };
    draw(undefined, ix);
    expect(card().getByText('You chose')).toBeTruthy();
    expect(card().getByText('Linux, macOS')).toBeTruthy();
  });

  test('declined reads "Declined"', () => {
    draw(ask('Which port?', ['8000'], { status: 'failed', output: 'the user declined to answer' }));
    expect(toggle().getAttribute('aria-label')).toBe('Question: Which port? Declined');
    expect(card().getByText('Declined')).toBeTruthy();
  });

  test('a call left open by a stopped turn reads "Not answered"', () => {
    draw(ask('Which port?', ['8000'], { status: 'running' }), undefined, false);
    expect(card().getByText('Not answered').className).toContain('text-muted');
  });

  test('the service\'s bare "answered" is not shown as the answer', () => {
    draw(undefined, { id: 'i', kind: 'question', title: 'Question', state: 'answered', resolution: 'answered', time: '', questions: [{ text: 'Which port?', choices: ['8000'], custom: true }] });
    expect(card().getByText('Answered')).toBeTruthy();
    expect(card().queryByText('You wrote')).toBeNull();
  });

  test('opening it shows the full question, every choice with the chosen one marked, and the whole answer', async () => {
    const user = userEvent.setup();
    draw(ask('Which port should the **local** server use?', ['8000', '8080'], { output: 'User selected: 8000' }));
    await user.click(toggle());
    const open = card().getByRole('button', { expanded: true });
    expect(open.getAttribute('aria-controls')).toBeTruthy();
    const body = within(document.getElementById(open.getAttribute('aria-controls')!)!);
    expect(body.getByText('local').tagName).toBe('STRONG');
    expect(body.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['8000(chosen)', '8080']);
    expect(body.getByText('You chose')).toBeTruthy();
    await user.keyboard('{Enter}');
    expect(card().getByRole('button', { expanded: false })).toBeTruthy();
  });

  test('several questions are one summary that opens onto each question', async () => {
    const user = userEvent.setup();
    const ix: Interaction = {
      id: 'i', kind: 'question', title: 'Question', state: 'answered', resolution: 'Answered: A; X, Y', time: '',
      questions: [{ text: 'Pick one', choices: ['A', 'B'], custom: true }, { text: 'Pick many', choices: ['X', 'Y', 'Z'], multiple: true, custom: false }, { text: 'Anything else?', custom: true }],
    };
    draw(undefined, ix);
    expect(toggle().getAttribute('aria-label')).toBe('3 questions: Pick one · Pick many · Anything else? You answered: A; X, Y');
    await user.click(toggle());
    const body = within(document.getElementById(card().getByRole('button', { expanded: true }).getAttribute('aria-controls')!)!);
    expect(body.getByText('Pick one')).toBeTruthy();
    expect(body.getByText('Pick many')).toBeTruthy();
    expect(body.getByText('Z')).toBeTruthy();
    expect(body.getByText('A; X, Y')).toBeTruthy();
  });
});
