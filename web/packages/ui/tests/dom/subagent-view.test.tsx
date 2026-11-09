// A subagent in the Conversation pane (DESIGN.md subagent view): its own address, the bar, the strip and the composer read-only.
import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import * as data from '../../src/mock/data';
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

test('on a phone the trail folds the levels above into "…"', async () => {
  const original = window.matchMedia.bind(window);
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => (query === PHONE ? ({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList) : original(query)));
  const { user } = renderApp('#task=t8&agent=a6');
  await transcript('Verify store callers');
  const trail = within(screen.getByRole('navigation', { name: 'Trail' }));
  await user.click(trail.getByRole('button', { name: 'Show the levels above' }));
  expect((await screen.findAllByRole('menuitem')).map((item) => item.textContent)).toEqual(['NSAccessibility pass on the post template', 'Check contrast of the theme tokens']);
});

test('the strip keeps the subagents as in the Thread, without the Thread\'s todo list', async () => {
  renderApp('#task=t8&agent=a1');
  await transcript('Survey templates for missing alt text and labels');
  expect(screen.getByRole('button', { name: /Show the subagents$/ })).toBeTruthy();
  expect(screen.queryByRole('button', { name: /Show the list$/ })).toBeNull();
  expect(screen.queryByRole('group', { name: 'Subagents' })).toBeNull();
  // The bar's state is its fill and name, not a tooltip over the crumbs.
  expect(screen.getByRole('group', { name: 'State: Running' }).hasAttribute('title')).toBe(false);
});

test('a subagent waiting on the owner reads as the Thread does: Needs permission, the waiting fill', async () => {
  const state = data.seed();
  const task = state.tasks.find((t) => t.id === 't8')!;
  task.interactions = [...task.interactions, { id: 'perm-a1', kind: 'permission', title: 'Run a shell command', detail: 'npx html-validate templates/', state: 'pending', time: new Date().toISOString(), agent_id: 'a1', options: [{ id: 'once', label: 'Allow once' }] }];
  vi.spyOn(data, 'seed').mockReturnValue(state);
  renderApp('#task=t8&agent=a1');
  await transcript('Survey templates for missing alt text and labels');
  const bar = screen.getByRole('group', { name: 'State: Needs permission' });
  expect(bar.className).toContain('before:bg-warning');
  // Another one, with nothing waiting, keeps its own state.
  window.location.hash = '#task=t8&agent=a2';
  await transcript('Check contrast of the theme tokens');
  expect(screen.getByRole('group', { name: 'State: Running' }).className).toContain('before:bg-accent');
});

test('Tools → Agent copies the agent ID', async () => {
  const { user } = renderApp('#task=t8&agent=a1');
  await transcript('Survey templates for missing alt text and labels');
  await user.click(screen.getByRole('button', { name: 'Tools for this subagent, read only' }));
  const tools = within(await screen.findByRole('dialog', { name: 'Tools' }));
  await user.click(tools.getByRole('tab', { name: 'Agent' }));
  await user.click(tools.getByRole('button', { name: 'Copy agent ID' }));
  expect(await tools.findByRole('button', { name: 'Copied' })).toBeTruthy();
  expect(await navigator.clipboard.readText()).toBe('a1');
});

// A subagent by name (DESIGN.md Transcript): the identity dot and its name, a button that shows it.
test('a reply\'s whole bold name or inline-code id of one of its subagents opens it; a shared name or more words stay text', async () => {
  const state = data.seed();
  const t8 = state.tasks.find((t) => t.id === 't8')!;
  const reply = t8.items.find((i) => i.id === 'i2')!;
  reply.text += '\n\n**Run the accessibility linter** stopped and **Run the accessibility linter again** waits; `a4` is done. **Verify store callers** and **Check contrast of the theme tokens** are running. Log: [**Verify store callers**](https://example.com/log).';
  // Two of the reply's subagents share a name: it names neither.
  t8.subagents!.find((s) => s.id === 'a1')!.name = 'Check contrast of the theme tokens';
  vi.spyOn(data, 'seed').mockReturnValue(state);
  const { user } = renderApp('#task=t8');
  const turn = await waitFor(() => {
    const el = document.querySelector<HTMLElement>('[data-reply="i1"]');
    expect(el?.textContent).toContain('are running.');
    return el!;
  });
  const links = within(turn).getAllByRole('button', { name: /^Open subagent / });
  expect(links.map((b) => b.getAttribute('aria-label'))).toEqual(['Open subagent Run the accessibility linter', 'Open subagent Check the heading order', 'Open subagent Verify store callers']);
  // A name inside a link stays text: no button inside a link.
  expect(within(turn).getByRole('link', { name: 'Verify store callers' }).querySelector('button')).toBeNull();
  // The reply's own emphasis stays inside: bold as bold, code as code.
  expect(links[0].querySelector('strong')?.textContent).toBe('Run the accessibility linter');
  expect(links[1].querySelector('code')?.textContent).toBe('a4');
  await user.click(links[0]);
  await waitFor(() => expect(window.location.hash).toBe('#task=t8&agent=a3'));
});

test('a todo row\'s subagent is a button that shows it', async () => {
  const { user } = renderApp('#task=t8');
  await user.click(await screen.findByRole('button', { name: /^Todo .*Show the list$/ }));
  const reader = await screen.findByRole('dialog', { name: 'Todo' });
  await user.click(within(reader).getByRole('button', { name: 'Open subagent Run the accessibility linter' }));
  await waitFor(() => expect(window.location.hash).toBe('#task=t8&agent=a3'));
});
