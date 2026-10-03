import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { getInstanceByDom } from 'echarts/core';
import { describe, expect, test, vi } from 'vitest';
import { api, type Chart } from '../../src/api';
import { ChartCard } from '../../src/components/Chart';
import { openTask } from './render';

describe('charts', () => {
  test('a saved chart with malformed zoom entries still draws its data', async () => {
    const chart: Chart = {
      title: 'Saved pie', kind: 'echarts', x: '', y: [], labels: [], series: [], at: '2026-10-03T12:00:00Z',
      options: JSON.parse('{"series":[{"type":"pie","data":[1]}],"dataZoom":[null]}'),
    };
    const read = vi.spyOn(api, 'chart').mockResolvedValue(chart);
    try {
      render(<ChartCard sessionId="chart-task" callId="saved-pie" />);
      const drawing = await screen.findByRole('img', { name: 'Chart: Saved pie' });
      await waitFor(() => expect(drawing.querySelector('svg path')).toBeTruthy());
      expect((getInstanceByDom(drawing)!.getOption().series as { data: number[] }[])[0].data).toEqual([1]);
    } finally {
      read.mockRestore();
    }
  });

  test('an advanced chart renders its series and exposes original JSON without an empty row table', async () => {
    const chart: Chart = {
      title: 'Files by language', kind: 'echarts', x: '', y: [], labels: [], series: [], at: '2026-10-03T12:00:00Z',
      options: { series: [{ type: 'pie', data: [{ name: 'Go', value: 8 }, { name: 'TypeScript', value: 5 }] }] },
    };
    const read = vi.spyOn(api, 'chart').mockResolvedValue(chart);
    const user = userEvent.setup();
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    try {
      render(<ChartCard sessionId="chart-task" callId="pie" />);
      const figure = within(await screen.findByRole('figure', { name: 'Chart: Files by language' }));
      const drawing = await figure.findByRole('img', { name: 'Chart: Files by language' });
      await waitFor(() => expect(drawing.querySelector('svg path')).toBeTruthy());
      expect(figure.queryByText(/0 rows/)).toBeNull();
      expect(figure.queryByRole('button', { name: 'Table' })).toBeNull();
      expect(figure.queryByRole('group', { name: 'Chart zoom' })).toBeNull();
      await user.click(figure.getByRole('button', { name: 'Data' }));
      expect(figure.getByRole('region', { name: 'Data for Files by language' }).textContent).toContain('"type": "pie"');
      await user.click(figure.getByRole('button', { name: 'Copy JSON' }));
      expect(copy).toHaveBeenCalledWith(JSON.stringify(chart.options, null, 2));
      await user.click(figure.getByRole('button', { name: 'Pin to project' }));
      expect(within(await screen.findByRole('dialog', { name: 'Pin to the project' })).getByText(/The agent supplied this chart’s data/)).toBeTruthy();
    } finally {
      read.mockRestore();
      copy.mockRestore();
    }
  });

  test('series controls toggle the actual drawing and keep their names available to the keyboard', async () => {
    const { user } = await openTask('t-chart');
    const figure = within(await screen.findByRole('figure', { name: 'Chart: Changes per week, by area' }));
    const host = figure.getByRole('img');
    await waitFor(() => expect(getInstanceByDom(host)).toBeTruthy());
    const control = figure.getByRole('button', { name: 'Show web series' });
    expect(control.getAttribute('aria-pressed')).toBe('true');
    await user.click(control);
    expect(control.getAttribute('aria-pressed')).toBe('false');
    await waitFor(() => expect((getInstanceByDom(host)!.getOption().legend as { selected: Record<string, boolean> }[])[0].selected.web).toBe(false));
    await user.click(control);
    await waitFor(() => expect((getInstanceByDom(host)!.getOption().legend as { selected: Record<string, boolean> }[])[0].selected.web).toBe(true));
  });

  test('a chart call shows its card; pinning asks with the command, then the header counts it', async () => {
    const { user } = await openTask('t-chart');
    const card = within(await screen.findByRole('figure', { name: 'Chart: Lines of code per file type' }));
    expect(card.getByText(/Drawn from 6 rows/)).toBeTruthy();
    // No code under the chart: the command shows when pinning.
    expect(card.queryByText(/git ls-files/)).toBeNull();
    expect(card.getByText(/From a command/)).toBeTruthy();

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
    expect(commits.queryByRole('group', { name: 'Chart zoom' })).toBeNull();

    await user.click(commits.getByRole('button', { name: 'Show Commits per day, September large' }));
    const zoomIn = commits.getByRole('button', { name: 'Zoom in on chart' });
    await waitFor(() => expect(zoomIn.hasAttribute('disabled')).toBe(false));
    await user.click(zoomIn);
    expect((getInstanceByDom(commits.getByRole('img'))!.getOption().dataZoom as { start: number }[])[0].start).toBeGreaterThan(0);
    await user.click(commits.getByRole('img'));
    expect(commits.getByRole('button', { name: 'Show Commits per day, September small' })).toBeTruthy();
    await user.click(commits.getByRole('button', { name: 'Show Commits per day, September small' }));
    expect(commits.queryByRole('group', { name: 'Chart zoom' })).toBeNull();

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
