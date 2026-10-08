import assert from 'node:assert/strict';
import test from 'node:test';
import { FOLDS, SQUEEZE_MIN, foldToFit } from '../src/lib/toolbarFold.ts';

// A row whose widths follow how many folds it carries: `need[n]` is its content width with n folds, and the
// squeezable label shows `label[n]` of its `full` width (0: hidden, so nothing to show). `checks` counts layout
// reads (each check reads the row's scrollWidth first) and `writes` the fold attribute's writes.
function row(width, need, label = [], full = 120) {
  let fold;
  const r = {
    checks: 0,
    writes: 0,
    dataset: { get fold() { return fold; }, set fold(value) { r.writes++; fold = value; } },
    clientWidth: width,
    get folds() { return r.dataset.fold ? r.dataset.fold.split(' ').length : 0; },
    get scrollWidth() { r.checks++; return Math.max(r.clientWidth, need[Math.min(r.folds, need.length - 1)]); },
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

test('the fold at each width is pinned, whether the row narrows or widens into it', () => {
  const need = [900, 820, 760, 650, 600, 520];
  const expected = [[1000, 0], [900, 0], [899, 1], [800, 2], [700, 3], [620, 4], [560, 5], [500, FOLDS.length]];
  const stepping = row(1000, need);
  for (const [width, folds] of [...expected, ...expected.slice().reverse()]) {
    stepping.clientWidth = width;
    const fresh = row(width, need);
    assert.equal(foldToFit(fresh), folds, `from none at ${width}px`);
    assert.equal(foldToFit(stepping), folds, `from the last fold at ${width}px`);
    assert.equal(stepping.dataset.fold, fresh.dataset.fold);
  }
});

test('a row with room checks its layout once and writes nothing', () => {
  const r = row(900, [700]);
  foldToFit(r);
  r.checks = r.writes = 0;
  assert.equal(foldToFit(r), 0);
  assert.deepEqual({ checks: r.checks, writes: r.writes }, { checks: 1, writes: 0 });
});

test('a folded row steps from its folds instead of unfolding every one and folding them again', () => {
  const r = row(600, [900, 820, 760, 650, 600, 520]);
  assert.equal(foldToFit(r), 4);
  // Still fits: checked where it is, then with one fold fewer, which is put back. Starting from none took five checks.
  r.checks = r.writes = 0;
  assert.equal(foldToFit(r), 4);
  assert.deepEqual({ checks: r.checks, writes: r.writes }, { checks: 2, writes: 2 });
  // Narrower by one fold: one check where it is, one with the next fold.
  r.clientWidth = 560;
  r.checks = r.writes = 0;
  assert.equal(foldToFit(r), 5);
  assert.deepEqual({ checks: r.checks, writes: r.writes }, { checks: 2, writes: 1 });
});
