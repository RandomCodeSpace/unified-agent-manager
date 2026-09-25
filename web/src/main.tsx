import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { CSPProvider } from '@base-ui/react/csp-provider';
import '@fontsource-variable/geist';
import '@fontsource-variable/jetbrains-mono';
import './index.css';
import App from './App';
import { applyMotion, loadMotion } from './lib/motion';

// The Motion setting applies before the first render, so nothing animates that should not.
applyMotion(loadMotion());

function render() {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      {/* The server's CSP is `style-src 'self'`: Base UI must never render an inline <style>. */}
      <CSPProvider disableStyleElements>
        <App />
      </CSPProvider>
    </StrictMode>,
  );
}

// Development only: `?mock` swaps fetch and EventSource for an in-browser fake of the
// service. Vite drops this branch, and the module, from the production bundle.
if (import.meta.env.DEV && new URLSearchParams(window.location.search).has('mock')) {
  import('./mock/install').then((m) => {
    m.install();
    render();
  });
} else {
  render();
}
