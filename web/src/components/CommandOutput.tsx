import { SquareSlash, SquareTerminal, X } from 'lucide-react';
import { useEffect } from 'react';
import type { CommandResult } from '../api';
import { popupOpen } from '../App';
import { Markdown } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';

/** A native command's output (`/context`, `/env`, `/skills`…), read beside the conversation. */
export interface CommandOutput {
  /** The submission's request ID: a new run replaces the shown output and starts at its top. */
  id: string;
  name: string;
  /** A shell command's output names the command instead of `/name`. */
  title?: string;
  text: string;
  markdown: boolean;
}

/** Output for the panel, not the composer: Markdown, or text (a confirmation's too) longer than one short line. */
export function isPanelOutput(result: CommandResult | null | undefined): result is Extract<CommandResult, { kind: 'text' | 'completed' }> {
  if (result?.kind !== 'text' && result?.kind !== 'completed') return false;
  const text = result.text ?? '';
  return (result.kind === 'text' && !!result.markdown) || text.includes('\n') || text.length > 160;
}

/** The panel's view of a command result that `isPanelOutput` sent to it. */
export function panelOutput(id: string, name: string, result: Extract<CommandResult, { kind: 'text' | 'completed' }>): CommandOutput {
  return { id, name, text: result.text ?? '', markdown: result.kind === 'text' && !!result.markdown };
}

/**
 * The command output panel: a side panel like Files, beside the conversation from the inline
 * breakpoint and a sheet below it, so a long output never grows the composer. Plain output is
 * terminal output, so it keeps its lines in mono; Markdown output renders as Markdown.
 */
export function CommandOutputPanel({ output, inline, open, onClose, onClosed }: Readonly<{
  output: CommandOutput;
  inline: boolean;
  /** False while the panel leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  // Inline, the panel is no dialog: Esc closes it unless a popup owns the key.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented && !popupOpen()) onClose();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, onClose]);

  const heading = output.title ?? `/${output.name}`;
  const Icon = output.title ? SquareTerminal : SquareSlash;
  const label = `Output of ${heading}`;
  return (
    <SidePanel id="command-output" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Command output" defaultWidth={520}>
      {/* Marks the panel's content, so a close returns focus only when focus was in here. */}
      <div data-command-output="" className="contents">
      <PanelHeader>
        <Icon aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <span className="min-w-0 truncate text-title text-ink" title={output.title}>{heading}</span>
        <span className="flex-1" />
        <Button size="icon-md" aria-label="Close command output" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
      <div key={output.id} role="region" aria-label={label} tabIndex={0} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 pb-4">
        {output.markdown ? <Markdown text={output.text} /> : <pre translate="no" className="font-mono text-code-sm whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{output.text}</pre>}
      </div>
      </div>
    </SidePanel>
  );
}
