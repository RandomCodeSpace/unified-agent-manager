import { useEffect, useMemo, useState } from 'react';
import { parsePatch, structuredPatch, type StructuredPatch } from 'diff';
import { api, describeError, type Changes as ChangesData, type FileDiff, type Scope, type SessionSummary } from '../api';

export function Changes({ session }: { session: SessionSummary }) {
  const canSession = session.capabilities.session_diff;
  const [scopePref, setScopePref] = useState<Scope>('session');
  const scope: Scope = canSession ? scopePref : 'workspace';
  const [changes, setChanges] = useState<ChangesData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [path, setPath] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let live = true;
    setLoading(true);
    setError(null);
    api
      .changes(session.id, scope)
      .then((c) => live && setChanges(c))
      .catch((e: unknown) => live && setError(describeError(e)))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [session.id, scope, tick]);

  return (
    <>
      <div className="panel-head">
        <h2>Changes</h2>
        <button type="button" className="btn small" onClick={() => setTick((t) => t + 1)} disabled={loading}>
          Refresh
        </button>
      </div>
      {canSession && (
        <div className="segmented" role="group" aria-label="Scope">
          {(['session', 'workspace'] as const).map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={scope === s}
              onClick={() => {
                setScopePref(s);
                setPath(null);
              }}
            >
              {s === 'session' ? 'Session' : 'Workspace'}
            </button>
          ))}
        </div>
      )}
      <div className="changes-body">
        {changes && <p className="scope-label">{changes.label}</p>}
        {loading && <p className="muted">Loading…</p>}
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        {changes && !changes.supported && <p className="muted">{changes.reason || 'Not available for this session.'}</p>}
        {changes?.supported && changes.files.length === 0 && !loading && <p className="muted">No changes.</p>}
        {changes?.supported && changes.files.length > 0 && (
          <ul className="file-list">
            {changes.files.map((f) => (
              <li key={f.path}>
                <button
                  type="button"
                  className="file-row"
                  aria-pressed={f.path === path}
                  onClick={() => setPath(f.path)}
                  title={f.path}
                >
                  <span className="file-status">{f.status}</span>
                  <span className="file-path">{f.path}</span>
                  <span className="file-counts">
                    <span className="add">+{f.additions}</span> <span className="del">-{f.deletions}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {path && <FileView key={`${scope}:${path}:${tick}`} sessionId={session.id} scope={scope} path={path} />}
      </div>
    </>
  );
}

function FileView({ sessionId, scope, path }: { sessionId: string; scope: Scope; path: string }) {
  const [file, setFile] = useState<FileDiff | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .changeFile(sessionId, scope, path)
      .then((f) => live && setFile(f))
      .catch((e: unknown) => live && setError(describeError(e)));
    return () => {
      live = false;
    };
  }, [sessionId, scope, path]);

  const patch = useMemo<StructuredPatch | null | Error>(() => {
    if (!file) return null;
    try {
      if (file.patch) return parsePatch(file.patch)[0] ?? null;
      return structuredPatch(file.path, file.path, file.before ?? '', file.after ?? '');
    } catch (e) {
      return e instanceof Error ? e : new Error(String(e));
    }
  }, [file]);

  if (error) {
    return (
      <p className="error" role="alert">
        {error}
      </p>
    );
  }
  if (!file) return <p className="muted">Loading diff…</p>;
  if (patch instanceof Error) return <p className="error">Could not parse diff: {patch.message}</p>;
  if (!patch || patch.hunks.length === 0) return <p className="muted">No textual changes in {file.path}.</p>;

  return (
    <table className="diff">
      <caption>{file.path}</caption>
      <tbody>{patch.hunks.flatMap((h, hi) => renderHunk(h, hi))}</tbody>
    </table>
  );
}

function renderHunk(h: StructuredPatch['hunks'][number], hi: number) {
  let oldNo = h.oldStart;
  let newNo = h.newStart;
  const rows = [
    <tr key={`h${hi}`} className="hunk">
      <td colSpan={3}>{`@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@`}</td>
    </tr>,
  ];
  h.lines.forEach((line, li) => {
    const sign = line[0] ?? ' ';
    const text = line.slice(1);
    let cls = 'ctx';
    let left: number | '' = '';
    let right: number | '' = '';
    if (sign === '+') {
      cls = 'add';
      right = newNo++;
    } else if (sign === '-') {
      cls = 'del';
      left = oldNo++;
    } else if (sign === '\\') {
      cls = 'meta';
    } else {
      left = oldNo++;
      right = newNo++;
    }
    rows.push(
      <tr key={`${hi}-${li}`} className={cls}>
        <td className="num">{left}</td>
        <td className="num">{right}</td>
        <td className="code">
          <span className="sign">{sign}</span>
          {text}
        </td>
      </tr>,
    );
  });
  return rows;
}
