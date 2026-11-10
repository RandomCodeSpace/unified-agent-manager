import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { TurnList } from '../../src/components/notebook/TurnList';
import { TurnJumpProvider, useTurnJumpTarget } from '../../src/components/notebook/slots';
import { applyLayout } from '../../src/lib/layout';
import { turnList } from '../../src/lib/turns';
import { openTask, renderApp } from './render';

// The layout is the page's: each test leaves the current one behind.
afterEach(() => applyLayout('classic'));

const notebook = () => localStorage.setItem('uam.layout', 'notebook');
const column = () => within(screen.getByRole('navigation', { name: 'Threads' }));
const section = async (name: string) => within(await column().findByRole('region', { name }));

function Target({ onJump }: Readonly<{ onJump: (key: string) => void }>) {
  useTurnJumpTarget(onJump);
  return null;
}

describe('notebook Contents', () => {
  test("This Thread lists the open Thread's turns in order with their states", async () => {
    notebook();
    applyLayout('notebook');
    await openTask('t1');
    const turns = (await section('This Thread')).getAllByRole('listitem').map((row) => row.textContent);
    expect(turns).toHaveLength(2);
    expect(turns[0]).toMatch(/^1After detaching and re-attaching.*Running$/);
    expect(turns[1]).toMatch(/^2Then describe the new behaviour.*Queued$/);
    // The running turn is marked; a queued message is not in the conversation yet, so its row is no button.
    const buttons = (await section('This Thread')).getAllByRole('button');
    expect(buttons).toHaveLength(1);
    expect(buttons[0].getAttribute('aria-current')).toBe('step');
  });

  test('a turn asks the open Thread for its reply key', async () => {
    const onJump = vi.fn();
    const at = (minute: number) => `2026-11-10T10:0${minute}:00Z`;
    const turns = turnList({
      state: 'working',
      items: [
        { id: 'u1', kind: 'user', time: at(1), text: 'First' },
        { id: 'u2', kind: 'user', time: at(3), text: 'Second' },
      ],
      turn_timings: [
        { id: 'tt1', user_item_id: 'u1', started_at: at(1), ended_at: at(2), state: 'completed' },
        { id: 'tt2', user_item_id: 'u2', started_at: at(3), state: 'working' },
      ],
    });
    render(<TurnJumpProvider><Target onJump={onJump} /><TurnList turns={turns} /></TurnJumpProvider>);
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /Second/ }));
    await user.click(screen.getByRole('button', { name: /First/ }));
    expect(onJump.mock.calls).toEqual([['u2'], ['u1']]);
  });

  test('Also running lists the other active Threads, not the open one or one that is done', async () => {
    notebook();
    applyLayout('notebook');
    await openTask('t1');
    const others = await section('Also running');
    // One that waits on you, with its request answered from the card, and one that works.
    const waiting = within((await others.findByText('Draft the release note for v0.12')).closest('li')!);
    expect(await waiting.findByRole('button', { name: 'Answer' })).toBeTruthy();
    expect(waiting.getByRole('button', { name: 'Open' })).toBeTruthy();
    expect(others.getByText('Audit every package for dead exports')).toBeTruthy();
    // The open Thread is the column's first section; a finished Thread that was read is in All Threads.
    expect(others.queryByText('Fix re-attach redraw regression')).toBeNull();
    expect(others.queryByText('Sign the release archives')).toBeNull();
  });

  test('a card opens its Thread', async () => {
    notebook();
    applyLayout('notebook');
    const { user } = await openTask('t1');
    const card = within((await (await section('Also running')).findByText('Audit every package for dead exports')).closest('li')!);
    await user.click(card.getByRole('button', { name: 'Open' }));
    await screen.findByRole('heading', { level: 1, name: /Audit every package for dead exports/ });
  });

  test('All Threads shows the whole list, and Back to this Thread returns', async () => {
    notebook();
    applyLayout('notebook');
    const { user } = await openTask('t1');
    await section('This Thread');
    expect(column().queryByRole('list', { name: 'Unsettled threads' })).toBeNull();
    await user.click(column().getByRole('button', { name: 'All Threads' }));
    const list = within(await column().findByRole('list', { name: 'Unsettled threads' }));
    expect(list.getByText('Sign the release archives')).toBeTruthy();
    expect(column().getByRole('button', { name: 'Add project' })).toBeTruthy();
    expect(column().queryByRole('region', { name: 'This Thread' })).toBeNull();
    await user.click(column().getByRole('button', { name: 'Back to this Thread' }));
    await section('This Thread');
    expect(column().queryByRole('list', { name: 'Unsettled threads' })).toBeNull();
  });

  test('without an open Thread the column is the whole list', async () => {
    notebook();
    applyLayout('notebook');
    renderApp();
    await waitFor(() => expect(column().getByRole('list', { name: 'Unsettled threads' })).toBeTruthy());
    expect(column().queryByRole('button', { name: 'Back to this Thread' })).toBeNull();
  });

  test('on the current layout the sidebar is as before', async () => {
    await openTask('t1');
    expect(document.documentElement.dataset.layout).toBe('classic');
    expect(column().getByRole('list', { name: 'Unsettled threads' })).toBeTruthy();
    expect(column().getByRole('button', { name: 'New thread' })).toBeTruthy();
    expect(column().queryByRole('region', { name: 'This Thread' })).toBeNull();
    expect(column().queryByRole('button', { name: 'All Threads' })).toBeNull();
  });
});
