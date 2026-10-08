import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { compile } from '@tailwindcss/node';

const base = fileURLToPath(new URL('../src', import.meta.url));
const styles = await compile(readFileSync(`${base}/index.css`, 'utf8'), { base, onDependency() {} });

test('the vertical fading rule works under a variant (the thought and step rails draw it on ::before)', () => {
  const css = styles.build(['before:fade-rule-y', 'fade-rule-y']);
  assert.match(css, /\.before\\:fade-rule-y::before\s*\{[^}]*linear-gradient\(to bottom/);
  assert.match(css, /\.fade-rule-y\s*\{[^}]*linear-gradient\(to bottom/);
});

test('the composer toolbar folds (lib/toolbarFold) match the row attribute, and the More fold wins over sm:hidden', () => {
  const css = styles.build(['sm:hidden', 'sm:in-data-[fold~=more]:inline-flex', 'in-data-[fold~=model]:hidden']);
  assert.match(css, /:where\(\[data-fold~="model"\]\) \.in-data-\\\[fold\\~\\=model\\\]\\:hidden\s*\{\s*display: none/);
  // Same specificity, so the later rule decides.
  const hidden = css.indexOf('.sm\\:hidden {');
  const shown = css.indexOf(':where([data-fold~="more"]) .sm\\:in-data-');
  assert.ok(hidden >= 0 && shown > hidden, 'the More fold comes after sm:hidden');
});

test('only the ring loops: the stylesheet has no other endless animation', () => {
  const source = readFileSync(`${base}/index.css`, 'utf8');
  const endless = source.split('\n').filter((line) => /\binfinite\b/.test(line));
  assert.deepEqual(endless.map((line) => line.trim()), ['--animate-spin: spin 1s linear infinite;']);
});
