// The owner's rule: every feature is in both layouts. The notebook layout (lib/layout) only moves
// what the current one draws, so every control the current layout offers on a Thread must be
// offered by the notebook layout too: on the page, or in a menu the page opens. A control that is
// reached another way there is named in ALLOWED with the control that reaches it and the reason.
import { cleanup, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';
import { applyLayout, type Layout } from '../../src/lib/layout';
import { openTask, type User } from './render';

afterEach(() => applyLayout('classic'));

/** What a person can press, follow or fill. */
const ROLES = ['button', 'link', 'textbox', 'searchbox', 'combobox', 'checkbox', 'radio', 'switch', 'slider', 'spinbutton', 'tab'];
const MENU_ROLES = ['menuitem', 'menuitemradio', 'menuitemcheckbox'];

/**
 * The current layout's controls that the notebook layout offers through another control (`through`,
 * which must be on its page). Keep it short: a control belongs here only when the notebook layout
 * still reaches what it does. Numbers in names read `#`.
 */
const ALLOWED: { control: RegExp; through: RegExp; reason: string }[] = [
  // The Contents column shows the open Thread's turns and the active Threads; "All Threads" brings the sidebar's whole list back, with all it has.
  { control: /^the sidebar's Thread rows$/, through: /^button: All Threads$/, reason: 'the list of Threads is behind All Threads; the active ones are cards under Also running' },
  { control: /^button: Settled #$/, through: /^button: All Threads$/, reason: 'the Settled section is part of the list of Threads' },
  { control: /^button: Project filter: /, through: /^button: All Threads$/, reason: 'the filter heads the list of Threads' },
  { control: /^button: Routines$/, through: /^button: All Threads$/, reason: 'Routines heads the list of Threads' },
  { control: /^button: Add project$/, through: /^button: All Threads$/, reason: 'Add project heads the list of Threads' },
  // The running turn's live card is the one home of the todo list and the subagents; the status strip's three buttons are its words.
  { control: /^button: Todo # of # done.* Show the list$/, through: /^button: Open the list$/, reason: 'the live card lists the todos, and "Open the list" opens the same reader' },
  { control: /^button: Todo history: .* Show the history$/, through: /^button: Earlier turns$/, reason: '"Earlier turns" on the live card opens the same reader' },
  { control: /^button: Subagents: .* Show the subagents$/, through: /^button: # subagents? · /, reason: "the live card's line of counts opens the same list of subagents" },
];

const HELP = 'add it to the notebook layout or to the allow-list with a reason';

/** The controls on the page under `roles`, each as "role: accessible name"; a clock or a count in a name reads `#`. */
function controls(roles: readonly string[]): string[] {
  return roles.flatMap((role) => {
    // The role query computes each candidate's accessible name for its matcher: that is where the names come from.
    const names = new Map<Element, string>();
    const found = screen.queryAllByRole(role, { name: (name, el) => { names.set(el, name); return true; } });
    return found.map((el) => (el.closest('nav [data-task-row]') ? "the sidebar's Thread rows" : `${role}: ${(names.get(el) ?? '').replace(/\d+/g, '#').trim()}`));
  });
}

const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** The page's controls once they have stopped changing: a Thread's parts arrive over a few requests. */
async function settled(): Promise<string[]> {
  let last = '';
  for (let same = 0, tries = 0; same < 3 && tries < 60; tries++) {
    await pause(100);
    const now = controls(ROLES).join('\n');
    same = now === last ? same + 1 : 0;
    last = now;
  }
  return last.split('\n');
}

/** The items of every menu the Thread's pane opens, each under its trigger's name (before its value: "Model", "Thread actions"). */
async function menuItems(user: User): Promise<string[]> {
  const triggers = () => [...document.querySelectorAll<HTMLButtonElement>('[aria-haspopup="menu"]:not(:disabled)')].filter((el) => !el.closest('nav'));
  const items: string[] = [];
  for (let i = 0; i < triggers().length; i++) {
    const trigger = triggers()[i];
    const name = (trigger.getAttribute('aria-label') ?? trigger.textContent ?? '').split(':')[0].replace(/\d+/g, '#').trim();
    await user.click(trigger);
    await screen.findByRole('menu');
    items.push(...controls(MENU_ROLES).map((item) => `${name} › ${item}`));
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[role="menu"]')).toBeNull());
  }
  return items;
}

async function offered(id: string, layout: Layout): Promise<Set<string>> {
  localStorage.setItem('uam.layout', layout);
  applyLayout(layout);
  const { user, unmount } = await openTask(id);
  const all = new Set([...(await settled()), ...(await menuItems(user))]);
  unmount();
  cleanup();
  return all;
}

test.each([
  ['a working Thread', 't8'],
  ['a Thread that waits on an answer', 't2'],
  ['a finished Thread', 't21'],
])('the notebook layout offers every control the current layout offers on %s', async (_what, id) => {
  const classic = await offered(id, 'classic');
  const notebook = await offered(id, 'notebook');
  // The fixture is what it is said to be: a Thread with its header, its composer and their menus.
  expect([...classic].filter((c) => c.includes(' › ')).length).toBeGreaterThan(10);
  expect(classic.has('textbox: Message')).toBe(true);
  const problems: string[] = [];
  for (const control of classic) {
    if (notebook.has(control)) continue;
    const allowed = ALLOWED.find((a) => a.control.test(control));
    if (!allowed) problems.push(`The notebook layout does not offer "${control}", which the current layout offers on ${id}: ${HELP}.`);
    else if (![...notebook].some((c) => allowed.through.test(c))) problems.push(`The allow-list says "${control}" is reached through ${allowed.through} (${allowed.reason}), but the notebook layout offers no such control on ${id}: ${HELP}.`);
  }
  expect(problems, problems.join('\n')).toEqual([]);
}, 60000);
