import type { Action } from './state';
import { createContext, useContext, type ReactNode } from 'react';

export interface FederationView {
  initialPath: string;
  /** Writes this instance's view into the fragment (`push` adds a history entry); false while the route is unresolved. */
  onRoute: (path: string, push: boolean) => boolean;
  onAuth: (authenticated: boolean) => void;
  onEvent?: (action: Action) => void;
  onTerminal: (open: boolean) => void;
  connectionsSettings: ReactNode;
  sourceControl: ReactNode;
  navigation?: (query: string) => ReactNode;
  otherAttention?: number;
  onHomeVersion: (version: string) => void;
  homeLoadedVersion?: string;
  homeVersion?: string;
  pendingRoute: boolean;
}
export const FederationContext = createContext<FederationView | null>(null);
export const useFederation = () => useContext(FederationContext);
