import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import App from './App';
import { ApiContext } from './ApiContext';
import { api, createApiClient, describeError, errorCode, subscribeAuthLoss, UPDATE_EVENTS, type ApiClient, type AddConnectionInput, type ConnectedInstance, type ConnectedStatus, type Meta, type SnapshotData, type UpdateConnectionInput, type UpdateData } from './api';
import { FederationContext, type Machine, type MachineChoice, type MachineFilter, type MachineIntent, type CarriedShell, type ShellCarry } from './FederationContext';
import { ConnectedInstancesSettings } from './components/ConnectedInstancesSettings';
import { Button } from './components/ui/button';
import { Select } from './components/ui/select';
import { initialState, reducer, type State, type Action } from './state';
import { hasIdentity, identityHash, resolveIdentity } from './lib/instanceIdentity';
import { needsYouCount, newsReader } from './lib/tasks';
import { pendingRequests } from './lib/board';
import { handleNotice, setNotificationSource, startNotifications, type Notice } from './lib/notify';
import { forgetArchive, readMarks } from './lib/historyArchive';

interface SourceState { state: State; status: ConnectedStatus }
interface Registry { instance_id: string; connections: ConnectedInstance[] }
interface Source { id: string; label: string; connection: ConnectedInstance | null; client: ApiClient }
const HOME = '';
const homeSource: Source = { id: HOME, label: 'This instance', connection: null, client: api };
const FILTER_KEY = 'uam.machineFilter';

function readFilter(): MachineFilter | null {
  try {
    const value = JSON.parse(localStorage.getItem(FILTER_KEY) ?? 'null') as MachineFilter | null;
    return value && typeof value.machine === 'string' && typeof value.project === 'string' ? value : null;
  } catch { return null; }
}
const STATUS_NOTE: Partial<Record<ConnectedStatus['status'], string>> = { offline: 'Offline', 'auth-required': 'Needs a new access key', unsupported: 'Needs an update', 'account-mismatch': 'Different Copilot account' };

/** The registry's verdict on a connection's Copilot account; it outranks what its stream reports. */
function accountStatus(connection: ConnectedInstance | undefined): ConnectedStatus | undefined {
  return connection?.status === 'account_mismatch' ? { status: 'account-mismatch', error: connection.reason } : undefined;
}

function connectionFailure(error: unknown): ConnectedStatus {
  const code = errorCode(error);
  return { status: code === 'remote_auth_required' ? 'auth-required' : code === 'unsupported_remote' ? 'unsupported' : 'offline', error: describeError(error) };
}

/** One independent reducer/sequence space per source. */
function SourceStream({ source, initial, onState }: { source: Source; initial: State; onState: (id: string, owner: ConnectedInstance | null, value: SourceState) => void }) {
  const [seed] = useState(initial);
  const sourceID = source.id;
  const client = source.client;
  const home = !source.connection;
  useEffect(() => {
    let alive = true;
    let current = seed;
    let es: EventSource | undefined;
    let retry: number | undefined;
    const publish = (status: ConnectedStatus) => { if (alive) onState(sourceID, client.owner, { state: current, status }); };
    const connect = () => {
      if (!alive) return;
      publish({ status: 'connecting' });
      try { es = new EventSource(client.eventsUrl(null)); }
      catch (error) { publish(connectionFailure(error)); return; }
      const stream = es;
      stream.addEventListener('snapshot', event => {
        if (!alive || es !== stream) return;
        try {
          current = reducer(current, { type: 'snapshot', data: JSON.parse((event as MessageEvent).data) as SnapshotData });
          publish({ status: 'online' });
        } catch { publish({ status: 'offline', error: 'This instance sent an invalid snapshot.' }); }
      });
      for (const name of UPDATE_EVENTS) stream.addEventListener(name, event => {
        if (!alive || es !== stream) return;
        try {
          current = reducer(current, { type: 'update', data: { name, ...JSON.parse((event as MessageEvent).data) } as UpdateData });
          publish({ status: 'online' });
        } catch { publish({ status: 'offline', error: 'This instance sent an invalid update.' }); }
      });
      if (home) stream.addEventListener('notify', event => { if (alive && es === stream) void handleNotice(JSON.parse((event as MessageEvent).data) as Notice); });
      stream.onerror = () => {
        if (!alive || es !== stream) return;
        stream.close();
        publish({ status: 'offline', error: 'Connection lost. Showing the last received task list.' });
        void client.meta().then(() => {
          if (alive && es === stream) retry = window.setTimeout(connect, 3000);
        }, error => {
          if (!alive || es !== stream) return;
          publish(connectionFailure(error));
          if (alive && !['remote_auth_required', 'connection_changed', 'connection_disabled', 'connection_not_found', 'identity_mismatch', 'unsupported_remote'].includes(errorCode(error) ?? '')) retry = window.setTimeout(connect, 5000);
        });
      };
    };
    connect();
    return () => { alive = false; es?.close(); window.clearTimeout(retry); };
  }, [sourceID, client, home, onState, seed]);
  return null;
}

/** Authentication, saved connections and notifications belong to this hosting UAM. */
export default function Federation() {
  const [authenticated, setAuthenticated] = useState(false);
  const authRef = useRef(false);
  const authEpoch = useRef(0);
  const registrySequence = useRef(0);
  const [sources, setSources] = useState<Source[]>([homeSource]);
  const [registry, setRegistry] = useState<Registry | null>(null);
  const registryRef = useRef(registry);
  useLayoutEffect(() => { registryRef.current = registry; }, [registry]);
  const [registryError, setRegistryError] = useState<string | null>(null);
  const [registryRead, setRegistryRead] = useState(false);
  const [active, setActive] = useState<ConnectedInstance | null>(null);
  const activeRef = useRef(active);
  useLayoutEffect(() => { activeRef.current = active; }, [active]);
  const [route, setRoute] = useState(() => ({ path: hasIdentity(window.location.hash) ? '' : window.location.hash, tick: 0 }));
  const routeReady = useRef(!hasIdentity(window.location.hash));
  const [routeError, setRouteError] = useState<string | null>(null);
  const [terminalOpen, setTerminalOpen] = useState(false);
  const [sourceStates, setSourceStates] = useState<Record<string, SourceState>>({});
  const [metaCache] = useState(() => new Map<string, Meta>());
  const carried = useRef<CarriedShell>({ query: '', scroll: 0, section: 'general', nextSection: null });
  const [carry] = useState<ShellCarry>(() => ({ read: () => carried.current, write: (patch) => { carried.current = { ...carried.current, ...patch }; } }));
  const [homeVersion, setHomeVersion] = useState<string>();
  const [homeLoadedVersion, setHomeLoadedVersion] = useState<string>();
  const onHomeVersion = useCallback((version: string) => { setHomeLoadedVersion(previous => previous ?? version); setHomeVersion(version); }, []);
  const homeId = registry?.instance_id ?? '';
  const connections = registry?.connections ?? [];
  /** This instance's own view: where a view falls back to once its connection is gone. */
  const showHome = useCallback(() => {
    routeReady.current = true;
    setRouteError(null);
    setTerminalOpen(false);
    setActive(null);
    setRoute(previous => ({ path: '', tick: previous.tick + 1 }));
    window.dispatchEvent(new CustomEvent('uam-route', { detail: { path: '', connection: HOME, force: true } }));
    history.replaceState(null, '', `${window.location.pathname}${window.location.search}`);
  }, []);
  /** Shows the registry `next`: its enabled connections are the machines, each keeping its state while its record is unchanged. */
  const applyRegistry = useCallback((next: Registry) => {
      const before = registryRef.current;
      registryRef.current = next;
      // The connection on screen: disabled or removed, its view falls back to this instance (in the same render that drops its rows); re-paired, its view carries on at the new generation.
      const viewing = activeRef.current;
      if (viewing) {
        const match = next.connections.find(connection => connection.enabled && connection.id === viewing.id && connection.instance_id === viewing.instance_id);
        if (!match) showHome();
        else if (match.generation !== viewing.generation) setActive(match);
      }
      setSourceStates(previous => Object.fromEntries(Object.entries(previous).filter(([id]) => id === HOME || next.connections.some(connection => connection.enabled && connection.id === id && before?.connections.some(old => old.id === id && old.generation === connection.generation && old.instance_id === connection.instance_id)))));
      setSources(previous => [homeSource, ...next.connections.filter(connection => connection.enabled).map(connection => {
        const prior = previous.find(source => source.connection?.id === connection.id && source.connection.instance_id === connection.instance_id && source.connection.generation === connection.generation);
        if (prior) return prior.label === connection.label ? prior : { ...prior, label: connection.label, connection };
        const valid = () => !!registryRef.current?.connections.some(current => current.enabled && current.id === connection.id && current.instance_id === connection.instance_id && current.generation === connection.generation);
        const client = createApiClient(connection, valid, error => {
          if (!valid() || !(error.status === 0 || ['remote_auth_required', 'remote_unavailable', 'connection_changed', 'identity_mismatch', 'unsupported_remote', 'connection_not_found', 'connection_disabled'].includes(errorCode(error) ?? ''))) return;
          setSourceStates(previous => ({ ...previous, [connection.id]: { state: previous[connection.id]?.state ?? initialState, status: connectionFailure(error) } }));
        });
        try {
          const since = client.storageKey('uam.viewedSince');
          if (!localStorage.getItem(since)) localStorage.setItem(since, JSON.stringify(new Date().toISOString()));
        } catch { /* The list remains usable without browser storage. */ }
        return { id: connection.id, label: connection.label, connection, client };
      })]);
      setRegistry(previous => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
  }, [showHome]);
  const refresh = useCallback(async () => {
    const epoch = authEpoch.current;
    const sequence = ++registrySequence.current;
    const current = () => authRef.current && epoch === authEpoch.current && sequence === registrySequence.current;
    try {
      const next = await api.connections();
      if (!current()) return;
      // An older standalone server may have no registry endpoint; that never removes local UI.
      if (!next || !Array.isArray(next.connections)) throw new Error('Connected instances are unavailable on this server.');
      applyRegistry(next);
      setRegistryError(null);
    } catch (error) { if (current()) setRegistryError(describeError(error)); }
    finally { if (current()) setRegistryRead(true); }
  }, [applyRegistry]);
  const onAuth = useCallback((value: boolean) => {
    if (authRef.current !== value) authEpoch.current++;
    authRef.current = value;
    setAuthenticated(value);
    if (!value) { registryRef.current = null; setRegistry(null); setRegistryRead(false); setSourceStates({}); setSources([homeSource]); setActive(null); setRouteError(null); routeReady.current = !hasIdentity(window.location.hash); setRoute(previous => ({ path: hasIdentity(window.location.hash) ? '' : window.location.hash, tick: previous.tick + 1 })); }
  }, []);
  useEffect(() => subscribeAuthLoss(() => onAuth(false)), [onAuth]);
  useEffect(() => {
    if (!authenticated) return;
    // This starts an asynchronous registry read; state changes only after its response.
    void refresh();
    const onFocus = () => { if (document.visibilityState === 'visible') void refresh(); };
    document.addEventListener('visibilitychange', onFocus);
    // Detect edits from another browser without relying on an old connection stream.
    const timer = window.setInterval(() => void refresh(), 30000);
    return () => { document.removeEventListener('visibilitychange', onFocus); window.clearInterval(timer); };
  }, [authenticated, refresh]);
  useEffect(() => { if (authenticated) return startNotifications(); }, [authenticated]);
  useEffect(() => {
    setNotificationSource(active ? { id: active.id, instance_id: active.instance_id, generation: active.generation } : null);
    return () => setNotificationSource(null);
  }, [active]);
  useEffect(() => {
    if (!authenticated || !active) return;
    const read = () => { void api.meta().then(meta => setHomeVersion(meta.version), () => {}); };
    read();
    const timer = window.setInterval(read, 60000);
    return () => window.clearInterval(timer);
  }, [authenticated, active]);

  const navigate = useCallback((connection: ConnectedInstance | null, path: string) => {
    if (terminalOpen && connection?.id !== activeRef.current?.id && !window.confirm('Switch instances and close the active terminal?')) return;
    routeReady.current = true;
    setRouteError(null);
    setTerminalOpen(false);
    setActive(connection);
    setRoute(previous => ({ path, tick: previous.tick + 1 }));
    window.dispatchEvent(new CustomEvent('uam-route', { detail: { path, connection: connection?.id ?? HOME, force: true } }));
    // Moving to another instance's view is a navigation, so Back returns to the previous one.
    const hash = identityHash(path, connection, registryRef.current?.instance_id ?? '');
    if (window.location.hash !== hash) history.pushState(null, '', `${window.location.pathname}${window.location.search}${hash}`);
  }, [terminalOpen]);
  const resolveRoute = useCallback((force = false) => {
    // Local navigation does not depend on the optional connection registry.
    if (!registryRead && hasIdentity(window.location.hash)) return;
    if (!force && !hasIdentity(window.location.hash) && !activeRef.current) return;
    const found = resolveIdentity(window.location.hash, registryRef.current?.instance_id ?? '', registryRef.current?.connections ?? []);
    if ('error' in found) { routeReady.current = false; setRouteError(found.error); return; }
    routeReady.current = true;
    setRouteError(null);
    const previous = activeRef.current;
    if (!force && previous?.id === found.connection?.id && previous?.instance_id === found.connection?.instance_id && previous?.generation === found.connection?.generation) return;
    setActive(found.connection);
    setRoute(previous => ({ path: found.path, tick: previous.tick + 1 }));
    window.dispatchEvent(new CustomEvent('uam-route', { detail: { path: found.path, connection: found.connection?.id ?? HOME } }));
  }, [registryRead]);
  useEffect(() => { if (authenticated) resolveRoute(); }, [authenticated, registry, resolveRoute]);
  // Back/Forward fires popstate (and hashchange when the fragment differs); an edited fragment fires hashchange.
  useEffect(() => {
    const onHash = () => resolveRoute(true);
    window.addEventListener('hashchange', onHash);
    window.addEventListener('popstate', onHash);
    return () => {
      window.removeEventListener('hashchange', onHash);
      window.removeEventListener('popstate', onHash);
    };
  }, [resolveRoute]);
  const onRoute = useCallback((path: string, push: boolean) => {
    if (!routeReady.current || routeError || (!registryRead && hasIdentity(window.location.hash))) return false;
    const owner = activeRef.current;
    const hash = identityHash(path, owner, registryRef.current?.instance_id ?? '');
    if (window.location.hash === hash) return true;
    const url = `${window.location.pathname}${window.location.search}${hash}`;
    if (push) history.pushState(null, '', url);
    else history.replaceState(null, '', url);
    return true;
  }, [registryRead, routeError]);
  const onState = useCallback((id: string, owner: ConnectedInstance | null, value: SourceState) => {
    if (!authRef.current || (owner && !registryRef.current?.connections.some(connection => connection.enabled && connection.id === owner.id && connection.instance_id === owner.instance_id && connection.generation === owner.generation))) return;
    setSourceStates(previous => {
      const before = previous[id];
      const state = value.state.loaded ? value.state : before?.state ?? value.state;
      if (before?.state === state && before.status.status === value.status.status && before.status.error === value.status.error) return previous;
      return { ...previous, [id]: { ...value, state } };
    });
  }, []);
  const activeID = active?.id ?? HOME;
  const activeGeneration = active?.generation ?? 0;
  const activeInstanceID = active?.instance_id;
  const onActiveEvent = useCallback((action: Action) => {
    if (!authRef.current) return;
    if ((activeRef.current?.id ?? HOME) !== activeID || (activeRef.current?.generation ?? 0) !== activeGeneration || activeRef.current?.instance_id !== activeInstanceID) return;
    if (activeID && !registryRef.current?.connections.some(connection => connection.id === activeID && connection.enabled && connection.generation === activeGeneration && connection.instance_id === activeInstanceID)) return;
    setSourceStates(previous => {
      const before = previous[activeID];
      const state = reducer(before?.state ?? initialState, action);
      if (before?.state === state) return previous;
      const status: ConnectedStatus = state.connection === 'connected' ? { status: 'online' } : before?.status.status === 'auth-required' ? before.status : { status: 'offline' };
      return { ...previous, [activeID]: { state, status } };
    });
  }, [activeID, activeGeneration, activeInstanceID]);
  const selectedSource = sources.find(source => source.id === (active?.id ?? HOME) && source.connection?.generation === active?.generation && source.connection?.instance_id === active?.instance_id);
  const client = selectedSource?.client ?? api;
  const statuses = Object.fromEntries(connections.map(connection => [connection.id, accountStatus(connection) ?? sourceStates[connection.id]?.status ?? { status: 'connecting' as const }]));
  const add = async (input: AddConnectionInput) => { await api.addConnection(input); await refresh(); };
  const update = async (id: string, input: UpdateConnectionInput) => {
    const updated = await api.updateConnection(id, input);
    // The saved record shows at once (a disabled machine's rows leave now); the registry re-read, which can wait on each machine's account check, confirms it.
    const known = registryRef.current;
    if (known && updated && known.connections.some(connection => connection.id === id)) applyRegistry({ ...known, connections: known.connections.map(connection => connection.id === id ? { ...connection, ...updated } : connection) });
    await refresh();
  };
  const remove = async (id: string) => {
    const connection = connections.find(entry => entry.id === id);
    await api.removeConnection(id);
    if (connection) {
      const old = createApiClient(connection);
      const prefix = old.storageKey('');
      try {
        for (const storage of [localStorage, sessionStorage]) for (const key of Object.keys(storage)) if (key.startsWith(prefix)) storage.removeItem(key);
        for (const key of Object.keys(localStorage)) if (key.startsWith(`uam.review.${old.cacheKey('')}`)) localStorage.removeItem(key);
      } catch { /* Storage unavailable. */ }
      const cachePrefix = old.cacheKey('');
      for (const key of Object.keys(readMarks() ?? {})) if (key.startsWith(cachePrefix)) forgetArchive(key);
    }
    await refresh();
  };
  // Settings' header names the instance on screen and switches it; switching keeps Settings open.
  const sourceControl = connections.length > 0 ? <Select aria-label="Active instance" className="h-8 w-auto max-w-[min(16rem,40vw)] sm:max-w-64" value={active?.id ?? HOME} onValueChange={id => { carry.write({ nextSection: carry.read().section }); navigate(connections.find(connection => connection.id === id) ?? null, '#settings'); }} items={[
    { value: HOME, label: homeSource.label },
    ...connections.map(connection => ({ value: connection.id, label: connection.label, disabled: !connection.enabled, description: connection.enabled ? STATUS_NOTE[statuses[connection.id].status] : 'Disabled' })),
  ]} /> : null;
  const unreadFor = (source: Source, selectedId: string | null = null) => {
    let viewed: Record<string, string> = {};
    let since = new Date().toISOString();
    try {
      viewed = JSON.parse(localStorage.getItem(source.client.storageKey('uam.viewed')) ?? '{}') as Record<string, string>;
      since = JSON.parse(localStorage.getItem(source.client.storageKey('uam.viewedSince')) ?? JSON.stringify(since)) as string;
    } catch { /* Without stored marks only currently actionable tasks count. */ }
    return newsReader(selectedId, viewed ?? {}, since);
  };
  const otherAttention = sources.filter(source => source.id !== activeID).reduce((count, source) => {
    const state = sourceStates[source.id]?.state;
    return state ? count + needsYouCount(state.sessions, unreadFor(source)) + pendingRequests(state.boards) : count;
  }, 0);
  const federated = connections.length > 0;
  // Every enabled machine with what it last streamed; the one on screen is App's live state, this its lagging copy.
  const machines = useMemo<Machine[] | undefined>(() => federated ? sources.map(source => {
    const value = sourceStates[source.id];
    const status = (source.connection && accountStatus(registry?.connections.find(connection => connection.id === source.id))) ?? value?.status ?? { status: 'connecting' };
    return { id: source.id, label: source.label, short: source.connection?.label ?? 'Local', connection: source.connection, client: source.client, active: source.id === activeID, state: value?.state ?? initialState, status, hasNews: unreadFor(source) };
  }) : undefined,
  // unreadFor reads this browser's marks, which change only while their machine is on screen.
  [federated, sources, sourceStates, activeID, registry]);
  const choices: MachineChoice[] | undefined = federated ? [
    { id: HOME, label: homeSource.label },
    ...connections.map(connection => ({ id: connection.id, label: connection.label, reason: connection.enabled ? STATUS_NOTE[statuses[connection.id].status] : 'Disabled' })),
  ] : undefined;
  const go = useCallback((id: string, path: string) => navigate(registryRef.current?.connections.find(connection => connection.id === id && connection.enabled) ?? null, path), [navigate]);
  const [filter, setFilter] = useState<MachineFilter | null>(readFilter);
  const onFilter = useCallback((value: MachineFilter | null) => {
    setFilter(value);
    try { localStorage.setItem(FILTER_KEY, JSON.stringify(value)); } catch { /* Kept for this page only. */ }
  }, []);
  const [intent, setIntent] = useState<MachineIntent | null>(null);
  const request = useCallback((value: MachineIntent) => { setIntent(value); go(value.machine, ''); }, [go]);
  const consumeIntent = useCallback(() => setIntent(null), []);
  // A connection on another Copilot account opens on its Settings, at the account.
  const openAccount = useCallback((id: string) => request({ machine: id, kind: 'account', projectId: '' }), [request]);
  const settings = <><ConnectedInstancesSettings homeInstanceID={homeId} connections={connections} statuses={statuses} onAdd={add} onUpdate={update} onRemove={remove} onRefresh={refresh} onOpenAccount={openAccount} activeTerminalConnectionID={terminalOpen ? active?.id : null} />{registryError && <p role="alert" className="text-caption text-error">{registryError}</p>}</>;
  const pendingRoute = authenticated && !registryRead && hasIdentity(window.location.hash);
  const unavailable = routeError || (active && !selectedSource ? 'This connection is unavailable. Open an enabled instance to continue.' : null);
  return <>
    {authenticated && connections.length > 0 && sources.filter(source => source.id !== (active?.id ?? HOME)).map(source => <SourceStream key={`${source.id}:${source.connection?.generation ?? 0}`} source={source} initial={sourceStates[source.id]?.state ?? initialState} onState={onState} />)}
    {unavailable ? <div role="alert" className="flex min-h-screen flex-col items-center justify-center gap-4 p-6"><p>{unavailable}</p><Button onClick={() => navigate(null, '#settings')}>Open home settings</Button></div> :
      <ApiContext.Provider value={client}><FederationContext.Provider value={{ initialPath: route.path, onRoute, onAuth, onEvent: connections.length ? onActiveEvent : undefined, onTerminal: setTerminalOpen, connectionsSettings: settings, sourceControl, otherAttention: connections.length ? otherAttention : undefined, homeVersion, homeLoadedVersion, onHomeVersion, pendingRoute, accountMismatch: active ? accountStatus(connections.find(connection => connection.id === active.id))?.error : undefined,
        ...(machines && { machines, choices, go, filter, onFilter, intent, request, consumeIntent, seed: sourceStates[activeID]?.state, authenticated, metaCache, carry }) }}>
        <App key={`${active?.id ?? HOME}:${active?.generation ?? 0}`} />
      </FederationContext.Provider></ApiContext.Provider>}
  </>;
}
