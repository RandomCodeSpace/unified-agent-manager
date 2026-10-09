import { act, screen, waitFor, within } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { Answer, Interaction, SnapshotData } from '../../src/api';
import { composer, renderApp } from './render';

const warning = 'Switch to Auto for this request? Copilot will choose a model; usage and cost may differ.';

async function fallback() {
  const rendered = renderApp('#task=t3');
  const Source = window.EventSource;
  let stream: EventSource | undefined;
  let snapshot: SnapshotData | undefined;
  window.EventSource = class extends Source {
    constructor(url: string | URL, options?: EventSourceInit) {
      super(url, options);
      if (String(url).startsWith('/api/events?')) this.addEventListener('snapshot', (event) => {
        stream = event.target as EventSource;
        snapshot = JSON.parse((event as MessageEvent).data) as SnapshotData;
      });
    }
  };
  await waitFor(() => expect(snapshot?.session?.id).toBe('t3'));
  const original = snapshot!;
  const replies: Answer[] = [];
  let current: Interaction;
  const fetch = window.fetch;
  window.fetch = async (input, init) => {
    if (String(input) === `/api/sessions/t3/interactions/${current?.id}` && init?.method === 'POST') {
      const answer = JSON.parse(String(init.body)) as Answer;
      replies.push(answer);
      return Response.json({ ...current, state: answer.reject ? 'rejected' : 'answered', resolution: answer.answers?.[0]?.[0] ?? 'Declined' });
    }
    return fetch(input, init);
  };
  let seq = 1_000_000;
  function request(id: string) {
    current = { id, kind: 'question', title: 'Auto model fallback', state: 'pending', time: new Date().toISOString(), questions: [{ text: warning, choices: ['No', 'Yes'], custom: false }] };
    act(() => stream!.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify({
      ...original, seq: ++seq,
      sessions: original.sessions.map((session) => session.id === 't3' ? { ...session, state: 'awaiting_answer', pending: 1, mode: 'yolo' } : session),
      session: { ...original.session!, state: 'awaiting_answer', mode: 'yolo', interactions: [current] },
    }) })));
  }
  request('auto-first');
  await screen.findByRole('radio', { name: 'No' });
  return { ...rendered, replies, request };
}

test('Auto fallback defaults to No in Yolo, and a second request needs a new explicit Yes choice', async () => {
  const { user, replies, request } = await fallback();
  const box = () => within(composer().form!);
  expect(box().getByText(warning)).toBeTruthy();
  expect(box().getAllByRole('radio')).toHaveLength(2);
  expect(box().getByRole('radio', { name: 'No' })).toHaveProperty('checked', true);
  expect(box().getByRole('radio', { name: 'Yes' })).toHaveProperty('checked', false);
  expect(replies).toEqual([]);
  await user.click(box().getByRole('radio', { name: 'Yes' }));
  expect(replies).toEqual([]);
  await user.click(box().getByRole('button', { name: 'Answer' }));
  await waitFor(() => expect(replies).toEqual([{ answers: [['Yes']] }]));
  request('auto-second');
  await waitFor(() => expect(box().getByRole('radio', { name: 'No' })).toHaveProperty('checked', true));
  expect(box().getByRole('radio', { name: 'Yes' })).toHaveProperty('checked', false);
  await user.keyboard('{Enter}');
  await waitFor(() => expect(replies).toEqual([{ answers: [['Yes']] }, { answers: [['No']] }]));
});

test('Decline sends rejection for Auto fallback without affirmative or sticky consent', async () => {
  const { user, replies } = await fallback();
  await user.click(screen.getByRole('button', { name: 'Decline' }));
  await waitFor(() => expect(replies).toEqual([{ reject: true }]));
});
