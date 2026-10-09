import { render, screen, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { Interaction, SessionDetail } from '../../src/api';
import { DecidedRow, InteractionCard } from '../../src/components/Interactions';
import { choose, openMenu, openTask } from './render';

test('Assisted is an opt-in, clearly labelled mode where the provider supports it', async () => {
  const { user } = await openTask('t1');
  const menu = await openMenu(user, 'Permissions and execution: Safe · Interactive');
  const item = await menu.findByRole('menuitemradio', { name: /^Assisted/ });
  expect(item.textContent).toContain('gpt-6-luna reviews each permission request');
  expect(item.getAttribute('aria-checked')).toBe('false');
  await user.keyboard('{Escape}');
  await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
  await choose(user, 'Permissions and execution: Safe · Interactive', /^Assisted/);
  expect(await screen.findByRole('button', { name: 'Permissions and execution: Assisted · Interactive' })).toBeTruthy();
  const again = await openMenu(user, 'Permissions and execution: Assisted · Interactive');
  expect(again.getByText(/A reviewer model allows the permission requests it approves/)).toBeTruthy();
});

test('a Task whose provider lacks assisted permissions does not offer it', async () => {
  const { user } = await openTask('t3');
  const menu = await openMenu(user, /^Permissions and execution: Safe/);
  await menu.findByRole('menuitemradio', { name: /^Yolo/ });
  expect(menu.queryByRole('menuitemradio', { name: /^Assisted/ })).toBeNull();
});

test('a runtime that refused assisted shows it disabled with the reason', async () => {
  const { user } = await openTask('t4');
  const menu = await openMenu(user, /^Permissions and execution: Safe/);
  const item = await menu.findByRole('menuitemradio', { name: /^Assisted/ });
  expect(item.textContent).toContain('kept permission mode "manual"');
  expect(item.getAttribute('aria-disabled')).toBe('true');
});

const session = { id: 't', capabilities: { permissions: true, questions: true } } as unknown as SessionDetail;
const request: Interaction = { id: 'p', kind: 'permission', title: 'Write file', detail: 'a.txt', state: 'pending', time: '2026-10-09T00:00:00Z', options: [{ id: 'approve_once', label: 'Allow once', allow_once: true }, { id: 'reject', label: 'Deny', reject: true }] };

test('a request the reviewer did not approve says why it still asks', () => {
  render(<InteractionCard session={session} interaction={{ ...request, assisted: { recommendation: 'requireApproval', model: 'gpt-6-luna', reason: 'writes outside the task' } }} onUpdate={() => {}} />);
  expect(screen.getByText('Assisted review (gpt-6-luna) asks for your decision: writes outside the task')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Allow once' })).toBeTruthy();
});

test('an unreviewed request shows no review, and an approved one reads as reviewed', () => {
  const view = render(<InteractionCard session={session} interaction={request} onUpdate={() => {}} />);
  expect(screen.queryByText(/Assisted review/)).toBeNull();
  view.unmount();
  render(<DecidedRow interaction={{ ...request, state: 'answered', resolution: 'allowed (assisted review)' }} />);
  expect(screen.getByText('reviewed')).toBeTruthy();
});
