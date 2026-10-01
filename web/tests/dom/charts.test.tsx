import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { openTask } from './render';

// The diagram frame cannot load here; the charts stay placeholders.
vi.mock('../../src/lib/diagram', async (original) => ({ ...(await original<typeof import('../../src/lib/diagram')>()), renderDiagram: () => new Promise(() => {}) }));

describe('charts', () => {
  test('a chart call shows its card; pinning asks with the command, then the header counts it', async () => {
    const { user } = await openTask('t-chart');
    const card = within(await screen.findByRole('figure', { name: 'Chart: Lines of code per file type' }));
    expect(card.getByText(/Drawn from 6 rows/)).toBeTruthy();
    expect(card.getByText(/git ls-files/)).toBeTruthy();

    await user.click(card.getByRole('button', { name: 'Table' }));
    const rows = within(card.getByRole('region', { name: 'Rows of Lines of code per file type' }));
    expect(rows.getByRole('rowheader', { name: 'go' })).toBeTruthy();
    expect(rows.getByText('41,280')).toBeTruthy();

    expect(screen.getByRole('button', { name: 'Pinned charts, 2' })).toBeTruthy();
    await user.click(card.getByRole('button', { name: 'Pin to project' }));
    const dialog = within(await screen.findByRole('dialog', { name: 'Pin to the project' }));
    expect(dialog.getByText(/no agent and no model call/)).toBeTruthy();
    expect(dialog.getByText(/sort \| uniq -c/)).toBeTruthy();
    await user.click(dialog.getByRole('button', { name: 'Pin' }));
    await waitFor(() => expect(card.getByText('Pinned')).toBeTruthy());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Pinned charts, 3' })).toBeTruthy());
  });

  test('the pinned charts panel shows each chart, refreshes on demand and unpins', async () => {
    const { user } = await openTask('t-chart');
    await user.click(await screen.findByRole('button', { name: 'Pinned charts, 2' }));
    const panel = within(await screen.findByRole('region', { name: 'Charts pinned to unified-agent-manager' }));
    const commits = within(await panel.findByRole('region', { name: 'Commits per day, September' }));
    expect(commits.getByText('5')).toBeTruthy();
    expect(panel.getByText(/No model call/)).toBeTruthy();

    await user.click(commits.getByRole('button', { name: /^Refresh Commits per day, September/ }));
    await waitFor(() => expect(commits.getByRole('button', { name: /refreshed just now/ })).toBeTruthy());
    expect(commits.getByText('6')).toBeTruthy();
    await user.click(commits.getByRole('button', { name: /^Refresh Commits per day, September/ }));
    expect(await commits.findByText(/less than a minute ago/)).toBeTruthy();

    await user.click(commits.getByRole('button', { name: 'Unpin Commits per day, September' }));
    await user.click(within(await screen.findByRole('dialog', { name: 'Unpin this chart?' })).getByRole('button', { name: 'Unpin' }));
    await waitFor(() => expect(panel.queryByRole('region', { name: 'Commits per day, September' })).toBeNull());
  });
});
