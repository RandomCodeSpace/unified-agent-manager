import { useState, type FormEvent } from 'react';
import { api, ApiError, describeError } from '../api';

export function Login({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.login(token);
      setToken('');
      onLoggedIn();
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? 'That token was not accepted.' : describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <form className="login-card form" onSubmit={submit}>
        <h1>UAM</h1>
        <p>
          Enter the access token for this server. To see it, run <code>uam web</code> on the server; it prints the token whether or not the service is already running.
        </p>
        <label>
          Access token
          <input
            type="password"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            required
            autoFocus
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </label>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <button type="submit" className="btn primary" disabled={busy || !token}>
            Log in
          </button>
        </div>
      </form>
    </main>
  );
}
