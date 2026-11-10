import { screen, waitFor, within } from '@testing-library/react';
import { expect, test, vi } from 'vitest';
import { openTask, type User } from './render';

/** The output lines the panel shows. */
const lines = () => (document.querySelector('[data-command-output] pre')?.textContent ?? '').split('\n').filter(Boolean);

/** The `cursor` of each output read so far, in order. */
const cursors = (fetch: { mock: { calls: unknown[][] } }) => fetch.mock.calls.map(([url]) => String(url)).filter((url) => url.includes('/background-tasks/')).map((url) => new URL(url, location.href).searchParams.get('cursor'));

async function openOutput(user: User, running: number, task: string, command: string) {
  await user.click(screen.getByRole('button', { name: `Background tasks: ${running} running` }));
  const dialog = await screen.findByRole('dialog', { name: 'Background tasks' });
  await user.click(within(dialog).getByRole('button', { name: `Show output of background task: ${task}` }));
  return within(await screen.findByRole('region', { name: `Output of ${command}` }));
}

test('only a shell whose output can be read offers Output; a snapshot without the field keeps it', async () => {
  const { user } = await openTask('t1');
  await user.click(screen.getByRole('button', { name: 'Background tasks: 1 running' }));
  const dialog = within(await screen.findByRole('dialog', { name: 'Background tasks' }));
  // The mock's lint shell is attached: listed, with no Output.
  expect(dialog.getByText('Lint the web package')).toBeTruthy();
  expect(dialog.queryByRole('button', { name: 'Show output of background task: Lint the web package' })).toBeNull();
  // The other two carry no field, as from an older server.
  expect(dialog.getByRole('button', { name: 'Show output of background task: Repeat the redraw tests' })).toBeTruthy();
  expect(dialog.getByRole('button', { name: 'Show output of background task: Serve the docs preview' })).toBeTruthy();
});

test('a running background command opens to its output, loads more on request, then follows until the panel closes', async () => {
  const { user } = await openTask('t1');
  const fetch = vi.spyOn(window, 'fetch');
  const panel = await openOutput(user, 1, 'Repeat the redraw tests', 'go test ./internal/vterm/... -run Redraw -count=200');
  // The list gives way to the panel, which reads one chunk from the start and no more.
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Background tasks' })).toBeNull());
  await waitFor(() => expect(lines()).toHaveLength(40));
  expect(lines().at(-1)).toBe('bg-watch output line 40');
  expect(new Set(cursors(fetch))).toEqual(new Set(['']));

  await user.click(panel.getByRole('button', { name: 'Load more' }));
  await waitFor(() => expect(lines().at(-1)).toBe('bg-watch output line 80'));
  expect(cursors(fetch).at(-1)).toBe('1');
  // Caught up with a running command: no button, and the panel asks again by itself.
  expect(await panel.findByText('Running. New output appears here.')).toBeTruthy();
  expect(panel.queryByRole('button', { name: 'Load more' })).toBeNull();
  await waitFor(() => expect(cursors(fetch).at(-1)).toBe('2'), { timeout: 5000 });

  await user.click(screen.getByRole('button', { name: 'Close command output' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: /^Output of go test/ })).toBeNull());
  const asked = cursors(fetch).length;
  await new Promise((done) => setTimeout(done, 2300));
  expect(cursors(fetch)).toHaveLength(asked);
});

test('a finished background command stays readable to its end, and one whose output is not kept says so', async () => {
  const { user } = await openTask('t1');
  const real = window.fetch;
  const fetch = vi.spyOn(window, 'fetch');
  const panel = await openOutput(user, 1, 'Serve the docs preview', 'npx vite --host 127.0.0.1 --port 8765 docs');
  await user.click(await panel.findByRole('button', { name: 'Load more' }));
  expect(await panel.findByText('End of output.')).toBeTruthy();
  expect(lines().at(-1)).toBe('bg-docs output line 80');
  const continued = cursors(fetch).filter(Boolean).length;

  fetch.mockImplementation((input, init) => (String(input).includes('/background-tasks/') ? Promise.resolve(new Response('{"error":"this command\'s output is not kept"}', { status: 404, headers: { 'Content-Type': 'application/json' } })) : real(input, init)));
  await user.click(screen.getByRole('button', { name: 'Close command output' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: /^Output of npx vite/ })).toBeNull());
  const other = await openOutput(user, 1, 'Repeat the redraw tests', 'go test ./internal/vterm/... -run Redraw -count=200');
  expect(await other.findByText('This command’s output cannot be read here.')).toBeTruthy();
  expect(other.queryByRole('button', { name: 'Load more' })).toBeNull();
  // The ended command was not read again after its end.
  expect(cursors(fetch).filter(Boolean)).toHaveLength(continued);
});
