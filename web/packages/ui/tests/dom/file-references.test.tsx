import { useRef } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Item } from '../../src/api';
import { FileReferencesProvider } from '../../src/components/FileReferences';
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

function Conversation({ density = 'compact', entries = items }: { density?: 'compact' | 'detailed'; entries?: Item[] }) {
  const fallback = useRef<HTMLDivElement>(null);
  const preview = useFilePreview('task', 'history', true, openPanel, fallback);
  return <FileReferencesProvider sessionId="task" workdir="/repo" generation="history" active items={entries}>
    <PreviewContext.Provider value={preview.open}>
      <Transcript sessionId="task" workdir="/repo" items={entries} interactions={[]} subagents={[]} live={false} working={false} density={density} />
      {preview.selection && <FilePreview selection={preview.selection} sessionId="task" workdir="/repo" inline onClose={preview.close} />}
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

test.each(['compact', 'detailed'] as const)('%s keeps the inline file preview without a declaration card', async density => {
  render(<Conversation density={density} />);
  const link = await screen.findByRole('link', { name: `Open ${path}` });
  expect(screen.queryByRole('group', { name: /^Declared file/ })).toBeNull();
  expect(screen.getAllByRole('link', { name: `Open ${path}` })).toHaveLength(1);
  fireEvent.click(link);
  expect(await screen.findByText('The report contents are available.')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Close preview' }));
  await waitFor(() => expect(screen.queryByText('The report contents are available.')).toBeNull());
});

test('an inline file link recovers after expiry during an unrelated reply update', async () => {
  const view = render(<Conversation />);
  await screen.findByRole('link', { name: `Open ${path}` });
  const now = Date.now();
  vi.spyOn(Date, 'now').mockReturnValue(now + 30_001);
  view.rerender(<Conversation entries={[items[0], { ...items[1], text: `${items[1].text} Still here.` }]} />);
  await waitFor(() => expect(vi.mocked(fetch).mock.calls.filter(([url]) => String(url).endsWith('/files/resolve'))).toHaveLength(2));
  expect(await screen.findByRole('link', { name: `Open ${path}` })).toBeTruthy();
});
