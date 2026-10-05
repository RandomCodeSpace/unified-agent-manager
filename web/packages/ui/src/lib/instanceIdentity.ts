import type { ConnectedInstance } from '../api';

const entityPrefix = 'uam:';
const identityKeys = ['home', 'instance', 'connection', 'generation'] as const;
const identityKeySet = new Set<string>(identityKeys);

/** Local keys remain byte-for-byte compatible with existing task/cache storage. */
export function encodeEntity(connectionId: string | null, entityId: string): string {
  return connectionId === null ? entityId : `${entityPrefix}${encodeURIComponent(connectionId)}:${encodeURIComponent(entityId)}`;
}

export function decodeEntity(value: string): { connectionId: string | null; entityId: string } | null {
  if (!value) return null;
  if (!value.startsWith(entityPrefix)) return { connectionId: null, entityId: value };
  const parts = value.slice(entityPrefix.length).split(':');
  if (parts.length !== 2) return null;
  try {
    const [connectionId, entityId] = parts.map((part) => decodeURIComponent(part));
    if (!connectionId || !entityId || Array.from(connectionId + entityId).some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)) return null;
    if (encodeEntity(connectionId, entityId) !== value) return null;
    return { connectionId, entityId };
  } catch {
    return null;
  }
}

function splitIdentity(hash: string): { path: string; fields: Map<string, string>; invalid: boolean } {
  const fields = new Map<string, string>();
  const route: string[] = [];
  let invalid = hash !== '' && !hash.startsWith('#');
  for (const part of hash.replace(/^#/, '').split('&')) {
    const equal = part.indexOf('=');
    const rawKey = equal < 0 ? part : part.slice(0, equal);
    let key: string;
    try { key = decodeURIComponent(rawKey.replaceAll(/\+/g, ' ')); } catch { invalid = true; continue; }
    if (!identityKeySet.has(key)) {
      route.push(part);
      continue;
    }
    if (fields.has(key)) invalid = true;
    try {
      fields.set(key, decodeURIComponent((equal < 0 ? '' : part.slice(equal + 1)).replaceAll(/\+/g, ' ')));
    } catch { invalid = true; }
  }
  return { path: hash === '' ? '' : `#${route.join('&')}`, fields, invalid };
}

/** A cold route must wait for registry validation even when its ownership keys are escaped. */
export function hasIdentity(hash: string): boolean {
  const { fields, invalid } = splitIdentity(hash);
  return invalid || fields.size > 0;
}

/** Adds ownership without changing route syntax such as #settings into #settings=. */
export function identityHash(path: string, connection: ConnectedInstance | null, homeId: string): string {
  const parsed = splitIdentity(path);
  if (parsed.invalid) throw new Error('Invalid instance route');
  if (!connection) return parsed.path;
  if (!homeId || !connection.id || !connection.instance_id || !Number.isSafeInteger(connection.generation) || connection.generation < 1) {
    throw new Error('Incomplete instance identity');
  }
  const suffix = new URLSearchParams({ home: homeId, instance: connection.instance_id, connection: connection.id, generation: String(connection.generation) });
  return `${parsed.path || '#'}${parsed.path && parsed.path !== '#' ? '&' : ''}${suffix}`;
}

export type ResolvedIdentity = { connection: ConnectedInstance | null; path: string } | { error: string };

/** Resolve against the live home registry before opening a notification or a shared link. */
export function resolveIdentity(hash: string, homeId: string, connections: readonly ConnectedInstance[]): ResolvedIdentity {
  const { path, fields, invalid } = splitIdentity(hash);
  if (invalid) return { error: 'This instance link is invalid.' };
  if (fields.size === 0) return { connection: null, path };
  if (fields.size !== identityKeys.length || identityKeys.some((key) => !fields.get(key))) return { error: 'This instance link is incomplete.' };
  if (fields.get('home') !== homeId) return { error: 'This link belongs to a different home instance.' };
  const generation = fields.get('generation')!;
  if (!/^[1-9]\d*$/.test(generation) || !Number.isSafeInteger(Number(generation))) return { error: 'This instance link is invalid.' };
  const connection = connections.find((entry) => entry.id === fields.get('connection'));
  if (!connection) return { error: 'This connection is no longer saved on this instance.' };
  if (!connection.enabled) return { error: 'This connection is disabled. Enable it in Connected instances.' };
  if (connection.instance_id !== fields.get('instance')) return { error: 'The connected instance no longer matches this link.' };
  if (connection.generation !== Number(generation)) return { error: 'This connection has changed. Open the task from its current instance.' };
  return { connection, path };
}
