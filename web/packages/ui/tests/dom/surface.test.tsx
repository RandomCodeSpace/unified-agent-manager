// PROTOTYPE: uam_surface (A2UI v1.0). Throwaway.
import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import type { Surface } from '../../src/api';
import { SurfaceView } from '../../src/components/Surface';
import { applyMessage } from '../../src/lib/surface';
import review from '../../src/mock/surfaces/review.json';
import board from '../../src/mock/surfaces/board.json';
import release from '../../src/mock/surfaces/release.json';

const media = (path: string) => `/api/sessions/t/files/raw?path=${encodeURIComponent(path)}`;
const build = (payload: { messages: object[] }, from: Surface | null = null) => payload.messages.reduce<Surface | null>((s, m) => applyMessage(s, m, 'call'), from)!;

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
    const onAction = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup();
    render(<SurfaceView surface={build(review.create)} onAction={onAction} media={media} />);
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
    const onAction = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { container } = render(<SurfaceView surface={build(release.create)} onAction={onAction} media={media} />);
    expect(screen.getByRole('img', { name: 'A migration board drawn as a card in a Thread' }).getAttribute('src')).toBe('/api/sessions/t/files/raw?path=release%2Fscreenshot.jpg');
    const video = container.querySelector('video')!;
    expect([video.getAttribute('src'), video.getAttribute('poster')]).toEqual(['/api/sessions/t/files/raw?path=release%2Fdemo.mp4', '/api/sessions/t/files/raw?path=release%2Fdemo-poster.jpg']);
    expect(container.querySelector('audio')!.getAttribute('src')).toBe('/api/sessions/t/files/raw?path=release%2Fvoice-note.mp3');
    expect(container.querySelectorAll('svg.lucide')).toHaveLength(4);
    const date = screen.getByLabelText('Release date') as HTMLInputElement;
    expect([date.type, date.value, date.min]).toEqual(['date', '2026-11-18', '2026-11-14']);
    fireEvent.change(date, { target: { value: '2026-11-20' } });
    await user.click(screen.getByRole('button', { name: 'Show changelog' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Approve release' }));
    expect(onAction).toHaveBeenCalledWith({ surface_id: 'release-notes', name: 'approve_release', source_component_id: 'approve', context: { date: '2026-11-20' } });
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(within(dialog).getByText('Sent to the agent')).toBeTruthy();
  });
});
