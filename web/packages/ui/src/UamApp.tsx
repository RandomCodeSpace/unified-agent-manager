import { useLayoutEffect } from 'react';
import { CSPProvider } from '@base-ui/react/csp-provider';
import App from './Federation';
import { applyMotion, loadMotion } from './lib/motion';
import { trackViewport } from './lib/viewport';

/** The full-page UAM application, with the same setup in every host. */
export function UamApp() {
  useLayoutEffect(() => {
    // Apply the saved motion preference before the first paint.
    applyMotion(loadMotion());
    return trackViewport();
  }, []);

  return (
    // The service's CSP allows styles from self; Base UI must not add a style element.
    <CSPProvider disableStyleElements>
      <App />
    </CSPProvider>
  );
}
