import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { CSPProvider } from '@base-ui/react/csp-provider';
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import './index.css';
import App from './App';

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
