import { createContext, useContext, useMemo, useSyncExternalStore, type ComponentProps, type ReactNode } from 'react';
import type { ExtraProps } from 'react-markdown';
import { useApi } from '../ApiContext';
import type { Item, Reference } from '../api';
import { citedNumbers, referenceName, referenceWords } from '../lib/references';
import { Tip } from './ui/tooltip';

// The references a reply cites (DESIGN.md turn outcome), held per Thread and reply, and the cited
// numbers in its prose. Kept apart from the ledger (References.tsx) so the Markdown renderer can use
// it without importing the transcript's components.

/* ---------- the service's answer, per Thread and reply ---------- */

export interface Held { text: string; end: string; refs: Reference[]; /** The last row a number in the prose asked for. */ pick?: { index: number; seq: number } }
const LIMIT = 300;
export const held = new Map<string, Held>();
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export function put(key: string, value: Held) {
  held.delete(key);
  held.set(key, value);
  while (held.size > LIMIT) held.delete(held.keys().next().value!);
  listeners.forEach((listener) => listener());
}

export function useHeld(sessionId: string | undefined, replyId: string | undefined): [string, Held | undefined] {
  const api = useApi();
  const key = sessionId && replyId ? `${api.cacheKey(sessionId)}\n${replyId}` : '';
  return [key, useSyncExternalStore(subscribe, () => (key ? held.get(key) : undefined))];
}

/* ---------- the prose: each cited number opens its row ---------- */

interface Cites { refs: readonly Reference[]; cited: ReadonlyMap<string, number>; pick: (index: number) => void }
const CitesContext = createContext<Cites | null>(null);

/** The cited numbers of the reply rendered inside, for the Markdown pass (`remarkReferences`); none until its references are in. */
export const useCites = () => useContext(CitesContext);

/** Holds a reply's references for its prose; `sessionId` only for a main-agent reply that ends its turn. */
export function ReferenceProse({ sessionId, reply, children }: Readonly<{ sessionId?: string; reply: Item; children: ReactNode }>) {
  const [key, entry] = useHeld(sessionId, reply.agent_id ? undefined : reply.id);
  const refs = entry?.refs;
  const value = useMemo<Cites | null>(() => {
    if (!refs?.length) return null;
    return { refs, cited: citedNumbers(refs), pick: (index) => { const now = held.get(key); if (now) put(key, { ...now, pick: { index, seq: (now.pick?.seq ?? 0) + 1 } }); } };
  }, [refs, key]);
  return <CitesContext.Provider value={value}>{children}</CitesContext.Provider>;
}

/** A cited number in the prose (the `button` `remarkReferences` makes): dotted underline, opens the reply's references on its row. */
export function MdCite({ children, ...props }: ComponentProps<'button'> & ExtraProps) {
  const cites = useCites();
  const index = Number((props as Record<string, unknown>)['data-reference']);
  const r = cites?.refs[index];
  if (!cites || !r) return <>{children}</>;
  const what = referenceWords(r) || r.state || '';
  const name = what ? `${referenceName(r)}: ${what}` : referenceName(r);
  return (
    <Tip label={what}>
      <button type="button" aria-label={`${name}. Show in this reply's references`} className="rounded-xs underline decoration-hairline-strong decoration-dotted underline-offset-3 transition-colors duration-100 hover:decoration-current" onClick={() => cites.pick(index)}>
        {children}
      </button>
    </Tip>
  );
}
