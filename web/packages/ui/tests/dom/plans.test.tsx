import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, onTestFinished, test, vi } from 'vitest';
import { api, type PlanReview } from '../../src/api';
import { ReadPlan } from '../../src/components/Plan';
import * as data from '../../src/mock/data';
import { composer, log, openTask } from './render';

function fixture(overrides: Partial<PlanReview> = {}) {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't16')!;
  const plan: PlanReview = { request_id: 'plan-review', summary: 'Keep data and validate it', revision: 2, content: '# Plan\n\n1. Keep data.\n2. Validate it.', previous: '# Plan\n\n1. Keep data.', actions: ['autopilot', 'autopilot_fleet', 'interactive', 'exit_only'], recommended: 'interactive', ...overrides };
  task.capabilities.plan = true;
  task.interactions = [{ id: plan.request_id, kind: 'plan_review', title: 'Plan ready', state: 'pending', time: new Date().toISOString(), plan }];
  task.state = 'awaiting_answer';
  task.pending = 1;
  vi.spyOn(data, 'seed').mockReturnValue(state);
  return { task, plan };
}

const box = () => within(composer().form!);

describe('native plan review', () => {
  test('a closed Task keeps the reviewed snapshot while its current draft is honestly unavailable', async () => {
    const plan: PlanReview = { request_id: 'review', content: '# Reviewed', revision: 1 };
    const draft = vi.spyOn(api, 'planDraft').mockResolvedValue({ exists: true, content: '# Reopened draft' });
    const user = userEvent.setup();
    const view = render(<ReadPlan plan={plan} sessionId="task" draftAvailable={false} />);
    await user.click(screen.getByRole('button', { name: 'Read plan' }));
    const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
    expect(reader.getByText('Reviewed')).toBeTruthy();
    await user.click(reader.getByRole('button', { name: 'Current draft' }));
    expect(reader.getByText('The current draft is unavailable while this Task is closed.')).toBeTruthy();
    expect(draft).not.toHaveBeenCalled();
    view.rerender(<ReadPlan plan={plan} sessionId="task" draftAvailable />);
    await reader.findByRole('heading', { name: 'Reopened draft' });
    expect(draft).toHaveBeenCalledTimes(1);
    view.rerender(<ReadPlan plan={plan} sessionId="task" draftAvailable={false} />);
    expect(reader.getByText('The current draft is unavailable while this Task is closed.')).toBeTruthy();
    await user.click(reader.getByRole('button', { name: 'Reviewed snapshot' }));
    expect(reader.getByText('Reviewed')).toBeTruthy();
  });
  test('current draft is explicit, refreshes only while open, and never replaces the reviewed choice', async () => {
    const plan: PlanReview = { request_id: 'review', content: '# Reviewed', revision: 1 };
    const draft = vi.spyOn(api, 'planDraft').mockResolvedValue({ exists: true, content: '# Current draft' });
    const user = userEvent.setup();
    const view = render(<ReadPlan plan={plan} sessionId="task" planVersion={0} />);
    await user.click(screen.getByRole('button', { name: 'Read plan' }));
    const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
    expect(reader.getByText('Reviewed')).toBeTruthy();
    expect(draft).not.toHaveBeenCalled();
    await user.click(reader.getByRole('button', { name: 'Current draft' }));
    await reader.findByRole('heading', { name: 'Current draft' });
    expect(draft).toHaveBeenCalledTimes(1);
    draft.mockResolvedValue({ exists: true, content: '# Updated draft' });
    view.rerender(<ReadPlan plan={plan} sessionId="task" planVersion={1} />);
    await reader.findByRole('heading', { name: 'Updated draft' });
    expect(draft).toHaveBeenCalledTimes(2);
    await user.click(reader.getByRole('button', { name: 'Reviewed snapshot' }));
    expect(reader.getByText('Reviewed')).toBeTruthy();
    expect(reader.getByText('Revision 1')).toBeTruthy();
    view.rerender(<ReadPlan plan={plan} sessionId="task" planVersion={2} />);
    expect(draft).toHaveBeenCalledTimes(2);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Plan' })).toBeNull());
    view.rerender(<ReadPlan plan={plan} sessionId="task" planVersion={3} />);
    expect(draft).toHaveBeenCalledTimes(2);
  });

  test.each(['empty', 'deleted', 'error'] as const)('current draft %s stays distinct and the reviewed snapshot remains available', async (state) => {
    fixture();
    const draft = vi.spyOn(api, 'planDraft');
    if (state === 'error') draft.mockRejectedValue(new Error('Current draft read failed'));
    else draft.mockResolvedValue({ exists: state === 'empty', content: '' });
    const { user } = await openTask('t16');
    await user.click(box().getByRole('button', { name: 'Read plan' }));
    const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
    await user.click(reader.getByRole('button', { name: 'Current draft' }));
    await reader.findByText(state === 'error' ? 'Current draft read failed' : state === 'empty' ? 'The current draft is empty.' : 'The current draft is missing or was deleted. The reviewed snapshot is still available.');
    await user.click(reader.getByRole('button', { name: 'Reviewed snapshot' }));
    expect(reader.getByText('Validate it.')).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Plan' })).toBeNull());
    expect(box().getByRole('radio', { name: 'Implement interactively' })).toHaveProperty('checked', true);
  });

  test('shortened previous revision is labelled without disabling the complete current review', async () => {
    fixture({ previous_truncated: true });
    const { user } = await openTask('t16');
    await user.click(box().getByRole('button', { name: 'Read plan' }));
    const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
    expect(reader.getByText('The previous revision is shortened. These changes are incomplete.')).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Plan' })).toBeNull());
    expect(box().getByRole('radio', { name: 'Implement interactively' }).closest('fieldset')).toHaveProperty('disabled', false);
  });
  test('on a phone a long summary scrolls on its own, so every offered action stays reachable', async () => {
    const [width, height] = [window.innerWidth, window.innerHeight];
    onTestFinished(() => { window.innerWidth = width; window.innerHeight = height; });
    window.innerWidth = 390;
    window.innerHeight = 844;
    fixture({ summary: 'Create the fixture file and its nested plan, then verify both markers. '.repeat(20) });
    await openTask('t16');
    const summary = box().getByText(/Create the fixture file/);
    const scroller = summary.closest('.overflow-y-auto');
    expect(scroller).toBeTruthy();
    const actions = box().getAllByRole('radio');
    expect(actions).toHaveLength(4);
    for (const radio of actions) expect(scroller!.contains(radio)).toBe(false);
  });
  test('only offered actions appear, recommendation is staged, and a second click answers once', async () => {
    fixture();
    const { user, mock } = await openTask('t16');
    expect(box().getByText('Plan ready')).toBeTruthy();
    expect(box().getAllByRole('radio')).toHaveLength(4);
    expect(box().getByRole('radio', { name: 'Implement interactively' })).toHaveProperty('checked', true);
    expect(mock.received).toEqual([]);
    const fleet = box().getByRole('radio', { name: 'Implement with subagents' });
    await user.click(fleet);
    expect(mock.received).toEqual([]);
    await user.click(fleet);
    await waitFor(() => expect(box().queryByRole('radio')).toBeNull());
    expect(mock.received.map((r) => r.route)).toEqual(['answer']);
    expect(mock.received[0].body).toEqual({ plan: { action: 'autopilot_fleet' } });
  });

  test('feedback clears approval and preserves the parked Task draft', async () => {
    fixture();
    localStorage.setItem('uam.draft.t16', JSON.stringify({ text: 'next task message', files: [], attachments: [] }));
    const { user, mock } = await openTask('t16');
    await user.type(composer(), 'Add a rollback check');
    expect(box().getAllByRole('radio').some((r) => (r as HTMLInputElement).checked)).toBe(false);
    await user.keyboard('{Enter}');
    await waitFor(() => expect(box().queryByText('Plan ready')).toBeNull());
    expect(mock.received[0].body).toEqual({ plan: { feedback: 'Add a rollback check' } });
    await waitFor(() => expect(composer().value).toBe('next task message'));
  });

  test('an unoffered recommendation selects nothing; choosing an action clears feedback', async () => {
    fixture({ actions: ['interactive', 'exit_only'], recommended: 'autopilot' });
    const { user, mock } = await openTask('t16');
    expect(box().getAllByRole('radio').some((r) => (r as HTMLInputElement).checked)).toBe(false);
    expect(box().getByRole('button', { name: 'Approve plan' }).getAttribute('aria-disabled')).toBe('true');
    await user.type(composer(), 'Maybe revise');
    await user.click(box().getByRole('radio', { name: 'Leave planning' }));
    expect(composer().value).toBe('');
    await user.keyboard('{Enter}');
    await waitFor(() => expect(mock.received).toHaveLength(1));
    expect(mock.received[0].body).toEqual({ plan: { action: 'exit_only' } });
  });

  test('reader uses reviewed content, displays revision changes, closes on Escape and restores focus', async () => {
    fixture();
    const { user } = await openTask('t16');
    const trigger = box().getByRole('button', { name: 'Read plan' });
    await user.click(trigger);
    const reader = within(await screen.findByRole('dialog', { name: 'Plan' }));
    expect(reader.getByText('Revision 2')).toBeTruthy();
    expect(reader.getByText('Validate it.')).toBeTruthy();
    // The shared anchored-reader parts: an eyebrow section and a keycap foot, no hairlines.
    expect(reader.getByRole('region', { name: 'Reviewed plan' })).toBeTruthy();
    expect(reader.getByText('Esc').tagName).toBe('KBD');
    expect(screen.getByRole('dialog', { name: 'Plan' }).querySelector('.border-b, .border-t')).toBeNull();
    await user.click(reader.getByRole('button', { name: 'Show changes' }));
    expect(reader.getByText(/\+ 2\. Validate it\./)).toBeTruthy();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Plan' })).toBeNull());
    expect(document.activeElement).toBe(trigger);
  });

  test('Stop stays available while reviewing, and a shortened plan cannot be approved', async () => {
    fixture({ truncated: true });
    const { user, mock } = await openTask('t16');
    expect(box().getByRole('radio', { name: 'Implement interactively' }).closest('fieldset')).toHaveProperty('disabled', true);
    expect(screen.queryByRole('button', { name: 'Decline' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Stop turn' }));
    await waitFor(() => expect(box().queryByText('Plan ready')).toBeNull());
    expect(mock.received).toEqual([]);
  });

  test('historical decisions have two literal lines and read the native revision only on demand', async () => {
    const { task, plan } = fixture();
    task.state = 'completed';
    task.pending = 0;
    task.interactions[0].state = 'answered';
    task.interactions[0].resolution = 'interactive';
    task.items = [{ id: 'plan-native-id', kind: 'notice', time: new Date().toISOString(), text: 'Plan approved · Keep data\nImplement interactively', plan: { request_id: 'native-id', summary: plan.summary } }];
    const read = vi.spyOn(api, 'planReview').mockResolvedValue({ ...plan, request_id: 'native-id' });
    const { user } = await openTask('t16');
    expect(log().getByText('Plan approved · Keep data Implement interactively')).toBeTruthy();
    expect(read).not.toHaveBeenCalled();
    await user.click(log().getByRole('button', { name: 'Read plan' }));
    await screen.findByRole('dialog', { name: 'Plan' });
    await waitFor(() => expect(read).toHaveBeenCalledWith('t16', 'native-id', expect.any(AbortSignal)));
    expect(await screen.findByText('Revision 2')).toBeTruthy();
  });
});
