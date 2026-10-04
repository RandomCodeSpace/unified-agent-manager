import assert from 'node:assert/strict';
import test from 'node:test';
import { FOLDS, SQUEEZE_MIN, foldToFit } from '../src/lib/toolbarFold.ts';

// A row whose widths follow how many folds it carries: `need[n]` is its content width with n folds, and the
// squeezable label shows `label[n]` of its `full` width (0: hidden, so nothing to show).
function row(width, need, label = [], full = 120) {
  const r = {
    dataset: {},
    clientWidth: width,
    get folds() { return r.dataset.fold ? r.dataset.fold.split(' ').length : 0; },
    get scrollWidth() { return Math.max(width, need[Math.min(r.folds, need.length - 1)]); },
    querySelectorAll: () => {
      const shown = () => label[Math.min(r.folds, label.length - 1)];
      return label.length ? [{ get scrollWidth() { return shown() ? full : 0; }, get clientWidth() { return shown(); } }] : [];
    },
  };
  return r;
}

test('the folds go least important first, the Model picker and then wrapping last', () => {
  assert.deepEqual(FOLDS, ['execution', 'tuning', 'permissions', 'more', 'model', 'wrap']);
});

test('a row with room keeps every label', () => {
  const r = row(900, [700]);
  assert.equal(foldToFit(r), 0);
  assert.equal(r.dataset.fold, '');
});

test('a narrower row takes the fewest folds that fit, each keeping the ones before it', () => {
  const r = row(600, [900, 820, 760, 650, 600, 520]);
  assert.equal(foldToFit(r), 4);
  assert.equal(r.dataset.fold, 'execution tuning permissions more');
});

test('the Model label may truncate, but folds instead of going below a few letters', () => {
  // Fits at every fold from the start, but the label is squeezed to one letter until the Model fold hides it.
  const squeezed = row(400, [400], [10, 10, 10, 10, 10, 0], 120);
  assert.equal(foldToFit(squeezed), FOLDS.indexOf('model') + 1);
  assert.match(squeezed.dataset.fold, / model$/);
  // A label truncated at or above the floor stays.
  const truncated = row(400, [400], [SQUEEZE_MIN], 120);
  assert.equal(foldToFit(truncated), 0);
  // A short name shown in full is not squeezed, however narrow.
  const short = row(400, [400], [30], 30);
  assert.equal(foldToFit(short), 0);
});

test('a row that fits at no fold takes them all, so the actions wrap under it', () => {
  const r = row(300, [900]);
  assert.equal(foldToFit(r), FOLDS.length);
  assert.equal(r.dataset.fold, FOLDS.join(' '));
});

test('a row that widens again unfolds', () => {
  const need = [900, 820, 760, 650, 600, 520];
  const narrow = row(600, need);
  foldToFit(narrow);
  const wide = row(1000, need);
  wide.dataset.fold = narrow.dataset.fold;
  assert.equal(foldToFit(wide), 0);
});
