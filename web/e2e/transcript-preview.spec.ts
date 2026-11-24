// The file preview beside the conversation: a Markdown file rendered or as source, and reloading when the file changes.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, send, startThread, test, threadState } from './harness';
import { yolo } from './transcript-helpers';

test('a Markdown preview renders and toggles to source, reloads in place when a tool edits the file, and Reload reads other changes', async ({ page, project, model, uam, workdir }) => {
  const file = path.join(workdir, 'notes.md');
  model.toolRound('write notes', [{ id: 'call_md', name: 'create', arguments: { path: file, file_text: '# Notes title\n\n- first point\n- second point\n' } }], 'Wrote the notes.');
  model.toolRound('edit notes', [{ id: 'call_edit', name: 'edit', arguments: { path: file, old_str: '- second point', new_str: '- edited point' } }], 'Edited the notes.');
  await startThread(page, project, 'write notes');
  await yolo(page, uam.api);
  const conv = conversation(page);
  await expect(threadState(page, 'Finished')).toBeVisible();

  // A file tool's row opens what it wrote.
  await conv.getByRole('button', { name: '1 file changed. Show the activity of this turn' }).click();
  await conv.getByRole('button', { name: /^Actions for create / }).click();
  await page.getByRole('menuitem', { name: 'Open notes.md' }).click();
  const preview = page.getByRole('complementary', { name: 'Preview notes.md' });
  await expect(preview.getByRole('heading', { level: 1, name: 'Notes title' })).toBeVisible();
  await expect(preview.getByRole('listitem')).toHaveText(['first point', 'second point']);
  await expect(preview).not.toContainText('# Notes title');

  const as = preview.getByRole('radiogroup', { name: 'Show the file as' });
  await expect(as.getByRole('radio', { name: 'Preview' })).toBeChecked();
  await as.getByRole('radio', { name: 'Source' }).click();
  await expect(as.getByRole('radio', { name: 'Source' })).toBeChecked();
  await expect(preview).toContainText('# Notes title');
  await expect(preview.getByRole('heading', { name: 'Notes title' })).toHaveCount(0);
  await as.getByRole('radio', { name: 'Preview' }).click();
  await expect(preview.getByRole('heading', { level: 1, name: 'Notes title' })).toBeVisible();

  // A tool in the Thread edits the open file: the preview shows the new text without a press.
  await send(page, 'edit notes');
  await expect(conv.getByText('Edited the notes.')).toBeVisible();
  expect(fs.readFileSync(file, 'utf8')).toContain('- edited point');
  await expect(preview.getByRole('listitem')).toHaveText(['first point', 'edited point']);

  // A change from outside the Thread shows after Reload.
  fs.writeFileSync(file, '# Notes title\n\n- changed outside\n');
  await expect(preview.getByRole('listitem')).toHaveText(['first point', 'edited point']);
  await preview.getByRole('button', { name: 'Reload' }).click();
  await expect(preview.getByRole('listitem')).toHaveText(['changed outside']);
});
