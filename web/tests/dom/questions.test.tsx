// A question with one question is answered from the composer: the card stages options, the
// composer holds the text and the files, and what the answer cannot carry is steered first.
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { composer, log, openTask } from './render';

const answerButton = () => screen.getByRole('button', { name: /^Answer|^Submitting/ });
const card = (title: string) => within(screen.getByRole('group', { name: title }));
const textFile = () => new File(['hello from a log\n'], 'build.log', { type: 'text/plain' });

describe('answering from the composer', () => {
  test('an option stages a chip in the composer; another replaces it; its remove button clears it', async () => {
    const { user } = await openTask('t16');
    const q = card('How should the retry be bounded?');
    expect(q.queryByRole('button', { name: 'Answer' })).toBeNull();
    expect(q.queryByRole('textbox')).toBeNull();
    expect(screen.getByText('Answering')).toBeTruthy();
    expect(composer().placeholder).toBe('Type your answer…');
    expect(answerButton()).toHaveProperty('disabled', true);
    await user.click(q.getByRole('radio', { name: 'Retry once' }));
    expect(screen.getByRole('button', { name: 'Remove answer Retry once' })).toBeTruthy();
    expect(composer().placeholder).toBe('Add a note (sent with your answer)…');
    expect(answerButton()).toHaveProperty('disabled', false);
    await user.click(q.getByRole('radio', { name: 'Retry up to 3 times' }));
    expect(screen.queryByRole('button', { name: 'Remove answer Retry once' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Remove answer Retry up to 3 times' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Remove answer Retry up to 3 times' }));
    expect(screen.queryByRole('button', { name: /^Remove answer/ })).toBeNull();
    expect(q.getByRole('radio', { name: 'Retry up to 3 times' })).toHaveProperty('checked', false);
    expect(answerButton()).toHaveProperty('disabled', true);
  });

  test('typed text answers a free-text question on Enter; nothing is steered', async () => {
    const { user, mock } = await openTask('t18');
    expect(composer().placeholder).toBe('Type your answer…');
    await user.type(composer(), 'Copilot web goes stable');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('group', { name: 'What should the release note lead with?' })).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['Copilot web goes stable']]);
    await waitFor(() => expect(composer().value).toBe(''));
    expect(await log().findByText('Copilot web goes stable')).toBeTruthy();
    expect(screen.queryByText('Answering')).toBeNull();
  });

  test('an option with a note and a file steers the note and the file first, then answers', async () => {
    const { user, mock } = await openTask('t16');
    const q = card('How should the retry be bounded?');
    await user.upload(document.querySelector<HTMLInputElement>('form input[type="file"]')!, textFile());
    expect(await screen.findByRole('button', { name: 'Remove build.log' })).toBeTruthy();
    await user.click(q.getByRole('radio', { name: 'Retry once' }));
    await user.type(composer(), 'The race is in the focus replay');
    await waitFor(() => expect(answerButton()).toHaveProperty('disabled', false), { timeout: 3000 });
    await user.click(answerButton());
    await waitFor(() => expect(screen.queryByRole('group', { name: 'How should the retry be bounded?' })).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['prompt', 'answer']);
    const [steer, answer] = mock.received;
    expect(steer.body.mode).toBe('steer');
    expect(steer.body.text).toBe('The race is in the focus replay');
    expect(steer.body.attachments).toHaveLength(1);
    expect(answer.body.answers).toEqual([['Retry once']]);
    expect(await log().findByText('The race is in the focus replay')).toBeTruthy();
    await waitFor(() => expect(composer().value).toBe(''));
    expect(screen.queryByRole('button', { name: /^Remove answer/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull();
  });

  test('an options-only question takes typed text as a note, never as the answer', async () => {
    const { user, mock } = await openTask('t17');
    expect(composer().placeholder).toBe('Add a note (sent with your answer)…');
    await user.type(composer(), 'CI pins it');
    expect(answerButton()).toHaveProperty('disabled', true);
    await user.click(card('Which package manager does this project use?').getByRole('radio', { name: 'pnpm' }));
    expect(answerButton()).toHaveProperty('disabled', false);
    // Staging leaves the focus on the option (arrow keys move between radios); Enter sends from the composer.
    await user.click(composer());
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Which package manager does this project use?' })).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['prompt', 'answer']);
    expect(mock.received[0].body.text).toBe('CI pins it');
    expect(mock.received[1].body.answers).toEqual([['pnpm']]);
  });

  test('Decline on the card ends answer mode', async () => {
    const { user, mock } = await openTask('t17');
    await user.click(card('Which package manager does this project use?').getByRole('button', { name: 'Decline' }));
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Which package manager does this project use?' })).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.reject).toBe(true);
    await waitFor(() => expect(screen.queryByText('Answering')).toBeNull());
    expect(composer().placeholder).toBe('Steer this turn, or queue a follow-up…');
  });

  test('the task draft is kept while answering and comes back once the question is answered', async () => {
    localStorage.setItem('uam.draft.t16', JSON.stringify({ text: 'half a thought', files: [], attachments: [] }));
    const { user } = await openTask('t16');
    expect(composer().value).toBe('');
    await user.type(composer(), 'Once is enough');
    // Typing pauses longer than the draft delay: the stored draft is still the task's, not the answer.
    await new Promise((r) => setTimeout(r, 400));
    expect(localStorage.getItem('uam.draft.t16')).toContain('half a thought');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByRole('group', { name: 'How should the retry be bounded?' })).toBeNull());
    await waitFor(() => expect(composer().value).toBe('half a thought'));
    expect(screen.queryByText('Answering')).toBeNull();
  });

  test('answered elsewhere after the note went: the note stands, the composer says so and clears', async () => {
    const { user, mock } = await openTask('t16');
    await user.click(card('How should the retry be bounded?').getByRole('radio', { name: 'Retry once' }));
    await user.type(composer(), 'see the CI log');
    const real = window.fetch;
    window.fetch = async (input, init) => {
      if (String(input).includes('/interactions/') && init?.method === 'POST') return new Response(JSON.stringify({ error: 'already resolved' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
      return real(input, init);
    };
    await user.click(answerButton());
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'This request was already answered elsewhere.');
    expect(mock.received.map((r) => r.route)).toEqual(['prompt']);
    expect(mock.received[0].body.text).toBe('see the CI log');
    expect(composer().value).toBe('');
    expect(await log().findByText('see the CI log')).toBeTruthy();
  });

  test('a refused note answers nothing and keeps the composer as it was', async () => {
    const { user, mock } = await openTask('t16');
    await user.click(card('How should the retry be bounded?').getByRole('radio', { name: 'Retry once' }));
    await user.type(composer(), 'see the CI log');
    const real = window.fetch;
    window.fetch = async (input, init) => {
      if (String(input).endsWith('/prompt') && init?.method === 'POST') return new Response(JSON.stringify({ request_id: 'r', status: 'rejected', error: 'The turn ended.', time: new Date().toISOString() }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return real(input, init);
    };
    await user.click(answerButton());
    expect(await screen.findByText('Last submission rejected: The turn ended.')).toBeTruthy();
    // The send is over once the Answer button is usable again; only then can "nothing answered" be read.
    await waitFor(() => expect(answerButton()).toHaveProperty('disabled', false));
    await new Promise((r) => setTimeout(r, 200));
    expect(mock.received.map((r) => r.route)).toEqual([]);
    expect(composer().value).toBe('see the CI log');
    expect(screen.getByRole('button', { name: 'Remove answer Retry once' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'How should the retry be bounded?' })).toBeTruthy();
  });

  test('a request with several questions keeps its form on the card', async () => {
    await openTask('t2');
    const q = card('Which terminals should the explanation cover?');
    expect(q.getByRole('button', { name: 'Answer' })).toBeTruthy();
    expect(q.getAllByRole('textbox', { name: 'Your answer' })).toHaveLength(2);
    expect(screen.queryByText('Answering')).toBeNull();
    expect(composer().placeholder).toBe('Steer this turn, or queue a follow-up…');
  });
});
