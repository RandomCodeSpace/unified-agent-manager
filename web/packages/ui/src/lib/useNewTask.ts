import { useCallback, useEffect, useLayoutEffect, useRef, type Dispatch, type SetStateAction } from 'react';
import { ACCOUNT_NOT_LINKED, SIGNED_OUT, describeError, errorCode, newRequestId, provider, resolveTaskDefaults, type ApiClient, type Meta, type Project, type SessionSummary, type Settings, type TaskDefaults } from '../api';
import type { FirstMessage } from '../components/Composer';
import type { Action } from '../state';
import { kindOf } from './attachments';
import { createRequest, draftKey, serializeDraft, type DraftAttachment } from './drafts';
import { sessionsUpdate } from './overlays';

/** A touch screen: focusing the composer would raise the keyboard over the conversation just opened. */
export const COARSE = '(pointer: coarse)';

/** A new Thread's draft: its Project, the settings it starts on, and `tick`, which refocuses its composer when New thread is chosen again. */
export interface NewTaskDraft { projectId: string; defaults: TaskDefaults; tick: number }

/**
 * A new Thread, from its draft to its first Send: `startTask` opens the draft for a Project, its composer takes
 * focus, and `createTask` creates the Thread and sends the message when the draft is sent.
 */
export function useNewTask({ api, meta, projects, settings, select, setNotice, newTask, setNewTask, refreshMeta, dispatch }: {
  api: ApiClient;
  meta: Meta | null;
  projects: Project[];
  settings: Settings;
  select: (id: string | null) => void;
  setNotice: Dispatch<SetStateAction<string | null>>;
  newTask: NewTaskDraft | null;
  setNewTask: Dispatch<SetStateAction<NewTaskDraft | null>>;
  refreshMeta: () => void;
  dispatch: Dispatch<Action>;
}) {
  const newTaskTick = useRef(0);
  // One create per project at a time; the request ID survives a failure so a retry is idempotent.
  const creating = useRef(new Map<string, { id: string; busy: boolean }>());
  /** New task: a draft for the Project on the Task defaults of Settings, with its composer focused; no request until its first Send. */
  const startTask = useCallback(
    (projectId: string) => {
      const project = projects.find((p) => p.id === projectId);
      const defaults = project && resolveTaskDefaults(meta, settings.task_defaults, settings.hidden_models);
      if (!defaults) {
        setNotice(`Could not start a thread: ${meta ? 'No provider is available.' : 'The provider list has not loaded yet.'}`);
        return;
      }
      select(null);
      setNewTask({ projectId, defaults, tick: ++newTaskTick.current });
    },
    [projects, settings.task_defaults, settings.hidden_models, meta, select, setNotice, setNewTask],
  );
  // Read at a press, so the Thread actions keep their identity (and the rows their renders) as Projects or Settings change.
  const latestStartTask = useRef(startTask);
  useLayoutEffect(() => { latestStartTask.current = startTask; }, [startTask]);
  const draftTick = newTask?.tick;
  useEffect(() => {
    if (draftTick !== undefined && !window.matchMedia(COARSE).matches) document.getElementById('composer-text')?.focus();
  }, [draftTick]);

  /**
   * A new Task's first Send: create the Task with the chosen settings, upload the held
   * attachments to it, send the message, then open it. A failed create throws, so the draft
   * keeps the text and says why; once the Task exists it opens whatever happens next, and a
   * message that was not accepted waits in its composer with the reason on the notice line.
   */
  const createTask = useCallback(
    async (projectId: string, first: FirstMessage) => {
      const entry = creating.current.get(projectId) ?? { id: newRequestId(), busy: false };
      if (entry.busy) throw new Error('This thread is already being created.');
      creating.current.set(projectId, { ...entry, busy: true });
      let s: SessionSummary;
      try {
        const info = provider(meta, first.settings.provider);
        // Signed out, the service decides: a custom model runs without the sign-in.
        if (info && !info.available && !info.signed_out) throw new Error(`${info.display_name} is unavailable: ${info.reason || 'not installed'}`);
        s = await api.createSession({ ...createRequest(projectId, first.settings, entry.id), ...(first.place && { place: first.place }) });
      } catch (e) {
        creating.current.set(projectId, { ...entry, busy: false });
        if (errorCode(e) === SIGNED_OUT || errorCode(e) === ACCOUNT_NOT_LINKED) refreshMeta();
        throw new Error(`Could not start the thread: ${describeError(e)}`, { cause: e });
      }
      creating.current.delete(projectId);
      sessionsUpdate(() => dispatch({ type: 'upsert_session', session: s }));
      const sent: DraftAttachment[] = [];
      let failed = '';
      try {
        for (const u of first.uploads) {
          const a = await api.upload(s.id, u.file, () => {}).done;
          // The kind the service sniffed, not the name's: a draft kept after a failed Send shows it.
          sent.push({ id: a.id, name: a.name, size: a.size ?? u.file.size, kind: kindOf(a.mime) });
        }
        const extras = { ...(first.files.length ? { files: first.files } : {}), ...(sent.length ? { attachments: sent.map((a) => a.id) } : {}) };
        const sub = await api.prompt(s.id, first.text, newRequestId(), 'send', extras);
        if (sub.status !== 'accepted' && sub.status !== 'queued') failed = sub.error || `the message was ${sub.status}`;
      } catch (e) {
        failed = describeError(e);
      }
      if (failed) {
        try {
          const raw = serializeDraft({ text: first.text, files: first.files, attachments: sent });
          if (raw) localStorage.setItem(api.storageKey(draftKey(s.id)), raw);
        } catch {
          // Storage unavailable: the notice still says what happened.
        }
      }
      select(s.id);
      if (failed) setNotice(`The thread was created, but its first message was not sent: ${failed}. The message is in its composer.`);
    },
    [select, meta, api, refreshMeta, dispatch, setNotice],
  );
  return { startTask, latestStartTask, createTask };
}
