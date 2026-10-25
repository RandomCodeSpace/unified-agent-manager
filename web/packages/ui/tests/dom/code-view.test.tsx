import { render, waitFor } from '@testing-library/react';
import { expect, test } from 'vitest';
import { CodeView } from '../../src/components/CodeView';

test('a long file previews whole and plain at once, then highlights a slice per frame down to its last line', async () => {
  const lines = 1000;
  const text = Array.from({ length: lines }, (_, i) => `export const value${i + 1} = compute("line ${i + 1}");`).join('\n') + '\n';
  const { container } = render(<CodeView path="src/big.ts" text={text} />);
  const pre = container.querySelector('pre')!;
  // Every line is on screen from the first render, highlighted or not.
  expect(pre.textContent).toContain('line 1');
  expect(pre.textContent).toContain(`line ${lines}`);
  expect(container.querySelectorAll('ol li').length).toBe(lines);
  // The highlighter loads, then the slices light up in turn until the last line has its tokens.
  await waitFor(() => expect([...pre.querySelectorAll('.hljs-string')].some((s) => s.textContent?.includes(`line ${lines}`))).toBe(true));
  expect(pre.textContent).toBe(text.slice(0, -1));
});
