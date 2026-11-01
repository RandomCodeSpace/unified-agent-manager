import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import type { Settings } from '../../src/api';
import { OutputStyleSettings } from '../../src/components/OutputStyles';

const saved = { output_styles: [{ name: 'Terse', text: 'Be terse.' }] } as Settings;

test('the library saves as a whole, trimmed, and refuses duplicate or reserved names', async () => {
  const onSave = vi.fn();
  render(<OutputStyleSettings settings={saved} saving={false} onSave={onSave} />);
  const save = screen.getByRole('button', { name: 'Save library' });
  expect(save.hasAttribute('disabled')).toBe(true);
  await userEvent.click(screen.getByRole('button', { name: 'Add style' }));
  const names = screen.getAllByRole('textbox', { name: 'Style name' });
  const texts = screen.getAllByRole('textbox', { name: 'Style text' });
  await userEvent.type(names[1], 'terse');
  await userEvent.type(texts[1], 'Be warm.');
  expect(screen.getByText(/unique name/)).toBeTruthy();
  expect(save.hasAttribute('disabled')).toBe(true);
  await userEvent.clear(names[1]);
  await userEvent.type(names[1], 'Copilot');
  expect(save.hasAttribute('disabled')).toBe(true);
  await userEvent.clear(names[1]);
  await userEvent.type(names[1], ' Warm ');
  expect(save.hasAttribute('disabled')).toBe(false);
  await userEvent.click(save);
  expect(onSave).toHaveBeenCalledWith({ output_styles: [{ name: 'Terse', text: 'Be terse.' }, { name: 'Warm', text: 'Be warm.' }] });
});

test('removing a row keeps the other rows\' text, and Discard puts the saved library back', async () => {
  render(<OutputStyleSettings settings={{ output_styles: [{ name: 'A', text: 'One.' }, { name: 'B', text: 'Two.' }] } as Settings} saving={false} onSave={vi.fn()} />);
  await userEvent.click(screen.getByRole('button', { name: 'Remove A' }));
  expect(screen.getAllByRole('textbox', { name: 'Style name' }).map((e) => (e as HTMLInputElement).value)).toEqual(['B']);
  expect(screen.getAllByRole('textbox', { name: 'Style text' }).map((e) => (e as HTMLTextAreaElement).value)).toEqual(['Two.']);
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }));
  expect(screen.getAllByRole('textbox', { name: 'Style name' })).toHaveLength(2);
});

test('with no styles the library says how to start', () => {
  render(<OutputStyleSettings settings={{} as Settings} saving={false} onSave={vi.fn()} />);
  expect(screen.getByText(/No styles yet/)).toBeTruthy();
});

test('built-in styles show read only, can be the default, and reserve their names', async () => {
  const onSave = vi.fn();
  const builtin = [{ name: 'Simplified Technical English', text: 'Write in ASD-STE100.\nBe brief.' }];
  render(<OutputStyleSettings settings={{} as Settings} builtin={builtin} saving={false} onSave={onSave} />);
  expect(screen.getByText('Built in')).toBeTruthy();
  expect(screen.getByText('Simplified Technical English', { selector: 'summary' })).toBeTruthy();
  await userEvent.click(screen.getByRole('button', { name: 'Add style' }));
  await userEvent.type(screen.getByRole('textbox', { name: 'Style name' }), 'simplified technical english');
  await userEvent.type(screen.getByRole('textbox', { name: 'Style text' }), 'Mine.');
  expect(screen.getByRole('button', { name: 'Save library' }).hasAttribute('disabled')).toBe(true);
  await userEvent.click(screen.getByRole('combobox'));
  await userEvent.click(await screen.findByRole('option', { name: 'Simplified Technical English' }));
  expect(onSave).toHaveBeenCalledWith({ output_style: 'Simplified Technical English' });
});
