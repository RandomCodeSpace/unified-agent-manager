import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { localPath, splitBlocks, taskFile } from '../src/lib/markdown.ts';

const html = (text) => renderToStaticMarkup(createElement(ReactMarkdown, { remarkPlugins: [remarkGfm] }, text));

const SAMPLES = [
  'One paragraph.\n\nAnother **one**.\n\n## Heading\n\nText after.',
  '1. first\n\n2. second, loose\n\n3. third\n\nAfter the list.',
  '- a\n- b\n\n  continued in b\n\n- c\n\nPara',
  'Before\n\n```go\nfunc main() {\n\n\tprintln("x")\n}\n```\n\nAfter the fence.',
  '~~~\nunclosed tilde fence\n\nstill code',
  'Para\n\n    indented code\n\n    more code\n\nNext para',
  '> quote one\n\n> quote two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nDone.',
  'Text[^1] and [ref].\n\n[^1]: A footnote\n\n    continued\n\n[ref]: https://example.com',
  '<!--\n\ncomment\n\n-->\n\nText',
  'Title\n=====\n\n---\n\n- [x] task\n- [ ] open\n\n* * *\n\nend',
  '\n\nleading blanks\n\n\n\ntrailing blanks\n\n',
  '1) paren list\n\n2) second\n\nText\n\n3. new list',
  'A\n\n```\ncode with ``` inside is not a closer ```\n\nstill code\n```\n\nB',
  'Steps:\n\n1. Run:\n\n   ```sh\n   make\n\n   make test\n   ```\n\n2. Check the output\n   - nested\n\n     nested para\n\nDone, see `x`.\n\n```mermaid\nflowchart LR\n\n  A --> B\n```',
];

test('the blocks render exactly as the whole text', () => {
  for (const text of SAMPLES) assert.equal(splitBlocks(text).map(html).join('\n'), html(text), text);
});

test('every streamed prefix renders as the whole prefix, and finished blocks never change', () => {
  for (const text of SAMPLES) {
    // A definition or an HTML comment arriving later makes the whole text one block again.
    const merges = /\n\[|<!--/.test(text);
    let previous = [];
    for (let n = 1; n <= text.length; n++) {
      const prefix = text.slice(0, n);
      const blocks = splitBlocks(prefix);
      assert.equal(blocks.map(html).join('\n'), html(prefix), JSON.stringify(prefix));
      // All but the last block of the previous split are final: same text, same place.
      if (!merges) for (let i = 0; i < previous.length - 1; i++) assert.equal(blocks[i], previous[i], JSON.stringify(prefix));
      previous = blocks;
    }
  }
});

test('blocks split at blank lines outside fences, never inside a list or a fence', () => {
  assert.deepEqual(splitBlocks('a\n\nb\n\n\nc'), ['a', 'b', 'c']);
  assert.deepEqual(splitBlocks('- a\n\n- b\n\nc'), ['- a\n\n- b', 'c']);
  assert.deepEqual(splitBlocks('```\nx\n\ny\n```\n\nz'), ['```\nx\n\ny\n```', 'z']);
  assert.deepEqual(splitBlocks('[a]: https://x\n\ntext'), ['[a]: https://x\n\ntext']);
});

test('a src or href is a host path unless it has a web scheme', () => {
  assert.equal(localPath('/home/dev/projects/config/sky-dodge.png'), '/home/dev/projects/config/sky-dodge.png');
  assert.equal(localPath('sky-dodge.png'), 'sky-dodge.png');
  assert.equal(localPath('./shots/a.png'), './shots/a.png');
  assert.equal(localPath('file:///home/dev/shots/a%20b.png'), '/home/dev/shots/a b.png');
  for (const web of ['https://example.com/a.png', 'http://h/a.png', 'data:image/png;base64,AAAA', 'blob:http://h/x', 'mailto:a@b', 'javascript:alert(1)', '', undefined]) {
    assert.equal(localPath(web), null, String(web));
  }
});

test('a link names a file of the Task folder by its path relative to it', () => {
  const dir = '/home/dev/proj';
  const file = (ref, workdir = dir) => taskFile(ref, workdir);
  assert.deepEqual(file('out/report.html'), { path: 'out/report.html', hash: '' });
  assert.deepEqual(file('./out/../shot.png'), { path: 'shot.png', hash: '' });
  assert.deepEqual(file('out/report.html#results'), { path: 'out/report.html', hash: '#results' });
  assert.deepEqual(file('notes/a%20b.md?x=1'), { path: 'notes/a b.md', hash: '' });
  assert.deepEqual(file('100%.txt'), { path: '100%.txt', hash: '' });
  assert.deepEqual(file('/home/dev/proj/out/report.html'), { path: 'out/report.html', hash: '' });
  assert.deepEqual(file('/home/dev/proj/out/r.html', '/home/dev/proj/'), { path: 'out/r.html', hash: '' });
  assert.deepEqual(file('file:///home/dev/proj/out/a%20b.html#top'), { path: 'out/a b.html', hash: '#top' });
  assert.deepEqual(file('Makefile'), { path: 'Makefile', hash: '' });
  for (const outside of ['/home/dev/project2/x.html', '/home/dev/proj', '/etc/passwd', '../x.html', 'out/../../x', '.', '#top', 'file:///etc/passwd', 'https://example.com/r.html', 'mailto:a@b', undefined]) {
    assert.equal(file(outside), null, String(outside));
  }
  assert.equal(taskFile('/home/dev/proj/x.html', undefined), null);
  assert.deepEqual(taskFile('x.html', undefined), { path: 'x.html', hash: '' });
});
