// uam's own form (`uam_form`): every field at once on a card, never on the composer; plain text only;
// a required field left empty says so in place; the chosen action and every value go in one answer;
// the settled call reads the same from its tool input and result after a restart.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Answer, ApiClient, Interaction, SessionDetail, ToolCall } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { InteractionCard } from '../../src/components/Interactions';
import { questionOf } from '../../src/lib/transcript';
import * as data from '../../src/mock/data';
import { composer, openTask } from './render';

afterEach(() => vi.restoreAllMocks());

const time = new Date().toISOString();
const source = { mode: 'form' as const, source: 'uam_form' };

const release: Interaction = {
  id: 'uam-form-1',
  kind: 'question',
  title: 'Release settings',
  detail: 'Pick the **target**. See [docs](https://example.com)',
  state: 'pending',
  time,
  elicitation: source,
  options: [
    { id: 'deploy', label: 'Deploy' },
    { id: 'later', label: 'Later' },
  ],
  questions: [
    { text: 'Version', custom: true, field: { name: 'version', type: 'string', required: true, help: 'Like 1.2.3', max_length: 500 } },
    { text: 'Notes', custom: true, field: { name: 'notes', type: 'string', multiline: true, max_length: 4000 } },
    { text: 'Environment', custom: false, choices: ['staging', 'prod'], field: { name: 'env', type: 'string', required: true, default: ['staging'] } },
    { text: 'Checks', custom: false, multiple: true, choices: ['lint', 'test'], field: { name: 'checks', type: 'array' } },
    { text: 'Tag the release', custom: false, choices: ['Yes', 'No'], field: { name: 'tag', type: 'boolean', default: ['No'] } },
  ],
};

const buttons: Interaction = {
  id: 'uam-form-2',
  kind: 'question',
  title: 'Next step?',
  state: 'pending',
  time,
  elicitation: source,
  options: [
    { id: 'merge', label: 'Merge' },
    { id: 'wait', label: 'Wait' },
  ],
  questions: [],
};

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
  return { sent, user: userEvent.setup(), card: within(screen.getByRole('group', { name: interaction.title })) };
}

describe('a uam form', () => {
  test('shows every field in order as plain text, the first focused, with no Decline', () => {
    const { card } = drawCard(release);
    expect(card.getByText('Pick the **target**. See [docs](https://example.com)')).toBeTruthy();
    expect(card.queryByRole('link')).toBeNull();
    const version = card.getByRole('textbox', { name: /^Version/ });
    expect(document.activeElement).toBe(version);
    expect(version.getAttribute('aria-describedby')).toBeTruthy();
    expect(card.getByText('Like 1.2.3')).toBeTruthy();
    expect(card.getByRole('textbox', { name: 'Notes' }).tagName).toBe('TEXTAREA');
    expect(card.getByRole('radio', { name: 'staging' })).toHaveProperty('checked', true);
    expect(card.getByRole('checkbox', { name: 'Tag the release' })).toHaveProperty('checked', false);
    const order = card.getAllByRole('button').map((b) => b.textContent);
    expect(order).toEqual(['Dismiss', 'Deploy', 'Later']);
    expect(card.queryByRole('button', { name: 'Decline' })).toBeNull();
  });

  test('a required field left empty says so in place and takes focus; nothing is sent', async () => {
    const { sent, user, card } = drawCard(release);
    await user.click(card.getByRole('button', { name: 'Deploy' }));
    const version = card.getByRole('textbox', { name: /^Version/ });
    expect(card.getByText('Fill this in to continue.')).toBeTruthy();
    expect(version.getAttribute('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(version);
    expect(sent).toEqual([]);
  });

  test('the chosen action and every value go in one answer', async () => {
    const { sent, user, card } = drawCard(release);
    await user.type(card.getByRole('textbox', { name: /^Version/ }), ' 1.2.3 ');
    await user.click(card.getByRole('radio', { name: 'prod' }));
    await user.click(card.getByRole('checkbox', { name: 'test' }));
    await user.click(card.getByRole('checkbox', { name: 'Tag the release' }));
    await user.click(card.getByRole('button', { name: 'Later' }));
    await waitFor(() => expect(sent).toEqual([{ decision: 'later', answers: [['1.2.3'], [], ['prod'], ['test'], ['Yes']] }]));
  });

  test('Enter in a line chooses the first action, and Dismiss cancels', async () => {
    const first = drawCard(release);
    await first.user.type(first.card.getByRole('textbox', { name: /^Version/ }), '2.0{Enter}');
    await waitFor(() => expect(first.sent).toEqual([{ decision: 'deploy', answers: [['2.0'], [], ['staging'], [], ['No']] }]));
  });

  test('Dismiss sends a cancel', async () => {
    const { sent, user, card } = drawCard(release);
    await user.click(card.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(sent).toEqual([{ cancel: true }]));
  });

  test('a button card sends the button it was given', async () => {
    const { sent, user, card } = drawCard(buttons);
    await user.click(card.getByRole('button', { name: 'Wait' }));
    await waitFor(() => expect(sent).toEqual([{ decision: 'wait', answers: [] }]));
  });

  test('a one-field form stays a card: the composer is not in answer mode', async () => {
    const state = data.seed();
    state.tasks.find((t) => t.id === 't18')!.interactions = [{ ...release, questions: release.questions!.slice(0, 1) }];
    vi.spyOn(data, 'seed').mockReturnValue(state);
    await openTask('t18');
    expect(screen.getByRole('group', { name: 'Release settings' })).toBeTruthy();
    expect(composer().placeholder).not.toMatch(/answer/i);
  });

  test('a settled call reads from its input and result alone, as after a restart', () => {
    const input = { title: 'Release settings', fields: [{ kind: 'TextField', name: 'version', label: 'Version' }, { kind: 'CheckBox', name: 'tag', label: 'Tag' }], actions: [{ name: 'deploy', label: 'Deploy' }] };
    const tool = (output: string, status: ToolCall['status'] = 'completed') => ({ name: 'uam_form', status, input: JSON.stringify(input), output }) as ToolCall;
    expect(questionOf(tool('{"status":"submitted","action":"deploy","values":{"tag":true,"version":"1.2.3"}}'), undefined, false)).toMatchObject({
      outcome: 'answered',
      form: true,
      answer: 'Deploy · Version: 1.2.3 · Tag: yes',
      questions: [{ text: 'Release settings' }],
    });
    expect(questionOf(tool('{"status":"dismissed"}'), undefined, false)?.outcome).toBe('cancelled');
    expect(questionOf(tool('kind "Image" is not one of …', 'failed'), undefined, false)).toBeNull();
  });
});
