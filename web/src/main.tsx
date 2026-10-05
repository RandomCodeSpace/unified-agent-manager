import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { UamApp } from '@uam/ui';

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
render();
