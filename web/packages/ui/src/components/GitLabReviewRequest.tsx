const marker = /^UAM GitLab review request: [0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\n\n/i;
const boundary = '\n\nBEGIN UNTRUSTED GITLAB REVIEW EVIDENCE\n';

/** Presentation only: the original prompt remains available for inspection and copy. */
export function gitLabReviewRequest(text: string) {
  const match = marker.exec(text);
  if (!match) return null;
  const instruction = text.slice(match[0].length);
  const action = instruction.startsWith('Investigate this GitLab review comment.') ? 'Investigate this review comment.'
    : instruction.startsWith('Fix the issue raised in this GitLab review comment.') ? 'Fix this review comment.'
      : instruction.startsWith('Draft a reply to this GitLab review thread ') ? 'Draft a reply to this review thread.' : '';
  const start = text.indexOf(boundary);
  if (!action || start < 0) return null;
  try {
    const line = text.slice(start + boundary.length).split('\n', 1)[0];
    const data = JSON.parse(line) as { commit?: { sha?: unknown }; merge_request?: { iid?: unknown }; discussion?: { notes?: { position?: { new_path?: unknown; old_path?: unknown; new_line?: unknown; old_line?: unknown } }[] } };
    const iid = data?.merge_request?.iid;
    const sha = data?.commit?.sha;
    if (!Number.isSafeInteger(iid) || Number(iid) <= 0 || typeof sha !== 'string' || !/^[0-9a-f]{40,64}$/i.test(sha)) return null;
    const notes = data.discussion?.notes;
    const position = Array.isArray(notes) ? notes.find(note => note?.position)?.position : undefined;
    const path = position?.new_path || position?.old_path;
    const lineNumber = position?.new_line ?? position?.old_line;
    const file = typeof path === 'string' ? path + (Number.isSafeInteger(lineNumber) ? `:${lineNumber}` : '') : '';
    return { action, reference: [`MR !${iid}`, file, sha.slice(0, 8)].filter(Boolean).join(' · ') };
  } catch {
    return null;
  }
}

export function GitLabReviewRequest({ request, text }: { request: NonNullable<ReturnType<typeof gitLabReviewRequest>>; text: string }) {
  return <>
    <span className="text-caption text-muted break-words">{request.reference}</span>
    <p>{request.action}</p>
    <details className="text-caption text-muted">
      <summary className="cursor-pointer rounded-xs hover:text-ink">Review context</summary>
      <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words font-sans">{text}</pre>
    </details>
  </>;
}
