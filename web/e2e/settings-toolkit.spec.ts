// Settings → Agent toolkit: Copilot's agents, skills, hooks and instructions of a Project, and the Terminal gate on them.
import fs from 'node:fs';
import path from 'node:path';
import { type Page } from '@playwright/test';
import { conversation, expect, startThread, test, threadState, type Project } from './harness';
import { keepSettings, openSettings, readSettings } from './settings-helpers';

/** Every file under `dir`, as paths relative to it, without the repository's own. */
const filesOf = (dir: string) => (fs.readdirSync(dir, { recursive: true }) as string[]).filter((f) => !f.startsWith('.git/') && f !== '.git' && fs.statSync(path.join(dir, f)).isFile()).sort();

/** Opens the toolkit page `label` on the Project's own files: the pages share one scope, Global to start with. */
async function openToolkit(page: Page, label: string, project: Project) {
  await openSettings(page, label);
  const scope = page.getByRole('region', { name: label, exact: true }).getByRole('combobox', { name: 'Scope' });
  if ((await scope.innerText()) !== project.name) {
    await scope.click();
    await page.getByRole('option', { name: project.name, exact: true }).click();
  }
  await expect(scope).toHaveText(project.name);
}

const saved = (page: Page, text: string) => page.getByRole('main').getByRole('status').filter({ hasText: text });

test('Terminal gates agents, skills and hooks but not instructions; turned on, an agent and a hook file are added, viewed and edited, and an edit of a file changed elsewhere is refused', async ({ page, uam, project, workdir, model }) => {
  const restore = await keepSettings(uam, ['terminal']);
  try {
    expect((await readSettings(uam)).terminal).toBe(false);
    const rule = `PACK-RULE-${Math.random().toString(36).slice(2, 8)}`;
    model.reply('follow the project rules', 'Following them.');

    // Off: the three kinds that can run commands as the service user are read-only, in the page and in the service.
    for (const [label, kind, add] of [['Agents', 'agents', 'Add agent'], ['Skills', 'skills', 'Add skill'], ['Hooks', 'hooks', 'Add hook file']]) {
      await openToolkit(page, label, project);
      const section = page.getByRole('region', { name: label, exact: true });
      await expect(section.getByText(`Turn on Terminal in UAM and access to manage ${kind}. These files can configure commands that run as the service user.`)).toBeVisible();
      await expect(section.getByRole('button', { name: add, exact: true })).toBeDisabled();
    }
    await expect(uam.api('PUT', `/api/configuration/agents/sneak?project_id=${project.id}`, { content: '---\nname: sneak\ndescription: x\n---\nx\n', revision: '' })).rejects.toThrow(/: 403 /);
    expect(filesOf(workdir)).toEqual(['README.md']);

    // Instructions run nothing: they are edited with Terminal off.
    await openToolkit(page, 'Instructions', project);
    const instructions = page.getByRole('region', { name: 'Instructions', exact: true });
    await expect(instructions.getByText(/Turn on Terminal/)).toHaveCount(0);
    await instructions.getByRole('button', { name: 'View AGENTS.md' }).click();
    const reader = page.getByRole('dialog', { name: 'AGENTS.md' });
    await expect(reader.getByText(path.join(workdir, 'AGENTS.md'))).toBeVisible();
    await reader.getByRole('button', { name: 'Add AGENTS.md' }).click();
    const rules = instructions.getByRole('form', { name: 'Add AGENTS.md' });
    await rules.getByRole('textbox', { name: 'AGENTS.md (Markdown)' }).fill(`Start every reply with ${rule}.\n`);
    await rules.getByRole('button', { name: 'Save AGENTS.md' }).click();
    await expect(saved(page, 'AGENTS.md saved. New and reopened threads use the updated file.')).toBeVisible();
    expect(fs.readFileSync(path.join(workdir, 'AGENTS.md'), 'utf8')).toBe(`Start every reply with ${rule}.\n`);

    await openSettings(page, 'UAM and access');
    await page.getByRole('region', { name: 'Access' }).getByRole('switch', { name: 'Terminal' }).click();
    await expect.poll(async () => (await readSettings(uam)).terminal).toBe(true);

    // An agent: added from the form, listed, read, then edited.
    await openToolkit(page, 'Agents', project);
    const agents = page.getByRole('region', { name: 'Agents', exact: true });
    await expect(agents.getByText(/Turn on Terminal/)).toHaveCount(0);
    await agents.getByRole('button', { name: 'Add agent', exact: true }).click();
    const agent = agents.getByRole('form', { name: 'Add agent' });
    await agent.getByRole('textbox', { name: 'Name' }).fill('pack-reviewer');
    await agent.getByRole('textbox', { name: 'Description' }).fill('Reviews the pack.');
    await agent.getByRole('textbox', { name: 'Agent instructions' }).fill('Review what you are given.');
    await agent.getByRole('button', { name: 'Add agent' }).click();
    await expect(saved(page, 'pack-reviewer saved. New and reopened threads use the updated file.')).toBeVisible();
    expect(filesOf(workdir)).toEqual(['.github/agents/pack-reviewer.agent.md', 'AGENTS.md', 'README.md']);
    const agentFile = path.join(workdir, '.github/agents/pack-reviewer.agent.md');
    expect(fs.readFileSync(agentFile, 'utf8')).toContain('Review what you are given.');

    const card = agents.getByRole('button', { name: 'View agent pack-reviewer' });
    await expect(card).toHaveAccessibleDescription('Reviews the pack.');
    await card.click();
    const agentReader = page.getByRole('dialog', { name: 'agent pack-reviewer' });
    await expect(agentReader.getByText('Review what you are given.')).toBeVisible();
    await agentReader.getByRole('button', { name: 'Edit', exact: true }).click();
    const edit = agents.getByRole('form', { name: 'Edit agent' });
    await expect(edit.getByRole('textbox', { name: 'Name' })).toBeDisabled();
    // A saved agent is edited as its whole document.
    const doc = edit.getByRole('textbox', { name: 'Full native document' });
    await expect(doc).toHaveValue(/description: "Reviews the pack\."/);
    const onDisk = fs.readFileSync(agentFile, 'utf8');

    // The file changes on disk while the form is open: saving is refused and the draft stays.
    fs.writeFileSync(agentFile, onDisk.replace('Reviews the pack.', 'Changed outside the page.'));
    await doc.fill(onDisk.replace('Reviews the pack.', 'Changed in the form.'));
    await edit.getByRole('button', { name: 'Save agent' }).click();
    await expect(edit.getByRole('alert')).toHaveText('configuration changed; reload before saving or removing it');
    await expect(agents.getByText('Your draft is kept above. Copy any changes you need before reloading the saved version.')).toBeVisible();
    await expect(doc).toHaveValue(/Changed in the form\./);
    expect(fs.readFileSync(agentFile, 'utf8')).toContain('Changed outside the page.');
    await agents.getByRole('button', { name: 'Reload saved version' }).click();
    await page.getByRole('alertdialog', { name: 'Reload saved version?' }).getByRole('button', { name: 'Discard draft and reload' }).click();
    await expect(doc).toHaveValue(/Changed outside the page\./);
    await doc.fill(onDisk.replace('Reviews the pack.', 'Saved after the reload.'));
    await edit.getByRole('button', { name: 'Save agent' }).click();
    await expect(saved(page, 'pack-reviewer saved.')).toBeVisible();
    expect(fs.readFileSync(agentFile, 'utf8')).toContain('Saved after the reload.');
    await expect(card).toHaveAccessibleDescription('Saved after the reload.');

    // A hook file, on an event no turn of this case raises.
    await openToolkit(page, 'Hooks', project);
    const hooks = page.getByRole('region', { name: 'Hooks', exact: true });
    await hooks.getByRole('button', { name: 'Add hook file', exact: true }).click();
    const hook = hooks.getByRole('form', { name: 'Add hook file' });
    await hook.getByRole('textbox', { name: 'Name' }).fill('pack-hook');
    await hook.getByRole('combobox', { name: 'Event' }).click();
    await page.getByRole('option', { name: 'preCompact', exact: true }).click();
    await hook.getByRole('textbox', { name: 'Bash command' }).fill('echo pack-hook-ran');
    await hook.getByRole('button', { name: 'Add hook file' }).click();
    await expect(saved(page, 'pack-hook saved.')).toBeVisible();
    expect(JSON.parse(fs.readFileSync(path.join(workdir, '.github/hooks/pack-hook.json'), 'utf8'))).toMatchObject({ hooks: { preCompact: [{ bash: 'echo pack-hook-ran' }] } });
    const hookCard = hooks.getByRole('button', { name: 'View hook file pack-hook' });
    await expect(hookCard).toHaveAccessibleDescription('preCompact');
    await hookCard.click();
    await expect(page.getByRole('dialog', { name: 'hook file pack-hook' }).getByText('echo pack-hook-ran')).toBeVisible();
    await page.keyboard.press('Escape');

    // A new Thread of the Project gets the instructions, and its header the Terminal button.
    await startThread(page, project, 'follow the project rules');
    await expect(conversation(page).getByText('Following them.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    expect(JSON.stringify(model.served()[0].request.messages)).toContain(`Start every reply with ${rule}.`);
    await expect(page.getByRole('button', { name: 'Terminal', exact: true })).toBeVisible();
  } finally {
    await restore();
  }
});

test('Skills: listed by source with a count and a filter; View, Disable, Enable and Remove change the files, and a new Thread is offered only the skills left on', async ({ page, uam, project, workdir, model }) => {
  const restore = await keepSettings(uam, ['terminal']);
  try {
    await uam.api('PATCH', '/api/settings', { terminal: true });
    const skill = (name: string, about: string) => {
      const dir = path.join(workdir, '.github', 'skills', name);
      fs.mkdirSync(dir, { recursive: true });
      fs.writeFileSync(path.join(dir, 'SKILL.md'), `---\nname: ${name}\ndescription: ${about}\n---\n\n# ${name}\n\nBody of ${name}.\n`);
      fs.writeFileSync(path.join(dir, 'notes.txt'), 'a supporting file\n');
    };
    skill('pack-alpha', 'Alpha skill of the pack.');
    skill('pack-beta', 'Beta skill of the pack.');
    model.reply('which skills do you have', 'Listed.');

    await openToolkit(page, 'Skills', project);
    const skills = page.getByRole('region', { name: 'Skills', exact: true });
    const list = skills.getByRole('group', { name: 'skills in this scope' });
    await expect(list.getByRole('group', { name: '.github · 2' })).toBeVisible();
    await expect(list.getByRole('button', { name: /^View skill / })).toHaveCount(2);
    await expect(skills.getByText('2 skills', { exact: true })).toBeVisible();
    const filter = skills.getByRole('searchbox', { name: 'Filter skills' });
    await filter.fill('alpha');
    await expect(skills.getByText('1 of 2 skills', { exact: true })).toBeVisible();
    await expect(list.getByRole('button', { name: /^View skill / })).toHaveText(['pack-alpha']);
    await filter.fill('no such skill');
    await expect(skills.getByText('No skills match “no such skill”.')).toBeVisible();
    await filter.fill('');
    await expect(skills.getByText('2 skills', { exact: true })).toBeVisible();

    const alpha = list.getByRole('button', { name: 'View skill pack-alpha' });
    const reader = page.getByRole('dialog', { name: 'skill pack-alpha' });
    await alpha.click();
    await expect(reader.getByText('Body of pack-alpha.')).toBeVisible();
    await reader.getByRole('button', { name: 'Disable', exact: true }).click();
    const disable = page.getByRole('alertdialog', { name: 'Disable pack-alpha?' });
    await expect(disable).toContainText('Keeps the file with a disabled filename so Copilot does not load it.');
    await disable.getByRole('button', { name: 'Disable', exact: true }).click();
    await expect(saved(page, 'pack-alpha disabled. New and reopened threads use the updated configuration.')).toBeVisible();
    expect(filesOf(workdir)).toContain('.github/skills/pack-alpha/SKILL.md.uam-disabled');
    expect(filesOf(workdir)).not.toContain('.github/skills/pack-alpha/SKILL.md');
    await expect(list.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'View skill pack-alpha' }) }).getByText('Disabled', { exact: true })).toBeVisible();

    // A Thread started now is offered beta, not the disabled alpha.
    await startThread(page, project, 'which skills do you have');
    await expect(conversation(page).getByText('Listed.')).toBeVisible();
    await expect(threadState(page, 'Finished')).toBeVisible();
    const request = JSON.stringify(model.served()[0].request);
    expect(request).toContain('Beta skill of the pack.');
    expect(request).not.toContain('Alpha skill of the pack.');

    await openToolkit(page, 'Skills', project);
    await alpha.click();
    await reader.getByRole('button', { name: 'Enable', exact: true }).click();
    await page.getByRole('alertdialog', { name: 'Enable pack-alpha?' }).getByRole('button', { name: 'Enable', exact: true }).click();
    await expect(saved(page, 'pack-alpha enabled.')).toBeVisible();
    expect(filesOf(workdir)).toContain('.github/skills/pack-alpha/SKILL.md');
    await expect(list.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'View skill pack-alpha' }) }).getByText('Disabled', { exact: true })).toHaveCount(0);

    await alpha.click();
    await reader.getByRole('button', { name: 'Remove', exact: true }).click();
    const remove = page.getByRole('alertdialog', { name: 'Remove pack-alpha?' });
    await expect(remove).toContainText('Removes the skill definition while keeping supporting files.');
    await remove.getByRole('button', { name: 'Remove', exact: true }).click();
    await expect(saved(page, 'pack-alpha removed.')).toBeVisible();
    await expect(alpha).toHaveCount(0);
    await expect(skills.getByText('1 skill', { exact: true })).toBeVisible();
    expect(filesOf(workdir).filter((f) => f.includes('pack-alpha'))).toEqual(['.github/skills/pack-alpha/notes.txt']);
    expect(filesOf(workdir)).toContain('.github/skills/pack-beta/SKILL.md');
  } finally {
    await restore();
  }
});
