// The Thread's branch under the composer (mock t15 runs in a worktree, t1 on the Project's branch).
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type { Project, SessionSummary } from '../../src/api';
import { BranchChip, bytesLabel } from '../../src/components/BranchChip';
import { openTask } from './render';

afterEach(() => vi.restoreAllMocks());

describe('the branch chip', () => {
  test('a worktree Thread shows its branch, "worktree" and what is not saved, in words', async () => {
    await openTask('t15');
    const chip = await screen.findByRole('button', { name: 'Branch uam/remove-unused-exports, worktree, 2 not committed · 3 not pushed' });
    expect(chip.closest('header')).toBeNull();
    expect(chip.parentElement!.textContent).toBe('uam/remove-unused-exports· worktree· 2 not committed · 3 not pushed');
  });

  test('its reader shows the branch and the folder, and copies the path', async () => {
    const { user } = await openTask('t15');
    const write = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    const chip = await screen.findByRole('button', { name: /^Branch uam\/remove-unused-exports/ });
    await user.click(chip);
    const reader = within(await screen.findByRole('dialog', { name: 'Branch and folder' }));
    expect(reader.getByRole('heading', { name: 'uam/remove-unused-exports' })).toBeTruthy();
    expect(reader.getByText('Worktree')).toBeTruthy();
    expect(reader.getByText('2 not committed')).toBeTruthy();
    const folder = reader.getByRole('region', { name: 'Folder' });
    // Lines break only after a slash: each folder name is one unbreakable piece.
    expect([...folder.querySelectorAll('code > span')].map((s) => s.textContent)).toEqual(['/', 'home/', 'user/', '.uam/', 'worktrees/', 'unified-agent-manager/', 'uam-remove-unused-exports']);
    await user.click(reader.getByRole('button', { name: 'Copy path' }));
    await waitFor(() => expect(write).toHaveBeenCalledWith('/home/user/.uam/worktrees/unified-agent-manager/uam-remove-unused-exports'));
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Branch and folder' })).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(chip));
  });

  test('a Thread in the Project folder shows the Project branch and nothing pending', async () => {
    await openTask('t1');
    const chip = await screen.findByRole('button', { name: 'Branch feat/web-project-defaults-and-sidebar-revamp' });
    expect(chip.textContent).not.toContain('worktree');
  });

  test('a worktree size and a low disk show only when passed', async () => {
    const session = { id: 's', workdir: '/w/x', branch: 'main', worktree: { path: '/w/x', branch: 'uam/x' }, capabilities: {} } as unknown as SessionSummary;
    const project = { id: 'p', name: 'p', dir: '/p', branch: 'main' } as Project;
    const { rerender } = render(<BranchChip session={session} project={project} />);
    expect(screen.queryByText(/Low disk/)).toBeNull();
    rerender(<BranchChip session={session} project={project} size={1.2 * 1024 ** 3} lowDisk={{ free: 31 * 1024 ** 3, total: 387 * 1024 ** 3 }} />);
    const warning = screen.getByRole('button', { name: 'Low disk: 31 GB free (8%)' });
    await userEvent.setup().click(warning);
    const reader = within(await screen.findByRole('dialog', { name: 'Branch and folder' }));
    expect(reader.getByText('1.2 GB')).toBeTruthy();
    expect(reader.getByText('Low disk: 31 GB free of 387 GB (8%)')).toBeTruthy();
    expect(bytesLabel(512)).toBe('512 B');
  });

  test('no branch, or no git, shows no chip', () => {
    const session = { id: 's', workdir: '/w', capabilities: {} } as unknown as SessionSummary;
    const { container, rerender } = render(<BranchChip session={session} project={{ id: 'p', name: 'p', dir: '/p' } as Project} />);
    expect(container.textContent).toBe('');
    rerender(<BranchChip session={{ ...session, branch: 'main' }} project={{ id: 'p', name: 'p', dir: '/p', no_git: 'not_repository' } as Project} />);
    expect(container.textContent).toBe('');
  });
});
