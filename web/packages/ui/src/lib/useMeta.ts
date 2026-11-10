import { useCallback, useEffect, useRef, useState } from 'react';
import { describeError, type ApiClient, type Meta } from '../api';
import type { FederationView } from '../FederationContext';
import type { Auth } from '../state';
import { editing, popupOpen } from './overlays';
import { checkDue, decideUpdate } from './update';
import { increment } from './useEventStream';

/**
 * What the service offers (providers, models, capabilities) and its version: read once signed in, again on a Retry or
 * a change to the custom models, and checked again after a reconnect or when the page comes back into view. A newer
 * version reloads the page or, with unsent work on it, sets `updated`.
 */
export function useMeta({ api, auth, federation, customModels }: { api: ApiClient; auth: Auth; federation: FederationView | null; /** The custom models of Settings as one string, or null before the first snapshot. */ customModels: string | null }) {
  const here = api.owner?.id ?? '';
  const metaCache = federation?.metaCache;
  const onHomeVersion = federation?.onHomeVersion;
  const [meta, setMeta] = useState<Meta | null>(() => metaCache?.get(here) ?? null);
  const [metaError, setMetaError] = useState<string | null>(null);
  // The service version this page loaded with, the time of the last version check, and whether a newer
  // version waits for a Reload.
  const loadedVersion = useRef<string | null>(null);
  const lastCheck = useRef(0);
  const [updated, setUpdated] = useState(false);

  // `metaAttempt` is a Retry after a failure, or a change to the custom models: they are part of the model lists. The
  // list is unknown until the first snapshot, which only shows what the first read already had.
  const [metaAttempt, setMetaAttempt] = useState(0);
  const [readModels, setReadModels] = useState(customModels);
  if (customModels !== readModels) {
    setReadModels(customModels);
    if (customModels !== null && readModels !== null) setMetaAttempt(increment);
  }
  useEffect(() => {
    if (auth !== 'in') return;
    lastCheck.current = Date.now();
    api
      .meta()
      .then((m) => {
        setMeta(m);
        setMetaError(null);
        metaCache?.set(api.owner?.id ?? '', m);
      })
      .catch((e: unknown) => {
        setMeta(null);
        setMetaError(describeError(e));
      });
  }, [api, auth, metaAttempt, metaCache]);

  /**
   * A redeploy shows as the stream reconnecting, so the version is read again once the stream
   * is back; a page coming back into view (a phone that slept through it) reads it too, at most
   * once a minute. A failed read keeps the providers on screen.
   */
  const checkVersion = useCallback((force: boolean) => {
    const now = Date.now();
    if (!force && !checkDue(lastCheck.current, now)) return;
    lastCheck.current = now;
    api.meta().then(setMeta).catch(() => {});
  }, [api]);
  useEffect(() => {
    if (auth !== 'in') return;
    const onVisible = () => document.visibilityState === 'visible' && checkVersion(false);
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [auth, checkVersion]);
  // A new version applies itself when nothing would be lost (no draft, no popup); otherwise the
  // snackbar over the pane offers Reload and the next check tries again.
  useEffect(() => {
    const version = api.owner ? federation?.homeVersion : meta?.version;
    if (!version) return;
    loadedVersion.current ??= federation?.homeLoadedVersion ?? version;
    const decision = decideUpdate(loadedVersion.current, version, { draft: !!document.querySelector('[data-draft]') || editing(), popup: popupOpen() });
    if (decision === 'reload') window.location.reload();
    else if (decision === 'offer') setUpdated(true);
  }, [api, meta, federation?.homeVersion, federation?.homeLoadedVersion]);
  useEffect(() => { if (!api.owner && meta) onHomeVersion?.(meta.version); }, [api, meta, onHomeVersion]);

  // A refresh with the catalogs on screen keeps them on a failure (checkVersion); without them it is a retry of the first read.
  const refreshMeta = useCallback(() => (meta ? checkVersion(true) : setMetaAttempt((n) => n + 1)), [meta, checkVersion]);
  return { meta, metaError, checkVersion, refreshMeta, updated };
}
