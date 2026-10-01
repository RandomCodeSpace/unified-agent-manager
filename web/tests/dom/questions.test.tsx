// A question with one question is the composer's extension: its options are chosen there, the
// composer holds the text and the files, Decline and Answer sit in its action row, and what the
// answer cannot carry is steered first.
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { composer, log, openTask } from './render';

/** The composer's surface, where the question sits (the transcript's question block repeats the text once answered). */
const box = () => within(composer().form!);
const answerButton = () => screen.getByRole('button', { name: /^Answer|^Submitting/ });
const declineButton = () => screen.getByRole('button', { name: 'Decline' });
const textFile = () => new File(['hello from a log\n'], 'build.log', { type: 'text/plain' });

/** Opens another Task by its hash, as the sidebar does; the composer remounts for it. */
async function switchTo(id: string, title?: string) {
  history.pushState(null, '', `/#task=${id}`);
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  if (title) await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(title));
  else await waitFor(() => expect(box().getByText('Needs answer')).toBeTruthy());
}

describe('answering from the composer', () => {
  test('the question sits on the composer: an option is chosen there, another replaces it, choosing it again clears it', async () => {
    const { user } = await openTask('t16');
    expect(screen.queryByRole('group', { name: 'How should the retry be bounded?' })).toBeNull();
    expect(box().getByText('Needs answer')).toBeTruthy();
    expect(box().getByText('Pick a bound for the retry, or describe one')).toBeTruthy();
    expect(composer().placeholder).toBe('Type your answer…');
    expect(answerButton()).toHaveProperty('disabled', true);
    const once = box().getByRole('radio', { name: 'Retry once' });
    const thrice = box().getByRole('radio', { name: 'Retry up to 3 times' });
    await user.click(once);
    expect(once).toHaveProperty('checked', true);
    expect(composer().placeholder).toBe('Add a note (sent with your answer)…');
    expect(answerButton()).toHaveProperty('disabled', false);
    await user.click(thrice);
    expect(once).toHaveProperty('checked', false);
    expect(thrice).toHaveProperty('checked', true);
    await user.click(thrice);
    expect(thrice).toHaveProperty('checked', false);
    expect(composer().placeholder).toBe('Type your answer…');
    expect(answerButton()).toHaveProperty('disabled', true);
  });

  test('the action row reads Stop, Decline, Answer', async () => {
    await openTask('t16');
    const order = screen.getAllByRole('button').map((b) => b.getAttribute('aria-label') ?? b.textContent?.trim() ?? '').filter((name) => ['Stop turn', 'Decline', 'Answer'].includes(name));
    expect(order).toEqual(['Stop turn', 'Decline', 'Answer']);
  });

  test('typed text answers a free-text question on Enter; nothing is steered', async () => {
    const { user, mock } = await openTask('t18');
    expect(composer().placeholder).toBe('Type your answer…');
    await user.type(composer(), 'Copilot web goes stable');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByText('Name the headline change for v0.12 in a sentence')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['Copilot web goes stable']]);
    await waitFor(() => expect(composer().value).toBe(''));
    expect(await log().findByText('Copilot web goes stable')).toBeTruthy();
    expect(box().queryByText('Needs answer')).toBeNull();
  });

  test('an option with a note and a file steers the note and the file first, then answers', async () => {
    const { user, mock } = await openTask('t16');
    await user.upload(document.querySelector<HTMLInputElement>('form input[type="file"]')!, textFile());
    expect(await screen.findByRole('button', { name: 'Remove build.log' })).toBeTruthy();
    await user.click(box().getByRole('radio', { name: 'Retry once' }));
    await user.type(composer(), 'The race is in the focus replay');
    await waitFor(() => expect(answerButton()).toHaveProperty('disabled', false), { timeout: 3000 });
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByText('Pick a bound for the retry, or describe one')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['prompt', 'answer']);
    const [steer, answer] = mock.received;
    expect(steer.body.mode).toBe('steer');
    expect(steer.body.text).toBe('The race is in the focus replay');
    expect(steer.body.attachments).toHaveLength(1);
    expect(answer.body.answers).toEqual([['Retry once']]);
    expect(await log().findByText('The race is in the focus replay')).toBeTruthy();
    await waitFor(() => expect(composer().value).toBe(''));
    expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull();
  });

  test('an options-only question takes typed text as a note, never as the answer', async () => {
    const { user, mock } = await openTask('t17');
    expect(composer().placeholder).toBe('Add a note (sent with your answer)…');
    // The recommended option arrives staged; cleared, nothing is.
    await user.click(box().getByRole('radio', { name: 'pnpm (Recommended)' }));
    await user.type(composer(), 'CI pins it');
    expect(answerButton()).toHaveProperty('disabled', true);
    await user.click(box().getByRole('radio', { name: 'npm' }));
    expect(answerButton()).toHaveProperty('disabled', false);
    // Choosing leaves the focus on the option (arrow keys move between radios); Enter sends from the text.
    await user.click(composer());
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['prompt', 'answer']);
    expect(mock.received[0].body.text).toBe('CI pins it');
    expect(mock.received[1].body.answers).toEqual([['npm']]);
  });

  test('several options may be chosen where the question allows it', async () => {
    const { user, mock } = await openTask('t19');
    await user.click(box().getByRole('checkbox', { name: 'linux/arm64' }));
    await user.click(box().getByRole('checkbox', { name: 'darwin/arm64' }));
    await user.click(box().getByRole('checkbox', { name: 'linux/amd64' }));
    await user.click(box().getByRole('checkbox', { name: 'darwin/arm64' }));
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByRole('checkbox')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['linux/arm64', 'linux/amd64']]);
  });

  test('Decline in the action row ends answer mode', async () => {
    const { user, mock } = await openTask('t17');
    await user.click(declineButton());
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.reject).toBe(true);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Decline' })).toBeNull());
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
    await waitFor(() => expect(box().queryByText('Pick a bound for the retry, or describe one')).toBeNull());
    await waitFor(() => expect(composer().value).toBe('half a thought'));
    expect(box().queryByText('Needs answer')).toBeNull();
  });

  test('answered elsewhere after the note went: the note stands, the composer says so and clears', async () => {
    const { user, mock } = await openTask('t16');
    await user.click(box().getByRole('radio', { name: 'Retry once' }));
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
    await user.click(box().getByRole('radio', { name: 'Retry once' }));
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
    expect(box().getByRole('radio', { name: 'Retry once' })).toHaveProperty('checked', true);
    expect(box().getByText('Pick a bound for the retry, or describe one')).toBeTruthy();
  });

  test('a request with several questions keeps its form on the card', async () => {
    await openTask('t2');
    const card = within(screen.getByRole('group', { name: 'Which terminals should the explanation cover?' }));
    expect(card.getByRole('button', { name: 'Answer' })).toBeTruthy();
    expect(card.getByRole('button', { name: 'Decline' })).toBeTruthy();
    expect(card.getAllByRole('textbox', { name: 'Your answer' })).toHaveLength(2);
    expect(box().queryByText('Needs answer')).toBeNull();
    expect(box().queryByRole('button', { name: 'Decline' })).toBeNull();
    expect(composer().placeholder).toBe('Steer this turn, or queue a follow-up…');
  });
});

// An option whose label ends with "(Recommended)" is staged when its question arrives, once; the owner still sends.
describe('a recommended option', () => {
  const recommended = () => box().getByRole('radio', { name: 'pnpm (Recommended)' });

  test('arrives staged, sends nothing by itself, and Answer sends its label as offered', async () => {
    const { user, mock } = await openTask('t17');
    expect(recommended()).toHaveProperty('checked', true);
    expect(answerButton()).toHaveProperty('disabled', false);
    expect(mock.received).toEqual([]);
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['pnpm (Recommended)']]);
  });

  test('another option replaces it', async () => {
    const { user, mock } = await openTask('t17');
    await user.click(box().getByRole('radio', { name: 'yarn' }));
    expect(recommended()).toHaveProperty('checked', false);
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received[0].body.answers).toEqual([['yarn']]);
  });

  test('cleared, it is not staged again', async () => {
    const { user } = await openTask('t17');
    await user.click(recommended());
    expect(recommended()).toHaveProperty('checked', false);
    await user.type(composer(), 'still thinking');
    await new Promise((r) => setTimeout(r, 400));
    expect(recommended()).toHaveProperty('checked', false);
    expect(answerButton()).toHaveProperty('disabled', true);
  });

  test('cleared, it is not staged again after a task switch', async () => {
    const { user } = await openTask('t17');
    await user.click(recommended());
    expect(recommended()).toHaveProperty('checked', false);
    await switchTo('t4', 'Bump GitHub Actions pins');
    await switchTo('t17');
    expect(box().getAllByRole('radio').some((r) => (r as HTMLInputElement).checked)).toBe(false);
    expect(answerButton()).toHaveProperty('disabled', true);
  });

  test('another option picked stays staged after a task switch', async () => {
    const { user, mock } = await openTask('t17');
    await user.click(box().getByRole('radio', { name: 'yarn' }));
    await switchTo('t4', 'Bump GitHub Actions pins');
    await switchTo('t17');
    expect(box().getByRole('radio', { name: 'yarn' })).toHaveProperty('checked', true);
    expect(recommended()).toHaveProperty('checked', false);
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received[0].body.answers).toEqual([['yarn']]);
  });

  test('without one, nothing is staged', async () => {
    await openTask('t16');
    expect(box().getAllByRole('radio').map((r) => (r as HTMLInputElement).checked)).toEqual([false, false, false]);
    expect(answerButton()).toHaveProperty('disabled', true);
  });

  test('the task draft stays parked and a typed note goes beside it', async () => {
    localStorage.setItem('uam.draft.t17', JSON.stringify({ text: 'half a thought', files: [], attachments: [] }));
    const { user, mock } = await openTask('t17');
    expect(composer().value).toBe('');
    expect(recommended()).toHaveProperty('checked', true);
    await user.type(composer(), 'CI pins it');
    await new Promise((r) => setTimeout(r, 400));
    expect(composer().value).toBe('CI pins it');
    expect(recommended()).toHaveProperty('checked', true);
    expect(localStorage.getItem('uam.draft.t17')).toContain('half a thought');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['prompt', 'answer']);
    expect(mock.received[0].body.text).toBe('CI pins it');
    expect(mock.received[1].body.answers).toEqual([['pnpm (Recommended)']]);
    await waitFor(() => expect(composer().value).toBe('half a thought'));
  });
});
