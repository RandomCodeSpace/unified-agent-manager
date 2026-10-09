// An MCP elicitation is a question: a one-field form sits on the composer like any one question,
// a larger form and a link are cards. Each may be declined or cancelled; a link is only shown,
// never opened by UAM.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Answer, ApiClient, Interaction, SessionDetail } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { InteractionCard } from '../../src/components/Interactions';
import * as data from '../../src/mock/data';
import { composer, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

const time = new Date().toISOString();

const oneField: Interaction = {
  id: 'f18',
  kind: 'question',
  title: 'Form from release-mcp',
  detail: 'How many release candidates should be cut?',
  state: 'pending',
  time,
  elicitation: { mode: 'form', source: 'release-mcp' },
  questions: [{ text: 'Candidates', custom: true, field: { name: 'count', type: 'integer', minimum: 1, maximum: 5 } }],
};

const form: Interaction = {
  id: 'f1',
  kind: 'question',
  title: 'Form from release-mcp',
  detail: 'Name the release',
  state: 'pending',
  time,
  elicitation: { mode: 'form', source: 'release-mcp' },
  questions: [
    { text: 'Name', custom: true, field: { name: 'name', type: 'string', required: true } },
    { text: 'Notes', custom: true, field: { name: 'notes', type: 'string' } },
  ],
};

const link = (url: string): Interaction => ({
  id: 'l1',
  kind: 'question',
  title: 'Link from release-mcp',
  detail: 'Sign in to the release service',
  state: 'pending',
  time,
  elicitation: { mode: 'url', source: 'release-mcp', url },
});

function drawCard(interaction: Interaction) {
  const sent: Answer[] = [];
  const fakeApi = {
    respond: async (_s: string, _i: string, answer: Answer) => {
      sent.push(answer);
      return { ...interaction, state: 'answered' };
    },
  } as unknown as ApiClient;
  const session = { id: 's1', capabilities: { questions: true, permissions: true } } as unknown as SessionDetail;
  render(
    <ApiContext.Provider value={fakeApi}>
      <InteractionCard session={session} interaction={interaction} onUpdate={() => {}} />
    </ApiContext.Provider>,
  );
  return { sent, user: userEvent.setup() };
}

describe('an elicitation form', () => {
  test('one field sits on the composer with its message and bounds, may be left empty, and may be cancelled', async () => {
    const state = data.seed();
    state.tasks.find((t) => t.id === 't18')!.interactions = [oneField];
    vi.spyOn(data, 'seed').mockReturnValue(state);
    const { user, mock } = await openTask('t18');
    const box = within(composer().form!);
    expect(box.getByText('Form from release-mcp')).toBeTruthy();
    expect(box.getByText('How many release candidates should be cut?')).toBeTruthy();
    expect(box.getByText('Optional · whole number · 1 to 5')).toBeTruthy();
    expect(composer().placeholder).toBe('Type a number, or leave it empty…');
    // Optional: Answer is live with nothing typed.
    expect(box.getByRole('button', { name: /^Answer/ }).getAttribute('aria-disabled')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(mock.received.map((r) => r.route)).toEqual(['answer']));
    expect(mock.received[0].body).toEqual({ cancel: true });
  });

  test('several fields are a card: a required one gates Answer, an optional one goes empty', async () => {
    const { sent, user } = drawCard(form);
    const card = within(screen.getByRole('group', { name: 'Form from release-mcp' }));
    expect(card.getByText('Name the release')).toBeTruthy();
    expect(card.getByText('Required')).toBeTruthy();
    expect(card.getByText('Optional')).toBeTruthy();
    const answer = card.getByRole('button', { name: 'Answer' });
    expect(answer).toHaveProperty('disabled', true);
    await user.type(card.getAllByPlaceholderText('Your answer')[0], 'v1');
    await user.click(answer);
    await waitFor(() => expect(sent).toEqual([{ answers: [['v1'], []] }]));
  });

  test('a form is cancelled apart from declined', async () => {
    const { sent, user } = drawCard(form);
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(sent).toEqual([{ cancel: true }]));
  });
});

describe('an elicitation link', () => {
  test('the link is shown for the user to open; UAM opens nothing, and Done accepts', async () => {
    const opened = vi.spyOn(window, 'open').mockImplementation(() => null);
    const url = 'https://example.com/device?code=ABCD';
    const { sent, user } = drawCard(link(url));
    const anchor = screen.getByRole('link', { name: url });
    expect(anchor.getAttribute('href')).toBe(url);
    expect(anchor.getAttribute('target')).toBe('_blank');
    expect(anchor.getAttribute('rel')).toBe('noopener noreferrer');
    expect(screen.getByText('Sign in to the release service')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(sent).toEqual([{ answers: [] }]));
    expect(opened).not.toHaveBeenCalled();
  });

  test('anything but an https link is never a link', () => {
    drawCard(link('javascript:alert(1)'));
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.getByRole('button', { name: 'Decline' })).toBeTruthy();
  });
});
