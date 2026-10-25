import { Check, Copy, RefreshCw } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type SessionStats } from '../api';
import { useCopied } from '../lib/clipboard';
import { Note, Skeleton } from './common';
import { Button } from './ui/button';

/** A key that may wrap after each `_` or `-` (wbr: copied text stays the key). */
function breakable(name: string) {
  return name.split(/(?<=[_-])/).flatMap((part, i) => (i ? [<wbr key={i} />, part] : [part]));
}

/**
 * Settings → Session stats: counts over every Copilot session journal on the server, grouped by key prefix, laid out
 * to fit one screenshot. Opening the page or Scan again rescans and rewrites the key=value file.
 */
export function SessionStatsSettings() {
  const api = useApi();
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [scanning, setScanning] = useState(false);
  const scan = useCallback(() => api.sessionStats().then((s) => { setStats(s); setError(null); }, (e: unknown) => setError(describeError(e))).finally(() => setScanning(false)), [api]);
  useEffect(() => { void scan(); }, [scan]);
  const [copied, copy] = useCopied();
  if (!stats) return error ? <Note tone="error" role="alert">Could not read the sessions: {error}</Note> : <Skeleton label="Reading every session…" rows={4} />;
  const groups = new Map<string, { name: string; value: string }[]>();
  const scanned = Object.fromEntries(stats.stats.filter((s) => s.key.startsWith('scan.')).map((s) => [s.key, s.value]));
  for (const { key, value } of stats.stats) {
    if (key.startsWith('scan.')) continue;
    const dot = key.indexOf('.');
    const group = key.slice(0, dot);
    groups.set(group, [...(groups.get(group) ?? []), { name: key.slice(dot + 1), value }]);
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="flex min-w-0 flex-1 flex-col text-caption text-muted">
          <span className="truncate font-mono">{stats.path}</span>
          <span className="tabular-nums">Scanned {scanned['scan.at']?.replace('T', ' ').replace('Z', ' UTC')}{scanned['scan.skipped'] !== '0' && ` · ${scanned['scan.skipped']} unreadable lines skipped`}</span>
        </span>
        <Button size="sm" variant="secondary" disabled={scanning} onClick={() => { setScanning(true); void scan(); }}>
          <RefreshCw aria-hidden="true" className={scanning ? 'animate-spin' : undefined} />
          Scan again
        </Button>
        {/* The same key=value lines as the file, for pasting where the file cannot go. */}
        <Button size="sm" variant="secondary" onClick={() => copy(stats.stats.map((s) => `${s.key}=${s.value}\n`).join(''))}>
          {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      {error && <Note tone="error" role="alert">{error}</Note>}
      {/* Columns, not a grid: groups of different lengths pack without gaps, so the whole page fits one screenshot. */}
      <div className="columns-2 gap-x-4 sm:gap-x-8 2xl:columns-3">
        {[...groups].map(([group, rows]) => (
          <section key={group} aria-labelledby={`session-stats-${group}`} className="mb-4 flex min-w-0 break-inside-avoid flex-col gap-0.5">
            <h3 id={`session-stats-${group}`} className="text-eyebrow uppercase text-muted">{group}</h3>
            <dl className="flex flex-col">
              {rows.map((row) => (
                <div key={row.name} className="flex items-baseline gap-3 border-b border-hairline py-0.5 text-caption last:border-b-0">
                  <dt className="min-w-0 flex-1 font-mono break-words text-body">{breakable(row.name)}</dt>
                  <dd className="font-medium text-ink tabular-nums">{row.value}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </div>
  );
}
