// What the agent organises: subagents, the todo list, plan review and autopilot.
import fs from 'node:fs';
import path from 'node:path';
import { conversation, expect, lastUserText, newThread, openHome, send, startThread, test, threadState } from './harness';
import type { ChatCompletionRequest } from '@copilotkit/aimock';
import { pickMode, script, subagentAsked } from './activity-helpers';

/** What the CLI sends the model between autopilot turns. */
const CONTINUE = 'You have not yet marked the task as complete';

test('a subagent shows inline while it works, opens its own transcript, and its work stays out of the Thread', async ({ page, project, model }) => {
  script(model, { userMessage: 'delegate the count' }, [
    [{ id: 'call_task', name: 'task', arguments: { name: 'counter', description: 'Count files', prompt: 'Count the files', agent_type: 'general-purpose' } }],
    'Subagent said 3.',
  ], {}, 'main');
  script(model, subagentAsked('Count the files'), [
    [{ id: 'call_sub_count', name: 'bash', arguments: { command: 'sleep 4; echo 3', description: 'Count' } }],
    'There are 3 files.',
  ], {}, 'subagent');
  await openHome(page);
  await newThread(page, project);
  await pickMode(page, 'Yolo');
  await send(page, 'delegate the count');
  await expect(page).toHaveURL(/#task=[0-9a-f-]{36}$/);
  const threadUrl = page.url();

  const atWork = page.getByRole('region', { name: 'Subagents at work' });
  const row = atWork.getByRole('button', { name: 'counter, running: Running: bash sleep 4; echo 3' });
  await expect(row).toBeVisible();
  await expect(atWork.getByRole('button', { name: /^1 subagent · .* · 1 running$/ })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Subagents: 1, 1 running. Show the subagents' })).toBeVisible();
  await row.click();

  await expect(page).toHaveURL(/&agent=/);
  const transcript = page.getByRole('region', { name: /^Transcript of / });
  await expect(transcript.getByText('Count the files')).toBeVisible();
  await expect(transcript.getByRole('button', { name: /^(bash|Ran) sleep 4; echo 3/ })).toBeVisible();
  await expect(transcript.getByText('There are 3 files.')).toBeVisible();
  const subUrl = page.url();

  await page.goBack();
  await expect(page).toHaveURL(threadUrl);
  const conv = conversation(page);
  await expect(conv.getByText('Subagent said 3.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByText('There are 3 files.')).toHaveCount(0);
  await expect(conv.getByRole('button', { name: /sleep 4; echo 3/ })).toHaveCount(0);
  await expect(conv.getByRole('button', { name: /^1 subagent · .* · 1 done$/ })).toBeVisible();

  await page.goto(subUrl);
  await expect(page.getByRole('region', { name: /^Transcript of / }).getByText('There are 3 files.')).toBeVisible();
});

test('the todo list counts each stage live on the status line, and the turn keeps its end state', async ({ page, project, model }) => {
  const sql = (id: string, query: string) => [{ id, name: 'sql', arguments: { description: 'Update todos', query } }];
  const wait = (id: string) => [{ id, name: 'bash', arguments: { command: 'sleep 3', description: 'Work' } }];
  script(model, { userMessage: 'plan the steps' }, [
    sql('call_add', "INSERT INTO todos (id, title, status) VALUES ('a','Read code','in_progress'),('b','Write fix','pending'),('c','Test','pending');"),
    wait('call_wait1'),
    sql('call_next', "UPDATE todos SET status='done' WHERE id='a'; UPDATE todos SET status='in_progress' WHERE id='b';"),
    wait('call_wait2'),
    sql('call_block', "UPDATE todos SET status='blocked', description='needs input' WHERE id='c';"),
    'Paused on the test.',
  ]);
  await startThread(page, project, 'plan the steps');
  await expect(page.getByRole('button', { name: /^Todo 0 of 3 done, 1 in progress, 2 to do/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /^Todo 1 of 3 done, 1 in progress, 1 to do/ })).toBeVisible();
  const conv = conversation(page);
  await expect(conv.getByText('Paused on the test.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  const kept = conv.getByRole('button', { name: 'Todo at the end of this turn: 1 of 3 done, 1 in progress, 1 blocked' });
  await expect(kept).toBeVisible();
  await kept.click();
  const reader = page.getByRole('dialog');
  await expect(reader.getByRole('region', { name: /^In progress/ })).toContainText('Write fix');
  await expect(reader.getByRole('region', { name: /^Blocked/ })).toContainText('Test');
  await expect(reader.getByRole('region', { name: /^Done/ })).toContainText('Read code');
});

test('plan review: Plan ready asks for an answer, feedback revises the plan, and Approve plan implements it', async ({ page, project, model, uam, workdir }) => {
  model.reply('warm the plan', 'Ready to plan.');
  const url = await startThread(page, project, 'warm the plan');
  await expect(threadState(page, 'Finished')).toBeVisible();
  // Plan mode asks the agent to keep the plan in its session folder, named by the Thread's session ID.
  const planFile = path.join(uam.home, '.copilot', 'session-state', url.split('#task=')[1], 'plan.md');
  const review = (id: string, summary: string) => [{ id, name: 'exit_plan_mode', arguments: { summary, recommendedAction: 'interactive' } }];
  script(model, { userMessage: 'My request: add a hello script' }, [
    [{ id: 'call_write1', name: 'create', arguments: { path: planFile, file_text: '# Plan\n\n- Add hello.sh\n' } }],
    review('call_plan1', '- Add hello.sh'),
    [{ id: 'call_write2', name: 'edit', arguments: { path: planFile, old_str: '- Add hello.sh', new_str: '- Add hello.sh\n- Add a test for it' } }],
    review('call_plan2', '- Add hello.sh and a test'),
    [{ id: 'call_hello', name: 'create', arguments: { path: path.join(workdir, 'hello.sh'), file_text: 'echo hello\n' } }],
    'Hello script added.',
  ]);
  await page.getByRole('textbox', { name: 'Message' }).fill('/plan add a hello script');
  await page.getByRole('button', { name: 'Run /plan' }).click();

  // The composer holds Plan ready; the conversation keeps a Read plan on each decision's receipt.
  const composer = page.locator('form').filter({ has: page.getByRole('textbox', { name: 'Your answer' }) });
  await expect(composer.getByText('Plan ready')).toBeVisible();
  await expect(threadState(page, 'Needs answer')).toBeVisible();
  await composer.getByRole('button', { name: 'Read plan' }).click();
  const reader = page.getByRole('dialog', { name: 'Plan' });
  await expect(reader).toContainText('Add hello.sh');
  // Escape can land before the reader takes focus; its own button cannot.
  await reader.getByRole('button', { name: 'Close' }).click();
  await expect(reader).toBeHidden();

  await page.getByRole('textbox', { name: 'Your answer' }).fill('also add a test');
  await page.keyboard.press('Enter');
  await expect(conversation(page).getByText('Plan revision requested · - Add hello.sh also add a test')).toBeVisible();
  await expect(composer.getByText('- Add hello.sh and a test')).toBeVisible();
  await composer.getByRole('button', { name: 'Read plan' }).click();
  await expect(reader.getByText('Revision 2')).toBeVisible();
  await reader.getByRole('button', { name: 'Show changes' }).click();
  await expect(reader).toContainText('+ - Add a test for it');
  await reader.getByRole('button', { name: 'Close' }).click();
  await expect(reader).toBeHidden();

  await page.getByRole('button', { name: 'Approve plan' }).click();
  await page.getByRole('button', { name: 'Allow once' }).click();
  await expect(conversation(page).getByText('Hello script added.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conversation(page).getByText(/^Plan approved/)).toBeVisible();
  expect(fs.readFileSync(path.join(workdir, 'hello.sh'), 'utf8')).toBe('echo hello\n');
});

test('Leave planning ends plan mode without implementing it', async ({ page, project, model, uam, workdir }) => {
  model.reply('warm the exit', 'Ready to plan.');
  const url = await startThread(page, project, 'warm the exit');
  await expect(threadState(page, 'Finished')).toBeVisible();
  const planFile = path.join(uam.home, '.copilot', 'session-state', url.split('#task=')[1], 'plan.md');
  script(model, { userMessage: 'My request: add a bye script' }, [
    [{ id: 'call_write', name: 'create', arguments: { path: planFile, file_text: '# Plan\n\n- Add bye.sh\n' } }],
    [{ id: 'call_plan', name: 'exit_plan_mode', arguments: { summary: '- Add bye.sh', recommendedAction: 'interactive' } }],
    'Left without changes.',
  ]);
  await page.getByRole('textbox', { name: 'Message' }).fill('/plan add a bye script');
  await page.getByRole('button', { name: 'Run /plan' }).click();
  const composer = page.locator('form').filter({ has: page.getByRole('textbox', { name: 'Your answer' }) });
  await expect(composer.getByText('Plan ready')).toBeVisible();
  const leave = composer.getByText('Leave planning', { exact: true });
  await leave.click();
  await leave.click();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conversation(page).getByText(/^Left planning/)).toBeVisible();
  await expect(conversation(page).getByText(/Plan not implemented/)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Permissions and execution: Safe · Interactive' })).toBeVisible();
  expect(fs.existsSync(path.join(workdir, 'bye.sh'))).toBe(false);
});

test('autopilot keeps working without a new message until the agent marks the task complete', async ({ page, project, model }) => {
  model.reply('warm the pilot', 'Ready.');
  model.reply('two steps please', 'Step one done.');
  const continued = (req: ChatCompletionRequest) => lastUserText(req).includes(CONTINUE);
  script(model, { predicate: continued }, [[{ id: 'call_done', name: 'task_complete', arguments: { summary: 'Both steps done.' } }], 'Step two done.'], {}, 'continue');
  await startThread(page, project, 'warm the pilot');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await pickMode(page, 'Autopilot');
  await expect(page.getByRole('button', { name: 'Permissions and execution: Safe · Autopilot' })).toBeVisible();
  await send(page, 'two steps please');
  await expect(conversation(page).getByText('Step one done.')).toBeVisible();
  await expect(conversation(page).getByText('Both steps done.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  // One continuation ran without a message from the user, and completing the task ended the run.
  expect(model.served().filter((s) => s.fixture === 'continue step 0')).toHaveLength(1);
});

test('Stop between autopilot turns ends the run and turns continuation off', async ({ page, project, model }) => {
  model.reply('warm the loop', 'Ready.');
  model.reply('loop forever', 'Loop started.');
  model.reply('after the loop', 'Back to one turn.');
  // The continuation never completes the task; at 1.5 s each, autopilot's own cap of 5 continuations is far off when Stop lands.
  const continued = (req: ChatCompletionRequest) => lastUserText(req).includes(CONTINUE);
  model.on({ predicate: continued }, { content: 'Still going.' }, { latency: 1500, chunkSize: 100_000 }, 'continue');
  const continuations = () => model.served().filter((s) => s.fixture === 'continue').length;
  await startThread(page, project, 'warm the loop');
  await expect(threadState(page, 'Finished')).toBeVisible();
  await pickMode(page, 'Autopilot');
  // The label follows the runtime: a message sent before the CLI reports autopilot runs as an interactive turn.
  await expect(page.getByRole('button', { name: 'Permissions and execution: Safe · Autopilot' })).toBeVisible();
  await send(page, 'loop forever');
  await expect.poll(continuations).toBeGreaterThanOrEqual(1);
  await page.getByRole('button', { name: 'Stop autopilot' }).click();
  await expect(threadState(page, 'Stopped')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Permissions and execution: Safe · Interactive' })).toBeVisible();

  const stoppedAt = continuations();
  await send(page, 'after the loop');
  await expect(conversation(page).getByText('Back to one turn.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  expect(continuations()).toBe(stoppedAt);
});
