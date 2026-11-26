import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, test } from 'vitest';
import type { AppContextValue } from '../../src/components/common';
import { AppContext } from '../../src/components/common';
import { ComposerTools } from '../../src/components/ComposerTools';
import type { SessionDetail } from '../../src/api';

const session = { provider: 'copilot', context: { used: 50_000, limit: 200_000 }, capabilities: {} } as unknown as SessionDetail;

function draw(compact_threshold?: number, open?: number) {
  const app = { settings: { send_default: 'steer', terminal: false, compact_threshold }, usage: null } as unknown as AppContextValue;
  return render(<AppContext.Provider value={app}><ComposerTools session={{ ...session, compact_threshold: open }} onAgent={() => {}} /></AppContext.Provider>);
}

test('the context bar marks where compaction starts, with no ring', async () => {
  const view = draw(60);
  // The tick sits 60% along the bar; the fill is the share used.
  const mark = view.container.querySelector<HTMLElement>('[data-compact-mark]')!;
  expect(mark.style.left).toBe('60%');
  expect(mark.parentElement!.firstElementChild!.getAttribute('style')).toContain('width: 25%');
  expect(view.container.querySelector('circle')).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect((await screen.findByText(/Compacts at/)).textContent).toBe('Compacts at 60% (120K tokens)');
  view.unmount();
  draw();
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect((await screen.findByText(/Compacts at/)).textContent).toBe('Compacts at 80% (160K tokens)');
});

test('an open conversation shows the threshold it opened with, not a later setting', async () => {
  // Settings now say 50%; the conversation opened at 80% and keeps it until it reopens.
  draw(50, 80);
  await userEvent.click(screen.getByRole('button', { name: /^Tools.*25%/ }));
  expect((await screen.findByText(/Compacts at/)).textContent).toBe('Compacts at 80% (160K tokens)');
});

test.each([
  [50_000, '25%', 'text-muted', 'bg-accent'],
  [168_000, '84% context', 'text-attention', 'bg-attention'],
  [192_000, '96% context', 'text-error', 'bg-error'],
])('at %i tokens the Tools button reads "%s" in its tone', (used, text, tone, fill) => {
  const view = render(<AppContext.Provider value={{ settings: {}, usage: null } as unknown as AppContextValue}><ComposerTools session={{ ...session, context: { used, limit: 200_000 } }} onAgent={() => {}} /></AppContext.Provider>);
  const button = screen.getByRole('button', { name: /^Tools\. Context / });
  expect(within(button).getByText(text).className).toContain(tone);
  expect(view.container.querySelector('[data-compact-mark]')!.previousElementSibling!.className).toContain(fill);
});
