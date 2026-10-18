import { render, screen } from '@testing-library/react';
import { Ban, Check } from 'lucide-react';
import { expect, test } from 'vitest';
import { Blocks } from '../../src/components/ui/blocks';

test('each block shows its number and names its meaning in words', () => {
  render(<Blocks items={[{ key: 'done', tone: 'success', Icon: Check, label: 3, title: '3 done' }, { key: 'blocked', tone: 'warning', Icon: Ban, label: 1, title: '1 blocked' }]} />);
  expect(screen.getByText('3 done').className).toContain('sr-only');
  expect(screen.getByTitle('1 blocked').textContent).toBe('11 blocked');
});

test('no blocks draws nothing', () => {
  const { container } = render(<Blocks items={[]} />);
  expect(container.innerHTML).toBe('');
});
