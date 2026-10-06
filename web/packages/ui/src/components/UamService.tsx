import { useApi } from '../ApiContext';
import { useEffect, useEffectEvent, useId, useRef, useState } from 'react';
import { describeError, type ServiceStatus } from '../api';
import { Loading, Note, useApp } from './common';
import { Button } from './ui/button';

/** A restart is on offer or under way: Settings and its General tab carry a dot. */
export function restartOffered(status: ServiceStatus | undefined): boolean {
  return !!status?.installed && (status.restart === 'available' || status.restart === 'pending');
}

/** The Settings button's notice while a restart is on offer. */
export function restartNotice(status: ServiceStatus | undefined): string | undefined {
  return restartOffered(status) ? `UAM ${status!.installed} installed; restart to run it` : undefined;
}

/**
 * Settings → General → UAM: the version the service runs and, once another one is installed at its binary's path,
 * Restart. The service waits until no Task works or waits, then stops as `uam web stop` does and starts the installed
 * binary in its place; the page reconnects and reloads onto the new version. While a restart waits or runs, the status
 * is read again every 2 s.
 */
export function UamService() {
  const api = useApi();
  const { meta, refreshMeta } = useApp();
  const [status, setStatus] = useState<ServiceStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reads, setReads] = useState(0);
  const [starting, setStarting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const titleId = useId();
  const refresh = useRef(refreshMeta);
  useEffect(() => {
    refresh.current = refreshMeta;
  });

  useEffect(() => {
    let current = true;
    api
      .service()
      .then((s) => {
        if (!current) return;
        setStatus(s);
        setError(null);
      })
      .catch((e: unknown) => current && setError(describeError(e)));
    return () => {
      current = false;
    };
  }, [api, reads]);

  // The catalogs disagree with what was just read: reload them, so the dot on Settings follows.
  const listed = restartOffered(meta?.service);
  useEffect(() => {
    if (status && status.restart !== 'restarting' && restartOffered(status) !== listed) refresh.current();
  }, [status, listed]);

  function follow(next: ServiceStatus) {
    // A connected instance comes back on its own; the home service's page reloads instead.
    if (status && status.running !== next.running) setDone(`UAM restarted on ${next.running}.`);
    setStatus(next);
  }
  const polled = useEffectEvent(follow);

  // Poll while the restart waits or runs; a hidden page skips its turns, and a service on its way down is not an error.
  const waiting = status?.restart === 'pending' || status?.restart === 'restarting';
  useEffect(() => {
    if (!waiting) return;
    let current = true;
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      api
        .service()
        .then((s) => current && polled(s))
        .catch(() => {});
    }, 2000);
    return () => {
      current = false;
      window.clearInterval(timer);
    };
  }, [api, waiting]);

  async function restart() {
    setStarting(true);
    setFormError(null);
    setDone(null);
    try {
      follow(await api.restartService());
    } catch (e) {
      setFormError(`Could not restart: ${describeError(e)}`);
    } finally {
      setStarting(false);
    }
  }

  if (!status) {
    return error ? (
      <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not read the UAM version: {error}</span>
        <Button size="sm" variant="secondary" onClick={() => setReads((n) => n + 1)}>
          Retry
        </Button>
      </Note>
    ) : (
      <Loading label="Reading the UAM version…" />
    );
  }

  const newer = status.installed && status.installed !== status.running ? status.installed : null;
  return (
    <div role="group" aria-labelledby={titleId} className="flex flex-col gap-3">
      <div className="flex flex-wrap items-start gap-x-6 gap-y-3">
        <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1">
          <div className="flex items-baseline gap-2 text-ui">
            <h3 id={titleId} className="font-medium text-ink">
              Version
            </h3>
            <span className="text-muted tabular-nums [overflow-wrap:anywhere]">{status.running}</span>
          </div>
          <div role="status" className="flex flex-col gap-1">
            {newer && status.restart !== 'restarting' && (
              <Note className="[overflow-wrap:anywhere]">
                UAM {newer} is installed (running {status.running}).
              </Note>
            )}
            {status.restart === 'pending' && <Note>Restarts when no task is working or waiting.</Note>}
            {status.restart === 'restarting' && <Note className="[overflow-wrap:anywhere]">Restarting onto UAM {newer ?? status.installed}…</Note>}
            {status.restart === 'none' && !status.error && <Note>The installed version is running.</Note>}
            {status.error && <Note className="[overflow-wrap:anywhere]">Could not read the installed UAM: {status.error}</Note>}
          </div>
        </div>
        {(status.restart === 'available' || status.restart === 'restarting') && (
          <Button size="md" variant="primary" loading={starting || status.restart === 'restarting'} onClick={() => void restart()}>
            Restart
          </Button>
        )}
      </div>
      {formError && (
        <Note tone="error" role="alert" className="[overflow-wrap:anywhere]">
          {formError}
        </Note>
      )}
      {done && <Note role="status">{done}</Note>}
    </div>
  );
}
