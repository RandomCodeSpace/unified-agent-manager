import { AtSign, ExternalLink, FileText, FileType, Image as ImageIcon, Paperclip, TriangleAlert, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { api, type Attachment } from '../api';
import { formatSize, kindOf, type Kind } from '../lib/attachments';
import { cn } from '../lib/cn';
import { previewClick } from '../lib/preview';
import { usePreview } from '../lib/previewContext';
import { Button, buttonVariants } from './ui/button';
import { Chip } from './ui/chip';
import { ViewerDialog } from './ui/dialog';

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
      className={cn('relative flex h-11 max-w-72 min-w-0 items-center gap-2 overflow-hidden rounded-sm bg-tint-well pr-0.5 pl-1 animate-rise', failed && 'bg-error-wash')}
      role={failed ? 'alert' : undefined}
    >
      <Thumb kind={item.kind} src={item.preview} name={item.name} className={cn(failed && 'opacity-60')} />
      <span className="flex min-w-0 flex-1 flex-col leading-tight">
        <span className="truncate text-ui text-ink" title={item.name}>{item.name}</span>
        <span className={cn('truncate text-caption tabular-nums text-muted', failed && 'text-error')} title={failed ? item.error : undefined}>
          {state}
        </span>
      </span>
      <Button size="icon" variant="subtle" className="text-muted" aria-label={`Remove ${item.name}`} onClick={onRemove}>
        <X />
      </Button>
      {item.status === 'uploading' && <span ref={bar} aria-hidden="true" className="absolute inset-x-0 bottom-0 h-0.5 w-(--fill) bg-accent transition-[width] duration-160 ease-app" />}
    </div>
  );
}

/** A referenced project path in the composer. */
export function FileRefChip({ path, onRemove }: { path: string; onRemove: () => void }) {
  return (
    <span className="inline-flex h-7 max-w-full min-w-0 items-center gap-1 rounded-sm bg-tint-well pl-1.5 text-caption text-ink animate-rise">
      <AtSign aria-hidden="true" className="size-3 shrink-0 text-faint" />
      <span className="truncate" title={path}>{path}</span>
      <Button size="icon-sm" variant="subtle" className="text-muted" aria-label={`Remove file reference ${path}`} onClick={onRemove}>
        <X className="!size-3.5" />
      </Button>
    </span>
  );
}

/** Covers the composer while files are dragged over it. */
export function DropOverlay({ note }: { note: string }) {
  return (
    <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-md bg-raised outline-2 -outline-offset-4 outline-dashed outline-accent animate-fade-in">
      <div className="flex flex-col items-center gap-1 text-accent">
        <Paperclip aria-hidden="true" className="size-5" />
        <span className="text-ui font-medium">Drop to attach</span>
        <span className="text-caption text-muted">{note}</span>
      </div>
    </div>
  );
}

/** Small chips for a queued prompt's files and uploads. */
export function QueuedExtras({ files = [], attachments = [] }: { files?: string[]; attachments?: Attachment[] }) {
  if (!files.length && !attachments.length) return null;
  return (
    <span className="flex flex-wrap gap-1">
      {files.map((f) => (
        <Chip key={`f-${f}`} fill="well" className="gap-1 text-meta">
          <AtSign aria-hidden="true" className="size-2.5" />
          {f}
        </Chip>
      ))}
      {attachments.map((a, i) => (
        <Chip key={`a-${a.id ?? i}`} fill="well" className="gap-1">
          <KindIcon kind={kindOf(a.mime)} className="size-3" />
          <span className="max-w-40 truncate" title={a.name}>{a.name}</span>
        </Chip>
      ))}
    </span>
  );
}

/** A stored image the serve route has: an upload with an ID, or an image a tool returned. */
export interface StoredImage {
  id: string;
  mime: string;
  name?: string;
  size?: number;
}

/**
 * Thumbnails up to 160px high from the serve route; clicking one opens the lightbox with
 * **Open original**. Shared by a user turn's uploads and a tool row's images.
 */
export function ImageThumbs({ sessionId, images, className }: { sessionId: string; images: StoredImage[]; className?: string }) {
  const preview = usePreview();
  const [shown, setShown] = useState<StoredImage | null>(null);
  const [open, setOpen] = useState(false);
  if (!images.length) return null;
  const nameOf = (a: StoredImage) => a.name || 'Image';
  return (
    <>
      <div className={cn('flex flex-wrap gap-1.5', className)}>
        {images.map((a) => (
          <button
            key={a.id}
            type="button"
            className="lift flex max-h-40 max-w-full items-center justify-center overflow-hidden rounded-sm bg-sunken pointer-coarse:min-h-11 pointer-coarse:min-w-11"
            aria-label={`Open ${nameOf(a)}`}
            onClick={event => {
              if (preview) {
                preview({ url: api.attachmentUrl(sessionId, a.id), name: nameOf(a), description: `${KIND_LABEL[kindOf(a.mime)]}${a.size ? ` · ${formatSize(a.size)}` : ''}`, image: true }, event.currentTarget);
                return;
              }
              setShown(a);
              setOpen(true);
            }}
          >
            <img src={api.attachmentUrl(sessionId, a.id)} alt={nameOf(a)} loading="lazy" decoding="async" className="block max-h-40 max-w-full object-contain transition-transform duration-160 ease-app group-hover/thumb:scale-[1.02]" />
          </button>
        ))}
      </div>
      {!preview && shown && (
        <Lightbox
          open={open}
          onOpenChange={setOpen}
          onClosed={() => setShown(null)}
          title={nameOf(shown)}
          description={`${KIND_LABEL[kindOf(shown.mime)]}${shown.size ? ` · ${formatSize(shown.size)}` : ''}`}
          src={api.attachmentUrl(sessionId, shown.id)}
          alt={nameOf(shown)}
          footer={
            <a href={api.attachmentUrl(sessionId, shown.id)} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'secondary', size: 'md' })}>
              <ExternalLink />
              Open original
            </a>
          }
        />
      )}
    </>
  );
}

/**
 * A user item's uploads in the transcript: images as thumbnails from the serve route
 * (click opens the lightbox), other files as chips that open the stored copy. Without a
 * stored copy the chip has no link. A document the model did not receive as a document
 * gets a warning line saying how it was read instead.
 */
export function ItemAttachments({ sessionId, attachments }: { sessionId: string; attachments: Attachment[] }) {
  const images = attachments.filter((a): a is Attachment & { id: string } => kindOf(a.mime) === 'image' && !!a.id);
  const rest = attachments.filter((a) => !images.includes(a as Attachment & { id: string }));
  return (
    <>
      <ImageThumbs sessionId={sessionId} images={images} />
      {rest.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {rest.map((a, i) => (
            <FileChip key={a.id ?? `${a.name}-${i}`} sessionId={sessionId} attachment={a} />
          ))}
        </div>
      )}
      {rest.filter((a) => a.not_native).map((a, i) => (
        <p key={`nn-${a.id ?? i}`} className="flex items-start gap-1.5 text-caption text-warning">
          <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
          <span>
            This model can’t take {a.name} as a PDF. The agent got the file to read with the tools on this machine instead,
            which can miss scanned pages, images and layout, or fail without a PDF text tool.
          </span>
        </p>
      ))}
    </>
  );
}

/** The image lightbox (DESIGN.md): the image up to 94vw by 1400px and 78dvh on a see-through scrim, `footer` right-aligned under it. */
export function Lightbox({ open, onOpenChange, onClosed, title, description, src, alt, footer }: { open: boolean; onOpenChange: (open: boolean) => void; onClosed?: () => void; title: ReactNode; description?: ReactNode; src: string; alt: string; footer?: ReactNode }) {
  return (
    <ViewerDialog open={open} onOpenChange={onOpenChange} onClosed={onClosed} title={title} description={description} footer={footer}>
      <img src={src} alt={alt} className="mx-auto block max-h-[78dvh] w-auto max-w-full min-h-0 rounded-sm bg-sunken shadow-modal" />
    </ViewerDialog>
  );
}

function FileChip({ sessionId, attachment: a }: { sessionId: string; attachment: Attachment }) {
  const preview = usePreview();
  const kind = kindOf(a.mime);
  const body: ReactNode = (
    <>
      <KindIcon kind={kind} className="size-4 shrink-0 text-muted" />
      <span className="min-w-0 truncate" title={a.name}>{a.name}</span>
      <span className="shrink-0 text-caption tabular-nums text-muted">{a.size ? formatSize(a.size) : KIND_LABEL[kind]}</span>
    </>
  );
  const cls = 'inline-flex h-8 max-w-full items-center gap-1.5 rounded-sm bg-tint-well px-2 text-ui text-ink';
  if (!a.id) {
    return (
      <span className={cn(cls, 'opacity-80')} title="No stored copy of this file">
        {body}
      </span>
    );
  }
  return (
    <a href={api.attachmentUrl(sessionId, a.id)} target="_blank" rel="noopener noreferrer" className={cn(cls, 'transition-colors duration-100 hover:bg-sunken')} title={`Open ${a.name}`} onClick={event => {
      if (preview && a.id && previewClick(event)) {
        event.preventDefault();
        preview({ url: api.attachmentUrl(sessionId, a.id), name: a.name }, event.currentTarget);
      }
    }}>
      {body}
    </a>
  );
}
