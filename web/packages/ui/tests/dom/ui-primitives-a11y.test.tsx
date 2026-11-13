import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import { AlertDialog } from '../../src/components/ui/dialog';
import { Tip, TooltipProvider } from '../../src/components/ui/tooltip';

test('an open tip describes its trigger, so text the accessible name lacks reaches a screen reader', async () => {
  render(
    <TooltipProvider>
      <Tip label={<>Model: Auto<span>Latest turn ran on Flash</span></>}>
        {/* An explicit undefined, as the sidebar row passes when it has no machine label. */}
        <button type="button" aria-label="Model: Auto" aria-describedby={undefined}>Auto</button>
      </Tip>
    </TooltipProvider>,
  );
  const trigger = screen.getByRole('button', { name: 'Model: Auto' });
  expect(trigger.hasAttribute('aria-describedby')).toBe(false);
  await userEvent.tab();
  const tip = await screen.findByRole('tooltip');
  expect(tip.textContent).toBe('Model: AutoLatest turn ran on Flash');
  expect(trigger.getAttribute('aria-describedby')).toBe(tip.id);
});

test('a trigger that carries its own description keeps it while the tip is open', async () => {
  render(
    <TooltipProvider>
      <span id="own">Own description</span>
      <Tip label="Usage">
        <button type="button" aria-label="Usage" aria-describedby="own">U</button>
      </Tip>
    </TooltipProvider>,
  );
  await userEvent.tab();
  await screen.findByRole('tooltip');
  expect(screen.getByRole('button', { name: 'Usage' }).getAttribute('aria-describedby')).toBe('own');
});

test('a tip that only repeats the accessible name adds no description', async () => {
  render(
    <TooltipProvider>
      <Tip label="Copy message">
        <button type="button" aria-label="Copy message">C</button>
      </Tip>
    </TooltipProvider>,
  );
  await userEvent.tab();
  await screen.findByRole('tooltip');
  expect(screen.getByRole('button', { name: 'Copy message' }).hasAttribute('aria-describedby')).toBe(false);
});

test('the anchored confirmation is a modal alert dialog', async () => {
  const anchor = document.createElement('button');
  document.body.append(anchor);
  anchor.getClientRects = () => [new DOMRect(0, 0, 10, 10)] as unknown as DOMRectList;
  render(<AlertDialog open onOpenChange={() => {}} title="Settle?" confirmLabel="Settle" onConfirm={() => {}} anchor={anchor} />);
  const dialog = await screen.findByRole('alertdialog', { name: 'Settle?' });
  // The anchored popover, not the centred dialog: no backdrop sits behind it.
  expect(dialog.className).toContain('max-w-80');
  await waitFor(() => expect(dialog.getAttribute('aria-modal')).toBe('true'));
  anchor.remove();
});
