import fs from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { expect, openHome, test, type Project } from './harness';

/** The mock model as the provider lists it. */
const MOCK_MODEL_ID = 'mock/mock-1';

const unsettled = (page: Page) => page.getByRole('list', { name: 'Unsettled threads' });

/** Opens Add project with the folder picker showing `dir`. */
async function pickerAt(page: Page, dir: string) {
  await page.getByRole('complementary').getByRole('button', { name: 'Add project', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Choose a project folder' });
  await picker.getByRole('textbox', { name: 'Folder path' }).fill(dir);
  await picker.getByRole('button', { name: 'Open folder path' }).click();
  return picker;
}

/** Opens Edit project for `name` from the Project filter. */
async function editProject(page: Page, name: string) {
  await page.getByRole('button', { name: /^Project filter: / }).click();
  await page.getByRole('dialog').getByRole('button', { name: `Edit ${name}`, exact: true }).click();
  return page.getByRole('dialog', { name: /Edit project/ });
}

test('a folder past the listing cap shows the first 1,000 and Filter folders finds the rest', async ({ page, workdir }) => {
  const many = path.join(workdir, 'many');
  for (let i = 0; i < 1100; i++) fs.mkdirSync(path.join(many, `d${String(i).padStart(4, '0')}`), { recursive: true });
  await openHome(page);
  const picker = await pickerAt(page, many);
  const folders = picker.getByRole('listbox', { name: 'Folders' }).getByRole('option');
  await expect(picker.getByText('Showing the first 1,000. Filter folders to search them all.')).toBeVisible();
  await expect(folders).toHaveCount(1000);
  await expect(folders.filter({ hasText: 'd1099' })).toHaveCount(0);
  const asked = page.waitForRequest((r) => r.url().includes('/api/fs/dirs') && new URL(r.url()).searchParams.get('filter') === 'd1099');
  await picker.getByRole('textbox', { name: 'Filter folders' }).fill('d1099');
  await asked;
  await expect(folders).toHaveCount(1);
  await expect(folders).toHaveAccessibleName(/^d1099/);
});

test('adding a folder that already has a Project opens that Project; a relative path is refused', async ({ page, uam, project }) => {
  await openHome(page);
  const picker = await pickerAt(page, path.dirname(project.dir));
  await picker.getByRole('listbox', { name: 'Folders' }).getByRole('option', { name: `${project.name} git` }).click();
  await picker.getByRole('button', { name: 'Continue' }).click();
  const review = page.getByRole('dialog', { name: 'Add a project' });
  await review.getByRole('button', { name: 'Add project' }).click();
  await expect(review).toBeHidden();
  await expect(page.getByRole('button', { name: `Project filter: ${project.name}`, exact: true })).toBeVisible();
  const projects = ((await uam.api('GET', '/api/projects')) as { projects: Project[] }).projects;
  expect(projects.filter((p) => p.dir === project.dir)).toHaveLength(1);

  const again = await pickerAt(page, path.dirname(project.dir));
  const field = again.getByRole('textbox', { name: 'Folder path' });
  await field.fill('relative/path');
  await expect(field).toHaveAttribute('aria-invalid', 'true');
  await expect(again.getByRole('textbox', { name: 'Folder path', description: 'Enter an absolute folder path.' })).toBeVisible();
  await expect(again.getByRole('button', { name: 'Open folder path' })).toBeDisabled();
  await expect(again.getByRole('button', { name: 'Continue' })).toBeDisabled();
});

test('Edit project renames it everywhere, kept after a reload; Remove waits until every Thread is archived', async ({ page, uam, project }) => {
  const created = (await uam.api('POST', '/api/sessions', { project_id: project.id, provider: 'copilot', model: MOCK_MODEL_ID, name: 'Rename target' })) as { id: string };
  await openHome(page);
  const row = unsettled(page).getByRole('button', { name: / Rename target / });
  await expect(row).toHaveAccessibleName(new RegExp(`^${project.name} `));

  const renamed = `${project.name}-renamed`;
  let dialog = await editProject(page, project.name);
  await dialog.getByRole('textbox', { name: 'Name' }).fill(renamed);
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect(row).toHaveAccessibleName(new RegExp(`^${renamed} `));
  await page.reload();
  await expect(row).toHaveAccessibleName(new RegExp(`^${renamed} `));

  dialog = await editProject(page, renamed);
  await dialog.getByRole('button', { name: 'Remove project' }).click();
  let confirm = page.getByRole('alertdialog', { name: `Remove ${renamed}?` });
  await expect(confirm.getByText('One thread is not archived. Archive every thread before removing this project.')).toBeVisible();
  await expect(confirm.getByRole('button', { name: 'Remove project' })).toBeDisabled();
  await confirm.getByRole('button', { name: 'Cancel' }).click();
  await expect(confirm).toBeHidden();
  await page.getByRole('dialog', { name: /Edit project/ }).getByRole('button', { name: 'Cancel' }).click();

  await uam.api('POST', `/api/sessions/${created.id}/archive`, {});
  await expect(row).toHaveCount(0);
  dialog = await editProject(page, renamed);
  await dialog.getByRole('button', { name: 'Remove project' }).click();
  confirm = page.getByRole('alertdialog', { name: `Remove ${renamed}?` });
  await confirm.getByRole('button', { name: 'Remove project' }).click();
  await expect(confirm).toBeHidden();
  const projects = ((await uam.api('GET', '/api/projects')) as { projects: Project[] }).projects;
  expect(projects.some((p) => p.id === project.id)).toBe(false);
  expect(fs.readFileSync(path.join(project.dir, 'README.md'), 'utf8')).toBe('# Fixture\n');
});
