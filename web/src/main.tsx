import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@fontsource/inter/latin-400.css';
import '@fontsource/inter/latin-500.css';
import '@fontsource/inter/latin-600.css';
import '@fontsource/eb-garamond/latin-400.css';
import '@fontsource/eb-garamond/latin-400-italic.css';
import './styles.css';
import App from './App';

function render() {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
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
