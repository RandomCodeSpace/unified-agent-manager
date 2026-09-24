import assert from 'node:assert/strict';
import test from 'node:test';
import { LIMITS, acceptFor, checkUpload, fileKind, formatSize, kindOf, mediaNote } from '../src/lib/attachments.ts';

const png = (size = 1000) => ({ name: 'shot.png', type: 'image/png', size });
const pdf = (size = 1000) => ({ name: 'spec.pdf', type: 'application/pdf', size });
const txt = (size = 1000) => ({ name: 'notes.md', type: 'text/markdown', size });
const vision = { images: true, pdf: true, max_images: 2 };
const textOnly = { images: false, pdf: false };

test('kinds come from the name, then the browser type; unknown types are left to the sniffer', () => {
  assert.equal(fileKind({ name: 'a.png', type: 'image/png' }), 'image');
  assert.equal(fileKind({ name: 'a.PDF', type: '' }), 'pdf');
  assert.equal(fileKind({ name: 'main.go', type: '' }), 'text');
  assert.equal(fileKind({ name: 'data.json', type: 'application/json' }), 'text');
  assert.equal(fileKind({ name: 'main.ts', type: 'video/mp2t' }), 'text');
  assert.equal(fileKind({ name: 'notes.md', type: 'application/octet-stream' }), 'text');
  assert.equal(fileKind({ name: 'clip', type: 'video/mp2t' }), 'unknown');
  assert.equal(fileKind({ name: 'blob', type: 'application/octet-stream' }), 'text');
  assert.equal(fileKind({ name: 'logo.svg', type: 'image/svg+xml' }), 'svg');
  assert.equal(fileKind({ name: 'logo.svg', type: '' }), 'svg');
  assert.equal(fileKind({ name: 'photo.heic', type: 'image/heic' }), 'unknown');
  assert.equal(fileKind({ name: 'song.mp3', type: 'audio/mpeg' }), 'unknown');
  assert.equal(fileKind({ name: 'site.zip', type: 'application/zip' }), 'unknown');
  assert.equal(kindOf('image/webp'), 'image');
  assert.equal(kindOf('application/pdf'), 'pdf');
  assert.equal(kindOf('text/plain'), 'text');
});

test('the service limits are refused before an upload starts', () => {
  assert.equal(checkUpload(png(), undefined, [], 'Auto'), null);
  assert.equal(checkUpload(png(LIMITS.image + 1), undefined, [], 'Auto'), 'Images can be at most 3 MiB');
  assert.equal(checkUpload(pdf(LIMITS.pdf + 1), undefined, [], 'Auto'), 'PDF files can be at most 10 MiB');
  assert.equal(checkUpload(txt(LIMITS.text + 1), undefined, [], 'Auto'), 'Text files can be at most 256 KiB');
  assert.equal(checkUpload(txt(0), undefined, [], 'Auto'), 'The file is empty');
  assert.equal(checkUpload({ name: 'a.svg', type: 'image/svg+xml', size: 10 }, undefined, [], 'Auto'), 'SVG images cannot be attached');
  assert.match(checkUpload({ name: 'a.zip', type: 'application/zip', size: 10 }, undefined, [], 'Auto'), /^Only png, jpeg/);
  assert.equal(checkUpload(txt(), undefined, ['text', 'text', 'text', 'text', 'text'], 'Auto'), 'A prompt carries at most 5 attachments');
});

test('the model gate refuses images and PDFs per model and counts images', () => {
  assert.equal(checkUpload(png(), textOnly, [], 'Kimi K3'), 'Kimi K3 does not accept images');
  assert.equal(checkUpload(pdf(), textOnly, [], 'Kimi K3'), 'Kimi K3 does not accept PDF files');
  assert.equal(checkUpload(txt(), textOnly, [], 'Kimi K3'), null);
  assert.equal(checkUpload(png(), vision, ['image'], 'Haiku'), null);
  assert.equal(checkUpload(png(), vision, ['image', 'image'], 'Haiku'), 'Haiku accepts at most 2 images per prompt');
  assert.equal(checkUpload(png(), { images: true, pdf: false, max_images: 1 }, ['image'], 'Mini'), 'Mini accepts at most 1 image per prompt');
  assert.equal(checkUpload(pdf(), { images: true, pdf: false }, [], 'Mini'), 'Mini does not accept PDF files');
});

test('the attach control explains a narrowed gate and accepts accordingly', () => {
  assert.equal(mediaNote(undefined, 'Auto'), '');
  assert.equal(mediaNote(vision, 'Haiku'), '');
  assert.equal(mediaNote(textOnly, 'Kimi K3'), 'Kimi K3 takes text files only');
  assert.equal(mediaNote({ images: true, pdf: false }, 'Mini'), 'Mini takes images and text files, not PDF');
  assert.equal(mediaNote({ images: false, pdf: true }, 'Odd'), 'Odd takes PDF and text files, not images');
  assert.equal(acceptFor(undefined), undefined);
  assert.equal(acceptFor(textOnly), undefined);
  assert.match(acceptFor(vision), /^image\/png,.*application\/pdf,\.pdf$/);
  assert.doesNotMatch(acceptFor({ images: true, pdf: false }), /pdf/);
});

test('sizes read like a file manager', () => {
  assert.equal(formatSize(320), '320 B');
  assert.equal(formatSize(48_213), '47 KB');
  assert.equal(formatSize(1_300_000), '1.2 MB');
  assert.equal(formatSize(10_500_000), '10 MB');
});
