import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Artifact } from '../../src/api';
import { ArtifactCard, ArtifactsPanel } from '../../src/components/Artifacts';
import { ArtifactsContext, ArtifactsRevisionContext, PreviewContext, type PreviewTarget } from '../../src/lib/previewContext';

const version = (n: number, call: string) => ({ version: n, at: '2026-11-26T10:00:00Z', entry: 'index.html', files: 3, bytes: 2048, call_id: call });
let artifacts: Artifact[];
const deleted: string[] = [];

beforeEach(() => {
  artifacts = [{ id: '0123456789abcdef', title: 'Demo site', source: 'site', versions: [version(1, 'call-1'), version(2, 'call-2')] }];
  deleted.length = 0;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input);
    if (init?.method === 'DELETE') { deleted.push(url); return new Response(null, { status: 204 }); }
    if (url.endsWith('/api/sessions/task/artifacts')) return Response.json({ artifacts });
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(() => vi.restoreAllMocks());

test('a publish card names its version, opens it and the latest in the preview, and opens the versions list', async () => {
  const opened: PreviewTarget[] = [];
  const list = vi.fn();
  render(<PreviewContext.Provider value={(target) => opened.push(target)}><ArtifactsContext.Provider value={list}>
    <ArtifactCard sessionId="task" callId="call-1" />
  </ArtifactsContext.Provider></PreviewContext.Provider>);
  const card = await screen.findByRole('region', { name: 'Published: Demo site' });
  expect(card.textContent).toContain('Version 1 of 2');
  fireEvent.click(within(card).getByRole('button', { name: 'Open' }));
  fireEvent.click(within(card).getByRole('button', { name: 'Open version 2' }));
  expect(opened.map((t) => t.url)).toEqual(['/api/sessions/task/artifacts/0123456789abcdef/v/1/', '/api/sessions/task/artifacts/0123456789abcdef/v/2/']);
  expect(opened[0].frameable).toBe(true);
  fireEvent.click(within(card).getByRole('button', { name: 'Versions' }));
  expect(list).toHaveBeenCalledOnce();
});

test('a card for the latest version offers no other, and a deleted version says it is gone', async () => {
  const view = render(<ArtifactCard sessionId="task" callId="call-2" />);
  const card = await screen.findByRole('region', { name: 'Published: Demo site' });
  expect(card.textContent).toContain('Version 2');
  expect(card.textContent).not.toContain('of 2');
  expect(within(card).queryByRole('button', { name: /^Open version/ })).toBeNull();
  view.unmount();
  render(<ArtifactCard sessionId="task" callId="call-gone" />);
  expect(await screen.findByText('This published version is no longer kept.')).toBeTruthy();
});

test('the published pages panel lists versions newest first, opens one and deletes one after confirming', async () => {
  const opened: PreviewTarget[] = [];
  render(<ArtifactsPanel sessionId="task" revision={1} inline open onClose={() => {}} onClosed={() => {}} onOpen={(target) => opened.push(target)} />);
  const versions = await screen.findByRole('list', { name: 'Versions of Demo site' });
  expect(within(versions).getAllByRole('listitem').map((li) => li.textContent?.slice(0, 9))).toEqual(['Version 2', 'Version 1']);
  fireEvent.click(screen.getByRole('button', { name: 'Open version 1 of Demo site' }));
  expect(opened[0].url).toBe('/api/sessions/task/artifacts/0123456789abcdef/v/1/');
  expect(screen.getByRole('link', { name: 'Open version 2 of Demo site in a new tab' }).getAttribute('href')).toBe('/api/sessions/task/artifacts/0123456789abcdef/v/2/');

  fireEvent.click(screen.getByRole('button', { name: 'Delete version 1 of Demo site' }));
  const dialog = await screen.findByRole('alertdialog', { name: 'Delete version 1?' });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(deleted).toEqual(['/api/sessions/task/artifacts/0123456789abcdef/versions/1']));
  await waitFor(() => expect(within(versions).getAllByRole('listitem')).toHaveLength(1));
});

test('a card reads the list again when the Thread\'s artifacts change, a version deleted included', async () => {
  const view = render(<ArtifactsRevisionContext.Provider value={1}><ArtifactCard sessionId="task" callId="call-1" /></ArtifactsRevisionContext.Provider>);
  const card = await screen.findByRole('region', { name: 'Published: Demo site' });
  expect(card.textContent).toContain('Version 1 of 2');
  artifacts = [{ ...artifacts[0], versions: [artifacts[0].versions[0]] }];
  view.rerender(<ArtifactsRevisionContext.Provider value={2}><ArtifactCard sessionId="task" callId="call-1" /></ArtifactsRevisionContext.Provider>);
  await waitFor(() => expect(screen.getByRole('region', { name: 'Published: Demo site' }).textContent).not.toContain('of 2'));
});
