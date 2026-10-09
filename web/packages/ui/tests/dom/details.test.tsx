import { act, render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { ApiClient, Item, SessionDetail } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { DetailsProvider, useItemBody } from '../../src/components/Details';

/** A detail stream the test feeds frames into. */
class FakeEventSource {
  static CLOSED = 2;
  static opened: FakeEventSource[] = [];
  readyState = 1;
  onerror: (() => void) | null = null;
  listeners = new Map<string, (event: { data: string }) => void>();
  constructor(readonly url: string) { FakeEventSource.opened.push(this); }
  addEventListener(name: string, listener: (event: { data: string }) => void) { this.listeners.set(name, listener); }
  close() { this.readyState = FakeEventSource.CLOSED; }
  emit(name: string, data: object) { act(() => this.listeners.get(name)?.({ data: JSON.stringify(data) })); }
}

const session = { id: 's1', epoch: 'e1', representation: 'compact-v1', detail_stream: true, items: [], interactions: [], subagents: [], history_truncated: false, last_submission: null } as unknown as SessionDetail;
const row: Item = { id: 'tool', kind: 'tool', time: '2026-10-08T12:00:00Z', compact: { has_text: false }, tool: { name: 'bash', status: 'running', has_input: true, has_output: true } };
const fakeApi = { cacheKey: (id: string) => id, detailEventsUrl: () => '/api/events/detail', auth: async () => ({ authenticated: true }) } as unknown as ApiClient;
const versions = {};

function Body() {
  const { body } = useItemBody(row, true);
  return <p data-testid="body">{`${body?.status}:${body?.item?.tool?.output ?? ''}`}</p>;
}

function Harness() {
  return (
    <ApiContext.Provider value={fakeApi}>
      <DetailsProvider session={session} active generation={0} versions={versions} onAuthLost={() => {}}>
        <Body />
      </DetailsProvider>
    </ApiContext.Provider>
  );
}

// The App re-renders the provider on every frame of the main stream. A running command's
// expanded body must stay loaded through that, not fall back to "Details load as you scroll here…".
test('a registered running body stays loaded across a provider re-render after a body_output frame', async () => {
  const saved = window.EventSource;
  window.EventSource = FakeEventSource as unknown as typeof EventSource;
  FakeEventSource.opened = [];
  try {
    const view = render(<Harness />);
    await waitFor(() => expect(FakeEventSource.opened.length).toBe(1));
    const stream = FakeEventSource.opened[0];
    const frame = { epoch: 'e1', session_id: 's1', agent_id: '' };
    stream.emit('body', { ...frame, seq: 5, item: { ...row, compact: undefined, tool: { name: 'bash', status: 'running', input: 'seq 3', output: '1\n' } } });
    stream.emit('body_output', { ...frame, seq: 6, item_id: 'tool', kind: 'tool', text: '2\n' });
    expect(screen.getByTestId('body').textContent).toBe('loaded:1\n2\n');
    view.rerender(<Harness />);
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); });
    stream.emit('body_output', { ...frame, seq: 7, item_id: 'tool', kind: 'tool', text: '3\n' });
    expect(screen.getByTestId('body').textContent).toBe('loaded:1\n2\n3\n');
    expect(FakeEventSource.opened.length).toBe(1);
  } finally {
    window.EventSource = saved;
  }
});
