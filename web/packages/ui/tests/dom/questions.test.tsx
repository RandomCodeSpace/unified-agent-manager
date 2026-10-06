// A question with one question is the composer's extension: its options are chosen there, the
// composer holds the typed answer, Decline and Answer sit in its action row, and the answer is
// exactly one of the chosen options or the typed text, with nothing sent beside it.
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { composer, log, openTask } from './render';

/** The composer's surface, where the question sits (the transcript's question block repeats the text once answered). */
const box = () => within(composer().form!);
// Within the composer: the sidebar's Needs you row has its own Answer.
const answerButton = () => box().getByRole('button', { name: /^Answer|^Submitting/ });
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
  test('the question sits on the composer: an option is chosen there, another replaces it, choosing it again answers with it', async () => {
    const { user, mock } = await openTask('t16');
    expect(screen.queryByRole('group', { name: 'How should the retry be bounded?' })).toBeNull();
    expect(box().getByText('Needs answer')).toBeTruthy();
    expect(box().getByText('Pick a bound for the retry, or describe one')).toBeTruthy();
    // The first option arrives staged: Answer is live and the placeholder offers the alternative.
    expect(composer().placeholder).toBe('Or type your own answer…');
    expect(answerButton().getAttribute('aria-disabled')).toBeNull();
    const once = box().getByRole('radio', { name: 'Retry once' });
    const thrice = box().getByRole('radio', { name: 'Retry up to 3 times' });
    expect(once).toHaveProperty('checked', true);
    await user.click(thrice);
    expect(once).toHaveProperty('checked', false);
    expect(thrice).toHaveProperty('checked', true);
    await user.click(once);
    expect(once).toHaveProperty('checked', true);
    expect(thrice).toHaveProperty('checked', false);
    await user.click(thrice);
    expect(thrice).toHaveProperty('checked', true);
    expect(box().getByText('Click again to answer')).toBeTruthy();
    expect(mock.received).toEqual([]);
    await user.click(thrice);
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['Retry up to 3 times']]);
  });

  test('a question that takes several says so, and its checkboxes toggle without sending', async () => {
    const { user, mock } = await openTask('t19');
    expect(box().getByText('Choose any that apply')).toBeTruthy();
    expect(box().queryByText('Click again to answer')).toBeNull();
    const arm = box().getByRole('checkbox', { name: 'linux/arm64' });
    await user.click(arm);
    await user.click(arm);
    expect(arm).toHaveProperty('checked', false);
    expect(mock.received).toEqual([]);
  });

  test('the action row reads Stop, Decline, Answer', async () => {
    await openTask('t16');
    const order = within(screen.getByRole('main')).getAllByRole('button').map((b) => b.getAttribute('aria-label') ?? b.textContent?.trim() ?? '').filter((name) => ['Stop turn', 'Decline', 'Answer'].includes(name));
    expect(order).toEqual(['Stop turn', 'Decline', 'Answer']);
    // Glyphs only: the names are for screen readers and the tooltips.
    expect(declineButton().textContent).toBe('');
    expect(answerButton().textContent).toBe('');
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

  test('a question with options takes a typed answer, which clears the staged option, and sends it as the answer', async () => {
    const { user, mock } = await openTask('t16');
    await user.type(composer(), 'Retry twice, then fail loudly');
    expect(box().getAllByRole('radio').some((r) => (r as HTMLInputElement).checked)).toBe(false);
    expect(answerButton().getAttribute('aria-disabled')).toBeNull();
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByText('Pick a bound for the retry, or describe one')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['Retry twice, then fail loudly']]);
  });

  test('typing clears a chosen option and the typed text is the answer', async () => {
    const { user, mock } = await openTask('t16');
    const thrice = box().getByRole('radio', { name: 'Retry up to 3 times' });
    await user.click(thrice);
    expect(thrice).toHaveProperty('checked', true);
    await user.type(composer(), 'Only on CI');
    expect(thrice).toHaveProperty('checked', false);
    expect(composer().placeholder).toBe('Type your answer…');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['Only on CI']]);
  });

  test('choosing an option after typing clears the text and sends only the option', async () => {
    const { user, mock } = await openTask('t16');
    await user.type(composer(), 'Only on CI');
    await user.click(box().getByRole('radio', { name: 'Retry up to 3 times' }));
    expect(composer().value).toBe('');
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['Retry up to 3 times']]);
  });

  test('typing clears every chosen option of a question that takes several', async () => {
    const { user, mock } = await openTask('t19');
    await user.click(box().getByRole('checkbox', { name: 'linux/arm64' }));
    await user.click(box().getByRole('checkbox', { name: 'darwin/arm64' }));
    await user.type(composer(), 'linux only');
    expect(box().getAllByRole('checkbox').some((c) => (c as HTMLInputElement).checked)).toBe(false);
    await user.click(answerButton());
    await waitFor(() => expect(box().queryByRole('checkbox')).toBeNull());
    expect(mock.received[0].body.answers).toEqual([['linux only']]);
  });

  test('files cannot go with an answer: Attach says why and a file is refused', async () => {
    const { user, mock } = await openTask('t16');
    const attach = screen.getByRole('button', { name: /^Attach files/ });
    expect(attach.getAttribute('aria-disabled')).toBe('true');
    expect(attach.getAttribute('aria-label')).toBe('Attach files. Answer the question first.');
    await user.upload(document.querySelector<HTMLInputElement>('form input[type="file"]')!, textFile());
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Answer the question first.');
    expect(screen.queryByRole('button', { name: 'Remove build.log' })).toBeNull();
    expect(mock.received).toEqual([]);
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
    expect(composer().placeholder).toBe('Send now to guide this turn, or after it…');
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

  test('answered elsewhere: the composer says so and keeps the typed answer', async () => {
    const { user, mock } = await openTask('t16');
    await user.type(composer(), 'see the CI log');
    const real = window.fetch;
    window.fetch = async (input, init) => {
      if (String(input).includes('/interactions/') && init?.method === 'POST') return new Response(JSON.stringify({ error: 'already resolved' }), { status: 409, headers: { 'Content-Type': 'application/json' } });
      return real(input, init);
    };
    await user.click(answerButton());
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'This request was already answered elsewhere.');
    expect(mock.received).toEqual([]);
    expect(composer().value).toBe('see the CI log');
  });

  test('a request with several questions keeps its form on the card', async () => {
    await openTask('t2');
    const card = within(screen.getByRole('group', { name: 'Which terminals should the explanation cover?' }));
    expect(card.getByRole('button', { name: 'Answer' })).toBeTruthy();
    expect(card.getByRole('button', { name: 'Decline' })).toBeTruthy();
    expect(card.getAllByRole('textbox', { name: 'Your answer' })).toHaveLength(2);
    expect(box().queryByText('Needs answer')).toBeNull();
    expect(box().queryByRole('button', { name: 'Decline' })).toBeNull();
    expect(composer().placeholder).toBe('Send now to guide this turn, or after it…');
  });
});

// An option whose label ends with "(Recommended)" is staged when its question arrives, once; the owner still sends.
describe('a recommended option', () => {
  const recommended = () => box().getByRole('radio', { name: 'pnpm (Recommended)' });

  test('arrives staged, sends nothing by itself, and Answer sends its label as offered', async () => {
    const { user, mock } = await openTask('t17');
    expect(recommended()).toHaveProperty('checked', true);
    expect(answerButton().getAttribute('aria-disabled')).toBeNull();
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

  test('one click on it sends it as offered', async () => {
    const { user, mock } = await openTask('t17');
    expect(box().getByText('Click again to answer')).toBeTruthy();
    await user.click(recommended());
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['pnpm (Recommended)']]);
  });

  test('cleared by typing, it is not staged again after a task switch', async () => {
    const { user } = await openTask('t17');
    await user.type(composer(), 'x');
    await user.keyboard('{Backspace}');
    expect(recommended()).toHaveProperty('checked', false);
    await switchTo('t4', 'Bump GitHub Actions pins');
    await switchTo('t17');
    expect(box().getAllByRole('radio').some((r) => (r as HTMLInputElement).checked)).toBe(false);
    expect(answerButton().getAttribute('aria-disabled')).toBe('true');
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

  test('without one, the first option is staged; a question that takes several starts empty', async () => {
    await openTask('t16');
    expect(box().getAllByRole('radio').map((r) => (r as HTMLInputElement).checked)).toEqual([true, false, false]);
    expect(answerButton().getAttribute('aria-disabled')).toBeNull();
    await switchTo('t4', 'Bump GitHub Actions pins');
    await switchTo('t19');
    expect(box().getAllByRole('checkbox').some((c) => (c as HTMLInputElement).checked)).toBe(false);
  });

  test('typing replaces it, the typed text alone is sent, and the task draft comes back', async () => {
    localStorage.setItem('uam.draft.t17', JSON.stringify({ text: 'half a thought', files: [], attachments: [] }));
    const { user, mock } = await openTask('t17');
    expect(composer().value).toBe('');
    expect(recommended()).toHaveProperty('checked', true);
    await user.type(composer(), 'bun');
    await new Promise((r) => setTimeout(r, 400));
    expect(composer().value).toBe('bun');
    expect(recommended()).toHaveProperty('checked', false);
    expect(localStorage.getItem('uam.draft.t17')).toContain('half a thought');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body.answers).toEqual([['bun']]);
    await waitFor(() => expect(composer().value).toBe('half a thought'));
  });
});
