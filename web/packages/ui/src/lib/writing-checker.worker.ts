import { LocalLinter, SuggestionKind } from 'harper.js';
import { binary } from 'harper.js/binary';
import { MAX_WRITING_CHECK_LENGTH, type WritingIssue } from './writing-checker.ts';

let ready: Promise<LocalLinter> | undefined;

function getLinter(): Promise<LocalLinter> {
  return ready ??= (async () => {
    const linter = new LocalLinter({ binary });
    await linter.setup();
    const defaults = await linter.getDefaultLintConfig();
    if ('LongSentences' in defaults) await linter.setLintConfig({ LongSentences: false });
    return linter;
  })();
}

// Harper handles Markdown code. Mask coding tokens outside it without changing UTF-16
// offsets; preserve backticks so a technical token cannot unbalance a code span or fence.
function proseOnly(text: string): string {
  return text.replace(/\S+/g, token => {
    const word = token.replace(/^[('"[{]+|[)'"\]},;:!?]+$/g, '');
    const technical = /[/\\@_$=]/.test(word) || /^-{1,2}[\w]/.test(word)
      || /[a-z][A-Z]|[A-Za-z]\d|\d[A-Za-z]|[A-Za-z0-9]\.[A-Za-z]|^\.[\w]/.test(word);
    return technical ? token.replace(/[^`]/g, ' ') : token;
  });
}

// Writing assistance, not prose style enforcement. Keep useful grammar categories such
// as articles (Miscellaneous), agreement, punctuation and repeated words.
const kinds = new Set(['Spelling', 'Typo', 'Agreement', 'Grammar', 'Repetition', 'Punctuation', 'Capitalization', 'Miscellaneous', 'WordOrder', 'BoundaryError']);

export async function checkWritingText(text: string): Promise<WritingIssue[]> {
  if (!text.trim() || text.length > MAX_WRITING_CHECK_LENGTH) return [];
  const linter = await getLinter();
  const lints = await linter.lint(proseOnly(text), { language: 'markdown' });
  const issues: WritingIssue[] = [];
  try {
    for (const lint of lints) {
      const kind = lint.lint_kind();
      if (!kinds.has(kind)) continue;
      const span = lint.span();
      const { start, end } = span;
      span.free();
      const original = text.slice(start, end);
      // Harper 2.10 returns UTF-16 offsets. Also reject any issue crossing masked text.
      if (start < 0 || end <= start || end > text.length || original !== lint.get_problem_text()) continue;
      const suggestions = lint.suggestions();
      try {
        const replacements = [...new Set(suggestions.map(suggestion => {
          const replacement = suggestion.get_replacement_text();
          return suggestion.kind() === SuggestionKind.InsertAfter ? original + replacement : replacement;
        }))].filter(replacement => replacement !== original);
        if (replacements.length) issues.push({ start, end, kind: kind === 'Spelling' || kind === 'Typo' ? 'spelling' : 'grammar', message: lint.message(), replacements, original });
      } finally {
        for (const suggestion of suggestions) suggestion.free();
      }
    }
  } finally {
    for (const lint of lints) lint.free();
  }
  return issues.sort((a, b) => a.start - b.start);
}

if (typeof self !== 'undefined') {
  self.onmessage = async ({ data }: MessageEvent<{ id: number; text: string }>) => {
    try {
      self.postMessage({ id: data.id, issues: await checkWritingText(data.text) });
    } catch {
      self.postMessage({ id: data.id, error: 'Writing suggestions are unavailable.' });
    }
  };
}
