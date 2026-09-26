/** Shared selected-task HTTP read admission. Streams do not consume these slots. */
let active = 0;
const pending: (() => void)[] = [];
const background: (() => void)[] = [];
export function foregroundRead<T>(read: () => Promise<T>, signal?: AbortSignal, priority: 'normal' | 'low' = 'normal'): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const queue = priority === 'low' ? background : pending;
    let started = false;
    const abort = () => {
      if (started) return;
      const index = queue.indexOf(start);
      if (index >= 0) queue.splice(index, 1);
      reject(signal?.reason ?? new DOMException('Aborted', 'AbortError'));
    };
    const start = () => {
      signal?.removeEventListener('abort', abort);
      if (signal?.aborted) { abort(); return; }
      started = true;
      active++;
      void read().then(resolve, reject).finally(() => {
        active--;
        (pending.shift() ?? background.shift())?.();
      });
    };
    if (signal?.aborted) { abort(); return; }
    signal?.addEventListener('abort', abort, { once: true });
    if (active < 2) start(); else queue.push(start);
  });
}
