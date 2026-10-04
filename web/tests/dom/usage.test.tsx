import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { AppContext, type AppContextValue } from '../../src/components/common';
import { UsageButton } from '../../src/components/Usage';
import { tokenPriceFixture, tokenUsageFixture } from '../../src/mock/token-usage';
import { composer, renderApp, sidebar } from './render';

beforeEach(() => { vi.spyOn(api, 'tokenPrices').mockResolvedValue(tokenPriceFixture()); });
afterEach(() => vi.restoreAllMocks());

function summaryCost(label: 'Estimated cost' | 'Without cache') {
  return screen.getByText(label, { exact: true }).parentElement!.nextElementSibling!;
}

function expectPricingCaveatsHidden() {
  for (const container of [screen.getByRole('region', { name: 'Model usage' }), summaryCost('Estimated cost'), summaryCost('Without cache')]) {
    for (const detail of within(container as HTMLElement).queryAllByText(/partial|unpriced|missing prices/i)) {
      expect(detail.closest('.sr-only')).toBeTruthy();
    }
  }
}

describe('Usage popover', () => {
  test('/usage opens the shared account popover', async () => {
    vi.spyOn(api, 'commands').mockResolvedValue([{ name: 'usage', description: 'Show usage', kind: 'command', input_hint: '' }]);
    vi.spyOn(api, 'command').mockResolvedValue({ request_id: 'usage', status: 'accepted', time: new Date().toISOString(), command_result: { kind: 'action', action: 'usage' } });
    const { user } = renderApp('#task=t3');
    await screen.findByRole('region', { name: 'Conversation' });
    await user.type(composer(), '/usage');
    await within(await screen.findByRole('listbox', { name: 'Commands' })).findByRole('option', { name: /^\/usage/ });
    await user.keyboard('{Enter}{Enter}');
    expect(await screen.findByRole('region', { name: 'AI allowance' })).toBeTruthy();
    expect(document.getElementById('composer-usage')).toBeNull();
  });

  test('moves the account allowance out of the composer and into Usage', async () => {
    const { user } = renderApp('#task=t20');
    const nav = await sidebar();
    const button = nav.getByRole('button', { name: 'Usage', exact: true });
    await waitFor(() => expect(button.textContent).toBe('96'));
    expect(document.getElementById(button.getAttribute('aria-describedby')!)?.textContent).toContain('96% left');
    expect(document.getElementById('composer-usage')).toBeNull();
    expect(button.querySelector('circle[pathLength]')?.getAttribute('stroke-dasharray')).toBe('96 100');
    await user.click(button);
    const allowance = within(await screen.findByRole('region', { name: 'AI allowance' }));
    expect(allowance.getByText('96% left')).toBeTruthy();
    expect(allowance.getByText('280 of 7,000 ai credits used')).toBeTruthy();
    expect(allowance.getByText(/until reset.*left at this pace/)).toBeTruthy();
  });

  test('stale quota keeps the number but clears the pace assessment', async () => {
    const user = userEvent.setup();
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
    const quota = { provider: 'copilot', type: 'ai_credits', used: 0, entitlement: 7000, remaining_percent: 100, unlimited: false, overage: 0 };
    const renderQuota = (stale: boolean) => <AppContext.Provider value={{ usage: { quotas: [quota], stale }, meta: null } as AppContextValue}><UsageButton /></AppContext.Provider>;
    const view = render(renderQuota(false));
    const button = screen.getByRole('button', { name: 'Usage', exact: true });
    expect(button.textContent).toBe('100');
    expect(button.className).toContain('text-warning');
    view.rerender(renderQuota(true));
    expect(button.className).toContain('text-muted');
    expect(button.textContent).toBe('100');
    await user.click(button);
    expect(await screen.findByText('Previous quota; refresh failed')).toBeTruthy();
    expect(screen.queryByText('Behind pace')).toBeNull();
  });

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
    expect(api.tokenPrices).not.toHaveBeenCalled();
    await user.click(button);
    const popover = within(await screen.findByRole('dialog', { name: 'Usage' }));
    await popover.findByRole('button', { name: 'Overview' });
    expect(summaryCost('Estimated cost').textContent).toBe('$1.87');
    await waitFor(() => expect(summaryCost('Without cache').textContent).toBe('$4.38'));
    expectPricingCaveatsHidden();
    const total = within(popover.getByRole('group', { name: 'Total token split' }));
    for (const label of ['Input', 'Output', 'Cache']) expect(total.getByText(label)).toBeTruthy();
    for (const count of ['102.5K', '45.2K', '1.2M']) expect(total.getByText(count)).toBeTruthy();
    const info = popover.getByRole('button', { name: 'About claude-sonnet-5 token split' });
    await user.hover(info);
    await waitFor(() => {
      const tooltip = document.querySelector('[data-popup="tooltip"]')!;
      for (const count of ['90K', '42K', '1.2M', '980K', '180K']) expect(tooltip.textContent).toContain(count);
      expect(tooltip.textContent).not.toContain('By tool');
    });
    await user.click(info);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
    await user.pointer([{ keys: '[TouchA]', target: info }]);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('980K'));
    await user.keyboard('{Escape}');
    await user.click(popover.getByRole('button', { name: /All models/ }));
    const table = within(popover.getByRole('table', { name: 'Today token usage by model' }));
    expect(table.getAllByText('—').length).toBeGreaterThan(0);
    for (const name of ['Model', 'Tokens', 'Est. cost', 'Cache hit']) expect(table.getByRole('columnheader', { name })).toBeTruthy();
    expect(table.getByRole('rowheader', { name: /claude-sonnet-5/ })).toBeTruthy();
    expect(table.queryByText('copilot', { exact: true })).toBeNull();
    for (const period of ['7 days', '30 days', 'Lifetime']) {
      await user.click(popover.getByRole('button', { name: period, exact: true }));
      expect(popover.getByRole('table', { name: `${period} token usage by model` })).toBeTruthy();
    }
    expect(popover.getAllByText('1.6B').length).toBeGreaterThan(0);
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

  test.each([false, true])('opens Token costs from the footer with Settings already open: %s', async (settingsOpen) => {
    const { user } = renderApp(settingsOpen ? '#settings' : '');
    const nav = await sidebar();
    if (settingsOpen) await user.click(screen.getByRole('button', { name: 'General', exact: true }));
    await user.click(nav.getByRole('button', { name: 'Usage', exact: true }));
    const popover = within(await screen.findByRole('dialog', { name: 'Usage' }));
    const notice = await popover.findByText('Prices missing for 1 model.');
    expect(notice.closest('.sr-only')).toBeNull();
    const addPrices = popover.getByRole('link', { name: 'Add prices' });
    expect(addPrices.getAttribute('href')).toBe('#settings');
    await user.click(addPrices);
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Usage' })).toBeNull());
    const prices = within(await screen.findByRole('region', { name: 'Token costs' }));
    await prices.findByText('Unpriced. Add input and output prices to estimate cost.');
    expect(prices.getByRole('checkbox', { name: 'Show all models' })).toHaveProperty('checked', false);
    expect(prices.getByRole('combobox', { name: 'Model', exact: true }).textContent).toContain('gpt-6-luna');
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
    expect(screen.getByRole('button', { name: 'About claude-sonnet-5 token split' })).toBeTruthy();
  });

  test('keeps descriptions in info tooltips accessible by hover, keyboard and touch', async () => {
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await screen.findByRole('button', { name: 'Overview' });
    for (const text of [/Costs use/, /Partial estimate: includes known costs only/, /UAM tracking started/]) {
      expect(screen.getByText(text).closest('.sr-only')).toBeTruthy();
    }
    const about = screen.getByRole('button', { name: 'About Usage' });
    await user.hover(about);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Cache'));
    await user.unhover(about);
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
    const cost = screen.getByRole('button', { name: 'About Estimated cost' });
    cost.focus();
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Costs use'));
    expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Partial estimate: includes known costs only.');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
    expect(screen.getByRole('dialog', { name: 'Usage' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'About Cost without cache' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toMatch(/partial|excluded|missing|without prices/i));
    await user.keyboard('{Escape}');
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')).toBeNull());
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
    await screen.findByRole('button', { name: 'About Unspecified model token split' });
    await user.click(screen.getByRole('button', { name: /All models/ }));
    const row = within(screen.getByRole('rowheader', { name: /Unspecified model/ }).closest('tr')!);
    expect(row.queryByText('crush', { exact: true })).toBeNull();
    expect(row.getByText('$2.50')).toBeTruthy();
    expect(summaryCost('Without cache').textContent).toBe('—');
    expect(summaryCost('Estimated cost').textContent).toBe('$2.50');
    expect(screen.queryByText('No usage recorded for this period.')).toBeNull();
    expect(screen.getByRole('status').textContent).toBe(`Last fetched ${new Date(report.collection.updated_at!).toLocaleString()}.`);
    expect(screen.getByText(/UAM tracking started/).textContent).toContain('local harness records and UAM task, subagent, and Background AI usage');
    expect(screen.getByText(/External Copilot usage/).textContent).toContain('requires local telemetry files');
    expect(screen.getByText(/External Copilot usage/).textContent).toContain(new Date(report.collection.copilot_since).toLocaleString());
    expect(screen.getByText(/Costs use/).textContent).toContain('source-reported amounts');
  });

  test.each([
    [null, '—'],
    [0, '$0.00'],
    [0.001, '<$0.01'],
    [0.01, '$0.01'],
    [2.5, '$2.50'],
    [12345.67, '$12,345.67'],
    [999999.99, '$999,999.99'],
    [1000000, '$1M'],
    [1234567, '$1.23M'],
    [1000000000, '$1B'],
    [1234567890, '$1.23B'],
  ] as const)('keeps pricing caveats in tooltips and formats model and total costs consistently for %s', async (cost, expected) => {
    const report = tokenUsageFixture();
    const model = { ...report.periods.today.models[0], cost_usd: cost, cost_partial: true };
    report.periods.today = { models: [model], total: model, cost_usd: cost, unpriced_models: 0 };
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await user.click(await screen.findByRole('button', { name: /All models/ }));
    expect(within(screen.getByRole('table')).getByText(expected)).toBeTruthy();
    expect(summaryCost('Estimated cost').textContent).toBe(expected);
    expectPricingCaveatsHidden();
    await user.click(screen.getByRole('button', { name: 'About claude-sonnet-5 token split' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toMatch(cost === null ? /unpriced|unavailable|missing|no .*cost/i : /partial/i));
  });

  test('uses compact counts throughout billion-scale model tooltips, including the source discrepancy', async () => {
    const report = tokenUsageFixture();
    const model = {
      ...report.periods.today.models[0], input: 2300000000, output: 34500000,
      cache_read: 1800000000, cache_write: 300000000, total: 2600000000,
    };
    report.periods.today = { models: [model], total: model, cost_usd: model.cost_usd, unpriced_models: 0 };
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await user.click(await screen.findByRole('button', { name: 'About claude-sonnet-5 token split' }));
    await waitFor(() => {
      const tooltip = document.querySelector('[data-popup="tooltip"]')!;
      for (const count of ['200M', '34.5M', '2.1B', '1.8B', '300M', '2.6B', '2.3B']) expect(tooltip.textContent).toContain(count);
      expect(tooltip.textContent).toMatch(/reported total/i);
      expect(tooltip.textContent).not.toMatch(/\d,\d{3}/);
      for (const titled of tooltip.querySelectorAll('[title]')) expect(titled.getAttribute('title')).not.toMatch(/\d,\d{3}/);
    });
  });

  test('shows cache hit beside model cost and tokens in both views, with unknown input shown as unavailable', async () => {
    const report = tokenUsageFixture();
    const model = { ...report.periods.today.models[0], input: 1000, output: 500, cache_read: 173, cache_write: 600, total: 1500, cost_usd: 2 };
    const unknown = { ...model, model: 'unknown-input', input: 0, cache_read: 0, cache_write: 0, total: 500 };
    report.periods.today.models = [model, unknown];
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    const overview = await screen.findByRole('figure');
    expect(overview.textContent).toContain('$2.00 (1.5K tokens) · 17.3% cache hit');
    expect(overview.textContent).toContain('— cache hit');
    await user.click(screen.getByRole('button', { name: /All models/ }));
    const table = within(screen.getByRole('table'));
    expect(table.getByRole('columnheader', { name: 'Cache hit' })).toBeTruthy();
    const row = within(table.getByRole('rowheader', { name: /claude-sonnet-5/ }).closest('tr')!);
    expect(row.getByRole('cell', { name: '17.3%' })).toBeTruthy();
    const missing = within(table.getByRole('rowheader', { name: /unknown-input/ }).closest('tr')!);
    expect(missing.getByRole('cell', { name: '—' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'About claude-sonnet-5 token split' }));
    await waitFor(() => expect(document.querySelector('[data-popup="tooltip"]')?.textContent).toContain('Cache hit: 17.3% of input tokens served by cache reads. Cache writes are not hits.'));
  });

  test('combines the same model across tools, limits the overview, and searches and sorts all models', async () => {
    const report = tokenUsageFixture();
    const sonnet = report.periods.today.models[0];
    const models = [
      ...report.periods.today.models,
      { ...sonnet, provider: 'claude', input: 200000, output: 8000, cache_read: 100000, cache_write: 0, total: 208000, cost_usd: 1 },
      ...Array.from({ length: 5 }, (_, i) => ({
        provider: 'opencode', model: `model-${i + 1}`, input: 18000 - i * 1000, output: 2000,
        cache_read: 1000, cache_write: 0, total: 20000 - i * 1000, cost_usd: i + 1,
      })),
    ];
    report.periods.today = {
      models,
      total: models.reduce((sum, row) => ({
        input: sum.input + row.input, output: sum.output + row.output,
        cache_read: sum.cache_read + row.cache_read, cache_write: sum.cache_write + row.cache_write,
        total: sum.total + row.total,
      }), { input: 0, output: 0, cache_read: 0, cache_write: 0, total: 0 }),
      cost_usd: models.reduce((sum, row) => sum + (row.cost_usd ?? 0), 0), unpriced_models: 1,
    };
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(report);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    const info = await screen.findByRole('button', { name: 'About claude-sonnet-5 token split' });
    expect(screen.getAllByRole('button', { name: 'About claude-sonnet-5 token split' })).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'About Other 2 models token split' })).toBeTruthy();
    expect(screen.getByRole('figure').textContent).toContain('$2.87 (1.5M tokens)');
    expect(screen.queryByRole('button', { name: 'About model-5 token split' })).toBeNull();
    await user.click(info);
    await waitFor(() => {
      const tooltip = document.querySelector('[data-popup="tooltip"]')!;
      expect(tooltip.textContent).toContain('190K');
      expect(tooltip.textContent).toContain('50K');
      expect(tooltip.textContent).toContain('1.3M');
      expect(tooltip.textContent).toContain('By tool');
      expect(tooltip.textContent).toMatch(/copilot/i);
      expect(tooltip.textContent).toMatch(/claude/i);
      expect(tooltip.textContent).toContain('1.3M tokens');
      expect(tooltip.textContent).toContain('208K tokens');
    });
    await user.keyboard('{Escape}');
    await user.click(screen.getByRole('button', { name: 'All models · 7' }));
    const table = within(screen.getByRole('table', { name: 'Today token usage by model' }));
    expect(table.getAllByRole('rowheader')).toHaveLength(7);
    const row = within(table.getByRole('rowheader', { name: /claude-sonnet-5/ }).closest('tr')!);
    expect(row.getByText('1.5M')).toBeTruthy();
    expect(row.getByText('$2.87')).toBeTruthy();
    await user.click(table.getByRole('button', { name: 'Est. cost' }));
    expect(table.getByRole('columnheader', { name: 'Est. cost' }).getAttribute('aria-sort')).toBe('descending');
    expect(table.getAllByRole('rowheader')[0].textContent).toContain('model-5');
    await user.click(table.getByRole('button', { name: 'Tokens' }));
    expect(table.getAllByRole('rowheader')[0].textContent).toContain('claude-sonnet-5');
    const search = screen.getByRole('textbox', { name: 'Find a model' });
    await user.type(search, 'SONNET');
    expect(table.getAllByRole('rowheader')).toHaveLength(1);
    expect(table.getByRole('rowheader', { name: /claude-sonnet-5/ })).toBeTruthy();
    await user.clear(search);
    await user.type(search, 'no-such-model');
    expect(table.queryAllByRole('rowheader')).toHaveLength(0);
    await user.clear(search);
    expect(table.getAllByRole('rowheader')).toHaveLength(7);
  });

  test('keeps recorded usage available when the no-cache price lookup fails', async () => {
    vi.mocked(api.tokenPrices).mockRejectedValue(new Error('prices offline'));
    vi.spyOn(api, 'tokenUsage').mockResolvedValue(tokenUsageFixture());
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    expect(await screen.findByRole('button', { name: 'About claude-sonnet-5 token split' })).toBeTruthy();
    await waitFor(() => expect(summaryCost('Without cache').textContent).toBe('—'));
    expect(summaryCost('Estimated cost').textContent).toBe('$1.87');
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByRole('group', { name: 'Total token split' })).toBeTruthy();
  });

  test('keeps polling usage while token prices remain pending', async () => {
    let refresh!: () => void;
    const setInterval = window.setInterval.bind(window);
    vi.spyOn(window, 'setInterval').mockImplementation((handler, timeout, ...args) => {
      if (timeout === 15000) refresh = handler as () => void;
      return setInterval(handler, timeout, ...args);
    });
    vi.mocked(api.tokenPrices).mockReturnValue(new Promise(() => {}));
    const refreshed = tokenUsageFixture();
    refreshed.periods.today = refreshed.periods['7d'];
    const read = vi.spyOn(api, 'tokenUsage').mockResolvedValueOnce(tokenUsageFixture()).mockResolvedValueOnce(refreshed);
    const user = userEvent.setup();
    render(<UsageButton />);
    await user.click(screen.getByRole('button', { name: 'Usage' }));
    await screen.findByRole('button', { name: 'About claude-sonnet-5 token split' });
    expect(summaryCost('Without cache').textContent).toBe('Loading…');
    refresh();
    await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByText('Total tokens').nextElementSibling!.textContent).toBe('9.2M'));
    expect(summaryCost('Estimated cost').textContent).toBe('$13.08');
    expect(api.tokenPrices).toHaveBeenCalledTimes(1);
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
    await screen.findByRole('button', { name: 'About claude-sonnet-5 token split' });
    refresh();
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringContaining('Showing the last read'));
    expect(screen.getByRole('button', { name: 'About claude-sonnet-5 token split' })).toBeTruthy();
  });
});
