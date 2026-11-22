import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { Checkbox, Radio, RadioGroup } from '../../src/components/ui/checkbox';
import { Textarea } from '../../src/components/ui/textarea';

test('a textarea is the input well without a height, three rows unless told otherwise', async () => {
  const onChange = vi.fn();
  render(<><Textarea aria-label="Notes" onChange={onChange} /><Textarea aria-label="Long" rows={6} /></>);
  const notes = screen.getByRole('textbox', { name: 'Notes' });
  expect(notes.getAttribute('rows')).toBe('3');
  expect(notes.className).toContain('resize-y');
  expect(notes.className).not.toMatch(/\bh-(7|8|9)\b/);
  expect(screen.getByRole('textbox', { name: 'Long' }).getAttribute('rows')).toBe('6');
  await userEvent.type(notes, 'hi');
  expect(onChange).toHaveBeenCalledTimes(2);
});

test('a checkbox toggles from a click and from the keyboard, and a disabled one does not', async () => {
  const onCheckedChange = vi.fn();
  render(<><Checkbox aria-label="Include tests" onCheckedChange={onCheckedChange} /><Checkbox aria-label="Locked" disabled /></>);
  const box = screen.getByRole('checkbox', { name: 'Include tests' });
  expect(box.getAttribute('aria-checked')).toBe('false');
  await userEvent.click(box);
  expect(box.getAttribute('aria-checked')).toBe('true');
  expect(box.hasAttribute('data-checked')).toBe(true);
  expect(onCheckedChange).toHaveBeenLastCalledWith(true, expect.anything());
  box.focus();
  await userEvent.keyboard(' ');
  expect(box.getAttribute('aria-checked')).toBe('false');
  const locked = screen.getByRole('checkbox', { name: 'Locked' });
  await userEvent.click(locked);
  expect(locked.getAttribute('aria-checked')).toBe('false');
});

test('a radio group keeps exactly one choice', async () => {
  const onValueChange = vi.fn();
  render(
    <RadioGroup aria-label="Install" defaultValue="dev" onValueChange={onValueChange}>
      <Radio value="dev" aria-label="Dev dependency" />
      <Radio value="npx" aria-label="Run with npx" />
    </RadioGroup>,
  );
  const dev = screen.getByRole('radio', { name: 'Dev dependency' });
  const npx = screen.getByRole('radio', { name: 'Run with npx' });
  expect(dev.getAttribute('aria-checked')).toBe('true');
  await userEvent.click(npx);
  expect(npx.getAttribute('aria-checked')).toBe('true');
  expect(dev.getAttribute('aria-checked')).toBe('false');
  expect(onValueChange).toHaveBeenLastCalledWith('npx', expect.anything());
});
