// Routines: a saved first message that starts its own Thread (Routines.tsx, internal/web/routines.go).
import { conversation, expect, openHome, test } from './harness';

test('a routine created in Routines starts a Thread with Run now; past its daily limit Run now is skipped with the reason', async ({ page, uam, project, model }) => {
  // Autopilot keeps the turn going until the agent calls task_complete, which ends it without another model call.
  model.on({ userMessage: 'check deps' }, { toolCalls: [{ id: 'call_done', name: 'task_complete', arguments: JSON.stringify({ summary: 'Dependencies checked: all current.' }) }] }, {}, 'complete check deps');
  const name = `Nightly ${Math.random().toString(36).slice(2, 6)}`;
  await openHome(page);
  await page.getByRole('button', { name: 'Routines', exact: true }).click();
  await page.getByRole('button', { name: 'New routine', exact: true }).click();
  const dialog = page.getByRole('dialog');
  // Other cases of this worker leave Projects behind: with several the form asks which one.
  const pick = dialog.getByRole('combobox', { name: 'Project' });
  if (await pick.count()) {
    await pick.click();
    await page.getByRole('option', { name: project.name, exact: true }).click();
    await expect(pick).toHaveText(project.name);
  }
  await dialog.getByRole('textbox', { name: 'Name' }).fill(name);
  await dialog.getByRole('textbox', { name: 'What the agent should do' }).fill('check deps');
  await dialog.getByRole('spinbutton', { name: 'Daily run limit' }).fill('1');
  await dialog.getByRole('button', { name: 'Create routine' }).click();
  await expect(dialog).toBeHidden();
  try {
    const card = page.getByRole('region', { name });
    await expect(card).toContainText('Yolo with autopilot');
    await expect(card).toContainText('at most 1 run a day');
    await expect(card).toContainText('Not run yet');
    await card.getByRole('button', { name: 'Run now' }).click();
    await expect(card.getByRole('button', { name: 'Open thread' })).toBeVisible();
    await expect(card).toContainText('Finished');
    await card.getByRole('button', { name: 'Run now' }).click();
    await expect(card).toContainText(/Skipped .*limit/i);

    await card.getByRole('button', { name: 'Open thread' }).click();
    await expect(conversation(page).getByText('check deps', { exact: true })).toBeVisible();
    await expect(conversation(page).getByText('Dependencies checked: all current.')).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toContainText(name);
    // One run, one Thread, one model call: the skipped Run now started nothing.
    expect(model.served().map((s) => s.fixture)).toEqual(['complete check deps']);
  } finally {
    const { routines } = (await uam.api('GET', '/api/routines')) as { routines: { id: string; name: string }[] };
    for (const r of routines.filter((x) => x.name === name)) await uam.api('DELETE', `/api/routines/${r.id}`);
  }
});
