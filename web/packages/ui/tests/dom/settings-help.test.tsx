import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import { Field, FieldHelpProvider } from '../../src/components/TaskDefaults';
import { HelpTip } from '../../src/components/ui/tooltip';
import { renderApp } from './render';

const tooltip = () => document.querySelector<HTMLElement>('[data-popup="tooltip"]');
const openTooltip = () => waitFor(() => {
  const popup = tooltip();
  expect(popup).not.toBeNull();
  return popup!;
});

describe('settings help', () => {
  test('field help opens on keyboard focus and Escape closes it without changing the control', async () => {
    const user = userEvent.setup();
    const submitted = vi.fn();
    render(
      <form onSubmit={submitted}>
        <FieldHelpProvider>
          <Field id="example" label="Example" hint="Optional field guidance.">
            <input id="example" aria-describedby="example-hint" defaultValue="Unchanged" />
          </Field>
        </FieldHelpProvider>
      </form>,
    );
    const help = screen.getByRole('button', { name: 'About Example' });
    const input = screen.getByRole('textbox', { name: 'Example' }) as HTMLInputElement;
    expect(help.closest('label')).toBeNull();
    expect(document.getElementById(input.getAttribute('aria-describedby')!)?.className).toBe('sr-only');
    expect(tooltip()).toBeNull();
    await user.tab();
    expect(document.activeElement).toBe(help);
    expect((await openTooltip()).textContent).toBe('Optional field guidance.');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(tooltip()).toBeNull());
    expect(document.activeElement).toBe(help);
    expect(input.value).toBe('Unchanged');
    expect(submitted).not.toHaveBeenCalled();
    await user.tab();
    expect(document.activeElement).toBe(input);
  });

  test('an info icon opens on hover and closes after leaving', async () => {
    const user = userEvent.setup();
    render(<HelpTip label="A setting">More about this setting.</HelpTip>);
    const help = screen.getByRole('button', { name: 'About A setting' });
    await user.hover(help);
    expect((await openTooltip()).textContent).toBe('More about this setting.');
    await user.unhover(help);
    await waitFor(() => expect(tooltip()).toBeNull());
  });

  test('touch taps toggle help and an outside press dismisses it', async () => {
    const user = userEvent.setup();
    render(<><HelpTip label="A setting">Touch guidance.</HelpTip><button type="button">Elsewhere</button></>);
    const help = screen.getByRole('button', { name: 'About A setting' });
    const tap = () => user.pointer([{ keys: '[TouchA]', target: help }]);
    await tap();
    expect((await openTooltip()).textContent).toBe('Touch guidance.');
    await tap();
    await waitFor(() => expect(tooltip()).toBeNull());
    await tap();
    await openTooltip();
    await user.pointer([{ keys: '[TouchA]', target: screen.getByRole('button', { name: 'Elsewhere' }) }]);
    await waitFor(() => expect(tooltip()).toBeNull());
  });

  test('field guidance outside Settings and disabled reasons stay inline', () => {
    render(<>
      <Field id="project" label="Project" hint="Required project guidance."><input id="project" /></Field>
      <FieldHelpProvider>
        <Field id="unavailable" label="Unavailable" hint="This model does not offer this option." hintVisible>
          <input id="unavailable" disabled aria-describedby="unavailable-hint" />
        </Field>
      </FieldHelpProvider>
    </>);
    expect(screen.queryByRole('button', { name: /^About / })).toBeNull();
    expect(document.getElementById('project-hint')?.classList.contains('sr-only')).toBe(false);
    expect(document.getElementById('unavailable-hint')?.classList.contains('sr-only')).toBe(false);
  });

  test('settings rows and sections expose help beside labels while service warnings remain visible', async () => {
    const { user } = renderApp('#settings');
    const help = await screen.findByRole('button', { name: 'About Suggest replies' });
    const composer = within(screen.getByRole('region', { name: 'Composer' }));
    const toggle = composer.getByRole('switch', { name: 'Suggest replies' });
    const description = document.getElementById(toggle.getAttribute('aria-describedby')!);
    expect(description?.className).toBe('sr-only');
    await user.pointer([{ keys: '[TouchA]', target: help }]);
    expect((await openTooltip()).textContent).toContain('one Utility model call');
    expect(toggle.getAttribute('aria-checked')).toBe('true');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(tooltip()).toBeNull());
    const background = within(screen.getByRole('region', { name: 'Background AI' }));
    expect((await background.findByRole('status')).textContent).toMatch(/^Background AI is paused/);
    expect(background.getByRole('button', { name: 'About Background AI' })).toBeTruthy();
    const limit = background.getByRole('spinbutton', { name: 'Daily limit (calls)' });
    expect(document.getElementById(limit.getAttribute('aria-describedby')!)?.className).toBe('sr-only');
    await user.click(screen.getByRole('button', { name: 'Providers', exact: true }));
    const providers = within(screen.getByRole('region', { name: 'Providers', exact: true }));
    expect(providers.getByRole('button', { name: 'About Providers' })).toBeTruthy();
    expect(providers.getByText("UAM_BYOM_OPENROUTER is not set in the service's environment").classList.contains('sr-only')).toBe(false);
  });
});
