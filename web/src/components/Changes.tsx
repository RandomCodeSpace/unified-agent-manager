import { useEffect, useMemo, useRef, useState } from 'react';
import { parsePatch, structuredPatch, type StructuredPatch } from 'diff';
import { api, describeError, type Changes as ChangesData, type FileDiff, type Scope, type SessionSummary } from '../api';

/** Scope the meta line counts: the provider's own diff when it has one, else the working tree. */
export function defaultScope(s: SessionSummary): Scope {
  return s.capabilities.session_diff ? 'session' : 'workspace';
}

/**
 * The Changes sheet: file list plus one unified diff. `changes` for the default scope is
 * owned by the Task view (it feeds the "n files changed" link); other scopes load here.
 */
export function ChangesSheet({
  session,
  projectName,
  changes,
  onRefresh,
  onClose,
}: {
  session: SessionSummary;
  projectName: string;
  changes: ChangesData | null;
  onRefresh: () => void;
  onClose: () => void;
}) {
  const canSession = session.capabilities.session_diff;
  const [scope, setScope] = useState<Scope>(defaultScope(session));
  const [other, setOther] = useState<ChangesData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [path, setPath] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus();
  }, []);

  const isDefault = scope === defaultScope(session);
  useEffect(() => {
    if (isDefault) return;
    let live = true;
    setError(null);
    api
      .changes(session.id, scope)
      .then((c) => live && setOther(c))
      .catch((e: unknown) => live && setError(describeError(e)));
    return () => {
      live = false;
    };
  }, [session.id, scope, isDefault, tick]);

  const data = isDefault ? changes : other;
  const files = data?.supported ? data.files : [];
  const shownPath = path && files.some((f) => f.path === path) ? path : (files[0]?.path ?? null);

  function refresh() {
    setTick((t) => t + 1);
    if (isDefault) onRefresh();
  }

  return (
    <section className="sheet" role="dialog" aria-modal="true" aria-label="Changes">
      <div className="sheet-head">
        <div>
          <div className="label">Changes</div>
          <span className="muted small">{data ? data.label : `${projectName} vs HEAD`}</span>
        </div>
        <span className="spacer" />
        {canSession && (
          <div className="segmented" role="group" aria-label="Scope">
            {(['session', 'workspace'] as const).map((s) => (
              <button key={s} type="button" aria-pressed={scope === s} onClick={() => setScope(s)}>
                {s === 'session' ? 'This task' : 'Workspace'}
              </button>
            ))}
          </div>
        )}
        <button type="button" className="pill pill-text pill-sm" onClick={refresh}>
          Refresh
        </button>
        <button ref={closeRef} type="button" className="iconbtn" aria-label="Close changes" onClick={onClose}>
          ×
        </button>
      </div>
      <ul className="files">
        {error && (
          <li className="error pad" role="alert">
            {error}
          </li>
        )}
        {!data && !error && <li className="muted small pad">Loading…</li>}
        {data && !data.supported && <li className="muted small pad">{data.reason || 'Not available for this task.'}</li>}
        {data?.supported && files.length === 0 && <li className="muted small pad">No changes.</li>}
        {files.map((f) => (
          <li key={f.path}>
            <button type="button" className="file" aria-pressed={f.path === shownPath} onClick={() => setPath(f.path)} title={f.path}>
              <span className="file-status mono">{f.status}</span>
              <span className="file-path mono">{f.path}</span>
              <span className="file-counts mono">
                <span className="add">+{f.additions}</span> <span className="del">−{f.deletions}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      <div className="sheet-diff">
        {shownPath && <FileView key={`${scope}:${shownPath}:${tick}`} sessionId={session.id} scope={scope} path={shownPath} />}
      </div>
    </section>
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
  if (!file) return <p className="muted small">Loading diff…</p>;
  if (patch instanceof Error) return <p className="error">Could not parse diff: {patch.message}</p>;
  if (!patch || patch.hunks.length === 0) return <p className="muted small">No textual changes in {file.path}.</p>;

  return (
    <table className="diff">
      <caption className="mono">{file.path}</caption>
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
        <td className="code-cell">
          <span className="sign">{sign}</span>
          {text}
        </td>
      </tr>,
    );
  });
  return rows;
}
