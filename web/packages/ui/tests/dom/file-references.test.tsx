import { useRef } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Item } from '../../src/api';
import { FileReferencesProvider } from '../../src/components/FileReferences';
import { PlainText, SessionContext, WorkdirContext } from '../../src/components/common';
import { FilePreview, useFilePreview } from '../../src/components/FilePreview';
import { Transcript } from '../../src/components/Transcript';
import { PreviewContext } from '../../src/lib/previewContext';

const path = 'CODE-REVIEW-2026-10-05.md';
const items: Item[] = [
  { id: 'call', kind: 'tool', time: '2026-10-05T10:00:00Z', tool: { name: 'uam_show_file', status: 'completed', file_paths: [`/repo/${path}`], declaration: { artifact_id: 'report', path: `/repo/${path}`, title: 'Code review' } } },
  { id: 'reply', kind: 'assistant', time: '2026-10-05T10:00:01Z', text: `Written to \`${path}\`.` },
];
const text = '# Code review\n\nThe report contents are available.';
const openPanel = () => {};

function Conversation({ entries = items }: { entries?: Item[] }) {
  const fallback = useRef<HTMLDivElement>(null);
  const preview = useFilePreview('task', 'history', true, openPanel, fallback);
  return <FileReferencesProvider sessionId="task" workdir="/repo" generation="history" active items={entries}>
    <PreviewContext.Provider value={preview.open}>
      <Transcript sessionId="task" workdir="/repo" items={entries} interactions={[]} subagents={[]} live={false} working={false} />
      {preview.selection && <FilePreview selection={preview.selection} sessionId="task" workdir="/repo" inline onClose={preview.close} onReload={preview.reload} />}
    </PreviewContext.Provider>
  </FileReferencesProvider>;
}

beforeEach(() => {
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private callback: IntersectionObserverCallback) {}
    observe(target: Element) { this.callback([{ target, isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
    unobserve() {}
    disconnect() {}
  });
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([new DOMRect(0, 0, 100, 20)] as unknown as DOMRectList);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 100, 20));
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith('/files/resolve')) return Response.json({ files: [{ path, exists: true, kind: 'file' }] });
    if (url.endsWith(`/files/view/${path}`)) return new Response(init?.method === 'HEAD' ? null : text, { headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': String(new TextEncoder().encode(text).length) } });
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

test('keeps the inline file preview without a declaration card', async () => {
  render(<Conversation />);
  const link = await screen.findByRole('link', { name: `Open ${path}` });
  expect(screen.queryByRole('group', { name: /^Declared file/ })).toBeNull();
  expect(screen.getAllByRole('link', { name: `Open ${path}` })).toHaveLength(1);
  fireEvent.click(link);
  expect(await screen.findByText('The report contents are available.')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Close preview' }));
  await waitFor(() => expect(screen.queryByText('The report contents are available.')).toBeNull());
});

test('an inline file link stays a link while it is rechecked after expiry', async () => {
  const resolves = () => vi.mocked(fetch).mock.calls.filter(([url]) => String(url).endsWith('/files/resolve')).length;
  const view = render(<Conversation />);
  await screen.findByRole('link', { name: `Open ${path}` });
  const before = resolves();
  const now = Date.now();
  vi.spyOn(Date, 'now').mockReturnValue(now + 30_001);
  view.rerender(<Conversation entries={[items[0], { ...items[1], text: `${items[1].text} Still here.` }]} />);
  // No drop back to plain code while the answer is rechecked.
  expect(screen.getByRole('link', { name: `Open ${path}` })).toBeTruthy();
  await waitFor(() => expect(resolves()).toBe(before + 1));
  expect(screen.getByRole('link', { name: `Open ${path}` })).toBeTruthy();
});

test('a path with a cited line links to the file and marks the line in the preview', async () => {
  const code = 'src/a.go';
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith('/files/resolve')) return Response.json({ files: [{ path: code, exists: true, kind: 'file' }] });
    return new Response(init?.method === 'HEAD' ? null : 'package a\n\nfunc A() {}\n', { headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': '26' } });
  });
  render(<Conversation entries={[{ ...items[1], text: `See \`${code}:3\`.` }]} />);
  const link = await screen.findByRole('link', { name: `Open ${code} at line 3` });
  expect(link.getAttribute('href')).toMatch(/\/files\/view\/src\/a\.go#L3$/);
  expect(link.textContent).toBe('a.go:3');
  fireEvent.click(link);
  await screen.findByRole('button', { name: 'Close preview' });
  await waitFor(() => expect(document.querySelector('li.font-medium')?.textContent).toBe('3'));
});

test('a bare name opens the one file the server says it means', async () => {
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith('/files/resolve')) return Response.json({ files: [{ path: 'gen.py', exists: false, kind: 'file', target: 'tools/gen.py' }, { path: 'dup.txt', exists: false, kind: 'unavailable' }] });
    return new Response(init?.method === 'HEAD' ? null : 'print(1)\n', { headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': '9' } });
  });
  render(<Conversation entries={[{ ...items[1], text: 'Ran `gen.py:1` and `dup.txt`.' }]} />);
  const link = await screen.findByRole('link', { name: 'Open tools/gen.py at line 1' });
  expect(link.getAttribute('href')).toMatch(/\/files\/view\/tools\/gen\.py#L1$/);
  expect(link.textContent).toBe('gen.py:1');
  expect(screen.getByText('dup.txt').tagName).toBe('CODE');
});

test('a link keeps its own words on the chip', async () => {
  render(<Conversation entries={[items[0], { ...items[1], text: `See [the review](${path}).` }]} />);
  const link = await screen.findByRole('link', { name: `Open ${path}` });
  expect(link.textContent).toBe('the review');
});

test('an open preview reloads when a tool in the Thread changes its file', async () => {
  let body = 'first version';
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith('/files/resolve')) return Response.json({ files: [{ path, exists: true, kind: 'file' }] });
    return new Response(init?.method === 'HEAD' ? null : body, { headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Content-Length': String(body.length) } });
  });
  const view = render(<Conversation />);
  fireEvent.click(await screen.findByRole('link', { name: `Open ${path}` }));
  expect(await screen.findByText('first version')).toBeTruthy();
  body = 'second version';
  const edit: Item = { id: 'edit', kind: 'tool', time: '2026-10-05T10:00:02Z', tool: { name: 'edit', status: 'completed', file_paths: [`/repo/${path}`] } };
  view.rerender(<Conversation entries={[...items, edit]} />);
  expect(await screen.findByText('second version')).toBeTruthy();
  body = 'third version';
  fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
  expect(await screen.findByText('third version')).toBeTruthy();
});

test('a PDF opens in place', async () => {
  const pdf = 'out/report.pdf';
  vi.mocked(fetch).mockImplementation(async (input) => {
    const url = String(input);
    if (url.endsWith('/files/resolve')) return Response.json({ files: [{ path: pdf, exists: true, kind: 'file' }] });
    return new Response(null, { headers: { 'Content-Type': 'application/pdf', 'Content-Length': '10' } });
  });
  render(<Conversation entries={[{ ...items[1], text: `Report: \`${pdf}\`` }]} />);
  fireEvent.click(await screen.findByRole('link', { name: `Open ${pdf}` }));
  expect((await screen.findByTitle('Preview report.pdf')).tagName).toBe('IFRAME');
});

test('a whole message too large for Markdown still links its files', async () => {
  render(<FileReferencesProvider sessionId="task" workdir="/repo" generation="history" active items={items}>
    <SessionContext.Provider value="task"><WorkdirContext.Provider value="/repo"><PlainText text={`Long text naming \`${path}\` and [it again](${path}).`} /></WorkdirContext.Provider></SessionContext.Provider>
  </FileReferencesProvider>);
  await waitFor(() => expect(screen.getAllByRole('link', { name: `Open ${path}` })).toHaveLength(2));
  expect(screen.getByText(/Long text naming/)).toBeTruthy();
});
