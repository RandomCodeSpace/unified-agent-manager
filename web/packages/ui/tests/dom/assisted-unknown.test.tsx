import { expect, test, vi } from 'vitest';
import { openMenu, openTask } from './render';

// t1's last permission mode change failed and could not be read back.
vi.mock('../../src/mock/data', async (importOriginal) => {
  const data = await importOriginal<typeof import('../../src/mock/data')>();
  return {
    ...data,
    seed: () => {
      const state = data.seed();
      const task = state.tasks.find((t) => t.id === 't1');
      if (task) task.mode_unknown = true;
      return state;
    },
  };
});

test('an unread permission mode is shown as unknown, never as the old mode', async () => {
  const { user } = await openTask('t1');
  const menu = await openMenu(user, 'Permissions and execution: Permissions unknown · Interactive');
  expect(menu.getByText(/could not be confirmed\. Nothing is allowed automatically/)).toBeTruthy();
  for (const name of [/^Safe/, /^Assisted/, /^Yolo/]) expect(menu.getByRole('menuitemradio', { name }).getAttribute('aria-checked')).toBe('false');
});
