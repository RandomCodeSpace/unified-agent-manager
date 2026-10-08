import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { THEME_KEY, parseTheme, resolveTheme } from '../src/lib/theme.ts';

const css = readFileSync(new URL('../src/index.css', import.meta.url), 'utf8');
const colors = (block) => Object.fromEntries([...block.matchAll(/--color-([a-z-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
const light = colors(css.match(/@theme \{([\s\S]*?)--font-sans/)[1]);
const dark = colors(css.match(/:root\[data-theme='dark'\] \{([\s\S]*?)\n {2}\}/)[1]);

test('the theme matches the system unless the browser stored Light or Dark', () => {
  assert.equal(THEME_KEY, 'uam.theme');
  assert.equal(parseTheme(null), 'system');
  assert.equal(parseTheme('light'), 'light');
  assert.equal(parseTheme('dark'), 'dark');
  assert.equal(parseTheme('black'), 'system');
  assert.equal(parseTheme(''), 'system');
  assert.equal(resolveTheme('system', true), 'dark');
  assert.equal(resolveTheme('system', false), 'light');
  assert.equal(resolveTheme('light', true), 'light');
  assert.equal(resolveTheme('dark', false), 'dark');
});

test('the pre-paint script picks the scheme lib/theme.ts does', () => {
  const script = readFileSync(new URL('../src/theme.js', import.meta.url), 'utf8');
  for (const stored of [null, 'light', 'dark', 'garbage']) {
    for (const systemDark of [false, true]) {
      const root = { dataset: {} };
      vm.runInNewContext(script, {
        localStorage: { getItem: (key) => (key === THEME_KEY ? stored : null) },
        window: { matchMedia: () => ({ matches: systemDark }) },
        document: { documentElement: root },
      });
      assert.equal(root.dataset.theme, resolveTheme(parseTheme(stored), systemDark), `stored ${stored}, system dark ${systemDark}`);
    }
  }
});

test('every colour token has a dark value', () => {
  assert.deepEqual(Object.keys(dark).sort(), Object.keys(light).sort());
});

// WCAG relative luminance and contrast of #rrggbb colours.
const luminance = (hex) => {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a, b) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

test('dark text keeps AA on every surface, and non-text faint keeps 3:1', () => {
  const grounds = ['rail', 'canvas', 'surface', 'raised', 'sunken', 'bubble', 'code-bg', 'tint-hover', 'tint-selected', 'tint-well'];
  const pairs = [];
  for (const text of ['ink', 'body', 'muted', 'accent', 'attention', 'success', 'warning', 'error']) for (const ground of grounds) pairs.push([text, ground, 4.5]);
  for (const signal of ['attention', 'success', 'warning', 'error', 'info', 'accent']) pairs.push([signal, `${signal}-wash`, 4.5]);
  for (const ground of grounds) pairs.push(['faint', ground, 3]);
  pairs.push(['on-primary', 'primary', 4.5], ['on-accent', 'accent', 4.5], ['diff-add-text', 'diff-add-bg', 4.5], ['diff-del-text', 'diff-del-bg', 4.5]);
  for (const badge of Object.keys(dark).filter((k) => k.startsWith('badge-'))) pairs.push(['on-primary', badge, 4.5]);
  for (const [fg, bg, min] of pairs) assert.ok(contrast(dark[fg], dark[bg]) >= min, `${fg} on ${bg}: ${contrast(dark[fg], dark[bg]).toFixed(2)} < ${min}`);
});
