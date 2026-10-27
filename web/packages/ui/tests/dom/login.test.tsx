import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { api, ApiError } from '../../src/api';
import { Login } from '../../src/components/Login';

afterEach(() => vi.restoreAllMocks());

test.each([
  [401, 'invalid access token', 'That token was not accepted.'],
  [429, 'too many sign-in attempts; try again later', 'Too many sign-in attempts. Wait a few minutes, then try again.'],
  [500, 'internal error', 'internal error'],
])('a %i sign-in says why', async (status, message, shown) => {
  vi.spyOn(api, 'login').mockRejectedValue(new ApiError(status, message));
  const signedIn = vi.fn();
  render(<ApiContext.Provider value={api}><Login onLoggedIn={signedIn} /></ApiContext.Provider>);
  await userEvent.type(screen.getByLabelText('Access token'), 'some-token');
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  expect((await screen.findByRole('alert')).textContent).toBe(shown);
  expect(signedIn).not.toHaveBeenCalled();
});
