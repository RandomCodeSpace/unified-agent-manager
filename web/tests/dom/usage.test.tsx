import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { UsageButton } from '../../src/components/Usage';
import { tokenUsageFixture } from '../../src/mock/token-usage';
import { renderApp, sidebar } from './render';

afterEach(() => vi.restoreAllMocks());

describe('Usage popover', () => {
  test.each(['top', 'right'] as const)('keeps its %s anchor on the first click and after reopening', async (side) => {
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
    const measure = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (!this.isConnected) return new DOMRect();
      return this.getAttribute('aria-label') === 'Usage' ? new DOMRect(70, 768, 28, 28) : measure.call(this);
    });
    const user = userEvent.setup();
    render(<UsageButton side={side} />);
    const trigger = screen.getByRole('button', { name: 'Usage' });
    for (let attempt = 0; attempt < 2; attempt++) {
      await user.click(trigger);
      const popup = await screen.findByRole('dialog', { name: 'Usage' });
      await waitFor(() => expect(popup.parentElement!.style.getPropertyValue('--anchor-width')).toBe('28px'));
      expect(screen.getByRole('button', { name: 'Usage' })).toBe(trigger);
      expect(document.querySelector('[data-popup="tooltip"]')).toBeNull();
      await user.keyboard('{Escape}');
      await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Usage' })).toBeNull());
      expect(document.activeElement).toBe(trigger);
    }
  });

  test('opens beside Settings and Planner, switches periods and shows input, output, cache and totals', async () => {
    const { user } = renderApp();
    const nav = await sidebar();
    expect(nav.getByRole('button', { name: 'Settings' })).toBeTruthy();
    expect(nav.getByRole('button', { name: 'Planner' })).toBeTruthy();
    const button = nav.getByRole('button', { name: 'Usage' });
    const read = vi.spyOn(api, 'tokenUsage');
    expect(read).not.toHaveBeenCalled();
    await user.click(button);
    const popover = within(await screen.findByRole('dialog', { name: 'Usage' }));
    await popover.findByRole('table', { name: 'Today token usage by model' });
    expect(popover.getByText('$1.87 · partial')).toBeTruthy();
    expect(popover.getByText('Unpriced')).toBeTruthy();
    for (const name of ['Input', 'Output', 'Cache']) expect(popover.getByRole('columnheader', { name })).toBeTruthy();
    const model = within(popover.getByRole('rowheader', { name: /claude-sonnet-5/ }).closest('tr')!);
    expect(model.getByText('1.3M')).toBeTruthy();
    expect(model.getByText('42K')).toBeTruthy();
    const cache = model.getByRole('button', { name: 'Cache: 980,000 read, 180,000 written' });
    await user.hover(cache);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Read: 980,000'));
    await user.click(cache);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
    await user.pointer([{ keys: '[TouchA]', target: cache }]);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Read: 980,000'));
    await user.keyboard('{Escape}');
    for (const period of ['7 days', '30 days', 'Lifetime']) {
      await user.click(popover.getByRole('button', { name: period, exact: true }));
      expect(popover.getByRole('table', { name: `${period} token usage by model` })).toBeTruthy();
    }
    expect(popover.getAllByText('1.6B').length).toBeGreaterThan(0);
    expect(popover.getByRole('rowheader', { name: 'All models' })).toBeTruthy();
    await user.click(popover.getByRole('button', { name: 'Close usage' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Usage' })).toBeNull());
    await user.click(nav.getByRole('button', { name: 'Hide sidebar' }));
    const rail = within(screen.getByRole('navigation', { name: 'Sidebar' }));
    await user.click(rail.getByRole('button', { name: 'Usage' }));
    await screen.findByRole('dialog', { name: 'Usage' });
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Usage' })).toBeNull());
  });

  test('shows loading, failure with retry, and an honest empty period', async () => {
    let reject!: (e: Error) => void;
    const read = vi.spyOn(api, 'tokenUsage').mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    expect(await screen.findByRole('status')).toHaveProperty('textContent', 'Loading usage…');
    reject(new Error('offline'));
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringContaining('Could not load usage'));
    const empty = tokenUsageFixture();
    empty.periods.today = { models: [], total: { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 }, cost_usd: 0, unpriced_models: 0 };
    read.mockResolvedValue(empty);
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('No usage recorded for this period.')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test.each([
    ['starting', 'Reading local harness usage. Totals may be incomplete.'],
    ['partial', 'Some local harness usage is missing. Totals may be incomplete.'],
    ['unavailable', 'Local harness usage is unavailable. Showing available usage.'],
  ] as const)('shows fetch time for %s collection with details in the info tooltip', async (status, message) => {
    const report = tokenUsageFixture();
    report.collection = { status, copilot_since: '2026-10-03T10:30:00Z', updated_at: '2026-10-03T12:00:00Z' };
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    expect(await screen.findByRole('status')).toHaveProperty('textContent', `Last fetched ${new Date(report.collection.updated_at!).toLocaleString()}.`);
    expect(document.querySelector('[data-popup="tooltip"]')).toBeNull();
    expect(screen.getByText(message).closest('.sr-only')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'About Usage' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain(message));
    expect(screen.getByRole('rowheader', { name: /claude-sonnet-5/ })).toBeTruthy();
  });

  test('keeps descriptions in info tooltips accessible by hover, keyboard and touch', async () => {
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await screen.findByRole('table');
    for (const text of [/Cache is read/, /Costs use/, /model is unpriced/, /UAM tracking started/]) {
      expect(screen.getByText(text).closest('.sr-only')).toBeTruthy();
    }
    const about = screen.getByRole('button', { name: 'About Usage' });
    await user.hover(about);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Cache is read'));
    await user.unhover(about);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
    const cost = screen.getByRole('button', { name: 'About Estimated cost' });
    cost.focus();
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Costs use'));
    expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Settings → Models → Token costs');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
    expect(screen.getByRole('dialog', { name: 'Usage' })).toBeTruthy();
    await user.pointer([{ keys: '[TouchA]', target: about }]);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('UAM tracking started'));
  });

  test('keeps cost-only harness records, marks partial costs, and explains history limits', async () => {
    const report = tokenUsageFixture();
    const zero = { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 };
    report.collection = { status: 'ready', copilot_since: '2026-10-03T10:30:00Z', updated_at: '2026-10-03T12:00:00Z' };
    report.periods.today = {
      models: [{ ...zero, provider: 'crush', model: '', cost_usd: 2.5, cost_partial: true }],
      total: zero, cost_usd: 2.5, unpriced_models: 0,
    };
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    const row = within(await screen.findByRole('rowheader', { name: /Unspecified model/ }));
    expect(row.getByText('crush')).toBeTruthy();
    expect(row.getByText('$2.50 · partial')).toBeTruthy();
    expect(screen.getAllByText('$2.50 · partial')).toHaveLength(2);
    expect(screen.queryByText('No usage recorded for this period.')).toBeNull();
    expect(screen.getByRole('status').textContent).toBe(`Last fetched ${new Date(report.collection.updated_at!).toLocaleString()}.`);
    expect(screen.getByText(/UAM tracking started/).textContent).toContain('local harness records and UAM task, subagent, and Background AI usage');
    expect(screen.getByText(/External Copilot usage/).textContent).toContain('requires local telemetry files');
    expect(screen.getByText(/External Copilot usage/).textContent).toContain(new Date(report.collection.copilot_since).toLocaleString());
    expect(screen.getByText(/Costs use/).textContent).toContain('source-reported amounts');
  });

  test.each([
    [null, 'Unpriced'],
    [0, '$0.00 · partial'],
    [0.001, '<$0.01 · partial'],
    [0.01, '$0.01 · partial'],
    [2.5, '$2.50 · partial'],
  ] as const)('formats partial model and total costs consistently for %s', async (cost, expected) => {
    const report = tokenUsageFixture();
    const model = { ...report.periods.today.models[0], cost_usd: cost, cost_partial: true };
    report.periods.today = { models: [model], total: model, cost_usd: cost, unpriced_models: 0 };
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await screen.findByRole('table');
    expect(screen.getAllByText(expected)).toHaveLength(2);
  });

  test('retains the last report when refreshing fails', async () => {
    let refresh!: () => void;
    const setInterval = window.setInterval.bind(window);
    vi.spyOn(window, 'setInterval').mockImplementation((handler, timeout, ...args) => {
      if (timeout === 15000) refresh = handler as () => void;
      return setInterval(handler, timeout, ...args);
    });
    vi.spyOn(api, 'tokenUsage').mockResolvedValueOnce(tokenUsageFixture()).mockRejectedValueOnce(new Error('offline'));
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await screen.findByRole('rowheader', { name: /claude-sonnet-5/ });
    refresh();
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringContaining('Showing the last read'));
    expect(screen.getByRole('rowheader', { name: /claude-sonnet-5/ })).toBeTruthy();
  });
});
