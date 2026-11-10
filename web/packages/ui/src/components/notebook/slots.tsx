// Where the notebook layout's parts meet (lib/layout). Each part is built apart from the others and
// reaches them only through this file:
//
//   TurnJump       the Contents column's list of turns asks the open Thread to scroll to one.
//   NotebookSlots  the running turn's cell gives two places to the Thread: `live`, its "did" track,
//                  where the Thread draws the live card; and `asks`, the foot of its "said" track,
//                  where the Thread draws a question or permission that waits.
//
// A slot is an element: the Thread fills it with a portal, so the state stays with the Thread and
// only the place changes.

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';

type Jump = (replyKey: string) => void;
type TurnJumpValue = { jump: Jump; register: (target: Jump) => () => void };

const TurnJumpContext = createContext<TurnJumpValue | null>(null);

/** Holds the open Thread's jump for the list of turns. One Thread is open at a time; the last to register answers. */
export function TurnJumpProvider({ children }: Readonly<{ children: ReactNode }>) {
  const target = useRef<Jump | null>(null);
  const value = useMemo<TurnJumpValue>(() => ({
    jump: (replyKey) => target.current?.(replyKey),
    register: (next) => {
      target.current = next;
      return () => {
        if (target.current === next) target.current = null;
      };
    },
  }), []);
  return <TurnJumpContext.Provider value={value}>{children}</TurnJumpContext.Provider>;
}

/** Scrolls the open Thread to the turn whose reply key is `replyKey` (a turn's message id, `start` for one without). */
export function useTurnJump(): Jump {
  const value = useContext(TurnJumpContext);
  return useCallback((replyKey: string) => value?.jump(replyKey), [value]);
}

/** The open Thread answers jumps with `target` while it is mounted. */
export function useTurnJumpTarget(target: Jump): void {
  const value = useContext(TurnJumpContext);
  const latest = useRef(target);
  useEffect(() => {
    latest.current = target;
  });
  useEffect(() => value?.register((replyKey) => latest.current(replyKey)), [value]);
}

export type NotebookSlot = 'live' | 'asks';
type SlotsValue = { slots: Partial<Record<NotebookSlot, HTMLElement | null>>; set: (name: NotebookSlot, el: HTMLElement | null) => void };

const NotebookSlotsContext = createContext<SlotsValue | null>(null);

/** Wraps the Thread's conversation and whatever fills its slots. */
export function NotebookSlots({ children }: Readonly<{ children: ReactNode }>) {
  const [slots, setSlots] = useState<SlotsValue['slots']>({});
  const set = useCallback((name: NotebookSlot, el: HTMLElement | null) => setSlots((held) => (held[name] === el ? held : { ...held, [name]: el })), []);
  const value = useMemo(() => ({ slots, set }), [slots, set]);
  return <NotebookSlotsContext.Provider value={value}>{children}</NotebookSlotsContext.Provider>;
}

/** The element of a slot, or null while no cell gives one (no running turn on screen): fill it with `createPortal`. */
export function useNotebookSlot(name: NotebookSlot): HTMLElement | null {
  return useContext(NotebookSlotsContext)?.slots[name] ?? null;
}

/** A ref callback for the element that is the slot; the slot empties when the element leaves. */
export function useNotebookSlotRef(name: NotebookSlot): (el: HTMLElement | null) => void {
  const set = useContext(NotebookSlotsContext)?.set;
  return useCallback((el: HTMLElement | null) => set?.(name, el), [set, name]);
}
