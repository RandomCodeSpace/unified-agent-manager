import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useRef } from 'react';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, type Item, type Reference } from '../../src/api';
import { MdCite, ReferenceProse } from '../../src/components/ReferenceCite';
import { useReferenceLedger } from '../../src/components/References';

afterEach(() => vi.restoreAllMocks());

const reply = { id: 'r2', kind: 'assistant', text: 'Issues 4 and 6 are open; 5 is closed. Issue 31 and #1 too.', time: '2026-11-22T10:00:00Z' } as Item;
const REFS: Reference[] = [
  { label: 'issue 4', noun: 'issue', number: '4', status: 'resolved', title: 'Login times out', state: 'open', claimed: 'open', source: { item_id: 'r1', time: '2026-11-22T09:00:00Z' } },
  { label: 'issue 5', noun: 'issue', number: '5', status: 'resolved', title: 'Crash on save', state: 'open', claimed: 'closed', source: { item_id: 'r1', time: '2026-11-22T09:00:00Z' } },
  { label: 'issue 6', noun: 'issue', number: '6', status: 'resolved', title: 'Copy', state: 'closed', claimed: '' },
  { label: 'issue 31', noun: 'issue', number: '31', status: 'unresolved', claimed: '' },
  { label: '#1', noun: '', number: '1', status: 'ambiguous', claimed: '', candidates: [{ title: 'Build the image', noun: 'step', item_id: 'r0' }, { title: 'Image too large', noun: 'bug' }] },
] as Reference[];

function SignOff({ sessionId }: Readonly<{ sessionId: string }>) {
  const anchor = useRef<HTMLSpanElement>(null);
  const ledger = useReferenceLedger({ id: 'turn-r2', sessionId, reply, end: '2026-11-22T10:00:05Z', anchor });
  return (
    <div>
      <ReferenceProse sessionId={sessionId} reply={reply}><MdCite data-reference="1">5</MdCite></ReferenceProse>
      <span ref={anchor}>
        {ledger.summary && <button type="button" aria-expanded={ledger.open} aria-controls={ledger.listId} onClick={ledger.toggle}>{ledger.summary}</button>}
      </span>
      {ledger.list}
    </div>
  );
}

function draw(sessionId: string, refs: Reference[] | Error) {
  const spy = vi.spyOn(api, 'references');
  if (refs instanceof Error) spy.mockRejectedValue(refs);
  else spy.mockResolvedValue({ references: refs });
  render(<ApiContext.Provider value={api}><SignOff sessionId={sessionId} /></ApiContext.Provider>);
  return spy;
}

test('the ledger counts the cited items by recorded state and lists each with what the Thread defined', async () => {
  const spy = draw('task-ledger', REFS);
  const summary = await screen.findByRole('button', { name: '5 references · 2 open · 1 closed · 2 unknown' });
  expect(spy).toHaveBeenCalledWith('task-ledger', 'r2', expect.any(AbortSignal));
  expect(summary.getAttribute('aria-expanded')).toBe('false');
  await userEvent.click(summary);
  expect(summary.getAttribute('aria-expanded')).toBe('true');

  const list = screen.getByRole('list', { name: 'References this reply cites' });
  const rows = within(list).getAllByRole('listitem').filter((li) => li.parentElement === list);
  const want = [
    ['#4', 'Login times out', 'Open'],
    ['#5', 'Crash on save', 'reply says closed', 'Open'],
    ['#6', 'Copy', 'Closed'],
    ['#31', 'no definition in this Thread'],
    ['#1', '2 matches', 'step · Build the image', 'bug · Image too large'],
  ];
  expect(rows).toHaveLength(want.length);
  rows.forEach((row, i) => want[i].forEach((part) => expect(row.textContent).toContain(part)));
  // The reply calls 5 closed; the Thread recorded it open.
  expect(rows[1].className).toContain('text-error');
  expect(within(rows[3]).queryByRole('button')).toBeNull();
  expect(within(rows[4]).getByRole('list', { name: 'Matches for #1' })).toBeTruthy();
});

test('a cited number in the prose opens the ledger', async () => {
  draw('task-prose', REFS);
  const cite = await screen.findByRole('button', { name: "#5: Crash on save. Show in this reply's references" });
  const summary = screen.getByRole('button', { name: /^5 references/ });
  expect(summary.getAttribute('aria-expanded')).toBe('false');
  await userEvent.click(cite);
  expect(summary.getAttribute('aria-expanded')).toBe('true');
  expect(screen.getByRole('list', { name: 'References this reply cites' })).toBeTruthy();
});

test('a failed read shows no ledger and the number stays text', async () => {
  const spy = draw('task-failed', new Error('offline'));
  await vi.waitFor(() => expect(spy).toHaveBeenCalled());
  expect(screen.queryByRole('button', { name: /references/ })).toBeNull();
  expect(screen.getByText('5').tagName).not.toBe('BUTTON');
});
