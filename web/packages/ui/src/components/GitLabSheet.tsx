import { ExternalLink, GitPullRequest, X } from 'lucide-react';
import { useCallback, useContext, useEffect, useRef } from 'react';
import { popupOpen, threadCovered } from '../App';
import type { SessionDetail } from '../api';
import { gitLabProjectUrl, type GitLabRef } from '../lib/gitlabRefs';
import { cn } from '../lib/cn';
import { InstanceName } from './common';
import { GitLabItemView } from './GitLabItems';
import { GitLabItemsContext, useGitLabItemHead } from './GitLabStrip';
import { PanelHeader, SidePanel } from './Subagents';
import { Button, buttonVariants } from './ui/button';
import { Tip } from './ui/tooltip';

/** An item opened from the conversation and the turn's other items, which the panel steps between. */
export interface GitLabOpenItem { refs: GitLabRef[]; ref: GitLabRef }

export default function GitLabSheet({ session, item, onItem, inline, open, onClose, onClosed }: Readonly<{
  session?: SessionDetail;
  /** The item opened from the conversation. */
  item: GitLabOpenItem | null;
  onItem: (ref: GitLabRef) => void;
  inline: boolean;
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const closeRef = useRef<HTMLButtonElement>(null);
  const { label, url } = useGitLabItemHead(item?.ref);
  const origin = useContext(GitLabItemsContext)?.origin;
  const close = useCallback(() => {
    onClose();
    const opener = document.getElementById('gitlab-link') ?? document.querySelector<HTMLElement>('main button[aria-label="Thread actions"]');
    opener?.focus({ preventScroll: true });
  }, [onClose]);
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || popupOpen() || threadCovered()) return;
      const at = document.activeElement;
      if (at && at !== document.body && !closeRef.current?.closest('aside')?.contains(at)) return;
      close();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, close]);
  return <SidePanel id="gitlab" inline={inline} open={open} onClose={close} onClosed={onClosed} label="GitLab" defaultWidth={520}>
    <PanelHeader>
      <GitPullRequest aria-hidden="true" className="size-4 text-muted" />
      <span className="min-w-0 truncate text-title text-ink">{item && label ? label : 'GitLab'}</span>
      {origin && <a href={gitLabProjectUrl(origin)} target="_blank" rel="noreferrer" title={`Open ${origin.path} in GitLab`} className="min-w-0 shrink-[2] truncate text-caption text-muted hover:text-ink hover:underline">{origin.path}</a>}
      <InstanceName />
      <span className="flex-1" />
      {item && url && <Tip label="Open in GitLab"><a href={url} target="_blank" rel="noreferrer" aria-label="Open in GitLab" className={cn(buttonVariants({ size: 'icon' }), 'text-muted')}><ExternalLink className="size-4" /></a></Tip>}
      <Button ref={closeRef} size="icon-md" aria-label="Close GitLab" className="text-muted" onClick={close}><X /></Button>
    </PanelHeader>
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain p-3">
      {item && <GitLabItemView session={session} refs={item.refs} current={item.ref} onOpen={onItem} />}
    </div>
  </SidePanel>;
}
