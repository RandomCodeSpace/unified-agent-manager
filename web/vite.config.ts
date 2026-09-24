import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss(), mockAttachmentRoute()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // Never inline assets as data: URIs; the server's CSP allows fonts from 'self' only.
    assetsInlineLimit: 0,
  },
});

/**
 * Dev only: the in-browser mock (`?mock`) fakes `fetch`, but `<img src>` and links load the
 * attachment serve route natively, so the dev server answers it with a generated file. The
 * id decides the kind: `txt` and `pdf` in the id serve text and a one-page PDF, anything
 * else a small PNG whose colours follow the id. Written without node typings (the tsconfig
 * has none), hence the structural request and response types and the hand-rolled PNG.
 */
function mockAttachmentRoute(): Plugin {
  return {
    name: 'uam-mock-attachments',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((rawReq, rawRes, next) => {
        const req = rawReq as unknown as { url?: string; method?: string };
        const res = rawRes as unknown as { setHeader(name: string, value: string): void; end(body: string | Uint8Array): void };
        const m = /^\/api\/sessions\/[^/]+\/attachments\/([^/?]+)/.exec(req.url ?? '');
        if (!m || req.method !== 'GET') return next();
        const id = decodeURIComponent(m[1]);
        res.setHeader('X-Content-Type-Options', 'nosniff');
        res.setHeader('Cache-Control', 'no-store');
        if (id.includes('txt')) {
          res.setHeader('Content-Type', 'text/plain; charset=utf-8');
          res.end('shell     zsh 5.9\nterminal  dumb · ASCII glyphs\n');
          return;
        }
        if (id.includes('pdf')) {
          res.setHeader('Content-Type', 'application/pdf');
          res.end('%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n');
          return;
        }
        res.setHeader('Content-Type', 'image/png');
        res.end(png([...id].reduce((h, c) => h + c.charCodeAt(0), 0)));
      });
    },
  };
}

function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    let c = (crc ^ byte) & 0xff;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    crc = (crc >>> 8) ^ c;
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function adler32(bytes: Uint8Array): number {
  let a = 1;
  let b = 0;
  for (const byte of bytes) {
    a = (a + byte) % 65521;
    b = (b + a) % 65521;
  }
  return ((b << 16) | a) >>> 0;
}

function u32(n: number): Uint8Array {
  return new Uint8Array([(n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff]);
}

function concat(parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

/** A zlib stream of stored (uncompressed) deflate blocks: valid for any PNG decoder, no zlib needed. */
function zlibStored(data: Uint8Array): Uint8Array {
  const blocks: Uint8Array[] = [new Uint8Array([0x78, 0x01])];
  for (let at = 0; at < data.length || at === 0; at += 65535) {
    const slice = data.subarray(at, Math.min(at + 65535, data.length));
    const last = at + 65535 >= data.length ? 1 : 0;
    blocks.push(new Uint8Array([last, slice.length & 0xff, slice.length >>> 8, ~slice.length & 0xff, (~slice.length >>> 8) & 0xff]), slice);
    if (data.length === 0) break;
  }
  blocks.push(u32(adler32(data)));
  return concat(blocks);
}

function chunk(type: string, data: Uint8Array): Uint8Array {
  const body = concat([new TextEncoder().encode(type), data]);
  return concat([u32(data.length), body, u32(crc32(body))]);
}

/** A 320×200 RGB PNG: a soft gradient with a diagonal band, tinted by `seed`. */
function png(seed: number, w = 320, h = 200): Uint8Array {
  const stride = w * 3 + 1;
  const raw = new Uint8Array(stride * h);
  const tint = seed % 3;
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * stride + 1 + x * 3;
      const t = x / w;
      const u = y / h;
      const band = Math.abs(x - y * 1.1 - w * 0.3) < 22 ? 0.55 : 1;
      const r = tint === 0 ? 205 - 50 * t : tint === 1 ? 170 + 40 * u : 190 - 30 * u;
      const g = tint === 0 ? 195 - 20 * u : tint === 1 ? 185 - 40 * t : 200 - 60 * t;
      const b = tint === 0 ? 170 + 50 * t : tint === 1 ? 160 + 30 * t : 175 + 40 * u;
      raw[i] = Math.round(r * band);
      raw[i + 1] = Math.round(g * band);
      raw[i + 2] = Math.round(b * band);
    }
  }
  const ihdr = concat([u32(w), u32(h), new Uint8Array([8, 2, 0, 0, 0])]);
  return concat([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk('IHDR', ihdr), chunk('IDAT', zlibStored(raw)), chunk('IEND', new Uint8Array())]);
}
