// Development-only: the surface routes of the in-browser mock service (see install.ts). The
// Cards Thread (t-cards) holds the drawn surfaces, five a model drew in real runs (real-*.json and
// live-*.json, verbatim) and one the agent deleted. The first read of the board starts its patches, a few
// seconds apart; the first read of the load test starts the agent's one change to it (the
// duration), 6 seconds later, over whatever the owner has edited by then. The media are in surfaces/media, served by the dev server (vite.config.ts).

import type { Surface } from '../api';
import { applyMessage } from '../lib/surface';
import board from './surfaces/board.json';
import liveRelease from './surfaces/live-release.json';
import liveReview from './surfaces/live-review.json';
import params from './surfaces/params.json';
import realBoard from './surfaces/real-board.json';
import realRelease from './surfaces/real-release.json';
import realReview from './surfaces/real-review.json';
import release from './surfaces/release.json';
import review from './surfaces/review.json';

type Payload = { messages: object[] };
const build = (callId: string, payload: Payload, ...patches: Payload[]) =>
  [payload, ...patches].flatMap((p) => p.messages).reduce<Surface | null>((s, m) => applyMessage(s, m, callId), null)!;

export function surfaceMock(broadcast: (name: string, payload: Record<string, unknown>, sessionId?: string) => void) {
  const surfaces: Record<string, Surface> = {
    'call-surface-board': build('call-surface-board', board.create),
    'call-surface-review': build('call-surface-review', review.create),
    'call-surface-params': build('call-surface-params', params.create),
    'call-surface-draft': { ...build('call-surface-draft', release.create), surface_id: 'release-draft', deleted: true },
    'call-surface-release': build('call-surface-release', release.create),
    'call-real-board': build('call-real-board', realBoard.create, ...realBoard.patches),
    'call-real-release': build('call-real-release', realRelease.create),
    'call-real-review': build('call-real-review', realReview.create),
    'call-live-review': build('call-live-review', liveReview.create),
    // Its own id here: a Thread holds one surface per id, and real-release.json took this one.
    'call-live-release': { ...build('call-live-release', liveRelease.create), surface_id: 'release-signoff-live' },
  };
  const patching = new Set<string>();
  const patch = (callId: string, patches: Payload[], every: number) => {
    if (patching.has(callId)) return;
    patching.add(callId);
    patches.forEach((p, i) => setTimeout(() => {
      const s = p.messages.reduce<Surface | null>((at, m) => applyMessage(at, m), surfaces[callId])!;
      surfaces[callId] = s;
      broadcast('surface', { session_id: 't-cards', call_id: callId, surface_id: s.surface_id, version: s.version }, 't-cards');
    }, every * (i + 1)));
  };
  const reply = (status: number, body?: unknown) =>
    body === undefined ? new Response(null, { status }) : new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

  return function route(method: string, url: URL, body: Record<string, unknown>): Response | null {
    if (url.pathname === '/api/sessions/t-cards/surface' && method === 'GET') {
      const callId = url.searchParams.get('call') ?? '';
      const s = surfaces[callId];
      if (!s) return reply(404, { error: 'no surface' });
      if (callId === 'call-surface-board') patch(callId, board.patches, 2500);
      if (callId === 'call-surface-params') patch(callId, params.patches, 6000);
      return reply(200, s);
    }
    if (url.pathname === '/api/sessions/t-cards/surface/action' && method === 'POST') {
      const shown = Object.values(surfaces).find((s) => s.surface_id === body.surface_id);
      if (shown && shown.version !== body.version) return reply(409, { error: 'the card changed; review it and press again' });
      console.info('[uam_surface action]', JSON.stringify(body));
      return reply(202);
    }
    return null;
  };
}
