import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import { GitLabReviewRequest, gitLabReviewRequest } from '../../src/components/GitLabReviewRequest';

const prompt = 'UAM GitLab review request: 91d12677-7369-49e4-b279-f2941329dfcb\n\nInvestigate this GitLab review comment. Do not edit files.\n\nBEGIN UNTRUSTED GITLAB REVIEW EVIDENCE\n' + JSON.stringify({ commit: { sha: 'a'.repeat(40) }, merge_request: { iid: 1 }, discussion: { notes: [{ position: { new_path: 'README.md', new_line: 32 } }] } }) + '\nEND UNTRUSTED GITLAB REVIEW EVIDENCE';

test('shows a compact review request and lets the user inspect the original evidence', async () => {
  render(<GitLabReviewRequest request={gitLabReviewRequest(prompt)!} text={prompt} />);
  expect(screen.getByText('MR !1 · README.md:32 · aaaaaaaa')).toBeTruthy();
  expect(screen.getByText('Investigate this review comment.')).toBeTruthy();
  const details = screen.getByText('Review context').closest('details')!;
  expect(details.open).toBe(false);
  await userEvent.setup().click(screen.getByText('Review context'));
  expect(details.open).toBe(true);
  expect(details.querySelector('pre')?.textContent).toBe(prompt);
});

test.each(['hello', prompt.replace('91d12677-7369-49e4-b279-f2941329dfcb', 'invalid'), prompt.replace('Investigate this GitLab review comment.', 'An ordinary message.'), prompt.replace('"iid":1', '"iid":"<script>"'), prompt.slice(0, 230)])('keeps ordinary or incomplete messages in the regular renderer', (text) => {
  expect(gitLabReviewRequest(text)).toBeNull();
});
