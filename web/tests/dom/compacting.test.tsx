import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { WorkingLabel } from '../../src/components/Transcript';
import { StateMark } from '../../src/components/common';

test('while the conversation compacts, the working label and the state chip say so', () => {
  const { container } = render(<WorkingLabel working compacting items={[]} />);
  expect(container.textContent).toContain('Compacting the conversation…');
  expect(screen.getByRole('status').textContent).toBe('Compacting the conversation');
  render(<StateMark state="working" label text="Compacting…" />);
  expect(screen.getByText('Compacting…')).toBeTruthy();
  expect(screen.queryByText('Working')).toBeNull();
});
