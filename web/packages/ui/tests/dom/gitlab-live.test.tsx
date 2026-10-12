import { act, render, screen } from '@testing-library/react';
import { useRef } from 'react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { api, type GitLabItemSummary } from '../../src/api';
import { GitLabItemView } from '../../src/components/GitLabItems';
import { GitLabItemsContext, useSummaries } from '../../src/components/GitLabStrip';
import { refKey, type GitLabRef } from '../../src/lib/gitlabRefs';

// GitLab items on screen stay live: a running pipeline or job every 20 s, an open merge request
// every minute, nothing once finished; only while on screen and while the tab is visible.

let visibility: DocumentVisibilityState = 'visible';
const observers: { callback: IntersectionObserverCallback; target?: Element }[] = [];

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
  visibility = 'visible';
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
  observers.length = 0;
  vi.stubGlobal('IntersectionObserver', class {
    entry: { callback: IntersectionObserverCallback; target?: Element };
    constructor(callback: IntersectionObserverCallback) { this.entry = { callback }; observers.push(this.entry); }
    observe(target: Element) { this.entry.target = target; }
    disconnect() {}
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const states = new Map<string, Partial<GitLabItemSummary>>();
function serve() {
  return vi.spyOn(api, 'gitlabItems').mockImplementation(async (_id, refs) => ({ items: refs.map((ref) => ({ ref, kind: ref.split(':')[0], ...states.get(ref) })) }));
}

function Probe({ sessionId, refs, box = false }: Readonly<{ sessionId: string; refs: GitLabRef[]; box?: boolean }>) {
  const target = useRef<HTMLDivElement>(null);
  const items = useSummaries(sessionId, refs, box ? target : undefined);
  return <div ref={target}>{refs.map((r) => { const item = items.get(refKey(r)); return <span key={refKey(r)}>{`${refKey(r)}=${item?.state ?? ''}${item?.stale ? ' stale' : ''}`}</span>; })}</div>;
}

const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
const asked = (read: ReturnType<typeof serve>) => read.mock.calls.map(([, refs]) => refs.join(' '));
const intersect = (on: boolean) => act(() => { for (const o of observers) o.callback([{ target: o.target!, isIntersecting: on } as IntersectionObserverEntry], {} as IntersectionObserver); });

test('running items are read every 20 s and open merge requests every minute, together; finished items stop', async () => {
  states.set('pipeline:1', { state: 'running' }).set('mr:2', { state: 'opened', draft: true }).set('pipeline:3', { state: 'success' });
  const read = serve();
  render(<Probe sessionId="cadence" refs={[{ kind: 'pipeline', id: 1 }, { kind: 'mr', id: 2 }, { kind: 'pipeline', id: 3 }]} />);
  await advance(0);
  expect(asked(read)).toEqual(['pipeline:1 mr:2 pipeline:3']);
  await advance(20_000);
  await advance(20_000);
  await advance(20_000);
  expect(asked(read)).toEqual(['pipeline:1 mr:2 pipeline:3', 'pipeline:1', 'pipeline:1', 'pipeline:1 mr:2']);
  states.set('pipeline:1', { state: 'failed' });
  await advance(20_000);
  expect(screen.getByText('pipeline:1=failed')).toBeTruthy();
  read.mockClear();
  await advance(180_000);
  expect(asked(read)).toEqual(['mr:2', 'mr:2', 'mr:2']);
});

test('a hidden tab reads nothing; coming back reads what is older than 15 s at once', async () => {
  states.set('job:7', { state: 'running' });
  const read = serve();
  render(<Probe sessionId="hidden" refs={[{ kind: 'job', id: 7 }]} />);
  await advance(0);
  visibility = 'hidden';
  document.dispatchEvent(new Event('visibilitychange'));
  read.mockClear();
  await advance(120_000);
  expect(read).not.toHaveBeenCalled();
  visibility = 'visible';
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
  await advance(0);
  expect(asked(read)).toEqual(['job:7']);
});

test('a strip off screen reads nothing until it scrolls into view', async () => {
  states.set('pipeline:4', { state: 'pending' });
  const read = serve();
  render(<Probe sessionId="offscreen" box refs={[{ kind: 'pipeline', id: 4 }]} />);
  await advance(30_000);
  expect(read).not.toHaveBeenCalled();
  intersect(true);
  await advance(0);
  expect(asked(read)).toEqual(['pipeline:4']);
  intersect(false);
  await advance(120_000);
  expect(asked(read)).toEqual(['pipeline:4']);
  intersect(true);
  await advance(0);
  expect(asked(read)).toEqual(['pipeline:4', 'pipeline:4']);
});

test('a failed or rate limited read keeps the last state, marked stale', async () => {
  states.set('pipeline:5', { state: 'running' });
  const read = serve();
  render(<Probe sessionId="stale" refs={[{ kind: 'pipeline', id: 5 }]} />);
  await advance(0);
  expect(screen.getByText('pipeline:5=running')).toBeTruthy();
  read.mockRejectedValueOnce(new Error('offline'));
  await advance(20_000);
  expect(screen.getByText('pipeline:5=running stale')).toBeTruthy();
  read.mockResolvedValueOnce({ items: [{ ref: 'pipeline:5', kind: '', error: 'GitLab is rate limiting requests. Try again later.', stale: true }] });
  await advance(20_000);
  expect(screen.getByText('pipeline:5=running stale')).toBeTruthy();
  await advance(20_000);
  expect(screen.getByText('pipeline:5=running')).toBeTruthy();
});

test('the open panel reads its running pipeline again on the same cadence', async () => {
  states.set('pipeline:91', { state: 'running', pipeline_id: 91 });
  serve();
  const detail = vi.spyOn(api, 'gitlabPipelineDetail').mockResolvedValue({ pipeline: { id: 91, status: 'running', sha: 'a'.repeat(40), ref: 'demo', web_url: '' }, jobs: [] });
  const state = { supported: true, auto_investigate: false, can_investigate: false, origin: { host: 'gitlab.example.com', path: 'group/repo' } };
  const current: GitLabRef = { kind: 'pipeline', id: 91 };
  render(<GitLabItemsContext.Provider value={{ sessionId: 'panel', origin: state.origin, state, current, open: vi.fn() }}><GitLabItemView refs={[current]} current={current} onOpen={vi.fn()} /></GitLabItemsContext.Provider>);
  await advance(0);
  expect(detail).toHaveBeenCalledTimes(1);
  await advance(20_000);
  expect(detail).toHaveBeenCalledTimes(2);
  states.set('pipeline:91', { state: 'success', pipeline_id: 91 });
  await advance(20_000);
  await advance(60_000);
  expect(detail).toHaveBeenCalledTimes(3);
});
