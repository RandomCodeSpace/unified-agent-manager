// uam's own tools the agent calls: uam_form and uam_surface (internal/web/form.go, surface.go).
import { conversation, expect, lastUserText, startThread, test, threadState, toolResult } from './harness';

test('uam_form renders, and the submitted answer reaches the model', async ({ page, project, model }) => {
  model.toolRound('show the form', [{
    id: 'call_form', name: 'uam_form', arguments: {
      title: 'Release settings', body: 'Pick what ships.',
      fields: [
        { kind: 'TextField', name: 'version', label: 'Version', required: true },
        { kind: 'ChoicePicker', name: 'channel', label: 'Channel', options: ['beta', 'stable'] },
      ],
      actions: [{ name: 'ship', label: 'Ship' }, { name: 'hold', label: 'Hold' }],
    },
  }], 'Form result received.');
  await startThread(page, project, 'show the form');
  const form = conversation(page).getByRole('group', { name: 'Release settings' });
  await expect(form.getByText('Pick what ships.')).toBeVisible();
  await expect(threadState(page, 'Needs answer')).toBeVisible();

  await form.getByRole('textbox', { name: /^Version/ }).fill('1.2.3');
  await form.getByRole('radio', { name: 'stable' }).check();
  await form.getByRole('button', { name: 'Ship' }).click();
  await expect(conversation(page).getByText('Form result received.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(JSON.parse(toolResult(model, 'call_form') ?? 'null')).toEqual({ status: 'submitted', action: 'ship', values: { version: '1.2.3', channel: 'stable' } });
});

test('uam_surface draws a card; pressing its button reaches the model, which updates the card', async ({ page, project, model }) => {
  const surface = (...messages: object[]) => ({ messages: messages.map((m) => ({ version: '1.0', ...m })) });
  model.toolRound('draw the card', [{
    id: 'call_draw', name: 'uam_surface', arguments: surface(
      { createSurface: { surfaceId: 'demo', catalogId: 'urn:uam:catalog:v1' } },
      { updateComponents: { surfaceId: 'demo', components: [
        { id: 'root', component: 'Column', children: ['title', 'press'] },
        { id: 'title', component: 'Text', text: 'Press the button below', variant: 'title' },
        { id: 'press', component: 'Button', child: 'press_label', variant: 'primary', action: { event: { name: 'button_clicked', context: {} } } },
        { id: 'press_label', component: 'Text', text: 'Press me' },
      ] } },
    ),
  }], 'Card drawn.');
  // A press comes back as a queued prompt starting "[uam_surface action] ".
  model.toolRound('[uam_surface action]', [{
    id: 'call_update', name: 'uam_surface', arguments: surface(
      { updateComponents: { surfaceId: 'demo', components: [{ id: 'title', component: 'Text', text: 'Button pressed', variant: 'title' }] } },
    ),
  }], 'Card updated.');

  await startThread(page, project, 'draw the card');
  const card = conversation(page).getByRole('region', { name: 'Press the button below' });
  await expect(conversation(page).getByText('Card drawn.')).toBeVisible();
  await card.getByRole('button', { name: 'Press me' }).click();

  await expect(conversation(page).getByText('Card updated.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  const updated = conversation(page).getByRole('region', { name: 'Button pressed' });
  await expect(updated.getByRole('button', { name: 'Press me' })).toBeVisible();
  await expect(card).toHaveCount(0);
  const press = model.served().map((s) => lastUserText(s.request)).find((t) => t.includes('[uam_surface action] '));
  expect(press).toContain('"name":"button_clicked"');
  expect(press).toContain('"label":"Press me"');
});
