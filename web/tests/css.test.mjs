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
