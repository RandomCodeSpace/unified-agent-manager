import { useEffect, useRef, useState, type FormEvent } from 'react';
import { api, describeError, isStatus } from '../api';
import { Note } from './common';
import { Brand } from './Sidebar';
import { Button } from './ui/button';

export function Login({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);
  // The page has one purpose; the field takes focus when it appears.
  useEffect(() => {
    field.current?.focus();
  }, []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.login(token);
      setToken('');
      onLoggedIn();
    } catch (err) {
      setError(isStatus(err, 401) ? 'That token was not accepted.' : describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="grid min-h-dvh place-items-center bg-canvas px-6 py-10">
      <form className="flex w-full max-w-[360px] flex-col gap-5 animate-rise" onSubmit={submit}>
        <div className="flex flex-col gap-3">
          <Brand markOnly className="[&_svg]:size-8" />
          <h1 className="text-display-md">Sign in to uam</h1>
          <p className="text-ui text-muted">
            Enter the access token for this server. To see it, run <code className="rounded-xs bg-sunken px-1 font-mono text-code-sm text-ink">uam web</code> on the server; it prints the token whether or not the service is already
            running.
          </p>
        </div>
        <div className="flex flex-col gap-1">
          <label htmlFor="token" className="text-caption text-muted">
            Access token
          </label>
          <input
            id="token"
            className="h-10 w-full rounded-sm border border-hairline-strong bg-raised px-3 font-mono text-code text-ink outline-hidden transition-colors focus:border-accent"
            type="password"
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            required
            ref={field}
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </div>
        {error && (
          <Note tone="error" role="alert">
            {error}
          </Note>
        )}
        <div>
          <Button type="submit" variant="primary" size="lg" disabled={busy || !token} className="min-w-28">
            {busy ? 'Signing in…' : 'Sign in'}
          </Button>
        </div>
      </form>
    </main>
  );
}
