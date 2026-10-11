import { GitPullRequest, X } from 'lucide-react';
import { useCallback, useEffect, useRef } from 'react';
import { popupOpen } from '../App';
import type { SessionDetail } from '../api';
import { InstanceName } from './common';
import { GitLabPipelinePanel } from './GitLabPipeline';
import { PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';

export default function GitLabSheet({ sessionId, session, inline, open, onClose, onClosed }: Readonly<{
  sessionId: string;
  session?: SessionDetail;
  inline: boolean;
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const closeRef = useRef<HTMLButtonElement>(null);
  const close = useCallback(() => {
    onClose();
    const opener = document.getElementById('gitlab-link') ?? document.querySelector<HTMLElement>('main button[aria-label="Task actions"]');
    opener?.focus({ preventScroll: true });
  }, [onClose]);
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || popupOpen()) return;
      const at = document.activeElement;
      if (at && at !== document.body && !closeRef.current?.closest('aside')?.contains(at)) return;
      close();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, close]);
  return <SidePanel id="gitlab" inline={inline} open={open} onClose={close} onClosed={onClosed} label="GitLab" defaultWidth={480}>
    <PanelHeader>
      <GitPullRequest aria-hidden="true" className="size-4 text-muted" />
      <span className="text-title text-ink">GitLab</span>
      <InstanceName />
      <span className="flex-1" />
      <Button ref={closeRef} size="icon-md" aria-label="Close GitLab" className="text-muted" onClick={close}><X /></Button>
    </PanelHeader>
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain p-3">
      <GitLabPipelinePanel sessionId={sessionId} session={session} refreshKey={0} />
    </div>
  </SidePanel>;
}
