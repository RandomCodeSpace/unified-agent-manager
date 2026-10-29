// A subagent in the Conversation pane (DESIGN.md subagent view): its own address, the bar, the switcher and the composer read-only.
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { PHONE } from '../../src/components/ui/dialog';
import { renderApp } from './render';

afterEach(() => vi.restoreAllMocks());

const transcript = (name: string) => screen.findByRole('region', { name: `Transcript of ${name}` });

test('a subagent has an address: the URL opens it, and its composer stays, locked to its settings', async () => {
  const { user } = renderApp('#task=t8&agent=a1');
  await transcript('Survey templates for missing alt text and labels');
  // Its type and time at the right of the bar, Stop while it runs.
  const bar = screen.getByRole('group', { name: 'State: Running' });
  expect(within(bar).getByText('explore')).toBeTruthy();
  expect(within(bar).getByRole('button', { name: 'Stop subagent Survey templates for missing alt text and labels' })).toBeTruthy();
  // The real composer, read-only: no message, its model and effort locked with why, the Thread's mode inherited, no Thread Stop or Send.
  expect((document.getElementById('composer-text') as HTMLTextAreaElement).disabled).toBe(true);
  expect(screen.getByRole('button', { name: /^Model: Claude Haiku 4\.5\. Set when the Thread spawned it/ }).getAttribute('aria-disabled')).toBe('true');
  expect(screen.getByRole('button', { name: /^Effort: Medium\. Locked\./ })).toBeTruthy();
  expect(screen.getByRole('button', { name: /^Permissions and execution: Safe.*, inherited from the Thread\. Locked\.$/ })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Send. Subagents take no messages' }).getAttribute('aria-disabled')).toBe('true');
  expect(screen.queryByRole('button', { name: 'Stop turn' })).toBeNull();
  // Its link goes back to the Thread to write.
  await user.click(screen.getByRole('button', { name: 'Write to Accessibility pass on the post template ↖' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: /^Transcript of / })).toBeNull());
  expect(window.location.hash).toBe('#task=t8');
  await waitFor(() => expect((document.getElementById('composer-text') as HTMLTextAreaElement).disabled).toBe(false));
});

test('on a phone the trail folds the levels above into "…", and the switcher is one "k of N" chip', async () => {
  const original = window.matchMedia.bind(window);
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === PHONE ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
  const { user } = renderApp('#task=t8&agent=a6');
  await transcript('Verify store callers');
  const trail = within(screen.getByRole('navigation', { name: 'Trail' }));
  await user.click(trail.getByRole('button', { name: 'Show the levels above' }));
  expect((await screen.findAllByRole('menuitem')).map((item) => item.textContent)).toEqual(['NSAccessibility pass on the post template', 'Check contrast of the theme tokens']);
  await user.keyboard('{Escape}');
  expect(screen.getByRole('button', { name: 'Subagent 3 of 5. Show all' })).toBeTruthy();
});
