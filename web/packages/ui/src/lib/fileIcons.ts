// Which file-type mark a path shows. Pure, so the unit tests run in node.

import type { FileKind } from './fileMarks';

/** Whole names first (lower case): they beat the extension. */
const NAMES: Record<string, FileKind> = {
  dockerfile: 'docker', 'docker-compose.yml': 'docker', 'docker-compose.yaml': 'docker', 'compose.yaml': 'docker', makefile: 'makefile', gnumakefile: 'makefile',
  'go.mod': 'go', 'go.sum': 'go', 'go.work': 'go', '.gitignore': 'git', '.gitattributes': 'git', '.gitmodules': 'git',
};

// A kept set of common types (bundle size); the rest fall back to a plain file.
const EXTENSIONS: Record<string, FileKind> = {
  ts: 'typescript', mts: 'typescript', cts: 'typescript', tsx: 'typescript', js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  go: 'go', py: 'python', rs: 'rust', java: 'java', c: 'c', cpp: 'cpp', cc: 'cpp', h: 'h', hpp: 'h', html: 'html', htm: 'html', css: 'css', scss: 'css',
  json: 'json', jsonc: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml', md: 'markdown', mdx: 'markdown', sh: 'console', bash: 'console', zsh: 'console',
  sql: 'database', env: 'tune', png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', webp: 'image', svg: 'image', ico: 'image', pdf: 'pdf',
  zip: 'zip', tar: 'zip', gz: 'zip', tgz: 'zip', csv: 'table', tsv: 'table', xlsx: 'table', txt: 'document', log: 'document',
};

/** The mark for `path` (a folder's when `folder`): its whole name, then its extension, else a plain file. */
export function fileKind(path: string, folder = false): FileKind {
  if (folder) return 'folder';
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  if (NAMES[name]) return NAMES[name];
  if (name.startsWith('.env')) return 'tune';
  if (name.startsWith('dockerfile') || name.endsWith('.dockerfile')) return 'docker';
  const dot = name.lastIndexOf('.');
  return (dot > 0 && EXTENSIONS[name.slice(dot + 1)]) || 'file';
}
