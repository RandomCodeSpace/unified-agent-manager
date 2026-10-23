import { act, render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { ApiClient, Item, SessionDetail } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { DetailsProvider, useItemBody } from '../../src/components/Details';
import { Thinking, ToolRow } from '../../src/components/Transcript';

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

test('tool progress remains visible with an unloaded body and requests no body', async () => {
  const saved = window.EventSource;
  window.EventSource = FakeEventSource as unknown as typeof EventSource;
  FakeEventSource.opened = [];
  try {
    render(
      <ApiContext.Provider value={fakeApi}>
        <DetailsProvider session={session} active generation={0} versions={versions} onAuthLost={() => {}}>
          <ToolRow item={{ ...row, tool: { ...row.tool!, progress: 'Waiting for file' } }} live />
        </DetailsProvider>
      </ApiContext.Provider>,
    );
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); });
    expect(screen.getByRole('status').textContent).toBe('Waiting for file');
    expect(FakeEventSource.opened).toHaveLength(0);
  } finally {
    window.EventSource = saved;
  }
});

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

// A closing row fades its body in place (Collapse): the body must not turn into a loading notice before the box steps away.
test('a closing compact tool row keeps its body through the fade, then lets it go', async () => {
  const saved = window.EventSource;
  window.EventSource = FakeEventSource as unknown as typeof EventSource;
  FakeEventSource.opened = [];
  sessionStorage.clear();
  try {
    const done: Item = { ...row, tool: { ...row.tool!, status: 'completed' } };
    const { container } = render(
      <ApiContext.Provider value={fakeApi}>
        <DetailsProvider session={session} active generation={0} versions={versions} onAuthLost={() => {}}>
          <ToolRow item={done} live={false} />
        </DetailsProvider>
      </ApiContext.Provider>,
    );
    const toggle = container.querySelector<HTMLButtonElement>('button[aria-expanded]')!;
    act(() => toggle.click());
    await waitFor(() => expect(FakeEventSource.opened.length).toBe(1));
    FakeEventSource.opened[0].emit('body', { epoch: 'e1', session_id: 's1', agent_id: '', seq: 5, item: { ...done, compact: undefined, tool: { name: 'bash', status: 'completed', input: 'echo kept', output: 'kept-output' } } });
    expect(container.textContent).toContain('kept-output');
    act(() => toggle.click());
    expect(container.textContent).toContain('kept-output');
    expect(container.textContent).not.toContain('Details load as you scroll here…');
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 300)); });
    expect(container.textContent).not.toContain('kept-output');
  } finally {
    window.EventSource = saved;
  }
});

// A closed thought reads nothing, so it says nothing about loading: the notice belongs to a body
// on its way, as for a subagent's Result.
test('a closed compact thought shows no loading notice and opens no detail stream', async () => {
  const saved = window.EventSource;
  window.EventSource = FakeEventSource as unknown as typeof EventSource;
  FakeEventSource.opened = [];
  sessionStorage.clear();
  try {
    const thought: Item = { id: 'think', kind: 'reasoning', time: '2026-10-08T12:00:00Z', compact: { has_text: false, has_reasoning: true } };
    const { container } = render(
      <ApiContext.Provider value={fakeApi}>
        <DetailsProvider session={session} active generation={0} versions={versions} onAuthLost={() => {}}>
          <Thinking item={thought} streaming={false} endedAt="2026-10-08T12:00:02Z" />
        </DetailsProvider>
      </ApiContext.Provider>,
    );
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); });
    expect(screen.getByRole('button', { name: 'Thought for 2s' }).getAttribute('aria-expanded')).toBe('false');
    expect(container.textContent).not.toContain('Details load as you scroll here…');
    expect(FakeEventSource.opened).toHaveLength(0);
  } finally {
    window.EventSource = saved;
  }
});
