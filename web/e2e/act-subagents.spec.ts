// Subagents that need the owner, and the subagent sheet the strip opens.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { pickMode, script, subagentAsked } from './activity-helpers';

const task = (id: string, name: string, prompt: string) => [{ id, name: 'task', arguments: { name, description: `Run ${name}`, prompt, agent_type: 'general-purpose' } }];

test('a subagent waiting on permission says so on its row and is answered from the Thread; in yolo it is not asked', async ({ page, project, model, workdir }) => {
  script(model, { userMessage: 'delegate the write' }, [task('call_task', 'writer', 'Write the sub file'), 'Subagent wrote it.'], {}, 'main');
  script(model, subagentAsked('Write the sub file'), [
    [{ id: 'call_sub_write', name: 'bash', arguments: { command: 'printf sub > sub.txt', description: 'Write it' } }],
    'Sub file written.',
  ], {}, 'subagent');
  script(model, { userMessage: 'delegate again' }, [task('call_task2', 'second', 'Write the second file'), 'Second subagent wrote it.'], {}, 'main2');
  script(model, subagentAsked('Write the second file'), [
    [{ id: 'call_sub_write2', name: 'bash', arguments: { command: 'printf second > second.txt', description: 'Write it' } }],
    'Second file written.',
  ], {}, 'subagent2');
  await startThread(page, project, 'delegate the write');
  const conv = conversation(page);
  await expect(page.getByRole('region', { name: 'Subagents at work' }).getByRole('button', { name: 'writer, running: Needs permission: Run shell command' })).toBeVisible();
  await expect(threadState(page, 'Needs permission')).toBeVisible();
  expect(fs.existsSync(path.join(workdir, 'sub.txt'))).toBe(false);
  const card = conv.getByRole('group', { name: 'Run shell command' });
  await expect(card).toContainText('printf sub > sub.txt');
  await card.getByRole('button', { name: 'Allow once' }).click();
  await expect(conv.getByText('Subagent wrote it.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'sub.txt'), 'utf8')).toBe('sub');

  await pickMode(page, 'Allow all');
  await send(page, 'delegate again');
  // Unanswered, the subagent's request would hold the turn on a card.
  await expect(conv.getByText('Second subagent wrote it.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(card).toHaveCount(0);
  expect(fs.readFileSync(path.join(workdir, 'second.txt'), 'utf8')).toBe('second');
});

test('the subagent sheet groups by reply, filters by state, and reads a subagent beside the list or in its place when narrow', async ({ page, project, model }) => {
  script(model, { userMessage: 'delegate two jobs' }, [task('call_quick', 'quick', 'Answer at once'), task('call_slow', 'sleeper', 'Sleep a while'), 'Both back.'], {}, 'main');
  model.on(subagentAsked('Answer at once'), { content: 'Quick result here.' }, {}, 'quick');
  script(model, subagentAsked('Sleep a while'), [
    [{ id: 'call_sub_sleep', name: 'bash', arguments: { command: 'sleep 8; echo slept', description: 'Sleep' } }],
    'Slept well.',
  ], {}, 'sleeper');
  await openHome(page);
  await newThread(page, project);
  await pickMode(page, 'Allow all');
  await send(page, 'delegate two jobs');
  await page.getByRole('button', { name: /^Subagents: 2, 1 running/ }).click();
  const sheet = page.getByRole('dialog', { name: 'Agents and commands' });
  const group = sheet.getByRole('region', { name: '“delegate two jobs”' });
  await expect(group.getByRole('button', { name: /^quick, / })).toBeVisible();
  await expect(group.getByRole('button', { name: /^sleeper, running/ })).toBeVisible();

  const filters = sheet.getByRole('group', { name: 'Show by status' });
  await expect(filters.getByRole('button', { name: 'All 2' })).toHaveAttribute('aria-pressed', 'true');
  await filters.getByRole('button', { name: 'Running 1' }).click();
  await expect(group.getByRole('button', { name: /^sleeper, running/ })).toBeVisible();
  await expect(group.getByRole('button', { name: /^quick, / })).toHaveCount(0);
  await filters.getByRole('button', { name: 'Done 1' }).click();
  await expect(group.getByRole('button', { name: /^quick, / })).toBeVisible();
  await expect(group.getByRole('button', { name: /^sleeper, / })).toHaveCount(0);
  await filters.getByRole('button', { name: 'All 2' }).click();

  // Wide: the reader stands beside the list, which stays.
  await group.getByRole('button', { name: /^quick, / }).click();
  const reader = page.getByRole('dialog', { name: 'quick' });
  await expect(reader).toContainText('Quick result here.');
  await expect(reader.getByRole('button', { name: 'Open as conversation' })).toBeVisible();
  // The reader holds the focus, so the sheet behind it is found as a hidden dialog: still drawn, and not covered.
  const behind = page.getByRole('dialog', { name: 'Agents and commands', includeHidden: true });
  await expect(behind.getByText('sleeper', { exact: true })).toBeVisible();
  const [list, beside] = [(await behind.boundingBox())!, (await reader.boundingBox())!];
  expect(beside.x >= list.x + list.width || beside.x + beside.width <= list.x).toBe(true);
  await reader.getByRole('button', { name: 'Close' }).click();
  await expect(reader).toBeHidden();

  // Narrow: it takes the list's place, behind Back.
  await page.setViewportSize({ width: 600, height: 900 });
  await group.getByRole('button', { name: /^quick, / }).click();
  const inPlace = sheet.getByRole('region', { name: 'quick' });
  await expect(inPlace).toContainText('Quick result here.');
  await expect(group).toHaveCount(0);
  await sheet.getByRole('button', { name: 'Back to the list' }).click();
  await expect(group.getByRole('button', { name: /^quick, / })).toBeVisible();
  await expect(inPlace).toHaveCount(0);

  await page.setViewportSize({ width: 1280, height: 900 });
  await sheet.getByRole('button', { name: 'Close' }).click();
  await expect(conversation(page).getByText('Both back.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
});

test('stopping a subagent ends its running tool call, and the Thread goes on', async ({ page, project, model }) => {
  script(model, { userMessage: 'delegate a long sleep' }, [task('call_task', 'napper', 'Sleep for long'), 'Napper was stopped.'], {}, 'main');
  model.on(subagentAsked('Sleep for long'), { toolCalls: [{ id: 'call_sub_nap', name: 'bash', arguments: JSON.stringify({ command: 'sleep 90', description: 'Nap' }) }] }, {}, 'napper');
  await openHome(page);
  await newThread(page, project);
  await pickMode(page, 'Allow all');
  await send(page, 'delegate a long sleep');
  await page.getByRole('region', { name: 'Subagents at work' }).getByRole('button', { name: /^napper, running/ }).click();
  const transcript = page.getByRole('region', { name: /^Transcript of / });
  await expect(transcript.getByRole('button', { name: 'bash sleep 90 , running' })).toBeVisible();
  await page.getByRole('button', { name: 'Stop subagent napper' }).click();
  await page.getByRole('alertdialog', { name: 'Stop subagent “napper”?' }).getByRole('button', { name: 'Stop subagent' }).click();
  // The call it was in ends with it: no step is left running, long before the sleep would end.
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(transcript.getByRole('region', { name: 'Result' })).toContainText('Stopped before it finished.');
  await transcript.getByRole('button', { name: '1 without a result. Show the activity of this turn' }).click();
  await expect(transcript.getByRole('button', { name: /sleep 90 , no result$/ })).toBeVisible();
  await expect(transcript.getByRole('button', { name: /, running$/ })).toHaveCount(0);

  await page.goBack();
  const conv = conversation(page);
  await expect(conv.getByText('Napper was stopped.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByRole('region', { name: 'Subagents at work' })).toHaveCount(0);
});

test('a subagent named in a reply wraps with the words around it and opens the subagent', async ({ page, project, model }) => {
  const name = 'reviewer of every accessibility linter finding in the templates folder';
  script(model, { userMessage: 'delegate the review' }, [task('call_review', name, 'Review the findings'), `Done. The subagent **${name}** reported nothing worth fixing in any template.`], {}, 'main');
  model.on(subagentAsked('Review the findings'), { content: 'Nothing to fix.' }, {}, 'reviewer');
  await openHome(page);
  await newThread(page, project);
  await pickMode(page, 'Allow all');
  await send(page, 'delegate the review');
  await page.setViewportSize({ width: 420, height: 900 });
  const tag = conversation(page).getByRole('button', { name: `Open subagent ${name}` });
  await expect(tag).toBeVisible();
  // An inline box: the long name breaks across lines and starts on the line of the words before it.
  const rects = await tag.evaluate((el) => {
    const line = el.closest('p')!.getBoundingClientRect();
    const boxes = [...el.getClientRects()];
    return { count: boxes.length, startsInLine: boxes[0].left > line.left + 1 };
  });
  expect(rects.count).toBeGreaterThan(1);
  expect(rects.startsInLine).toBe(true);
  await tag.click();
  await expect(page).toHaveURL(/&agent=/);
});
