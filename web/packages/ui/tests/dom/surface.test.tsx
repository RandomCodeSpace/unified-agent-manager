import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import type { Item, Surface } from '../../src/api';
import { SurfaceView } from '../../src/components/Surface';
import { SurfaceActionChip } from '../../src/components/SurfaceActionChip';
import { applyMessage, cardTitle, isSurfaceCall, parseActionLine, rememberSurface } from '../../src/lib/surface';
import board from '../../src/mock/surfaces/board.json';
import realBoard from '../../src/mock/surfaces/real-board.json';
import realRelease from '../../src/mock/surfaces/real-release.json';
import realReview from '../../src/mock/surfaces/real-review.json';
import release from '../../src/mock/surfaces/release.json';
import review from '../../src/mock/surfaces/review.json';
import { openTask } from './render';

const media = (path: string) => `/api/sessions/t/files/raw?path=${encodeURIComponent(path)}`;
const build = (payload: { messages: object[] }, from: Surface | null = null) => payload.messages.reduce<Surface | null>((s, m) => applyMessage(s, m, 'call'), from)!;
const draw = (surface: Surface, onAction = vi.fn().mockResolvedValue(undefined)) => ({ onAction, ...render(<SurfaceView surface={surface} onAction={onAction} media={media} />) });

describe('uam_surface', () => {
  test('a list template binds each row relatively, and a data patch updates it in place', () => {
    const first = build(board.create);
    const { rerender } = render(<SurfaceView surface={first} onAction={vi.fn()} media={media} />);
    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(8);
    expect(within(rows[3]).getByText('0044_receipts_backfill')).toBeTruthy();
    expect(within(rows[3]).getByText('Running')).toBeTruthy();
    const patched = build(board.patches[0], first);
    expect(patched.version).toBe(first.version + 3);
    rerender(<SurfaceView surface={patched} onAction={vi.fn()} media={media} />);
    expect(within(screen.getAllByRole('listitem')[3]).getByText('Done')).toBeTruthy();
    expect(screen.getByText('4 of 8 done, 1 running, 3 queued')).toBeTruthy();
  });

  test('Send stays disabled until every row has a choice, then posts every choice', async () => {
    const user = userEvent.setup();
    const { onAction } = draw(build(review.create));
    const send = screen.getByRole('button', { name: 'Send decisions' }) as HTMLButtonElement;
    const groups = screen.getAllByRole('radiogroup');
    expect(groups).toHaveLength(6);
    for (const [i, g] of groups.entries()) {
      expect(send.disabled).toBe(true);
      await user.click(within(g).getByRole('radio', { name: i % 2 ? 'Skip' : 'Fix' }));
    }
    expect(send.disabled).toBe(false);
    await user.click(send);
    expect(onAction).toHaveBeenCalledWith({
      surface_id: 'review-surface-pr', name: 'review_decisions', source_component_id: 'send',
      context: { F1: ['fix'], F2: ['skip'], F3: ['fix'], F4: ['skip'], F5: ['fix'], F6: ['skip'] },
    });
  });

  test('media load only through the raw file route, icons draw, the date writes back, and the changelog dialog keeps its Button', async () => {
    const user = userEvent.setup();
    const { container, onAction } = draw(build(release.create));
    expect(screen.getByRole('img', { name: 'A migration board drawn as a card in a Thread' }).getAttribute('src')).toBe('/api/sessions/t/files/raw?path=release%2Fscreenshot.jpg');
    const video = container.querySelector('video')!;
    expect([video.getAttribute('src'), video.getAttribute('poster')]).toEqual(['/api/sessions/t/files/raw?path=release%2Fdemo.mp4', '/api/sessions/t/files/raw?path=release%2Fdemo-poster.jpg']);
    expect(container.querySelector('audio')!.getAttribute('src')).toBe('/api/sessions/t/files/raw?path=release%2Fvoice-note.mp3');
    expect(container.querySelectorAll('svg.lucide')).toHaveLength(4);
    const date = screen.getByLabelText('Release date') as HTMLInputElement;
    expect([date.type, date.value, date.min]).toEqual(['date', '2026-11-18', '2026-11-14']);
    fireEvent.change(date, { target: { value: '2026-11-20' } });
    await user.click(screen.getByRole('button', { name: 'Show changelog' }));
    const dialog = await screen.findByRole('dialog', { name: 'Show changelog' });
    // The trigger has no action: opening the dialog sends nothing.
    expect(onAction).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Approve release' }));
    expect(onAction).toHaveBeenCalledWith({ surface_id: 'release-notes', name: 'approve_release', source_component_id: 'approve', context: { date: '2026-11-20' } });
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(within(dialog).getByText('Sent to the agent')).toBeTruthy();
  });
});

describe('the card keeps its design whatever the model sends', () => {
  test('a root Card and nested Cards draw flat inside the one house card', () => {
    const { container } = draw(build(realRelease.create));
    const card = screen.getByRole('region', { name: 'Release sign-off · v0.23.0' });
    expect(card.classList.contains('shadow-raised')).toBe(true);
    // No box in a box: nothing inside carries a fill or a shadow of its own but the media and the inputs.
    expect(container.querySelectorAll('section div.bg-sunken, section div[class*="shadow"], section div.rounded-md')).toHaveLength(0);
  });

  test('a short lead line titles the card, a marked title too; a long instruction stays body text', () => {
    draw(build(realBoard.create));
    expect(screen.getByText('Staging database migrations').classList.contains('text-title')).toBe(true);
    expect(screen.getByText('8 migrations · simulated progress').classList.contains('-mt-2')).toBe(true);
    draw(build(release.create));
    expect(screen.getByText('uam 0.23.0 is ready to ship').classList.contains('text-title')).toBe(true);
    const { container } = draw(build(realReview.create));
    expect(screen.getByText(/^Choose Fix, Skip, or Later/).classList.contains('text-title')).toBe(false);
    expect(cardTitle(build(realReview.create))).toBeNull();
    expect(container.querySelector('section')!.getAttribute('aria-label')).toBe('Card from the agent');
  });

  test('a row of icons or buttons packs to the start though the agent spread it', () => {
    draw(build(realRelease.create));
    const icons = screen.getByText('Settings').closest('div.flex-row')!;
    const actions = screen.getByRole('button', { name: 'Approve' }).closest('div.flex-row')!;
    for (const row of [icons, actions]) expect(row.className).not.toContain('justify-between');
  });

  test('an image without a variant fills its column, contained and rounded', () => {
    draw(build(realRelease.create));
    const image = screen.getByRole('img', { name: 'Settings page screenshot' });
    expect(image.getAttribute('src')).toBe('/api/sessions/t/files/raw?path=shots%2Fsettings-page.png');
    for (const name of ['w-full', 'max-h-[28rem]', 'object-contain', 'rounded-md']) expect(image.classList.contains(name)).toBe(true);
  });

  test('template rows share one grid, a missing note keeps its cell, and Progress shows its share instead of its own name', () => {
    draw(build(realBoard.create, null));
    const list = screen.getByRole('list');
    expect((list as HTMLElement).style.gridTemplateColumns).toBe('fit-content(24rem) auto minmax(6rem,1fr) fit-content(16rem)');
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(8);
    for (const row of rows) expect(row.children).toHaveLength(4);
    expect(screen.queryByText('Progress')).toBeNull();
    expect(within(rows[0]).getByRole('progressbar').getAttribute('aria-valuetext')).toBe('100%');
    expect(within(rows[0]).getByText('100%')).toBeTruthy();
  });

  test('beside its row text a picker reads its own label but does not show it', () => {
    draw(build(realReview.create));
    const group = screen.getByRole('radiogroup', { name: 'Choice 1' });
    expect(group).toBeTruthy();
    expect(screen.getByText('Choice 1').classList.contains('sr-only')).toBe(true);
    expect(screen.getByText(/^1\. High/).classList.contains('flex-1')).toBe(true);
  });

  test('the real release Modal is titled by its content and sends nothing when opened', async () => {
    const user = userEvent.setup();
    const { onAction } = draw(build(realRelease.create));
    await user.click(screen.getByRole('button', { name: 'Show changelog' }));
    const dialog = await screen.findByRole('dialog', { name: "What's new in v0.23.0" });
    expect(within(dialog).getByText('Faster pickers')).toBeTruthy();
    expect(within(dialog).queryByText("What's new in v0.23.0", { selector: 'p' })).toBeNull();
    expect(onAction).not.toHaveBeenCalled();
  });
});

describe('the transcript', () => {
  const call = (tool: Partial<NonNullable<Item['tool']>>): Item => ({ id: 'c', kind: 'tool', time: '', tool: { name: 'uam_surface', status: 'completed', ...tool } });

  test('only a call that created a surface shows a card', () => {
    expect(isSurfaceCall(call({ surface_id: 'board' }))).toBe(true);
    expect(isSurfaceCall(call({}))).toBe(false);
    expect(isSurfaceCall(call({ name: 'uam_chart', surface_id: 'board' }))).toBe(false);
  });

  test('an action line reads as the chip, named from the card, its values on demand', async () => {
    const user = userEvent.setup();
    const line = '[uam_surface action] {"version":"1.0","action":{"name":"approve","surfaceId":"release-signoff-v0230","sourceComponentId":"approve-button","timestamp":"2026-11-13T18:00:00Z","context":{"releaseAt":"2026-11-20T10:00:00"}}}';
    const action = parseActionLine(line)!;
    expect(parseActionLine('[uam_surface action] not json')).toBeNull();
    const { rerender } = render(<SurfaceActionChip action={action} sessionId="s" />);
    expect(screen.getByRole('button', { name: 'You: Pressed approve' })).toBeTruthy();
    rememberSurface('s', build(realRelease.create));
    rerender(<SurfaceActionChip action={action} sessionId="s" />);
    const chip = await screen.findByRole('button', { name: 'You: Pressed Approve · Release sign-off · v0.23.0' });
    await user.click(chip);
    const values = await screen.findByRole('dialog', { name: 'Sent to the agent' });
    expect(within(values).getByText('releaseAt').nextElementSibling!.textContent).toBe('2026-11-20T10:00:00');
  });

  test('the Cards Thread: cards for creating calls only, a deleted card says so, and the action line copies as sent', async () => {
    const { user } = await openTask('t-cards');
    await waitFor(() => expect(document.querySelectorAll('[data-surface]')).toHaveLength(7), { timeout: 5000 });
    expect(screen.getAllByText('Removed by the agent')).toHaveLength(1);
    expect(screen.queryByText(/no longer kept/)).toBeNull();
    const chip = await screen.findByRole('button', { name: 'You: Pressed Send decisions · Review of #863: 6 findings' }, { timeout: 5000 });
    expect(screen.queryByText(/\[uam_surface action\]/)).toBeNull();
    const copy = vi.spyOn(navigator.clipboard, 'writeText');
    await user.click(within(chip.closest<HTMLElement>('[data-history-anchor]')!).getByRole('button', { name: 'Copy message' }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith(expect.stringMatching(/^\[uam_surface action\] \{"version":"1\.0","action":\{"name":"review_decisions"/)));
    copy.mockRestore();
  }, 20000);
});
