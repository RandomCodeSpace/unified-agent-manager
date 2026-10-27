// What the owner sees when the page or the service fails: a render error gets one full-page
// message instead of a blank page, and a failed save of the Threads a strip until a save succeeds.
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { AppFailed } from '../../src/UamApp';
import { VisualBoundary } from '../../src/components/VisualBoundary';
import { renderApp, sidebar } from './render';

describe('a render error', () => {
  test.each([
    ['an Error', new Error('cannot read properties of undefined'), 'cannot read properties of undefined'],
    ['a value', 'broken', 'broken'],
  ])('shows the app fallback with its message and Reload (%s thrown)', (_kind, thrown, message) => {
    const quiet = vi.spyOn(console, 'error').mockImplementation(() => {});
    const Boom = () => { throw thrown; };
    const view = render(<VisualBoundary fallback={(error) => <AppFailed error={error} />}><Boom /></VisualBoundary>);
    const alert = view.getByRole('alert');
    expect(alert.querySelector('h1')?.textContent).toBe('UAM could not show this page');
    expect(view.getByText(message)).toBeTruthy();
    expect(view.getByRole('button', { name: 'Reload' })).toBeTruthy();
    expect(alert.textContent).not.toMatch(/at Boom/);
    // React reports what it caught to the console.
    expect(quiet).toHaveBeenCalled();
    quiet.mockRestore();
  });
});

describe('a failed save', () => {
  test('shows a strip over the pane until a save succeeds', async () => {
    const { mock } = renderApp();
    await sidebar();
    const strip = 'Threads could not be saved: read sessions.json: no space left on device. UAM tries again with the next change.';
    mock.broadcast('save_status', { save_error: 'read sessions.json: no space left on device' });
    expect(await screen.findByText(strip)).toBeTruthy();
    mock.broadcast('save_status', {});
    await waitFor(() => expect(screen.queryByText(strip)).toBeNull());
  });
});
