import path from 'node:path';
import { conversation, expect, newThread, openHome, send, test, threadState } from './harness';

test('a Project added with the folder picker gets its first Thread on the first Send', async ({ page, workdir, model }) => {
  const name = path.basename(workdir);
  model.reply('first message here', 'First reply from the mock.').title('first message here', 'Mock title for the first thread');

  await openHome(page);
  await page.getByRole('complementary').getByRole('button', { name: 'Add project', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Choose a project folder' });
  await picker.getByRole('textbox', { name: 'Folder path' }).fill(path.dirname(workdir));
  await picker.getByRole('button', { name: 'Open folder path' }).click();
  await picker.getByRole('listbox', { name: 'Folders' }).getByRole('option', { name: `${name} git` }).click();
  await picker.getByRole('button', { name: 'Continue' }).click();
  const review = page.getByRole('dialog', { name: 'Add a project' });
  await expect(review.getByRole('heading', { name: `Ready to add ${name}?` })).toBeVisible();
  await review.getByRole('button', { name: 'Add project' }).click();
  await expect(review).toBeHidden();

  await newThread(page, { id: '', name, dir: workdir });
  const rows = page.getByRole('list', { name: 'Unsettled threads' }).getByRole('listitem');
  await expect(page).not.toHaveURL(/#task=/);

  await send(page, 'first message here');
  await expect(page).toHaveURL(/#task=[0-9a-f-]{36}$/);
  await expect(conversation(page).getByText('first message here')).toBeVisible();
  await expect(conversation(page).getByText('First reply from the mock.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const row = rows.filter({ hasText: 'Mock title for the first thread' });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText(name);
  await expect(page.getByRole('heading', { level: 1, name: 'Mock title for the first thread' })).toBeVisible();
});
