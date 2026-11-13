// PROTOTYPE: uam_surface (A2UI v1.0). Throwaway.
// Development-only: the surface routes of the in-browser mock service (see install.ts). t-surface
// created four surfaces (the release card's media are in surfaces/release, served by vite.config.ts); the first read of the board starts its patches, a few seconds apart.

import type { Surface } from '../api';
import { applyMessage } from '../lib/surface';
import board from './surfaces/board.json';
import params from './surfaces/params.json';
import release from './surfaces/release.json';
import review from './surfaces/review.json';

type Payload = { messages: object[] };
const build = (callId: string, payload: Payload) => payload.messages.reduce<Surface | null>((s, m) => applyMessage(s, m, callId), null)!;

export function surfaceMock(broadcast: (name: string, payload: Record<string, unknown>, sessionId?: string) => void) {
  const surfaces: Record<string, Surface> = {
    'call-surface-board': build('call-surface-board', board.create),
    'call-surface-review': build('call-surface-review', review.create),
    'call-surface-params': build('call-surface-params', params.create),
    'call-surface-release': build('call-surface-release', release.create),
  };
  let patching = false;
  const patch = () => {
    if (patching) return;
    patching = true;
    board.patches.forEach((p: Payload, i) => setTimeout(() => {
      const s = p.messages.reduce<Surface | null>((at, m) => applyMessage(at, m), surfaces['call-surface-board'])!;
      surfaces['call-surface-board'] = s;
      broadcast('surface', { session_id: 't-surface', call_id: 'call-surface-board', surface_id: s.surface_id, version: s.version }, 't-surface');
    }, 2500 * (i + 1)));
  };
  const reply = (status: number, body?: unknown) =>
    body === undefined ? new Response(null, { status }) : new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

  return function route(method: string, url: URL, body: Record<string, unknown>): Response | null {
    if (url.pathname === '/api/sessions/t-surface/surface' && method === 'GET') {
      const callId = url.searchParams.get('call') ?? '';
      const s = surfaces[callId];
      if (!s) return reply(404, { error: 'no surface' });
      if (callId === 'call-surface-board') patch();
      return reply(200, s);
    }
    if (url.pathname === '/api/sessions/t-surface/surface/action' && method === 'POST') {
      console.info('[uam_surface action]', JSON.stringify(body));
      return reply(202);
    }
    return null;
  };
}
