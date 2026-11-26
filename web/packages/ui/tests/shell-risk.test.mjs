import assert from 'node:assert/strict';
import test from 'node:test';
import { shellRisk } from '../src/lib/shellRisk.ts';

const roots = ['/work/project'];

test('a destructive command outside the project says why, most telling reasons first', () => {
  assert.equal(shellRisk('chmod 0644 ~/.config/zsh/aliases.zsh && rm -f ~/.zcompdump*', roots), 'Outside the project · Deletes files · Changes file permissions');
  assert.equal(shellRisk('sudo apt-get install jq', roots), 'Runs as root');
  assert.equal(shellRisk('git push --force origin main', roots), 'Rewrites git history · Forces the change');
  assert.equal(shellRisk('git reset --hard HEAD~1', roots), 'Rewrites git history');
  assert.equal(shellRisk('printf one > one.txt', roots), 'Writes to a file');
  assert.equal(shellRisk('cat /etc/passwd', roots), 'Outside the project');
});

test('the CLI warning and sandbox escape come first, and their text is not matched as the command', () => {
  assert.equal(shellRisk('ls\n\nWarning: rm -rf is mentioned here', roots), 'Copilot warns about it');
  assert.equal(shellRisk('make\n\nRequests to run outside the sandbox. sudo needed', roots), 'Runs outside the sandbox');
});

test('a harmless or unrecognised command gets no line, never a safe verdict', () => {
  for (const command of ['go test ./...', 'ls -la /work/project/src', 'npm test 2>&1 | tail -5', 'grep -r foo . > /dev/null', 'node -e "[1].map(x => x)"', 'git status'])
    assert.equal(shellRisk(command, roots), '', command);
});
