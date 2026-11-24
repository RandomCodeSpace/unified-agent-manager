// What a Thread keeps beside its conversation: todo dependencies, compaction, its custom agent, and uam's Background AI log.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { pickMode, script, subagentAsked } from './activity-helpers';
import { systemText, useMockUtility } from './composer-helpers';

test('todo dependencies draw as rails beside the list, and as a board once the setting says so', async ({ page, project, model }) => {
  const sql = (id: string, query: string) => [{ id, name: 'sql', arguments: { description: 'Update todos', query } }];
  script(model, { userMessage: 'plan with dependencies' }, [
    sql('call_add', "INSERT INTO todos (id, title, status) VALUES ('a','Read code','done'),('b','Write fix','in_progress'),('c','Test fix','pending'),('d','Ship fix','pending');"),
    sql('call_deps', "INSERT INTO todo_deps (todo_id, depends_on) VALUES ('b','a'),('c','b'),('d','c');"),
    'Planned with dependencies.',
  ]);
  const url = await startThread(page, project, 'plan with dependencies');
  const conv = conversation(page);
  await expect(conv.getByText('Planned with dependencies.')).toBeVisible();
  const kept = conv.getByRole('button', { name: 'Todo at the end of this turn: 1 of 4 done, 1 in progress, 2 to do' });
  await kept.click();
  const reader = page.getByRole('dialog', { name: 'Todo at the end of this turn' });
  await expect(reader.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Test fix' }) })).toContainText('waits on Write fix');
  await expect(reader.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Ship fix' }) })).toContainText('waits on Test fix');
  // One rail per wait between open rows; the wait on a done row is met and draws none.
  await expect(reader.locator('svg g[data-kind]')).toHaveCount(2);
  await expect(reader.getByRole('group', { name: 'Todo board' })).toHaveCount(0);
  await reader.getByRole('button', { name: 'Close' }).click();

  await page.goto('/#settings');
  await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Appearance & alerts' }).click();
  await page.getByRole('radio', { name: 'Board' }).click();
  await page.goto(url);
  await kept.click();
  const board = reader.getByRole('group', { name: 'Todo board' });
  await expect(board).toBeVisible();
  await expect(board).toContainText('waits on Write fix');
  await expect(board).toContainText('waits on Test fix');
  await expect(reader).toContainText('3 links');
  await expect(reader.locator('svg g[data-kind]')).toHaveCount(0);
});

test('/compact reads Compacting the conversation on the status line of a working Thread, then leaves a quiet note', async ({ page, project, model }) => {
  model.reply('warm the compaction', 'Warm.');
  // The CLI asks the main model for the summary; it takes 3 s here.
  model.reply('Please create a detailed summary of the conversation so far', '<overview>Warmed up.</overview><checkpoint_title>Warm up</checkpoint_title>', { chaos: { latencyMs: 3000 } });
  model.reply('after the compaction', 'Still here.');
  await startThread(page, project, 'warm the compaction');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await page.getByRole('textbox', { name: 'Message' }).fill('/compact');
  await page.getByRole('button', { name: 'Run /compact' }).click();
  // Compacting is work: the Thread sits under Working, and its status line and its row say what the work is.
  await expect(page.getByRole('main').getByRole('status').filter({ hasText: /^Compacting the conversation/ })).toBeVisible();
  await expect(threadState(page, 'Working')).toBeVisible();
  const row = page.getByRole('list', { name: 'Unsettled threads' }).getByRole('button', { name: new RegExp(`^${project.name} `) });
  await expect(row).toHaveAccessibleName(new RegExp(`^${project.name} Working `));

  await expect(conversation(page).getByText(/^Compacted the conversation/)).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByRole('main').getByText(/^Compacting the conversation/)).toHaveCount(0);
  await expect(row).toHaveAccessibleName(new RegExp(`^${project.name} Finished `));
  await send(page, 'after the compaction');
  await expect(conversation(page).getByText('Still here.')).toBeVisible();
});

test('automatic compaction beside a turn is a quiet note on the working line, and does not hold the turn', async ({ page, project, model, uam }) => {
  // Half the context is the earliest Copilot compacts at, and one large reply passes it. The setting is the worker's: put back below.
  await uam.api('PATCH', '/api/settings', { compact_threshold: 50 });
  try {
    const big = Array.from({ length: 3200 }, (_, i) => `filler paragraph ${i} ${'lorem ipsum dolor sit amet '.repeat(3)}`).join('\n\n');
    model.reply('fill the context', big, { chunkSize: 1_000_000 });
    // The summary takes 9 s; the turn beside it about 3.
    model.reply('Please create a detailed summary of the conversation so far', '<overview>Filled.</overview><checkpoint_title>Fill</checkpoint_title>', { chaos: { latencyMs: 9000 } });
    script(model, { userMessage: 'work beside it' }, [[{ id: 'call_beside', name: 'bash', arguments: { command: 'sleep 3', description: 'Work' } }], 'Worked beside it.']);
    await openHome(page);
    await newThread(page, project);
    await pickMode(page, 'Yolo');
    await send(page, 'fill the context');
    await expect(threadState(page, 'Finished')).toBeVisible({ timeout: 60_000 });

    await send(page, 'work beside it');
    await expect(page.getByRole('main').getByText('Compacting in background', { exact: true })).toBeVisible();
    await expect(threadState(page, 'Working')).toBeVisible();
    const conv = conversation(page);
    await expect(conv.getByText('Worked beside it.')).toBeVisible();
    // The turn ended first: the compaction neither held it nor has reported yet.
    await expect(conv.getByText(/^Compacted the conversation/)).toHaveCount(0);
    await expect(conv.getByText(/^Compacted the conversation · freed [\d,]+ tokens\.$/)).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
  } finally {
    await uam.api('PATCH', '/api/settings', { compact_threshold: 80 });
  }
});

test('the Agent tab lists the Project\'s custom agents, holds the choice during a turn, and the picked agent runs the next turn', async ({ page, project, model, workdir }) => {
  fs.mkdirSync(path.join(workdir, '.github/agents'), { recursive: true });
  fs.writeFileSync(path.join(workdir, '.github/agents/reviewer.agent.md'), '---\nname: reviewer\ndescription: Reviews code carefully\n---\nYou review code with the E2E-REVIEWER-MARK rule.\n');
  model.reply('slow turn first', Array.from({ length: 40 }, (_, i) => `agent line ${i + 1}`).join('\n\n'), { latency: 250, chunkSize: 4 });
  model.reply('review this please', 'Reviewed.');
  await startThread(page, project, 'slow turn first');
  await expect(conversation(page).getByText('agent line 1', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: /^Tools\./ }).click();
  const tools = page.getByRole('dialog', { name: 'Tools' });
  await tools.getByRole('tab', { name: 'Agent' }).click();
  const agents = tools.getByRole('radiogroup', { name: 'Agent' });
  await expect(agents.getByRole('radio', { name: 'Default' })).toBeChecked();
  const reviewer = agents.getByRole('radio', { name: 'reviewer Reviews code carefully' });
  // While the turn runs the choice cannot change.
  await expect(reviewer).toBeDisabled();
  await tools.getByRole('button', { name: 'Close' }).click();
  await page.getByRole('button', { name: 'Stop turn' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();

  await page.getByRole('button', { name: /^Tools\./ }).click();
  await tools.getByRole('tab', { name: 'Agent' }).click();
  await expect(reviewer).toBeEnabled();
  await reviewer.click();
  await expect(reviewer).toBeChecked();
  await tools.getByRole('button', { name: 'Close' }).click();
  await send(page, 'review this please');
  await expect(conversation(page).getByText('Reviewed.')).toBeVisible();
  const served = model.served();
  expect(systemText(served.find((s) => s.fixture === 'reply review this please')!.request)).toContain('E2E-REVIEWER-MARK');
  expect(systemText(served.find((s) => s.fixture === 'reply slow turn first')!.request)).not.toContain('E2E-REVIEWER-MARK');
});

test('Settings, Background AI logs uam\'s Utility calls, and a subagent\'s name costs one call', async ({ page, project, model, uam }) => {
  const restore = await useMockUtility(uam);
  try {
    uam.scripts.push({ match: { predicate: (req) => systemText(req).includes("You name a coding agent's subagent") }, response: () => ({ content: JSON.stringify({ name: 'Tallier' }) }) });
    uam.apply();
    script(model, { userMessage: 'delegate the tally for the log' }, [
      [{ id: 'call_task', name: 'task', arguments: { name: 'counter', description: 'Count files', prompt: 'Tally for the log', agent_type: 'general-purpose' } }],
      'Tally back.',
    ], {}, 'main');
    script(model, subagentAsked('Tally for the log'), [
      [{ id: 'call_sub_tally', name: 'bash', arguments: { command: 'sleep 8; echo 3', description: 'Count' } }],
      'There are 3 files.',
    ], {}, 'subagent');
    model.title('delegate the tally for the log', 'Tally thread');
    await openHome(page);
    await newThread(page, project);
    await pickMode(page, 'Yolo');
    await send(page, 'delegate the tally for the log');
    await expect(page.getByRole('region', { name: 'Subagents at work' }).getByRole('button', { name: /^Tallier \(counter\), running/ })).toBeVisible();
    await expect(conversation(page).getByText('Tally back.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const id = page.url().split('#task=')[1];

    await page.goto('/#settings');
    await page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: 'Background AI' }).click();
    const section = page.getByRole('region', { name: 'Background AI' });
    await section.getByRole('button', { name: 'Show log' }).click();
    await expect(section.getByText('Thread title', { exact: true }).first()).toBeVisible();
    await expect(section.getByText('Subagent name', { exact: true }).first()).toBeVisible();
    // The log's own record: this Thread's subagent was named by exactly one call.
    const log = (await uam.api('GET', '/api/utility')) as { calls: { purpose: string; task_id?: string }[] };
    expect(log.calls.filter((c) => c.purpose === 'subagent-label' && c.task_id === id)).toHaveLength(1);
  } finally {
    await restore();
  }
});
