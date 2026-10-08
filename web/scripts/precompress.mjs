// After `vite build`: a brotli (.br) and a gzip (.gz) copy beside each text file of the built
// app, at the highest levels, which the service sends as they are to browsers that accept
// them (Server.precompressed in internal/web/server.go). Static files only: API responses and
// event streams keep the service's own response compression.
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { extname, join } from 'node:path';
import { promisify } from 'node:util';
import { brotliCompress, constants, gzip } from 'node:zlib';

const TEXT = new Set(['.css', '.html', '.js', '.svg', '.webmanifest']);
const dir = process.argv[2];
if (!dir) throw new Error('usage: node scripts/precompress.mjs DIST_DIR');
const [toBrotli, toGzip] = [promisify(brotliCompress), promisify(gzip)];

async function precompress(file) {
  const data = await readFile(file);
  const [br, gz] = await Promise.all([
    toBrotli(data, { params: { [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY, [constants.BROTLI_PARAM_SIZE_HINT]: data.length } }),
    toGzip(data, { level: constants.Z_BEST_COMPRESSION }),
  ]);
  // The header's OS byte follows the build machine; pinned to Unix (3), every platform
  // builds the same file, which release trees check (make check-web).
  gz[9] = 3;
  // A copy no smaller than the file saves nothing; the service then sends the file.
  if (br.length < data.length) await writeFile(`${file}.br`, br);
  if (gz.length < data.length) await writeFile(`${file}.gz`, gz);
}

const entries = await readdir(dir, { recursive: true, withFileTypes: true });
await Promise.all(entries.filter((e) => e.isFile() && TEXT.has(extname(e.name))).map((e) => precompress(join(e.parentPath, e.name))));
