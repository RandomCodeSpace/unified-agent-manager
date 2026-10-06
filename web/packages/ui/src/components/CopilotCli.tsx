import { useApi } from '../ApiContext';
import { useEffect, useEffectEvent, useId, useRef, useState, type ReactNode } from 'react';
import { describeError, type ProviderCLI, type ProviderInfo } from '../api';
import { dateTime, Loading, Note, timeAgo, useApp } from './common';
import { Button } from './ui/button';

/** The update on offer, or that there is none; nothing before a check has succeeded. */
function versionText(c: ProviderCLI): string {
  if (c.update_available) return `${c.latest} available`;
  if (c.incompatible && c.incompatible === c.latest) return `${c.incompatible} needs a newer UAM`;
  if (!c.latest) return '';
  if (c.manual && c.latest !== c.installed) return `Newest release ${c.latest}`;
  return 'Up to date';
}

/** The version line and when the server last looked: "1.0.92 available · Checked 5m ago". */
function statusText(c: ProviderCLI): ReactNode {
  const version = versionText(c);
  if (!c.checked_at) return version;
  return (
    <>
      {version && `${version} · `}Checked <time dateTime={c.checked_at} title={dateTime(c.checked_at)}>{timeAgo(c.checked_at)}</time>
    </>
  );
}

/**
 * Settings → GitHub Copilot → Copilot CLI: the version the server runs and the newest release, read each time the
 * section opens and checked again on request. Update installs the release the server picked, one the SDK accepts; it
 * runs on the server and outlives the page, so one in progress is picked up on open and read again every 2 s until it
 * ends. The server refuses it while a Copilot Task is working or waiting for an answer, and says why.
 */
export function CopilotCli({ provider }: Readonly<{ provider: ProviderInfo }>) {
  const api = useApi();
  const { refreshMeta } = useApp();
  const [cli, setCli] = useState<ProviderCLI | null>(null);
  const [error, setError] = useState<string | null>(null);
  // The first read takes what the server has; each Check again (and Retry) asks it to check first.
  const [reads, setReads] = useState(0);
  const [checking, setChecking] = useState(false);
  const [starting, setStarting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const titleId = useId();
  const name = provider.name;
  const refresh = useRef(refreshMeta);
  useEffect(() => {
    refresh.current = refreshMeta;
  });

  useEffect(() => {
    let current = true;
    api
      .providerCli(name, reads > 0)
      .then((c) => {
        if (!current) return;
        setCli(c);
        setError(null);
      })
      .catch((e: unknown) => current && setError(describeError(e)))
      .finally(() => current && setChecking(false));
    return () => {
      current = false;
    };
  }, [api, name, reads]);

  // The catalogs disagree with what was just read (an update ended, a check found a release): reload them, so the dot on Settings follows.
  const listed = provider.cli_update;
  useEffect(() => {
    if (!cli || cli.state === 'updating') return;
    if ((cli.update_available ? cli.latest : undefined) !== listed) refresh.current();
  }, [cli, listed]);

  function follow(next: ProviderCLI) {
    if (cli?.state === 'updating' && next.state === 'updated') setDone(`Copilot CLI updated to ${next.target ?? next.installed}.`);
    setCli(next);
  }
  const polled = useEffectEvent(follow);

  // Poll while the update runs; a hidden page skips its turns.
  const updating = cli?.state === 'updating';
  useEffect(() => {
    if (!updating) return;
    let current = true;
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      api
        .providerCli(name)
        .then((c) => current && polled(c))
        .catch(() => {});
    }, 2000);
    return () => {
      current = false;
      window.clearInterval(timer);
    };
  }, [api, name, updating]);

  function check() {
    setChecking(true);
    setDone(null);
    setReads((n) => n + 1);
  }

  async function update() {
    setStarting(true);
    setFormError(null);
    setDone(null);
    try {
      follow(await api.updateProviderCli(name));
    } catch (e) {
      setFormError(`Could not update: ${describeError(e)}`);
    } finally {
      setStarting(false);
    }
  }

  if (!cli) {
    return error ? (
      <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not read the Copilot CLI version: {error}</span>
        <Button size="sm" variant="secondary" loading={checking} onClick={check}>
          Retry
        </Button>
      </Note>
    ) : (
      <Loading label="Reading the Copilot CLI version…" />
    );
  }

  const failed = cli.state === 'failed';
  const target = (updating && cli.target) || cli.latest;
  const status = updating ? `Updating to ${target}…` : statusText(cli);
  return (
    <div role="group" aria-labelledby={titleId} className="flex flex-col gap-3">
      <div className="flex flex-wrap items-start gap-x-6 gap-y-3">
        <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1">
          <div className="flex items-baseline gap-2 text-ui">
            <h3 id={titleId} className="font-medium text-ink">
              Copilot CLI
            </h3>
            {cli.installed && <span className="text-muted tabular-nums">{cli.installed}</span>}
          </div>
          <div role="status" className="flex flex-col gap-1">
            {status && <Note>{status}</Note>}
            {updating && <Note>Open Copilot tasks reconnect when they are next used; new ones wait until the update finishes.</Note>}
            {!updating && cli.check_error && <Note className="[overflow-wrap:anywhere]">Could not check for a newer release: {cli.check_error}</Note>}
            {cli.manual && <Note className="[overflow-wrap:anywhere]">UAM cannot update it here: {cli.manual}</Note>}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!updating && (
            <Button size="sm" variant="ghost" loading={checking} disabled={starting} onClick={check}>
              Check again
            </Button>
          )}
          {(updating || (cli.update_available && !failed)) && (
            <Button size="md" variant="primary" loading={updating || starting} onClick={() => void update()}>
              Update to {target}
            </Button>
          )}
        </div>
      </div>
      {failed && (
        <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">
            Could not update{cli.target ? ` to ${cli.target}` : ''}: {cli.error || 'the update did not finish.'}
          </span>
          {cli.update_available && (
            <Button size="sm" variant="secondary" loading={starting} disabled={checking} onClick={() => void update()}>
              Retry
            </Button>
          )}
        </Note>
      )}
      {formError && (
        <Note tone="error" role="alert" className="[overflow-wrap:anywhere]">
          {formError}
        </Note>
      )}
      {done && <Note role="status">{done}</Note>}
    </div>
  );
}
