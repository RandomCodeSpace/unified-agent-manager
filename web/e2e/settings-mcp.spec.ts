// MCP servers: what a Thread's conversation sees of them, the built-in GitHub server's switch, and the Terminal gate on
// servers that run a command. The pack's signed-out CLI manages no MCP configuration through uam, so the servers come
// from Copilot's own user file and Settings' list read is stubbed where a case needs the list.
import fs from 'node:fs';
import path from 'node:path';
import { type Page } from '@playwright/test';
import { conversation, expect, newThread, send, test, threadState } from './harness';
import { keepSettings, mockMcpServer, openSettings } from './settings-helpers';
import { ownService } from './shell-helpers';

const TOOL = 'packtools-pack_echo';

/** Opens the open Thread's MCP servers dialog and returns the row of `name`. */
async function openMcp(page: Page) {
  await page.getByRole('button', { name: 'Thread actions' }).click();
  await page.getByRole('menuitem', { name: 'MCP servers…' }).click();
  const dialog = page.getByRole('dialog', { name: 'MCP servers' });
  const row = (name: string) => dialog.getByRole('list', { name: "This thread's MCP servers" }).getByRole('listitem').filter({ has: page.getByRole('switch', { name: `Use ${name} in this thread` }) });
  return { dialog, row };
}

test('a Thread\'s MCP servers: a connected one lists its tools, a failed one says why, and its switch takes a server\'s tools from the next turn; the built-in GitHub server is off until its switch in Settings turns it on', async ({ browser, uam, workdir, model }) => {
  const mcp = await mockMcpServer([{ name: 'pack_echo', description: 'Echoes the pack.' }]);
  const own = await ownService(browser, uam.mock.url, workdir);
  try {
    // Copilot's user-wide MCP configuration, as the copilot command writes it.
    const copilotHome = path.join(own.root, 'home', '.copilot');
    fs.mkdirSync(copilotHome, { recursive: true });
    fs.writeFileSync(path.join(copilotHome, 'mcp-config.json'), JSON.stringify({ mcpServers: {
      packtools: { type: 'http', url: mcp.url, tools: ['*'] },
      packdead: { type: 'http', url: 'http://127.0.0.1:1/mcp', tools: ['*'] },
    } }));
    for (const turn of ['one', 'two', 'three']) model.reply(`mcp turn ${turn}`, `Reply ${turn}.`);
    const tools = (turn: string) => model.served().find((s) => s.fixture === `reply mcp turn ${turn}`)!.request.tools!.map((t) => t.function.name);
    const api = async (method: string, route: string, data?: unknown) => (await own.context.request.fetch(own.url + route, { method, data })).json() as Promise<Record<string, unknown>>;
    const { page } = own;
    const turn = async (name: string) => {
      await send(page, `mcp turn ${name}`);
      await expect(conversation(page).getByText(`Reply ${name}.`)).toBeVisible();
      await expect(threadState(page, 'Finished')).toBeVisible();
    };

    await page.goto('/');
    await expect(page.getByRole('region', { name: 'Home' })).toBeVisible();
    await newThread(page, own.project);
    await turn('one');
    expect(tools('one')).toContain(TOOL);

    const { dialog, row } = await openMcp(page);
    await expect(row('packtools')).toContainText('Connected');
    await row('packtools').getByRole('button', { name: 'Show tools' }).click();
    const listed = row('packtools').getByRole('list', { name: 'packtools tools' });
    await expect(listed.getByRole('listitem')).toHaveText(['pack_echoEchoes the pack.']);
    await expect(row('packtools').getByRole('button', { name: '1 tool', exact: true })).toHaveAttribute('aria-expanded', 'true');

    await expect(row('packdead')).toContainText('Failed');
    await expect(row('packdead')).toContainText('http://127.0.0.1:1/mcp');
    await row('packdead').getByRole('button', { name: 'Restart' }).click();
    await expect(row('packdead').getByRole('button', { name: 'Restart' })).toBeEnabled();
    await expect(row('packdead')).toContainText('Failed');
    await expect(row('packtools').getByRole('button', { name: 'Restart' })).toHaveCount(0);

    // Off by default, on an install that never saved the setting.
    expect(await api('GET', '/api/settings')).not.toHaveProperty('github_mcp');
    await expect(row('github-mcp-server')).toContainText('Off for this thread');
    await expect(row('github-mcp-server').getByRole('switch')).not.toBeChecked();

    // Off for this Thread: the next turn has no tool of the server, and the row folds its tools away.
    await row('packtools').getByRole('switch').click();
    await expect(row('packtools')).toContainText('Off for this thread');
    await expect(listed).toHaveCount(0);
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await turn('two');
    expect(tools('two')).not.toContain(TOOL);

    await openMcp(page);
    await row('packtools').getByRole('switch').click();
    await expect(row('packtools')).toContainText('Connected');
    await dialog.getByRole('button', { name: 'Refresh' }).click();
    await expect(row('packtools')).toContainText('Connected');
    await page.keyboard.press('Escape');
    await turn('three');
    expect(tools('three')).toContain(TOOL);
    const thread = page.url();

    // Settings lists no server while Copilot is signed out; with the list answered, the GitHub card shows uam's own switch.
    await page.route(/\/api\/mcp$/, (route) => route.fulfill({ json: { available: true, stdio_allowed: false, servers: [] } }));
    await openSettings(page, 'MCP servers');
    const section = page.getByRole('region', { name: 'MCP servers' });
    const github = section.getByRole('switch', { name: 'Use github-mcp-server in threads' });
    await expect(github).not.toBeChecked();
    await expect(github).toHaveAccessibleDescription(/^UAM's own switch, not shared with the copilot command\. Off by default/);
    await github.click();
    await expect(github).toBeChecked();
    await expect.poll(async () => (await api('GET', '/api/settings')).github_mcp).toBe(true);

    // The change reaches the open Thread at once: its conversation no longer holds the server turned off. Signed out,
    // Copilot cannot start it, and lists none in its place.
    await page.goto(thread);
    await openMcp(page);
    await expect(row('packtools')).toContainText('Connected');
    await expect(row('github-mcp-server')).toHaveCount(0);
    await page.keyboard.press('Escape');

    await openSettings(page, 'MCP servers');
    await expect(github).toBeChecked();
    await github.click();
    await expect.poll(async () => (await api('GET', '/api/settings')).github_mcp ?? false).toBe(false);
    await page.goto(thread);
    await openMcp(page);
    await expect(row('github-mcp-server')).toContainText('Off for this thread');
  } finally {
    await own.stop();
    await mcp.close();
  }
});

test('Add server offers HTTP and SSE only while Terminal is off, and a server that runs a command once it is on', async ({ page, uam }) => {
  const restore = await keepSettings(uam, ['terminal']);
  try {
    // The signed-out CLI lists no servers: the list read is answered for it; Terminal is the service's real setting.
    await page.route(/\/api\/mcp$/, (route) => route.fulfill({ json: { available: true, stdio_allowed: false, servers: [] } }));
    await openSettings(page, 'MCP servers');
    const section = page.getByRole('region', { name: 'MCP servers' });
    await section.getByRole('button', { name: 'Add server' }).click();
    const form = section.getByRole('form', { name: 'Add an MCP server' });
    const types = form.getByRole('radiogroup', { name: 'Server type' }).getByRole('radio');
    await expect(types).toHaveText(['HTTP', 'SSE']);
    await expect(form.getByText('A server that runs a command runs it on this machine for anyone signed in, so adding or editing one needs Settings → UAM and access → Terminal on. Remote servers (HTTP or SSE) need no Terminal.')).toBeVisible();
    await expect(form.getByRole('textbox', { name: 'Address' })).toBeVisible();
    await expect(form.getByRole('textbox', { name: 'Command' })).toHaveCount(0);
    await form.getByRole('button', { name: 'Cancel' }).click();

    await openSettings(page, 'UAM and access');
    await page.getByRole('region', { name: 'Access' }).getByRole('switch', { name: 'Terminal' }).click();
    await expect(page.getByRole('region', { name: 'Access' }).getByRole('switch', { name: 'Terminal' })).toBeChecked();
    await openSettings(page, 'MCP servers');
    await section.getByRole('button', { name: 'Add server' }).click();
    await expect(types).toHaveText(['HTTP', 'SSE', 'Command']);
    await expect(form.getByText(/needs Settings → UAM and access → Terminal on/)).toHaveCount(0);
    await types.filter({ hasText: 'Command' }).click();
    await expect(form.getByRole('textbox', { name: 'Command' })).toBeVisible();
    await expect(form.getByRole('textbox', { name: 'Address' })).toHaveCount(0);
  } finally {
    await restore();
  }
});
