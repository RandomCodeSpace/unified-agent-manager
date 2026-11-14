// What the agent is doing: live tool steps, model and local phases, background shells, retries.
import { conversation, expect, newThread, openHome, send, startThread, test, threadState } from './harness';
import { pickMode, script } from './activity-helpers';

const COUNT = 'for i in 1 2 3 4 5 6; do echo line$i; sleep 1; done';

test('a command runs as a live step with its output, then shows as a finished row; a failing command shows as failed and the turn still finishes', async ({ page, project, model }) => {
  script(model, { userMessage: 'run the steps' }, [
    [{ id: 'call_count', name: 'bash', arguments: { command: COUNT, description: 'Count' } }],
    [{ id: 'call_fail', name: 'bash', arguments: { command: 'echo boom >&2; exit 3', description: 'Fail' } }],
    'Counted and failed.',
  ]);
  await openHome(page);
  await newThread(page, project);
  await pickMode(page, 'Yolo');
  await send(page, 'run the steps');

  const conv = conversation(page);
  await expect(conv.getByRole('button', { name: `bash ${COUNT} , running` })).toBeVisible();
  const live = page.getByRole('button', { name: `Running ${COUNT}, under a minute. Show the output` });
  await live.click();
  // The newest output arrives while the command still runs.
  const output = page.getByRole('dialog', { name: 'The newest output' });
  await expect(output).toContainText('line2');
  await expect(conv.getByRole('button', { name: `bash ${COUNT} , running` })).toBeVisible();
  // Escape can land before the popover takes focus: press until it closes.
  await expect(async () => {
    await page.keyboard.press('Escape');
    await expect(output).toBeHidden({ timeout: 1000 });
  }).toPass();

  await expect(conv.getByText('Counted and failed.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(conv.getByRole('button', { name: /, running$/ })).toHaveCount(0);
  await conv.getByRole('button', { name: '2 commands. Show the activity of this turn' }).click();
  const steps = conv.getByRole('list', { name: 'Steps' });
  await expect(steps.getByRole('button', { name: `Ran ${COUNT} , ok` })).toBeVisible();
  const failed = steps.getByRole('button', { name: /^Ran echo boom >&2; exit 3 , / });
  await expect(failed).not.toHaveAccessibleName(/ , ok$/);
  await failed.click();
  await expect(steps).toContainText('boom');
  await steps.getByRole('button', { name: `Ran ${COUNT} , ok` }).click();
  await expect(steps).toContainText('line6');
});

test('the sidebar and the status line tell the model phase from a local tool, and go idle after the turn', async ({ page, project, model }) => {
  // The first answer takes 3 s to arrive; the command needs approval, then runs 3 s.
  model.toolRound('phase check', [{ id: 'call_phase', name: 'bash', arguments: { command: 'printf x > phase.txt && sleep 3', description: 'Write and wait' } }], 'Phases done.', { latency: 3000, chunkSize: 100_000 });
  await startThread(page, project, 'phase check');
  const sidebar = page.getByRole('navigation', { name: 'Threads' });
  await expect(page.getByText('Waiting for the model', { exact: true })).toBeVisible();
  await expect(sidebar.getByText(/Model working now/)).toBeVisible();

  await expect(threadState(page, 'Needs permission')).toBeVisible();
  await expect(sidebar.getByText(/No model calls now/)).toBeVisible();
  await page.getByRole('button', { name: 'Allow once' }).click();

  await expect(page.getByRole('button', { name: 'Running printf x > phase.txt && sleep 3, under a minute. Show the output' })).toBeVisible();
  await expect(sidebar.getByText(/Local: 1 Thread running a tool/)).toBeVisible();

  await expect(conversation(page).getByText('Phases done.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(sidebar.getByText(/No model calls now/)).toBeVisible();
  await expect(page.getByRole('button', { name: /^Running / })).toHaveCount(0);
});

test('a background shell is counted on the toolbar, streams into the output panel, and stopping it leaves the Thread working', async ({ page, project, model }) => {
  const ticker = 'for i in $(seq 1 600); do echo tick$i; sleep 0.2; done';
  model.toolRound('start the ticker', [{ id: 'call_ticker', name: 'bash', arguments: { command: ticker, description: 'Ticker', mode: 'async', detach: true } }], 'Ticker started.');
  model.reply('still there', 'Still here.');
  await openHome(page);
  await newThread(page, project);
  await pickMode(page, 'Yolo');
  await send(page, 'start the ticker');
  await expect(conversation(page).getByText('Ticker started.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();

  await page.getByRole('button', { name: 'Background tasks: 1 running' }).click();
  const tasks = page.getByRole('dialog', { name: 'Background tasks' });
  const row = tasks.getByRole('listitem').filter({ hasText: 'Ticker' });
  await expect(row).toContainText('Running');
  await expect(row.getByRole('code')).toHaveText(ticker);
  await row.getByRole('button', { name: 'Show output of background task: Ticker' }).click();
  const panel = page.getByRole('complementary', { name: 'Command output' });
  await expect(panel).toContainText('tick1');
  // New output keeps arriving without a click.
  await expect(panel).toContainText('tick20');
  await page.getByRole('button', { name: 'Close command output' }).click();

  await page.getByRole('button', { name: /^Background tasks/ }).click();
  await row.getByRole('button', { name: 'Stop background task: Ticker' }).click();
  await page.getByRole('alertdialog').getByRole('button', { name: /^Stop/ }).click();
  await expect(row).toContainText('Cancelled');
  await tasks.getByRole('button', { name: 'Close' }).click();
  await expect(tasks).toBeHidden();
  await expect(page.getByRole('button', { name: /^Background tasks/ })).toHaveAccessibleName('Background tasks: 0 running');

  await send(page, 'still there');
  await expect(conversation(page).getByText('Still here.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
});

test('a rate-limited model call shows the retry on the status line, and the turn goes through', async ({ page, project, model }) => {
  // The first attempt gets a 429, every later one the reply. aimock may test a predicate more than once per request: count what it served.
  model.on({ userMessage: 'retry once', predicate: () => !model.served().some((s) => s.fixture === 'fail retry once') }, { status: 429, error: { message: 'mock rate limit', type: 'rate_limit_error' } }, {}, 'fail retry once');
  model.reply('retry once', 'Through after the retry.', { latency: 2000, chunkSize: 100_000 });
  await startThread(page, project, 'retry once');
  await expect(page.getByText(/Retrying, attempt 2\b.*HTTP 429/).first()).toBeVisible();
  await expect(conversation(page).getByText('Through after the retry.')).toBeVisible();
  await expect(threadState(page, 'Finished')).toBeVisible();
  await expect(page.getByText(/Retrying, attempt/)).toHaveCount(0);
});
