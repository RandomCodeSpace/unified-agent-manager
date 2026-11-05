import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import type { Interaction, SessionDetail } from '../../src/api';
import { ComposerQuestion, InteractionCard } from '../../src/components/Interactions';
import { composer, openTask } from './render';

const session = { id: 't', capabilities: { permissions: true, questions: true } } as unknown as SessionDetail;
const PATH = '/tmp/uam-fixture-probe.txt';
// The adapter's write detail: the path, a blank line, then the CLI's git patch as it sends it.
const PATCH = `diff --git a${PATH} b${PATH}\ncreate file mode 100644\nindex 0000000..0000000\n--- a/dev/null\n+++ b${PATH}\n@@ -1,0 +1,1 @@\n+hi\n`;
const options = [{ id: 'approve_once', label: 'Allow once', allow_once: true }, { id: 'reject', label: 'Deny', reject: true }];
const write: Interaction = { id: 'w', kind: 'permission', title: 'Write file', detail: `${PATH}\n\n${PATCH}`, state: 'pending', time: '2026-11-04T00:00:00Z', options };

test('a write request shows its change as a diff: no git headers, and the path once', async () => {
  const { container } = render(<InteractionCard session={session} interaction={write} onUpdate={() => {}} />);
  const table = await screen.findByRole('table');
  expect(table.textContent).toContain('hi');
  const text = container.textContent ?? '';
  for (const header of ['diff --git', 'index 0000000', '--- a/', '+++ b/', 'create file mode']) expect(text).not.toContain(header);
  expect(text.split(PATH).length - 1).toBe(1);
  expect(screen.getByRole('region', { name: 'Proposed change' })).toBeTruthy();
});

test('a write with no text hunk still names its path once, without headers', () => {
  const { container } = render(<InteractionCard session={session} interaction={{ ...write, detail: `${PATH}\n\ndiff --git a${PATH} b${PATH}\nnew file mode 100644\n` }} onUpdate={() => {}} />);
  const text = container.textContent ?? '';
  expect(text).not.toContain('diff --git');
  expect(text.split(PATH).length - 1).toBe(1);
});

test('a shell request the provider lets you allow for the session offers it', () => {
  const shell: Interaction = { ...write, id: 's', title: 'Run shell command', detail: 'go test ./...', options: [options[0], { id: 'approve_session', label: 'Allow for this session' }, options[1]] };
  render(<InteractionCard session={session} interaction={shell} onUpdate={() => {}} />);
  expect(screen.getByRole('button', { name: 'Allow for this session' })).toBeTruthy();
  expect(screen.getByText('go test ./...')).toBeTruthy();
});

test("the chosen option's second-click hint is announced, not hidden from screen readers", () => {
  const question = { text: 'Which one?', choices: ['Red', 'Blue'], custom: false };
  const asked: Interaction = { id: 'q', kind: 'question', title: 'Question', state: 'pending', time: '2026-11-04T00:00:00Z', questions: [question] };
  render(<ComposerQuestion interaction={asked} question={question} chosen={['Red']} disabled={false} onChoose={() => {}} onAnswer={() => {}} />);
  const red = screen.getByRole('radio', { name: 'Red' });
  const hint = document.getElementById(red.getAttribute('aria-describedby') ?? '');
  expect(hint?.textContent).toContain('Click again to answer');
  expect(hint?.closest('[aria-hidden="true"]')).toBeNull();
  expect(screen.getByRole('radio', { name: 'Blue' }).hasAttribute('aria-describedby')).toBe(false);
});

test('typing /btw shows the aside cue beside Send as the text changes', async () => {
  const { user } = await openTask('t3');
  await user.type(composer(), '/');
  await screen.findByRole('listbox', { name: 'Commands' });
  expect(screen.queryByText('Aside · not sent to the agent')).toBeNull();
  await user.type(composer(), 'btw');
  expect(screen.getByText('Aside · not sent to the agent')).toBeTruthy();
  await user.clear(composer());
  expect(screen.queryByText('Aside · not sent to the agent')).toBeNull();
});
