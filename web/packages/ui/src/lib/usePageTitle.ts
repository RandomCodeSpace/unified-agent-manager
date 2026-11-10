import { useEffect } from 'react';
import { pageTitle } from './tasks';

/** The tab title (how many Threads wait for the owner, and `shownName`, what the pane shows) and the installed app's badge. */
export function usePageTitle(attention: number, shownName: string | null, loggedOut: boolean) {
  useEffect(() => {
    document.title = loggedOut ? pageTitle(0, null) : pageTitle(attention, shownName);
    // The badge is this page's count while it is visible; otherwise the service worker sets the service's count with each push (public/sw.js).
    const badge = () => {
      if (document.visibilityState !== 'visible') return;
      if (attention > 0) navigator.setAppBadge?.(attention).catch(() => {});
      else navigator.clearAppBadge?.().catch(() => {});
    };
    badge();
    document.addEventListener('visibilitychange', badge);
    return () => document.removeEventListener('visibilitychange', badge);
  }, [attention, shownName, loggedOut]);
}
