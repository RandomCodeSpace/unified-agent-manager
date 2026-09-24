import { AtSign, ExternalLink, FileText, FileType, Image as ImageIcon, Paperclip, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { api, type Attachment } from '../api';
import { formatSize, kindOf, type Kind } from '../lib/attachments';
import { cn } from '../lib/cn';
import { Button, buttonVariants } from './ui/button';
import { Dialog } from './ui/dialog';

/** One upload in the composer, from the moment it is chosen until it is sent or removed. */
export interface Pending {
  key: string;
  name: string;
  size: number;
  kind: Kind;
  /** `data:` URL for images (the CSP forbids `blob:`). */
  preview?: string;
  /** 0…1 while uploading. */
  progress: number;
  status: 'uploading' | 'done' | 'error';
  /** The upload ID once stored. */
  id?: string;
  error?: string;
  abort?: () => void;
}

const KIND_LABEL: Record<Kind, string> = { image: 'Image', pdf: 'PDF', text: 'Text' };

function KindIcon({ kind, className }: { kind: Kind; className?: string }) {
  const Icon = kind === 'image' ? ImageIcon : kind === 'pdf' ? FileType : FileText;
  return <Icon aria-hidden="true" className={className} />;
}

/** A 36px square: the image itself, or the kind's glyph on a well. */
function Thumb({ kind, src, name, className }: { kind: Kind; src?: string; name: string; className?: string }) {
  return (
    <span className={cn('flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-xs bg-sunken', className)}>
      {src ? <img src={src} alt="" className="size-full object-cover" /> : <KindIcon kind={kind} className="size-4 text-muted" />}
      <span className="sr-only">{name}</span>
    </span>
  );
}

/**
 * An upload in the composer: thumbnail or glyph, name, then its state (progress, size and
 * type, or the refusal). A well, not a box: the composer already has the hairline.
 */
export function UploadChip({ item, onRemove }: { item: Pending; onRemove: () => void }) {
  const bar = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    bar.current?.style.setProperty('--fill', `${Math.round(item.progress * 100)}%`);
  }, [item.progress]);
  const failed = item.status === 'error';
  const state = failed ? item.error : item.status === 'uploading' ? `Uploading… ${Math.round(item.progress * 100)}%` : `${formatSize(item.size)} · ${KIND_LABEL[item.kind]}`;
  return (
    <div
      className={cn('relative flex h-11 max-w-72 min-w-0 items-center gap-2 overflow-hidden rounded-sm bg-sunken/60 pr-0.5 pl-1 animate-rise', failed && 'bg-error-wash')}
      role={failed ? 'alert' : undefined}
    >
      <Thumb kind={item.kind} src={item.preview} name={item.name} className={cn(failed && 'opacity-60')} />
      <span className="flex min-w-0 flex-1 flex-col leading-tight">
        <span className="truncate text-ui text-ink">{item.name}</span>
        <span className={cn('truncate text-caption tabular-nums text-muted', failed && 'text-error')} title={failed ? item.error : undefined}>
          {state}
        </span>
      </span>
      <Button size="icon" variant="subtle" className="size-7 text-muted pointer-coarse:size-9" aria-label={`Remove ${item.name}`} onClick={onRemove}>
        <X />
      </Button>
      {item.status === 'uploading' && <span ref={bar} aria-hidden="true" className="absolute inset-x-0 bottom-0 h-0.5 w-(--fill) bg-accent transition-[width] duration-160 ease-app" />}
    </div>
  );
}

/** A referenced project path in the composer. */
export function FileRefChip({ path, onRemove }: { path: string; onRemove: () => void }) {
  return (
    <span className="inline-flex h-7 max-w-full min-w-0 items-center gap-1 rounded-sm bg-sunken/60 pl-1.5 font-mono text-code-sm text-ink animate-rise">
      <AtSign aria-hidden="true" className="size-3 shrink-0 text-faint" />
      <span className="truncate">{path}</span>
      <Button size="icon" variant="subtle" className="size-6 text-muted pointer-coarse:size-9" aria-label={`Remove file reference ${path}`} onClick={onRemove}>
        <X className="!size-3.5" />
      </Button>
    </span>
  );
}

/** Covers the composer while files are dragged over it. */
export function DropOverlay({ note }: { note: string }) {
  return (
    <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-md bg-raised/92 outline-2 -outline-offset-4 outline-dashed outline-accent animate-fade-in">
      <div className="flex flex-col items-center gap-1 text-accent">
        <Paperclip aria-hidden="true" className="size-5" />
        <span className="text-ui font-medium">Drop to attach</span>
        <span className="text-caption text-muted">{note}</span>
      </div>
    </div>
  );
}

/** Small mono chips for a queued prompt's files and uploads. */
export function QueuedExtras({ files = [], attachments = [] }: { files?: string[]; attachments?: Attachment[] }) {
  if (!files.length && !attachments.length) return null;
  return (
    <span className="flex flex-wrap gap-1">
      {files.map((f) => (
        <span key={`f-${f}`} className="inline-flex h-5 items-center gap-1 rounded-xs bg-sunken/60 px-1.5 font-mono text-keycap text-muted">
          <AtSign aria-hidden="true" className="size-2.5" />
          {f}
        </span>
      ))}
      {attachments.map((a, i) => (
        <span key={`a-${a.id ?? i}`} className="inline-flex h-5 items-center gap-1 rounded-xs bg-sunken/60 px-1.5 text-caption text-muted">
          <KindIcon kind={kindOf(a.mime)} className="size-3" />
          <span className="max-w-40 truncate">{a.name}</span>
        </span>
      ))}
    </span>
  );
}

/**
 * A user item's uploads in the transcript: images as thumbnails from the serve route
 * (click opens the lightbox), other files as chips that open the stored copy. Without a
 * stored copy the chip has no link.
 */
export function ItemAttachments({ sessionId, attachments }: { sessionId: string; attachments: Attachment[] }) {
  const [shown, setShown] = useState<Attachment | null>(null);
  const [open, setOpen] = useState(false);
  const images = attachments.filter((a) => kindOf(a.mime) === 'image' && a.id);
  const rest = attachments.filter((a) => !images.includes(a));
  return (
    <>
      {images.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {images.map((a) => (
            <button
              key={a.id}
              type="button"
              className="group/thumb flex max-h-40 max-w-full overflow-hidden rounded-sm bg-sunken transition-[box-shadow] duration-100 hover:shadow-float"
              aria-label={`Open ${a.name}`}
              onClick={() => {
                setShown(a);
                setOpen(true);
              }}
            >
              <img src={api.attachmentUrl(sessionId, a.id!)} alt={a.name} loading="lazy" decoding="async" className="block max-h-40 max-w-full object-contain transition-transform duration-160 ease-app group-hover/thumb:scale-[1.02]" />
            </button>
          ))}
        </div>
      )}
      {rest.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {rest.map((a, i) => (
            <FileChip key={a.id ?? `${a.name}-${i}`} sessionId={sessionId} attachment={a} />
          ))}
        </div>
      )}
      {shown && (
        <Dialog open={open} onOpenChange={setOpen} onClosed={() => setShown(null)} title={shown.name} description={`${KIND_LABEL[kindOf(shown.mime)]}${shown.size ? ` · ${formatSize(shown.size)}` : ''}`} className="max-w-[min(92vw,1100px)]">
          <img src={api.attachmentUrl(sessionId, shown.id!)} alt={shown.name} className="mx-auto block max-h-[72dvh] w-auto max-w-full rounded-sm bg-sunken" />
          <div className="mt-4 flex justify-end">
            <a href={api.attachmentUrl(sessionId, shown.id!)} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'secondary', size: 'md' })}>
              <ExternalLink />
              Open original
            </a>
          </div>
        </Dialog>
      )}
    </>
  );
}

function FileChip({ sessionId, attachment: a }: { sessionId: string; attachment: Attachment }) {
  const kind = kindOf(a.mime);
  const body: ReactNode = (
    <>
      <KindIcon kind={kind} className="size-4 shrink-0 text-muted" />
      <span className="min-w-0 truncate">{a.name}</span>
      <span className="shrink-0 text-caption tabular-nums text-muted">{a.size ? formatSize(a.size) : KIND_LABEL[kind]}</span>
    </>
  );
  const cls = 'inline-flex h-8 max-w-full items-center gap-1.5 rounded-sm bg-sunken/70 px-2 text-ui text-ink';
  if (!a.id) {
    return (
      <span className={cn(cls, 'opacity-80')} title="No stored copy of this file">
        {body}
      </span>
    );
  }
  return (
    <a href={api.attachmentUrl(sessionId, a.id)} target="_blank" rel="noopener noreferrer" className={cn(cls, 'transition-colors duration-100 hover:bg-sunken')} title={`Open ${a.name}`}>
      {body}
    </a>
  );
}
