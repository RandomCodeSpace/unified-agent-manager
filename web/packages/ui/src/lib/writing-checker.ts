export interface WritingIssue {
  start: number;
  end: number;
  kind: 'spelling' | 'grammar';
  message: string;
  replacements: string[];
  original: string;
}

export const MAX_WRITING_CHECK_LENGTH = 12_000;
const CHECK_TIMEOUT = 20_000;

interface PendingCheck {
  resolve: (issues: WritingIssue[]) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

let worker: Worker | undefined;
let nextID = 0;
const pending = new Map<number, PendingCheck>();

function stopWorker(instance: Worker, error: Error) {
  if (worker !== instance) return;
  worker = undefined;
  instance.terminate();
  for (const check of pending.values()) {
    clearTimeout(check.timer);
    check.reject(error);
  }
  pending.clear();
}

function startWorker(): Worker {
  // An external worker keeps WASM and draft processing off the main thread.
  const instance = new Worker(new URL('./writing-checker.worker.ts', import.meta.url), { type: 'module' });
  instance.onmessage = ({ data }: MessageEvent<{ id: number; issues?: WritingIssue[]; error?: string }>) => {
    if (worker !== instance) return;
    if (data.error || !Array.isArray(data.issues)) {
      stopWorker(instance, new Error('Writing suggestions are unavailable.'));
      return;
    }
    const check = pending.get(data.id);
    if (!check) return;
    clearTimeout(check.timer);
    pending.delete(data.id);
    check.resolve(data.issues);
  };
  instance.onerror = () => stopWorker(instance, new Error('Writing suggestions could not load.'));
  instance.onmessageerror = () => stopWorker(instance, new Error('Writing suggestions could not be read.'));
  return instance;
}

/** Check a bounded draft locally. Callers must discard results when their draft changes. */
export function checkWriting(text: string): Promise<WritingIssue[]> {
  if (!text.trim() || text.length > MAX_WRITING_CHECK_LENGTH) return Promise.resolve([]);
  return new Promise((resolve, reject) => {
    try {
      const instance = worker ??= startWorker();
      const id = ++nextID;
      const timer = setTimeout(() => stopWorker(instance, new Error('Writing suggestions took too long.')), CHECK_TIMEOUT);
      pending.set(id, { resolve, reject, timer });
      instance.postMessage({ id, text });
    } catch (error) {
      const failure = error instanceof Error ? error : new Error('Writing suggestions are unavailable.');
      if (worker) stopWorker(worker, failure);
      reject(failure);
    }
  });
}
