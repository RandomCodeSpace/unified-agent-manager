import { useCallback, useLayoutEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { describeError, newRequestId } from '../api';
import type { FederationView, Machine } from '../FederationContext';
import type { UnsavedTarget, useUnsavedGuard } from '../components/SettleGuard';
import type { Renaming, TaskActions } from '../components/taskActions';
import { saveBlob } from './assist';
import { putAwayWork } from './tasks';
import { HASH_PREFIX } from './useHashRoute';

const NO_BUSY: Readonly<Record<string, boolean>> = {};

type Guard = ReturnType<typeof useUnsavedGuard>;

/**
 * With connected instances, the row actions of the machines not on screen: the same verbs as this machine's, each
 * run against its own machine's client, with that machine's busy rows and its failures on the notice line.
 */
export function useMachineActions({ machines, metaCache, go, setNotice, renaming, setRenaming, openTaskDialog, askUnsaved, askArchive }: {
  machines: Machine[] | undefined;
  metaCache: FederationView['metaCache'] | undefined;
  go: FederationView['go'] | undefined;
  setNotice: Dispatch<SetStateAction<string | null>>;
  renaming: (Renaming & { owner?: string }) | null;
  setRenaming: Dispatch<SetStateAction<(Renaming & { owner?: string }) | null>>;
  openTaskDialog: (d: { kind: 'delete' | 'close'; id: string; owner?: string }) => void;
  askUnsaved: Guard['askUnsaved'];
  askArchive: Guard['askArchive'];
}) {
  /** Another machine's lifecycle request: its stream brings the result; a failure becomes the notice line, naming the machine. */
  const [remoteBusy, setRemoteBusy] = useState<Readonly<Record<string, Readonly<Record<string, boolean>>>>>({});
  const runRemote = useCallback(async (m: Machine, id: string, op: () => Promise<unknown>, verb: string) => {
    setRemoteBusy((b) => ({ ...b, [m.id]: { ...b[m.id], [id]: true } }));
    setNotice(null);
    try {
      await op();
    } catch (e) {
      setNotice(`Could not ${verb} on ${m.label}: ${describeError(e)}`);
      throw e;
    } finally {
      setRemoteBusy(({ [m.id]: mine = {}, ...rest }) => {
        const { [id]: _, ...left } = mine;
        return { ...rest, [m.id]: left };
      });
    }
  }, [setNotice]);
  /** Another machine's Thread in the confirmation: its forced request, run against that machine. */
  const remoteTarget = useCallback((m: Machine, id: string, kind: UnsavedTarget['kind']) => (refused: Pick<UnsavedTarget, 'pending' | 'paths' | 'anchor'>): UnsavedTarget => (
    { kind, ...refused, force: () => runRemote(m, id, () => m.client.stage(id, kind, { force: true }), `${kind} the thread`) }
  ), [runRemote]);
  /** Another machine's Settle or Archive: the same confirmation, its forced request run against that machine. */
  const remoteAway = useCallback((m: Machine, id: string, kind: UnsavedTarget['kind']) => askUnsaved(
    putAwayWork(m.state.sessions.find((s) => s.id === id)),
    () => m.client.stage(id, kind),
    remoteTarget(m, id, kind),
    () => m.client.changes(id, 'workspace'),
  ), [askUnsaved, remoteTarget]);
  // Row actions read their machine when they run: the machines are rebuilt on every streamed frame, and a new
  // context value would re-render each of that machine's rows.
  const latestMachines = useRef(machines);
  useLayoutEffect(() => { latestMachines.current = machines; }, [machines]);
  const remoteKey = JSON.stringify((machines ?? []).filter((m) => !m.active).map((m) => [m.id, !!(m.connection?.capabilities ?? metaCache?.get(m.id)?.capabilities)?.includes('unarchive-v1')] as const));
  /** The row actions of each machine not on screen: the same verbs, run against that machine's own client. */
  const machineActions = useMemo(() => new Map((JSON.parse(remoteKey) as [string, boolean][]).map(([owner, unarchive]): [string, TaskActions] => {
    const machine = () => latestMachines.current?.find((m) => m.id === owner);
    const run = (id: string, op: (m: Machine) => Promise<unknown>, verb: string) => { const m = machine(); if (m) void runRemote(m, id, () => op(m), verb).catch(() => {}); };
    const open = (id: string) => go?.(owner, `${HASH_PREFIX}${encodeURIComponent(id)}`);
    return [owner, {
      select: open,
      startRename: (id, place) => setRenaming({ id, place, owner }),
      renaming: renaming?.owner === owner ? renaming : null,
      cancelRename: () => setRenaming(null),
      rename: async (id, name) => {
        setRenaming(null);
        const m = machine();
        const current = m?.state.sessions.find((s) => s.id === id);
        if (!m || !current || current.name === name) return;
        await runRemote(m, id, () => m.client.rename(id, name), 'rename the thread').catch(() => {});
      },
      settle: (id) => run(id, (m) => remoteAway(m, id, 'settle'), 'settle the thread'),
      reopen: (id) => run(id, (m) => m.client.stage(id, 'reopen'), 'reopen the thread'),
      unarchive: unarchive ? (id) => run(id, async (m) => {
        await m.client.stage(id, 'unarchive');
        open(id);
      }, 'unarchive the thread') : undefined,
      archive: (id) => {
        const m = machine();
        if (!m) return;
        const s = m.state.sessions.find((x) => x.id === id);
        if (s && putAwayWork(s)) run(id, () => remoteAway(m, id, 'archive'), 'archive the thread');
        else askArchive(() => m.client.archiveCheck(id), remoteTarget(m, id, 'archive'), () => runRemote(m, id, () => remoteAway(m, id, 'archive'), 'archive the thread'));
      },
      remove: (id) => openTaskDialog({ kind: 'delete', id, owner }),
      close: (id) => openTaskDialog({ kind: 'close', id, owner }),
      busy: remoteBusy[owner] ?? NO_BUSY,
      runAgain: (id) => run(id, async (m) => { open((await m.client.rerun(id, { request_id: newRequestId() })).id); }, 'run the thread again'),
      exportMarkdown: (id) => run(id, async (m) => { const f = await m.client.exportMarkdown(id); saveBlob(f.blob, f.name); }, 'export the thread'),
    }];
  })), [remoteKey, renaming, remoteBusy, runRemote, go, openTaskDialog, remoteAway, askArchive, remoteTarget, setRenaming]);
  return { machineActions, runRemote, remoteBusy };
}
