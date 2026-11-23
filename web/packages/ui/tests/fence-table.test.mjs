import assert from 'node:assert/strict';
import test from 'node:test';
import { fenceTable, parseDelimited } from '../src/lib/fence-table.ts';

test('quoted fields keep commas, escaped quotes and line breaks', () => {
  assert.deepEqual(parseDelimited('name,note\n"Doe, Jane","said ""hi"""\n"two\nlines",x\n', ','), [['name', 'note'], ['Doe, Jane', 'said "hi"'], ['two\nlines', 'x']]);
});
test('CRLF and LF end a record, blank lines are skipped and empty fields are kept', () => {
  assert.deepEqual(parseDelimited('a,b\r\n1,2\r\n\r\n3,\n,4', ','), [['a', 'b'], ['1', '2'], ['3', ''], ['', '4']]);
  assert.deepEqual(parseDelimited('', ','), []);
  assert.deepEqual(parseDelimited('a\tb,c\n"1\t2"\t3', '\t'), [['a', 'b,c'], ['1\t2', '3']]);
});
test('a quote inside an unquoted field is text; an unclosed or trailed quoted field does not parse', () => {
  assert.deepEqual(parseDelimited('size,n\n5" pipe,2', ','), [['size', 'n'], ['5" pipe', '2']]);
  assert.equal(parseDelimited('a,b\n"open,2', ','), null);
  assert.equal(parseDelimited('a,b\n"x"y,2', ','), null);
});
test('a csv or tsv fence with a header and regular rows is a table', () => {
  assert.deepEqual(fenceTable('csv', 'pkg, requests\r\nrouter,"48,210"\r\nauth,31904\r\n'), { columns: ['pkg', 'requests'], rows: [['router', '48,210'], ['auth', '31904']] });
  assert.deepEqual(fenceTable('tsv', 'pkg\tn\nrouter\t1\n'), { columns: ['pkg', 'n'], rows: [['router', '1']] });
});
test('ragged rows, a lone header, one column, an unnamed column and an oversized fence stay code', () => {
  for (const source of ['a,b\n1,2,3', 'a,b\n1', 'a,b\n', 'a\n1\n2', 'a,\n1,2', '', 'a,b\n"open,2', `a,b\n${'1,2\n'.repeat(25_000)}`]) assert.equal(fenceTable('csv', source), null, JSON.stringify(source.slice(0, 20)));
  assert.equal(fenceTable('python', 'a,b\n1,2'), null);
});
test('a json array of two or more flat objects with the same keys is a table', () => {
  assert.deepEqual(fenceTable('json', '[{"name":"a","n":1,"ok":true,"note":null},{"n":2.5,"name":"b","ok":false,"note":"x"}]'), { columns: ['name', 'n', 'ok', 'note'], rows: [['a', 1, 'true', null], ['b', 2.5, 'false', 'x']] });
});
test('any other json stays code', () => {
  for (const source of ['{"a":1}', '[{"a":1}]', '[1,2]', '[{"a":1},{"b":2}]', '[{"a":1},{"a":2,"b":3}]', '[{"a":1,"b":2},{"a":2}]', '[{"a":{"x":1}},{"a":2}]', '[{"a":[1]},{"a":2}]', '[{},{}]', '[{"a":1},null]', '[{"a":1},{"a":2}', 'not json']) assert.equal(fenceTable('json', source), null, source);
});
