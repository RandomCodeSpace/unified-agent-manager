/**
 * Pages a Thread published with `uam_publish` (docs/web.md "Published pages"): a card on the call in the
 * conversation, and the Thread's list of them beside it, with their versions. Each version is an immutable
 * snapshot the service keeps; it opens in the file preview.
 */
import { useApi } from '../ApiContext';
import { AppWindow, ExternalLink, History, Trash2, X } from 'lucide-react';
import { memo, useContext, useEffect, useState } from 'react';
import { describeError, type ApiClient, type Artifact, type ArtifactVersion } from '../api';
import { popupOpen, threadCovered } from '../App';
import { formatSize } from '../lib/attachments';
import { ArtifactsContext, ArtifactsRevisionContext, usePreview, type OpenPreview, type PreviewTarget } from '../lib/previewContext';
import { Note, Skeleton, timeAgo } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { Button, buttonVariants } from './ui/button';
import { AlertDialog, useConfirm } from './ui/dialog';

/** A version's page in the file preview, framed as a page of the Thread's folder is. */
function versionTarget(api: ApiClient, sessionId: string, artifact: Artifact, v: ArtifactVersion): PreviewTarget {
  return { url: api.artifactUrl(sessionId, artifact.id, v.version), name: artifact.title, description: `${artifact.source}, version ${v.version}`, frameable: true };
}

const facts = (v: ArtifactVersion) => `${v.files} ${v.files === 1 ? 'file' : 'files'} · ${formatSize(v.bytes)} · ${timeAgo(v.at)}`;

// One list request per Thread and revision while it is in flight, shared by the Thread's cards and panel.
const reading = new Map<string, Promise<Artifact[]>>();
function readArtifacts(api: ApiClient, sessionId: string, revision: number): Promise<Artifact[]> {
  const key = `${sessionId}\n${revision}`;
  let list = reading.get(key);
  if (!list) {
    list = api.artifacts(sessionId);
    reading.set(key, list);
    const forget = () => { reading.delete(key); };
    list.then(forget, forget);
  }
  return list;
}

function useArtifacts(sessionId: string, revision: number) {
  const api = useApi();
  const [list, setList] = useState<Artifact[] | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let live = true;
    readArtifacts(api, sessionId, revision).then(
      (next) => { if (live) { setList(next); setError(''); } },
      (err: unknown) => { if (live) setError(describeError(err)); },
    );
    return () => { live = false; };
  }, [api, sessionId, revision]);
  return { list, setList, error };
}

/** A `uam_publish` call's card: the title, which version the call made, and Open. */
export const ArtifactCard = memo(function ArtifactCard({ sessionId, callId }: Readonly<{ sessionId: string; callId: string }>) {
  const api = useApi();
  const preview = usePreview();
  const openList = useContext(ArtifactsContext);
  const { list, error } = useArtifacts(sessionId, useContext(ArtifactsRevisionContext));
  if (error) return <Note tone="error" role="alert">Could not load this page: {error}</Note>;
  if (!list) return <Skeleton label="Loading the page…" rows={2} className="rounded-lg bg-raised p-4 shadow-raised" />;
  const artifact = list.find((a) => a.versions.some((v) => v.call_id === callId));
  const version = artifact?.versions.find((v) => v.call_id === callId);
  if (!artifact || !version) return <Note>This published version is no longer kept.</Note>;
  const latest = artifact.versions.at(-1)!;
  return (
    <section aria-label={`Published: ${artifact.title}`} className="flex flex-col gap-2 rounded-lg bg-raised p-3.5 shadow-raised">
      <header className="flex min-w-0 items-center gap-2">
        <AppWindow aria-hidden="true" className="size-4 shrink-0 text-accent" />
        <h3 className="min-w-0 flex-1 truncate text-ui font-medium text-ink" title={artifact.title}>{artifact.title}</h3>
        <span className="shrink-0 text-meta text-muted tabular-nums">Version {version.version}{latest !== version ? ` of ${latest.version}` : ''}</span>
      </header>
      <p className="text-meta text-muted">{facts(version)}</p>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="secondary" onClick={(e) => preview?.(versionTarget(api, sessionId, artifact, version), e.currentTarget)}>Open</Button>
        {latest !== version && <Button size="sm" variant="secondary" onClick={(e) => preview?.(versionTarget(api, sessionId, artifact, latest), e.currentTarget)}>Open version {latest.version}</Button>}
        {openList && artifact.versions.length > 1 && <Button size="sm" variant="subtle" onClick={openList}><History />Versions</Button>}
      </div>
    </section>
  );
});

type Doomed = { artifact: Artifact; version?: number };

/** The Thread's published pages beside the conversation, newest first, each with its versions. */
export function ArtifactsPanel({ sessionId, revision, inline, open, onClose, onClosed, onOpen }: Readonly<{
  sessionId: string;
  /** The Thread's `artifacts_revision`: a publish or delete elsewhere reads the list again. */
  revision: number;
  inline: boolean;
  /** False while the panel leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
  onOpen: OpenPreview;
}>) {
  const api = useApi();
  const { list, setList, error } = useArtifacts(sessionId, revision);
  const [notice, setNotice] = useState('');
  // Deleting removes the snapshot for good, so it is confirmed first (DESIGN.md Confirmations).
  const confirm = useConfirm<Doomed>();

  // Inline, the panel is no dialog: Esc closes it unless a popup owns the key.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented && !popupOpen() && !threadCovered()) onClose();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, onClose]);

  const remove = async ({ artifact, version }: Doomed) => {
    setNotice('');
    try {
      await api.deleteArtifact(sessionId, artifact.id, version);
      setList((all) => all?.flatMap((a) => {
        if (a.id !== artifact.id) return [a];
        const versions = version ? a.versions.filter((v) => v.version !== version) : [];
        return versions.length ? [{ ...a, versions }] : [];
      }) ?? all);
    } catch (err) {
      setNotice(`Could not delete: ${describeError(err)}`);
    }
  };
  const doomed = confirm.target;

  return (
    <SidePanel id="artifacts" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Published pages" defaultWidth={380}>
      <PanelHeader>
        <AppWindow aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <h2 className="min-w-0 flex-1 truncate text-title text-ink">Published pages</h2>
        <Button size="icon-md" aria-label="Close published pages" className="text-muted" onClick={onClose}><X /></Button>
      </PanelHeader>
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
      <div role="region" aria-label="This thread's published pages" tabIndex={0} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overflow-x-hidden overscroll-contain px-3 pt-1 pb-4">
        {error && <Note tone="error" role="alert">Could not load the published pages: {error}</Note>}
        {notice && <Note tone="warn" role="status">{notice}</Note>}
        {!list && !error && <Skeleton label="Loading the published pages…" rows={3} />}
        {list?.length === 0 && <Note>This thread has published no pages.</Note>}
        {list?.map((a) => (
          <section key={a.id} aria-label={a.title} className="flex flex-col gap-1 rounded-lg bg-raised p-3.5 shadow-raised">
            <div className="flex min-w-0 items-center gap-1">
              <h3 className="min-w-0 flex-1 truncate text-ui font-medium text-ink" title={a.title}>{a.title}</h3>
              <Button size="icon" aria-label={`Delete ${a.title}`} className="text-muted" onClick={() => confirm.ask({ artifact: a })}><Trash2 /></Button>
            </div>
            <p className="truncate text-meta text-muted" title={a.source}>{a.source}</p>
            <ul aria-label={`Versions of ${a.title}`} className="flex flex-col">
              {[...a.versions].reverse().map((v) => (
                <li key={v.version} className="flex min-w-0 items-center gap-1">
                  <Button size="sm" variant="subtle" className="min-w-0 flex-1 justify-start" aria-label={`Open version ${v.version} of ${a.title}`} onClick={(e) => onOpen(versionTarget(api, sessionId, a, v), e.currentTarget)}>
                    <span className="tabular-nums text-ink">Version {v.version}</span>
                    <span className="min-w-0 truncate font-normal text-muted">{facts(v)}</span>
                  </Button>
                  <a href={api.artifactUrl(sessionId, a.id, v.version)} target="_blank" rel="noopener noreferrer" aria-label={`Open version ${v.version} of ${a.title} in a new tab`} className={buttonVariants({ size: 'icon', className: 'text-muted' })}><ExternalLink /></a>
                  <Button size="icon" aria-label={`Delete version ${v.version} of ${a.title}`} className="text-muted" onClick={() => confirm.ask({ artifact: a, version: v.version })}><Trash2 /></Button>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
      <AlertDialog
        {...confirm.props}
        title={doomed?.version && doomed.artifact.versions.length > 1 ? `Delete version ${doomed.version}?` : `Delete ${doomed?.artifact.title ?? 'this page'}?`}
        description={doomed?.version && doomed.artifact.versions.length > 1 ? 'Its snapshot is removed. The other versions stay.' : 'Every version is removed. The files in the thread folder stay.'}
        confirmLabel="Delete"
        onConfirm={() => { confirm.close(); if (doomed) void remove(doomed); }}
      />
    </SidePanel>
  );
}
