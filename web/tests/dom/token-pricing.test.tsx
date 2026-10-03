import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { renderApp, sidebar } from './render';

describe('model token prices', () => {
  test('saves input/output prices without cache, persists on reopening, and recalculates Usage', async () => {
    const { user } = renderApp('#settings');
    await sidebar();
    await user.click(screen.getByRole('button', { name: 'Models', exact: true }));
    const section = within(await screen.findByRole('region', { name: 'Token costs' }));
    await section.findByText('Unpriced. Add input and output prices to estimate cost.');
    const save = section.getByRole('button', { name: 'Save token prices' });
    expect(save).toHaveProperty('disabled', true);
    await user.type(section.getByRole('spinbutton', { name: 'Input', exact: true }), '2');
    await user.type(section.getByRole('spinbutton', { name: 'Output', exact: true }), '10');
    expect(section.getByRole('spinbutton', { name: 'Cache read', exact: true })).toHaveProperty('value', '');
    expect(section.getByRole('spinbutton', { name: 'Cache write', exact: true })).toHaveProperty('value', '');
    await user.click(save);
    await section.findByText('Manual prices');
    await user.click(screen.getByRole('button', { name: 'Usage', exact: true }));
    const popover = within(await screen.findByRole('dialog', { name: 'Usage' }));
    await popover.findByText('$0.08');
    expect(popover.queryByText(/models? (is|are) unpriced/)).toBeNull();
    await user.click(popover.getByRole('button', { name: 'Close usage' }));
    await user.click(screen.getByRole('button', { name: 'General', exact: true }));
    await user.click(screen.getByRole('button', { name: 'Models', exact: true }));
    const reopened = within(await screen.findByRole('region', { name: 'Token costs' }));
    await user.click(await reopened.findByRole('combobox', { name: 'Model', exact: true }));
    await user.click(await screen.findByRole('option', { name: /copilot · gpt-6-luna/ }));
    await reopened.findByText('Manual prices');
    expect(reopened.getByRole('spinbutton', { name: 'Input', exact: true })).toHaveProperty('value', '2');
    // Zero is an explicit cache rate; it must not be treated as an absent one.
    await user.type(reopened.getByRole('spinbutton', { name: 'Cache read', exact: true }), '0');
    await user.click(reopened.getByRole('button', { name: 'Save token prices' }));
    await waitFor(() => expect(reopened.getByRole('button', { name: 'Remove override' })).toHaveProperty('disabled', false));
    await waitFor(() => expect(reopened.getByRole('spinbutton', { name: 'Cache read', exact: true })).toHaveProperty('value', '0'));
    await user.click(reopened.getByRole('button', { name: 'Remove override' }));
    await reopened.findByText('Unpriced. Add input and output prices to estimate cost.');
  });
});
