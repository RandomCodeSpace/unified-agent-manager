import assert from 'node:assert/strict';
import test from 'node:test';
import { fileKind } from '../src/lib/fileIcons.ts';

test('fileKind reads whole names before extensions, then falls back to a plain file', () => {
  const cases = {
    'web/src/App.tsx': 'typescript', 'internal/web/changes.go': 'go', 'go.mod': 'go', 'web/package.json': 'json', '.gitignore': 'git',
    'Dockerfile': 'docker', 'build/app.Dockerfile': 'docker', 'Makefile': 'makefile', '.env.local': 'tune', 'README.md': 'markdown',
    'assets/theme.css': 'css', 'scripts/run.sh': 'console', 'logo.PNG': 'image', 'main.kt': 'file', '.bashrc': 'file', 'notes': 'file', 'archive.tar.gz': 'zip',
  };
  for (const [path, kind] of Object.entries(cases)) assert.equal(fileKind(path), kind, path);
  assert.equal(fileKind('src', true), 'folder');
});
