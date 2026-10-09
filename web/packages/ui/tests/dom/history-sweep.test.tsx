import { act, waitFor } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import type { SnapshotData } from '../../src/api';
import { renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

/** Capture the current list stream, including the new stream opened when selection changes. */
async function home() {
  const rendered = renderApp();
  const Source = window.EventSource;
  let stream: EventSource | undefined;
  let snapshot: SnapshotData | undefined;
  window.EventSource = class extends Source {
    constructor(url: string | URL, options?: EventSourceInit) {
      super(url, options);
      if (String(url).startsWith('/api/events?')) this.addEventListener('snapshot', (e) => {
        stream = e.target as EventSource;
        snapshot = JSON.parse((e as MessageEvent).data) as SnapshotData;
      });
    }
  };
  await sidebar();
  await waitFor(() => expect(snapshot).toBeDefined());
  await act(async () => {});
  let seq = 1_000_000;
  return {
    ...rendered,
    snapshot: () => snapshot!,
    send: (data: SnapshotData) => act(() => {
      stream!.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify({ ...data, seq: ++seq }) }));
    }),
    remove: (id: string) => act(() => {
      stream!.dispatchEvent(new MessageEvent('session_removed', { data: JSON.stringify({ seq: ++seq, session_id: id }) }));
    }),
    removeProject: (id: string) => act(() => {
      stream!.dispatchEvent(new MessageEvent('project_removed', { data: JSON.stringify({ seq: ++seq, project_id: id }) }));
    }),
    disconnect: () => act(() => { stream!.onerror?.(new Event('error')); }),
  };
}

test('stable membership skips storage enumeration and archive sweeps across snapshots and Task switches', async () => {
  const app = await home();
  const keys = vi.spyOn(Object, 'keys');
  const reads = vi.spyOn(localStorage, 'getItem');
  const initial = app.snapshot();
  for (let i = 0; i < 30; i++) app.send({
    ...initial,
    projects: [...initial.projects].reverse(),
    sessions: [...initial.sessions].reverse().map((s) => ({ ...s, name: `${s.name} ${i}`, updated_at: new Date().toISOString() })),
  });
  const repeated = {
    enumerations: keys.mock.calls.filter(([object]) => object === localStorage).length,
    archiveReads: reads.mock.calls.filter(([key]) => key === 'uam.history-archive').length,
  };

  keys.mockClear();
  reads.mockClear();
  const tasks = await sidebar();
  await app.user.click(tasks.getAllByRole('button', { name: /Doctor: add terminal line/ }).find((button) => button.hasAttribute('data-nav'))!);
  await waitFor(() => expect(app.snapshot().session?.id).toBe('t3'));
  await act(async () => {});
  expect({ repeated, selection: {
    enumerations: keys.mock.calls.filter(([object]) => object === localStorage).length,
    archiveReads: reads.mock.calls.filter(([key]) => key === 'uam.history-archive').length,
  } }).toEqual({ repeated: { enumerations: 0, archiveReads: 0 }, selection: { enumerations: 0, archiveReads: 0 } });
});

test('first snapshot and Task or Project membership changes still prune only stale owned data', async () => {
  localStorage.setItem('uam.draft.gone', 'gone');
  localStorage.setItem('uam.draft.new.gone', 'gone');
  localStorage.setItem('uam.review.gone', 'gone');
  localStorage.setItem('uam.looked', JSON.stringify({ gone: 'old', t3: 'kept' }));
  localStorage.setItem('uam.viewed', JSON.stringify({ gone: 'old', t3: 'kept' }));
  localStorage.setItem('uam.history-archive', JSON.stringify({ gone: 1, t3: 1, '@uam:other:owner:gone': 1 }));
  localStorage.setItem('uam.owner.other.owner.uam.draft.gone', 'other draft');
  localStorage.setItem('uam.review.@uam:other:owner:gone', 'other review');
  const app = await home();
  for (const key of ['uam.draft.gone', 'uam.draft.new.gone', 'uam.review.gone']) expect(localStorage.getItem(key)).toBeNull();
  expect(JSON.parse(localStorage.getItem('uam.looked')!)).toEqual({ t3: 'kept' });
  expect(JSON.parse(localStorage.getItem('uam.viewed')!)).toEqual({ t3: 'kept' });
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ t3: 1, '@uam:other:owner:gone': 1 });
  expect(localStorage.getItem('uam.owner.other.owner.uam.draft.gone')).toBe('other draft');
  expect(localStorage.getItem('uam.review.@uam:other:owner:gone')).toBe('other review');

  const original = app.snapshot();
  const task = original.sessions.find((s) => s.id === 't3')!;
  const project = original.projects[0];
  localStorage.setItem('uam.draft.t3', 'task draft');
  localStorage.setItem('uam.review.t3', 'task review');
  localStorage.setItem(`uam.draft.new.${project.id}`, 'project draft');
  app.send({ ...original, sessions: original.sessions.filter((s) => s.id !== task.id) });
  expect(localStorage.getItem('uam.draft.t3')).toBeNull();
  expect(localStorage.getItem('uam.review.t3')).toBeNull();
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ '@uam:other:owner:gone': 1 });
  expect(localStorage.getItem(`uam.draft.new.${project.id}`)).toBe('project draft');
  app.send({ ...app.snapshot(), projects: original.projects.filter((p) => p.id !== project.id) });
  expect(localStorage.getItem(`uam.draft.new.${project.id}`)).toBeNull();

  localStorage.setItem('uam.draft.t-added', 'new task');
  localStorage.setItem('uam.draft.new.p-added', 'new project');
  localStorage.setItem('uam.draft.stale-after-addition', 'stale');
  app.send({ ...app.snapshot(),
    sessions: [...app.snapshot().sessions, { ...task, id: 't-added', project_id: 'p-added' }],
    projects: [...app.snapshot().projects, { ...project, id: 'p-added' }],
  });
  expect(localStorage.getItem('uam.draft.stale-after-addition')).toBeNull();
  expect(localStorage.getItem('uam.draft.t-added')).toBe('new task');
  expect(localStorage.getItem('uam.draft.new.p-added')).toBe('new project');
});

test('a failed storage sweep retries unchanged membership, and removal events still forget archive marks immediately', async () => {
  const app = await home();
  const snapshot = app.snapshot();
  const next = { ...snapshot, sessions: snapshot.sessions.filter((s) => s.id !== 't3') };
  localStorage.setItem('uam.draft.t3', 'retry draft');
  localStorage.setItem('uam.history-archive', JSON.stringify({ t3: 1, t4: 1 }));
  const original = localStorage.removeItem;
  const remove = vi.spyOn(localStorage, 'removeItem');
  remove.mockImplementation((key) => {
    if (key === 'uam.draft.t3' || key === 'uam.history-archive') throw new DOMException('Storage unavailable');
    return original.call(localStorage, key);
  });
  app.send(next);
  expect(localStorage.getItem('uam.draft.t3')).toBe('retry draft');
  remove.mockRestore();
  app.send(next);
  expect(localStorage.getItem('uam.draft.t3')).toBeNull();
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ t4: 1 });
  app.remove('t4');
  expect(localStorage.getItem('uam.history-archive')).toBeNull();
});

test('unreadable archive marks do not mark a membership sweep complete', async () => {
  const app = await home();
  const snapshot = app.snapshot();
  const next = { ...snapshot, sessions: snapshot.sessions.filter((s) => s.id !== 't3') };
  localStorage.setItem('uam.history-archive', JSON.stringify({ t3: 1, t4: 1 }));
  const original = localStorage.getItem;
  const reads = vi.spyOn(localStorage, 'getItem').mockImplementation((key) => {
    if (key === 'uam.history-archive') throw new DOMException('Storage unavailable');
    return original.call(localStorage, key);
  });
  app.send(next);
  reads.mockRestore();
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ t3: 1, t4: 1 });
  app.send(next);
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ t4: 1 });
});

test('archive mark write failures retry unchanged membership', async () => {
  const app = await home();
  const snapshot = app.snapshot();
  const next = { ...snapshot, sessions: snapshot.sessions.filter((s) => s.id !== 't3') };
  localStorage.setItem('uam.history-archive', JSON.stringify({ t3: 1, t4: 1 }));
  const original = localStorage.setItem;
  const writes = vi.spyOn(localStorage, 'setItem').mockImplementation((key, value) => {
    if (key === 'uam.history-archive') throw new DOMException('Storage unavailable');
    return original.call(localStorage, key, value);
  });
  app.send(next);
  writes.mockRestore();
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ t3: 1, t4: 1 });
  app.send(next);
  expect(JSON.parse(localStorage.getItem('uam.history-archive')!)).toEqual({ t4: 1 });
});

test('removals between equal-membership snapshots still sweep Task and Project drafts', async () => {
  const app = await home();
  const snapshot = app.snapshot();
  // Another tab can create and remove a Task between snapshots seen by this tab.
  localStorage.setItem('uam.draft.between-snapshots', 'removed Task');
  localStorage.setItem('uam.draft.new.between-snapshots', 'removed Project');
  app.remove('between-snapshots');
  app.removeProject('between-snapshots');
  app.send(snapshot);
  expect(localStorage.getItem('uam.draft.between-snapshots')).toBeNull();
  expect(localStorage.getItem('uam.draft.new.between-snapshots')).toBeNull();
});

test('reconnecting sweeps unchanged membership after possible missed removals', async () => {
  const app = await home();
  const snapshot = app.snapshot();
  app.disconnect();
  localStorage.setItem('uam.draft.missed-removal', 'removed while disconnected');
  app.send(snapshot);
  expect(localStorage.getItem('uam.draft.missed-removal')).toBeNull();
});
