import { expect, test, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { openTask } from './render';

// t3 has no live todo list (its conversation is not open), only the list kept when its turn ended:
// one of these rows done, one in progress, one to do. Its last reply restates them as a task list.
vi.mock('../../src/mock/data', async (importOriginal) => {
  const data = await importOriginal<typeof import('../../src/mock/data')>();
  return {
    ...data,
    seed: () => {
      const state = data.seed();
      const reply = state.tasks.find((t) => t.id === 't3')?.items.find((i) => i.id === 'i6');
      if (reply) reply.text = 'Where it stands:\n\n- [ ] Test the new doctor line\n- [ ] Check the line in macOS Terminal\n- [ ] Mention the line in docs/doctor.md';
      return state;
    },
  };
});

test('with no live todo list, a reply ticks off from the kept list the strip shows', async () => {
  await openTask('t3');
  await waitFor(() => expect(document.querySelector('.md-progress')?.textContent).toBe('1 of 3 done· from the todo list'));
  expect([...document.querySelectorAll('.md-progress + ul li')].map((li) => li.getAttribute('data-state'))).toEqual(['done', 'in_progress', 'pending']);
});
