// The drawing editor (DESIGN.md Drawing editor): the anchored reader under a drawing card's head.
// Loaded on the first open (Drawing.tsx `lazy`), so the conversation's cold start carries only the
// cards. Yours is Excalidraw (ExcalidrawCanvas, its own chunk) or the draw.io embed; Agent's and
// Changes are read-only pictures; a notice's Compare opens the Changes of a past version.

import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { ArrowUp, X } from 'lucide-react';
import { lazy, Suspense, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, isStatus, type DrawingDoc } from '../api';
import { cn } from '../lib/cn';
import { drawioEmbedUrl, drawioMessage, DRAWIO_SANDBOX } from '../lib/drawio';
import { changesWord, drawioOverlayXml, excalidrawFile, overlay, parseExcalidraw, type DrawnElement } from '../lib/drawings';
import { useScheme } from '../lib/theme';
import { Note, Spinner } from './common';
import { DrawingsContext, Figure, fileName, useBackground, useDrawioPicture, useParsed, usePicture, type DrawingView, type OpenDrawing } from './Drawing';
import type { ExcalidrawImperativeAPI } from './ExcalidrawCanvas';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { backdropClass, readerPopupClass } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Segmented } from './ui/segmented';

const ExcalidrawCanvas = lazy(() => import('./ExcalidrawCanvas'));

const DESCRIPTION: Record<DrawingView, string> = {
  mine: 'Edit with the full toolset. Every change saves to the file; the agent gets them with your next message.',
  agent: 'Read-only: the version the agent last wrote or read.',
  changes: 'Changes since the agent’s version. Edit them under Yours.',
};

type Save = { state: 'saved' } | { state: 'saving' } | { state: 'failed'; error: string } | { state: 'conflict' };

/**
 * The editor: the anchored reader under the card's head (the whole viewport's height below it,
 * or above the anchor when there is no room; a bottom sheet on a phone). Yours is Excalidraw
 * or the draw.io embed; Agent's and Changes are pictures; history (`at`) is Changes only.
 */
export default function DrawingEditor({ target, onClose, onClosed }: Readonly<{ target: OpenDrawing & { open: boolean; phone: boolean }; onClose: () => void; onClosed: () => void }>) {
  const d = useContext(DrawingsContext)!;
  const api = useApi();
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const history = target.at !== undefined;
  const [view, setView] = useState<DrawingView>(history ? 'changes' : target.view);
  const [past, setPast] = useState<{ doc?: DrawingDoc; error?: string } | null>(null);
  const [save, setSave] = useState<Save>({ state: 'saved' });
  const state = d.doc(target.path);
  const liveDoc = state?.doc;
  const doc = history ? past?.doc : liveDoc;
  const error = history ? past?.error : state?.error;
  const load = d.load;
  useEffect(() => {
    if (!liveDoc && !history) void load(target.path).catch(() => {});
  }, [liveDoc, history, load, target.path]);
  useEffect(() => {
    if (!history) return;
    let on = true;
    api.drawing(d.sessionId, target.path, target.at).then(
      (doc) => on && setPast({ doc }),
      (err: unknown) => on && setPast({ error: isStatus(err, 404) ? 'That version is no longer kept.' : describeError(err) }),
    );
    return () => {
      on = false;
    };
  }, [api, d.sessionId, history, target.path, target.at]);

  const count = doc?.changes.length ?? 0;
  const name = doc ? fileName(doc.path) : fileName(target.path);
  const canvasApi = useRef<ExcalidrawImperativeAPI | null>(null);
  const popup = useRef<HTMLDivElement>(null);
  const fit = useCallback(() => {
    canvasApi.current?.scrollToContent(undefined, { fitToViewport: true, viewportZoomFactor: 0.7 });
    popup.current?.querySelector<HTMLElement>('.excalidraw-container')?.focus();
  }, []);
  useEffect(() => {
    if (view === 'mine') requestAnimationFrame(fit);
  }, [view, fit]);

  const { current, base } = useParsed(doc);
  const background = useBackground();
  const scheme = useScheme();
  const lastSaved = useRef(doc?.content ?? '');
  const onChange = useCallback((elements: DrawnElement[], files: Record<string, unknown>) => {
    const content = excalidrawFile(elements, files);
    if (content === lastSaved.current) return;
    void saveContent(content);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- saveContent reads the latest doc through the closure below
  }, []);
  const latest = useRef({ doc: liveDoc, save: d.save });
  useEffect(() => {
    latest.current = { doc: liveDoc, save: d.save };
  }, [liveDoc, d.save]);
  // Puts `content` on the canvas and returns it as Excalidraw will report it back (its restored
  // form), so the onChange that follows a programmatic scene is not a save.
  const setScene = (content: string): string => {
    const api = canvasApi.current;
    if (!api) return content;
    api.updateScene({ elements: structuredClone(parseExcalidraw(content).elements) as never });
    return excalidrawFile(api.getSceneElements() as unknown as DrawnElement[], api.getFiles());
  };
  async function saveContent(content: string, reported = content) {
    const doc = latest.current.doc;
    if (!doc) return;
    lastSaved.current = reported;
    setSave({ state: 'saving' });
    try {
      await latest.current.save(target.path, content, doc.version);
      setSave({ state: 'saved' });
    } catch (err) {
      setSave(isStatus(err, 409) ? { state: 'conflict' } : { state: 'failed', error: describeError(err) });
    }
  }
  const reload = () => {
    void load(target.path).then((doc) => {
      lastSaved.current = doc.format === 'excalidraw' ? setScene(doc.content) : doc.content;
      setSave({ state: 'saved' });
    }, () => {});
  };
  const revert = () => {
    if (!doc || doc.baseline === null) return;
    void saveContent(doc.baseline, doc.format === 'excalidraw' ? setScene(doc.baseline) : doc.baseline);
  };

  const overlaid = useMemo(() => (current && base && doc ? overlay(base.elements, current.elements, doc.status) : null), [current, base, doc]);
  const agentPic = usePicture(view === 'agent' && doc?.format === 'excalidraw' ? (base?.elements ?? null) : null, base?.files ?? {});
  const changesPic = usePicture(view === 'changes' && doc?.format === 'excalidraw' ? overlaid : null, current?.files ?? {}, true);
  const drawioXml = useMemo(() => {
    if (!doc || doc.format !== 'drawio') return { agent: null, changes: null };
    return { agent: view === 'agent' ? (doc.baseline ?? doc.content) : null, changes: view === 'changes' ? drawioOverlayXml(doc.baseline ?? doc.content, doc.content, doc.status) : null };
  }, [doc, view]);
  const agentDrawio = useDrawioPicture(d.drawio, drawioXml.agent);
  const changesDrawio = useDrawioPicture(d.drawio, drawioXml.changes);

  const saveWord = save.state === 'saving' ? 'Saving' : save.state === 'saved' ? 'Saved' : 'Save failed';
  const facts = doc ? [`↳ Agent’s v${doc.agent_version}${doc.item_id ? ' · from the reply’s mermaid' : ''}`, doc.path, ...(history || !d.writable ? [] : [saveWord])] : [];
  const pillText = history && doc ? `Agent’s v${doc.agent_version} → yours v${doc.version} · ${changesWord(count)}` : count ? `Yours · ${changesWord(count)}` : 'Same as the agent’s';
  const editable = d.writable && !history;

  const head = (close: ReactNode, sheet: boolean) => (
    <PanelHead className={cn('gap-1 pt-2 pb-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
      <button type="button" className="flex h-5 items-center gap-1 self-start rounded-xs text-caption text-muted hover:text-ink" onClick={onClose}>
        <ArrowUp aria-hidden="true" className="size-3" />
        {d.title}
      </button>
      <div className="flex min-h-7 min-w-0 flex-wrap items-center gap-2">
        <h2 ref={heading} tabIndex={-1} className="min-w-0 truncate text-title text-ink outline-hidden">{name}</h2>
        <Chip tone={count ? 'warning' : 'muted'} fill="well">{pillText}</Chip>
        <span className="flex-1" />
        {!history && doc && (
          <Segmented
            aria-label="Version"
            value={view}
            onValueChange={(v) => setView(v as DrawingView)}
            items={[
              { value: 'agent', label: `Agent’s v${doc.agent_version}` },
              { value: 'mine', label: count ? `Yours v${doc.version}` : 'Yours' },
              { value: 'changes', label: count ? <span className="flex items-center gap-1.5">Changes<span className="rounded-full bg-warning-wash px-1.5 text-meta font-semibold text-warning tabular-nums">{count}</span></span> : 'Changes', disabled: !count },
            ]}
          />
        )}
        <Button variant="primary" size="sm" onClick={onClose}>Done</Button>
        {close}
      </div>
      <p className="text-caption text-muted">{history ? 'What you changed before this message, as the agent received it.' : DESCRIPTION[view]}</p>
      {facts.length > 0 && (
        <p className="flex flex-wrap gap-1.5 text-caption text-muted">
          {facts.map((f) => <span key={f} className="rounded-xs bg-tint-well px-1.5">{f}</span>)}
          {save.state === 'failed' && <span className="text-error">{save.error}</span>}
        </p>
      )}
    </PanelHead>
  );

  const canvas = doc && editable && (
    <div className={cn('absolute inset-0', view !== 'mine' && 'invisible [&_*]:invisible!')}>
      {doc.format === 'drawio' && d.drawio ? (
        <DrawioFrame origin={d.drawio} xml={doc.content} onSave={(xml) => { if (xml !== lastSaved.current) void saveContent(xml); }} />
      ) : current ? (
        <Suspense fallback={<div className="flex h-full items-center justify-center"><Spinner className="border-muted" /></div>}>
          <ExcalidrawCanvas
            elements={current.elements}
            files={current.files}
            theme={scheme}
            background={background}
            onChange={onChange}
            onApi={(a) => {
              canvasApi.current = a;
              // The file as Excalidraw restored it (added defaults): the first edit's save must not carry that noise as a version of its own.
              lastSaved.current = excalidrawFile(a.getSceneElements() as unknown as DrawnElement[], a.getFiles());
              // Dev only: scripts/check-drawings.mjs finds the boxes to double-click through it.
              if (import.meta.env.DEV) (window as unknown as { __uamExcalidraw?: ExcalidrawImperativeAPI }).__uamExcalidraw = a;
              setTimeout(fit, 60);
            }}
          />
        </Suspense>
      ) : null}
    </div>
  );

  const body = (
    <div className="relative min-h-0 flex-1">
      {error && <Note tone="error" role="alert" className="m-4">{error}</Note>}
      {!doc && !error && <div className="flex h-full items-center justify-center"><Spinner className="border-muted" /></div>}
      {save.state === 'conflict' && (
        <Note tone="warn" role="alert" className="absolute inset-x-4 top-3 z-10 flex items-center gap-3 rounded-sm bg-raised px-3 py-2 shadow-float">
          The agent changed this drawing.
          <Button size="sm" variant="secondary" onClick={reload}>Reload</Button>
        </Note>
      )}
      {canvas}
      {doc && view === 'agent' && (
        <div className="absolute inset-0 flex flex-col gap-2.5 overflow-x-hidden overflow-y-auto overscroll-contain px-5 py-4">
          <p className="text-caption text-muted">Read-only. The version the agent last wrote or read (v{doc.agent_version}).</p>
          <Figure picture={doc.format === 'drawio' ? agentDrawio.picture : agentPic} error={agentDrawio.error} alt="Agent’s version" className="max-h-none" />
        </div>
      )}
      {doc && view === 'changes' && (
        <div className="absolute inset-0 flex gap-6 overflow-x-hidden overflow-y-auto overscroll-contain px-5 py-4 max-sm:flex-col">
          <div className="flex min-w-0 flex-1 flex-col gap-2.5">
            <Legend />
            <Figure picture={doc.format === 'drawio' ? changesDrawio.picture : changesPic} error={changesDrawio.error} alt="Changes between the agent’s version and yours" className="max-h-none" />
          </div>
          <div className="flex w-[360px] shrink-0 flex-col gap-3.5 max-sm:w-auto">
            <PanelSection label="Your changes" meta={`since the agent’s v${doc.agent_version}`}>
              <ul className="flex flex-col gap-1" aria-label="Changes">
                {doc.changes.map((c, i) => (
                  <li key={i} className="flex gap-2 text-ui text-ink">
                    <span className={cn('w-3 shrink-0 font-mono font-bold', c.kind === '+' ? 'text-success' : c.kind === '-' ? 'text-error' : 'text-warning')}>{c.kind}</span>
                    <span className="min-w-0 break-words">{c.text}</span>
                  </li>
                ))}
              </ul>
            </PanelSection>
            <PanelSection label={history ? 'Sent with this message' : 'Sent with your next message'} meta="added to your message, no model call">
              <pre className="rounded-sm bg-sunken px-3 py-2 font-mono text-code-sm whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{doc.note}</pre>
            </PanelSection>
            <PanelSection label="What the agent reads" meta="uam_diagram_read · your version">
              <p className="text-caption text-muted">The whole drawing, every node and edge by its label, when it asks for it.</p>
            </PanelSection>
          </div>
        </div>
      )}
    </div>
  );

  const foot = (sheet: boolean) => (
    <PanelFoot className={cn(sheet && 'h-11')}>
      {view === 'mine' && editable && !sheet ? (
        <>
          <span className="flex items-center gap-1.5"><Key>R</Key>box</span>
          <span className="flex items-center gap-1.5"><Key>A</Key>drag box to box</span>
          <span className="flex items-center gap-1.5"><Key>Ctrl</Key><Key>→</Key>connected box</span>
          <span className="flex items-center gap-1.5"><Key>Dbl-click</Key>rename</span>
          <span className="flex items-center gap-1.5"><Key>Del</Key>remove</span>
          <span className="flex items-center gap-1.5"><Key>Ctrl</Key><Key>Z</Key>undo</span>
        </>
      ) : (
        <span>{history ? 'Already with the agent: it replied from this version.' : view === 'mine' ? 'Every change saves to the file.' : DESCRIPTION[view]}</span>
      )}
      <span className="flex-1" />
      {count > 0 && editable && doc?.baseline !== null && <Button size="sm" onClick={revert}>Revert to agent’s</Button>}
    </PanelFoot>
  );

  const label = history ? `Changes to ${name}` : `Edit ${name}`;
  const back = () => target.anchor;
  if (target.phone || !target.anchor.isConnected) {
    return (
      <BottomSheet id={id} open={target.open} onClose={onClose} onClosed={onClosed} label={label} className="h-[85dvh] duration-160" backdropClassName="duration-160" initialFocus={heading} finalFocus={back}>
        <div ref={popup} className="flex min-h-0 flex-1 flex-col">
          {head(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>, true)}
          {body}
          {foot(true)}
        </div>
      </BottomSheet>
    );
  }
  return (
    <BasePopover.Root
      open={target.open}
      modal
      onOpenChange={(o, details) => {
        // Escape inside the canvas ends a text edit or a selection; only a second one, outside it, closes
        // the editor. Excalidraw removes its text editor on Escape, so a detached target was its too.
        const t = details.event.target as Element | null;
        if (!o && details.reason === 'escape-key' && (!t?.isConnected || t.closest('.excalidraw'))) return details.cancel();
        if (!o) onClose();
      }}
      onOpenChangeComplete={(o) => !o && onClosed()}
    >
      <BasePopover.Portal>
        <BasePopover.Backdrop className={cn(backdropClass, 'duration-160')} />
        <LiftedRow row={target.anchor} />
        <BasePopover.Positioner anchor={target.anchor} side="bottom" align="start" sideOffset={6} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup
            ref={popup}
            id={id}
            data-popup=""
            aria-label={label}
            aria-modal="true"
            initialFocus={heading}
            finalFocus={back}
            className={cn('flex h-[calc(var(--available-height)-8px)] min-h-80 w-(--anchor-width) max-w-(--available-width)', readerPopupClass)}
          >
            {head(null, false)}
            {body}
            {foot(false)}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

const LEGEND: readonly [string, string][] = [
  ['Added', 'border-success bg-success-wash'],
  ['Changed', 'border-warning bg-warning-wash'],
  ['Moved', 'border-dashed border-accent bg-accent-wash'],
  ['Removed', 'border-dashed border-error'],
  ['Unchanged', 'border-faint'],
];

function Legend() {
  return (
    <p className="flex flex-wrap gap-3.5 text-caption text-muted">
      {LEGEND.map(([word, swatch]) => (
        <span key={word} className="flex items-center gap-1.5">
          <span aria-hidden="true" className={cn('inline-block size-2.5 rounded-xs border-[1.5px]', swatch)} />
          {word}
        </span>
      ))}
    </p>
  );
}

/**
 * The draw.io embed in Yours: loads the file once the page says `init`, saves on its `autosave`
 * and `save` events. Messages count only from this frame's window.
 */
function DrawioFrame({ origin, xml, onSave }: Readonly<{ origin: string; xml: string; onSave: (xml: string) => void }>) {
  const frame = useRef<HTMLIFrameElement>(null);
  const first = useRef(xml);
  const latest = useRef(onSave);
  useEffect(() => {
    latest.current = onSave;
  }, [onSave]);
  useEffect(() => {
    const el = frame.current;
    if (!el) return;
    const onMessage = (e: MessageEvent) => {
      if (e.source !== el.contentWindow) return;
      const m = drawioMessage(e.data);
      if (!m) return;
      if (m.event === 'init') el.contentWindow?.postMessage(JSON.stringify({ action: 'load', xml: first.current, autosave: 1 }), '*');
      if ((m.event === 'autosave' || m.event === 'save') && m.xml) latest.current(m.xml);
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, []);
  return <iframe ref={frame} title="draw.io editor" src={drawioEmbedUrl(origin)} sandbox={DRAWIO_SANDBOX} className="size-full border-0 bg-raised" />;
}
