import { useLayoutEffect } from 'react';
import { CSPProvider } from '@base-ui/react/csp-provider';
import { LucideProvider } from 'lucide-react';
import App from './Federation';
import { Button } from './components/ui/button';
import { VisualBoundary } from './components/VisualBoundary';
import { applyMotion, loadMotion } from './lib/motion';
import { startTheme } from './lib/theme';
import { trackViewport } from './lib/viewport';

/** The full-page UAM application, with the same setup in every host. */
export function UamApp() {
  useLayoutEffect(() => {
    // Apply the saved motion and theme preferences before the first paint.
    applyMotion(loadMotion());
    const stopTheme = startTheme();
    const stopViewport = trackViewport();
    return () => {
      stopTheme();
      stopViewport();
    };
  }, []);

  return (
    // The service's CSP allows styles from self; Base UI must not add a style element.
    // Every lucide icon draws at a 1.5 stroke; an explicit strokeWidth still wins.
    <CSPProvider disableStyleElements>
      <LucideProvider strokeWidth={1.5}>
        <VisualBoundary fallback={(error) => <AppFailed error={error} />}>
          <App />
        </VisualBoundary>
      </LucideProvider>
    </CSPProvider>
  );
}

/** A render error anywhere in the app: one calm full-page message with what was thrown (never its stack) and Reload. */
export function AppFailed({ error }: Readonly<{ error: unknown }>) {
  return (
    <div role="alert" className="flex h-dvh flex-col items-center justify-center gap-3 bg-canvas px-6 pb-16 text-center animate-rise">
      <h1 className="text-display-md">UAM could not show this page</h1>
      <p className="max-w-prose text-ui text-muted [overflow-wrap:anywhere]">{error instanceof Error ? error.message : String(error)}</p>
      <Button variant="primary" onClick={() => window.location.reload()}>
        Reload
      </Button>
    </div>
  );
}
