// Upload rules mirrored from the service (ADR 0004, "Browser attachments"), so the obvious
// refusals are immediate. The server sniffs the bytes and decides; these use the name and
// the browser's type only. No DOM, so the unit tests run in node.

import type { Media } from '../api';

export const LIMITS = {
  /** Uploads one prompt may carry. */
  count: 5,
  image: 3 * 1024 * 1024,
  pdf: 10 * 1024 * 1024,
  text: 256 * 1024,
} as const;

export type Kind = 'image' | 'pdf' | 'text';

export interface FileLike {
  name: string;
  type: string;
  size: number;
}

const IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
const TEXT_APP_TYPES = /^application\/(json|xml|javascript|ecmascript|x-sh|x-shellscript|x-yaml|yaml|toml|x-httpd-php|sql|x-python|x-perl|x-ruby|x-tex|typescript|x-typescript)$/;
const TEXT_EXT = /\.(txt|md|markdown|rst|adoc|csv|tsv|log|json|jsonc|ya?ml|toml|ini|cfg|conf|env|xml|html?|css|scss|less|js|mjs|cjs|jsx|ts|tsx|go|rs|py|rb|php|java|kt|swift|c|h|cc|cpp|hpp|cs|sh|bash|zsh|fish|ps1|sql|proto|graphql|gql|tf|dockerfile|makefile|mod|sum|lock|diff|patch|vue|svelte|astro)$/i;

/** The kind UAM would store this file as, or why it cannot: `svg` (refused by name) or `unknown` (audio, video, archives, other images). */
export function fileKind(f: Pick<FileLike, 'name' | 'type'>): Kind | 'svg' | 'unknown' {
  const type = f.type.toLowerCase();
  const name = f.name.toLowerCase();
  if (type === 'image/svg+xml' || name.endsWith('.svg') || name.endsWith('.svgz')) return 'svg';
  if (IMAGE_TYPES.includes(type)) return 'image';
  if (type === 'application/pdf' || name.endsWith('.pdf')) return 'pdf';
  if (type.startsWith('image/') || type.startsWith('audio/') || type.startsWith('video/') || type.startsWith('font/')) return 'unknown';
  if (type.startsWith('text/') || TEXT_APP_TYPES.test(type) || TEXT_EXT.test(name)) return 'text';
  // No type, or one the OS made up: let the server sniff it.
  if (!type || type === 'application/octet-stream') return 'text';
  return 'unknown';
}

/** The kind of a stored upload, from the MIME type the server reported. */
export function kindOf(mime: string): Kind {
  if (mime.startsWith('image/')) return 'image';
  if (mime === 'application/pdf') return 'pdf';
  return 'text';
}

/**
 * Why `file` cannot be attached now, or null. `existing` is the kinds already attached,
 * `media` the current model's gate (absent means the model reports nothing and takes all).
 */
export function checkUpload(file: FileLike, media: Media | undefined, existing: readonly Kind[], modelName: string): string | null {
  if (existing.length >= LIMITS.count) return `A prompt carries at most ${LIMITS.count} attachments`;
  if (file.size === 0) return 'The file is empty';
  const kind = fileKind(file);
  switch (kind) {
    case 'svg':
      return 'SVG images cannot be attached';
    case 'unknown':
      return 'Only png, jpeg, gif and webp images, PDF files and text files can be attached';
    case 'image': {
      if (media && !media.images) return `${modelName} does not accept images`;
      if (file.size > LIMITS.image) return 'Images can be at most 3 MiB';
      const max = media?.max_images ?? 0;
      const images = existing.filter((k) => k === 'image').length;
      if (max > 0 && images + 1 > max) return `${modelName} accepts at most ${max} image${max === 1 ? '' : 's'} per prompt`;
      return null;
    }
    case 'pdf':
      if (media && !media.pdf) return `${modelName} does not accept PDF files`;
      if (file.size > LIMITS.pdf) return 'PDF files can be at most 10 MiB';
      return null;
    case 'text':
      if (file.size > LIMITS.text) return 'Text files can be at most 256 KiB';
      return null;
  }
}

/** What the attach control adds to its name when the model narrows the kinds; empty when it takes everything. */
export function mediaNote(media: Media | undefined, modelName: string): string {
  if (!media || (media.images && media.pdf)) return '';
  if (!media.images && !media.pdf) return `${modelName} takes text files only`;
  if (!media.images) return `${modelName} takes PDF and text files, not images`;
  return `${modelName} takes images and text files, not PDF`;
}

/** `accept` for the file input: narrowed by the gate; unset when text is the only kind, so the OS picker hides nothing the sniffer might take. */
export function acceptFor(media: Media | undefined): string | undefined {
  if (!media || (!media.images && !media.pdf)) return undefined;
  const parts = ['text/*', '.md', '.txt', '.json', '.yaml', '.yml', '.toml', '.csv', '.log', '.go', '.ts', '.tsx', '.js', '.py', '.rs', '.sh', '.diff', '.patch'];
  if (media.images) parts.unshift(...IMAGE_TYPES);
  if (media.pdf) parts.push('application/pdf', '.pdf');
  return parts.join(',');
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(bytes < 10 * 1024 * 1024 ? 1 : 0)} MB`;
}
