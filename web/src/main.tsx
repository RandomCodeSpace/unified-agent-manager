import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { enableSocketStreams, UamApp } from '@uam/ui';

function render() {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <UamApp />
    </StrictMode>,
  );
}

// Development only: `?mock` swaps fetch and EventSource for an in-browser fake of the
// service. Vite drops this branch, and the module, from the production bundle.
if (import.meta.env.DEV && new URLSearchParams(window.location.search).has('mock')) {
  const m = await import('../packages/ui/src/mock/install');
  m.install();
}
// Over plain HTTP/1.1 every tab's event streams share six connections per host; the
// service's event socket carries them outside that pool (lib/eventStream.ts).
if (import.meta.env.PROD && window.location.protocol === 'http:') enableSocketStreams();
render();
