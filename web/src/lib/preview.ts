import { localPath, taskFile } from './markdown.ts';

/** Both the response prefix and its decoded UTF-8 text stay within this budget. */
export const PREVIEW_BYTES = 64 * 1024;

export type PreviewKind = 'image' | 'html' | 'pdf' | 'text' | 'binary';
export interface PreviewMetadata { mime: string; size?: number; kind: PreviewKind }
export interface TextPreview { text: string; truncated: boolean }

export type FileFormat = 'image' | 'pdf' | 'code' | 'data' | 'archive' | 'audio' | 'video' | 'sheet' | 'text' | 'file';

/** Display hints only. MIME and the server's checks still decide how a file is opened. */
export function fileLabel(path: string): { path: string; name: string; format: FileFormat } {
  const name = path.split('/').pop() || path;
  const dot = name.lastIndexOf('.');
  const extension = dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
  let format: FileFormat = 'file';
  if (/^(png|jpe?g|gif|webp|svg|avif|bmp|ico|tiff?)$/.test(extension)) format = 'image';
  else if (extension === 'pdf') format = 'pdf';
  else if (/^(html?|css|scss|less|js|mjs|cjs|jsx|ts|tsx|go|rs|py|rb|php|java|kt|swift|c|h|cc|cpp|hpp|cs|sh|bash|zsh|fish|ps1|sql|proto|graphql|gql|tf|vue|svelte|astro)$/.test(extension)) format = 'code';
  else if (/^(jsonc?|ya?ml|toml|xml|ini|cfg|conf|env|lock)$/.test(extension)) format = 'data';
  else if (/^(zip|tar|gz|bz2|xz|7z|rar|tgz)$/.test(extension)) format = 'archive';
  else if (/^(mp3|wav|ogg|flac|aac|m4a)$/.test(extension)) format = 'audio';
  else if (/^(mp4|webm|mov|avi|mkv)$/.test(extension)) format = 'video';
  else if (/^(csv|tsv|xlsx?|ods)$/.test(extension)) format = 'sheet';
  else if (/^(txt|md|markdown|rst|adoc|log|diff|patch|docx?|odt|rtf)$/.test(extension)) format = 'text';
  return { path, name, format };
}

/** Eligibility only. The explicit click still asks the server to authorize this exact file. */
export function tempFile(href: string | undefined, workdir: string | undefined, meta: { temp_root?: string; temp_root_aliases?: readonly string[] } | null): { path: string; hash: string } | null {
  if (!href || !meta?.temp_root || taskFile(href, workdir)) return null;
  const path = localPath(href.replace(/[?#].*$/s, ''));
  if (!path?.startsWith('/') || /\p{Cc}/u.test(path) || new TextEncoder().encode(path).length > 4096) return null;
  if (path.slice(1).split('/').some(part => !part || part === '.' || part === '..')) return null;
  const roots = [meta.temp_root, ...(meta.temp_root_aliases ?? []).slice(0, 1)].map(root => root.replace(/\/+$/, '')).filter(root => root.startsWith('/'));
  const root = roots.find(root => path.startsWith(`${root}/`));
  if (!root) return null;
  // The canonical/configured spellings name the same root. Neither spelling may turn
  // a project under that root into a temporary-file grant.
  const projectRoot = roots.find(root => workdir === root || workdir?.startsWith(`${root}/`));
  if (projectRoot && workdir) {
    const relativeProject = workdir.slice(projectRoot.length).replace(/\/+$/, '');
    const relativePath = path.slice(root.length);
    if (!relativeProject || relativePath === relativeProject || relativePath.startsWith(`${relativeProject}/`)) return null;
  }
  return { path, hash: /#.*$/s.exec(href)?.[0] ?? '' };
}

/** One active preview request and any exact-file grant acquired by that request. */
export class PreviewOwner {
  private current?: AbortController;
  private revoke?: () => Promise<unknown>;

  start(): AbortController {
    this.stop();
    this.current = new AbortController();
    return this.current;
  }

  owns(controller: AbortController): boolean {
    return this.current === controller && !controller.signal.aborted;
  }

  retain(controller: AbortController, revoke: () => Promise<unknown>): boolean {
    if (!this.owns(controller)) {
      void revoke().catch(() => {});
      return false;
    }
    this.revoke = revoke;
    return true;
  }

  stop(): void {
    const controller = this.current;
    const revoke = this.revoke;
    this.current = undefined;
    this.revoke = undefined;
    controller?.abort();
    // Cleanup uses a fresh request, not the aborted content/create signal. If the
    // response was lost or authentication expired, the server's bounded TTL remains.
    if (revoke) void revoke().catch(() => {});
  }
}

export function previewMetadata(headers: Headers): PreviewMetadata {
  const mime = (headers.get('Content-Type') ?? '').split(';')[0].trim().toLowerCase();
  const length = headers.get('Content-Length');
  const size = length !== null && /^\d+$/.test(length) && Number.isSafeInteger(Number(length)) ? Number(length) : undefined;
  const kind: PreviewKind = mime === 'text/html' ? 'html' : mime === 'application/pdf' ? 'pdf'
    : /^image\/(png|jpeg|gif|webp|svg\+xml|avif|bmp|x-icon)$/.test(mime) ? 'image'
      : mime.startsWith('text/') || /^(application\/(json|xml|javascript)|[^/]+\/[^;]+\+(json|xml))$/.test(mime) ? 'text' : 'binary';
  return { mime, size, kind };
}

/** Keep the fragment last and preserve existing query parameters and literal escaping. */
export function downloadUrl(url: string): string {
  const hash = url.indexOf('#');
  const path = hash < 0 ? url : url.slice(0, hash);
  return `${path}${path.includes('?') ? '&' : '?'}download=1${hash < 0 ? '' : url.slice(hash)}`;
}

/** Only an ordinary primary click replaces a link's native new-tab/download behavior. */
export function previewClick(event: { button: number; altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; defaultPrevented: boolean }): boolean {
  return event.button === 0 && !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.defaultPrevented;
}

/**
 * A Range request is not a transfer guarantee. Read at most the bounded prefix, cancel
 * an ignored/oversized response, and never hand an unrestricted body to text().
 */
export async function readTextPreview(response: Response, signal?: AbortSignal): Promise<TextPreview> {
  const reader = response.body?.getReader();
  const abort = () => { void reader?.cancel().catch(() => {}); };
  signal?.addEventListener('abort', abort, { once: true });
  try {
    signal?.throwIfAborted();
    const encoding = response.headers.get('Content-Encoding');
    if (encoding && encoding.toLowerCase() !== 'identity') throw new Error('The server encoded the preview unexpectedly. Open the full file instead.');
    if (response.status === 416 && response.headers.get('Content-Range') === 'bytes */0') return { text: '', truncated: false };
    if (response.status !== 200 && response.status !== 206) throw new Error('The server could not provide a text preview. Open the full file instead.');

    let expected: number | undefined;
    let total: number | undefined;
    if (response.status === 206) {
      const range = /^bytes 0-(\d+)\/(\d+|\*)$/.exec(response.headers.get('Content-Range') ?? '');
      if (!range || !Number.isSafeInteger(Number(range[1])) || Number(range[1]) >= PREVIEW_BYTES) throw new Error('The server returned an invalid preview range.');
      expected = Number(range[1]) + 1;
      if (range[2] !== '*') {
        total = Number(range[2]);
        if (!Number.isSafeInteger(total) || total < expected) throw new Error('The server returned an invalid preview size.');
      }
    }
    const length = previewMetadata(response.headers).size;
    if (expected !== undefined && length !== undefined && expected !== length) throw new Error('The server returned an inconsistent preview size.');
    if (response.status === 200) total = length;
    const decoder = new TextDecoder('utf-8', { fatal: false });
    const encoder = new TextEncoder();
    const parts: string[] = [];
    let received = 0;
    let decoded = 0;
    let stoppedAtBudget = false;
    let truncated = total !== undefined && total > PREVIEW_BYTES;
    const append = (text: string) => {
      const bytes = encoder.encode(text).length;
      if (decoded + bytes <= PREVIEW_BYTES) {
        parts.push(text);
        decoded += bytes;
        return;
      }
      let kept = '';
      for (const char of text) {
        const point = char.codePointAt(0)!;
        const size = point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4;
        if (decoded + size > PREVIEW_BYTES) break;
        kept += char;
        decoded += size;
      }
      parts.push(kept);
      truncated = true;
    };
    while (reader) {
      const { done, value } = await reader.read();
      signal?.throwIfAborted();
      if (done) break;
      if (expected !== undefined && received + value.byteLength > expected) throw new Error('The text preview exceeded its advertised range.');
      const remaining = PREVIEW_BYTES - received;
      const taken = value.subarray(0, remaining);
      received += taken.byteLength;
      append(decoder.decode(taken, { stream: true }));
      if (taken.byteLength < value.byteLength || received === PREVIEW_BYTES || decoded === PREVIEW_BYTES) {
        stoppedAtBudget = true;
        truncated ||= taken.byteLength < value.byteLength || total === undefined || total > received;
        break;
      }
    }
    // Make room for the decoder's final replacement character when the byte boundary
    // cuts UTF-8. Keeping it makes the clipped encoding visible within the text budget.
    const tail = decoder.decode();
    const tailBytes = encoder.encode(tail).length;
    if (tailBytes && decoded + tailBytes > PREVIEW_BYTES) {
      let prefix = parts.join('');
      while (prefix && decoded + tailBytes > PREVIEW_BYTES) {
        const last = prefix.charCodeAt(prefix.length - 1);
        const units = last >= 0xdc00 && last <= 0xdfff ? 2 : 1;
        decoded -= encoder.encode(prefix.slice(-units)).length;
        prefix = prefix.slice(0, -units);
      }
      parts.length = 0;
      parts.push(prefix);
      truncated = true;
    }
    append(tail);
    if (expected !== undefined && received < expected && !stoppedAtBudget) throw new Error('The text preview ended before its advertised range.');
    if (total !== undefined && received < total) truncated = true;
    return { text: parts.join(''), truncated };
  } finally {
    signal?.removeEventListener('abort', abort);
    await reader?.cancel().catch(() => {});
    reader?.releaseLock();
  }
}
