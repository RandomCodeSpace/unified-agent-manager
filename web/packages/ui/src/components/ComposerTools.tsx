import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Tabs } from '@base-ui/react/tabs';
import { ChevronDown, SlidersHorizontal, X } from 'lucide-react';
import { useId, useImperativeHandle, useRef, useState, type ReactNode, type Ref } from 'react';
import { useApi } from '../ApiContext';
import type { Model, SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { contextLabel, ContextReport, ContextRing, contextShown } from './ComposerUsage';
import { ContextTab } from './Context';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { AgentChoices } from './TaskAgent';
import { UsageTab } from './TaskUsage';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead } from './ui/panel';
import { Tip } from './ui/tooltip';

export type Tool = 'context' | 'usage' | 'agent';
/** Opens the panel on a tab (the `/context` action); false when the Task has no such tab. */
export interface ToolsHandle { open: (tool: Tool) => boolean }

const LABEL: Record<Tool, string> = { context: 'Context', usage: 'Usage', agent: 'Agent' };
const NOTE: Record<Tool, string> = {
  context: 'Read when opened; reopen for current counts.',
  usage: 'Premium request cost is a multiplier, not USD.',
  agent: 'Takes effect from the next turn.',
};

/**
 * The composer's Tools: one button for the Task's context, usage and custom agent, opening one
 * anchored reader (a bottom sheet on phones) with a tab per tool. A tool the Task cannot show has no
 * tab, and a Task with none has no button. The button carries the context ring, the one live value
 * worth keeping on the toolbar. Each tab mounts its body only while shown, so a native read happens
 * on open and is aborted on close; a change of Task, conversation, selection, stage or open state,
 * or of the owning API, closes the panel.
 */
export function ComposerTools({ session, model, agentReason, onAgent, ref }: Readonly<{
  session: SessionDetail;
  model?: Model;
  /** Why the agent cannot change now. */
  agentReason?: string;
  onAgent: (agent: string) => void;
  ref?: Ref<ToolsHandle>;
}>) {
  const api = useApi();
  const id = useId();
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const activeTab = useRef<HTMLButtonElement>(null);
  const tools: Tool[] = [
    ...(contextShown(session) ? ['context' as const] : []),
    ...(session.capabilities.usage_metrics ? ['usage' as const] : []),
    ...(session.capabilities.custom_agents ? ['agent' as const] : []),
  ];
  const scope = [session.id, session.provider, session.conversation_id, session.model, session.effort, session.context_size, session.stage, session.open].join('\u0000');
  const [reader, setReader] = useState<{ open: boolean; phone: boolean; scope: string; owner: typeof api } | null>(null);
  const [chosen, setChosen] = useState<Tool>('context');
  if (reader && (reader.scope !== scope || reader.owner !== api)) setReader(null);
  const tab = tools.includes(chosen) ? chosen : tools[0];
  const show = (tool?: Tool) => {
    if (tool) setChosen(tool);
    setReader({ open: true, phone: window.matchMedia(PHONE).matches, scope, owner: api });
  };
  useImperativeHandle(ref, () => ({
    open: (tool) => {
      if (!tools.includes(tool)) return false;
      show(tool);
      return true;
    },
  }));
  if (!tab) return null;
  const close = () => setReader((r) => r && { ...r, open: false });
  const closed = () => setReader(null);
  const sheet = !!reader?.phone || !anchor;
  const reported = !!(session.context && session.context.limit > 0);
  const label = reported ? `Tools. Context ${contextLabel(session)}` : 'Tools';

  const body = (closeButton: ReactNode) => (
    <Tabs.Root value={tab} onValueChange={(v) => setChosen(v as Tool)} className="flex min-h-0 flex-1 flex-col">
      <PanelHead className={cn('gap-1 pt-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          <SlidersHorizontal aria-hidden="true" className="size-4 shrink-0 text-muted" />
          <h2 className="min-w-0 flex-1 truncate text-title text-ink">Tools</h2>
          {closeButton}
        </div>
        <Tabs.List activateOnFocus loopFocus aria-label="Tool" className="-ml-1 flex gap-0.5 pb-1">
          {tools.map((t) => (
            <Tabs.Tab key={t} value={t} ref={t === tab ? activeTab : undefined} className="flex h-7 items-center rounded-sm px-2 text-ui whitespace-nowrap text-muted outline-hidden hover:text-ink focus-visible:shadow-focus data-active:bg-tint-selected data-active:text-ink pointer-coarse:h-9">
              {LABEL[t]}
            </Tabs.Tab>
          ))}
        </Tabs.List>
      </PanelHead>
      {/* One fixed height for every tab, so switching never moves or resizes the panel. */}
      <Tabs.Panel value={tab} className={cn('flex flex-none flex-col gap-4 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4 outline-hidden', sheet ? 'h-[60dvh]' : 'h-[min(440px,60vh)]')}>
        {tab === 'context' && <ContextTab session={session}><ContextReport session={session} model={model} /></ContextTab>}
        {tab === 'usage' && <UsageTab session={session} />}
        {tab === 'agent' && <AgentChoices session={session} reason={agentReason} onChange={onAgent} />}
      </Tabs.Panel>
      {sheet
        ? <p className="flex min-h-11 shrink-0 items-center px-4 pb-2 text-caption text-muted">{NOTE[tab]}</p>
        : <PanelFoot>
          {tools.length > 1 && <span className="flex items-center gap-1.5"><Key>←</Key><Key>→</Key>tool</span>}
          <span className="flex items-center gap-1.5"><Key>Esc</Key>close</span>
          <span className="flex-1" />
          <span className="truncate">{NOTE[tab]}</span>
        </PanelFoot>}
    </Tabs.Root>
  );

  return <>
    <Tip label={<>Tools: {tools.map((t) => LABEL[t].toLowerCase()).join(', ')}{reported && <span className="block text-on-primary/70">{contextLabel(session)}</span>}</>}>
      <Button ref={setAnchor} id="composer-tools" size="sm" variant="subtle" aria-label={label} aria-haspopup="dialog" aria-expanded={!!reader?.open} aria-controls={reader?.open ? id : undefined} className="text-body pointer-coarse:min-w-11" onClick={() => show()}>
        {reported ? <ContextRing session={session} /> : <SlidersHorizontal aria-hidden="true" className="text-faint" />}
        <span className="in-data-[fold~=model]:hidden">Tools</span>
        <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
      </Button>
    </Tip>
    {reader && (sheet
      ? <BottomSheet id={id} open={reader.open} onClose={close} onClosed={closed} label="Tools" initialFocus={activeTab} finalFocus={() => anchor} className="duration-160" backdropClassName="duration-160">
        {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
      </BottomSheet>
      : <BasePopover.Root open={reader.open} modal onOpenChange={(o) => !o && close()} onOpenChangeComplete={(o) => !o && closed()}>
        <BasePopover.Portal>
          <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
          {reader.open && anchor.isConnected && <LiftedRow row={anchor} />}
          <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
            <BasePopover.Popup id={id} data-popup="" aria-label="Tools" aria-modal="true" initialFocus={activeTab} finalFocus={() => anchor} className="flex max-h-[min(80vh,var(--available-height))] w-[480px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0">
              {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
            </BasePopover.Popup>
          </BasePopover.Positioner>
        </BasePopover.Portal>
      </BasePopover.Root>)}
  </>;
}
