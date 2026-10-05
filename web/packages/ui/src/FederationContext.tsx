import type { Action, State } from './state';
import type { ApiClient, ConnectedInstance, ConnectedStatus, Meta } from './api';
import type { Unread } from './lib/tasks';
import { createContext, useContext, type ReactNode } from 'react';

/** One enabled instance ("machine") the hosting UAM shows: this one (`id` '') or a connection. */
export interface Machine {
  id: string;
  /** "This instance" or the connection's label. */
  label: string;
  /** The compact row label: "Local" or the connection's label. */
  short: string;
  connection: ConnectedInstance | null;
  client: ApiClient;
  /** The instance on screen; its live state is App's own, `state` lags it. */
  active: boolean;
  state: State;
  status: ConnectedStatus;
  hasNews: Unread;
}

/** An instance the Add project dialog lists: the enabled ones, and the others with why they cannot be chosen. */
export interface MachineChoice {
  id: string;
  label: string;
  reason?: string;
}

/** The sidebar's Project filter under federation: a Project on a machine. */
export interface MachineFilter {
  machine: string;
  project: string;
}

/** Work that waits for another machine's view to mount: its New task draft or its Edit project dialog. */
export interface MachineIntent {
  machine: string;
  kind: 'new-task' | 'edit-project';
  projectId: string;
}

/** Shell state that outlives a switch of the instance on screen. */
export interface CarriedShell {
  query: string;
  scroll: number;
  /** The Settings section on screen, and the one the next Settings view opens on (set while switching). */
  section: string;
  nextSection: string | null;
}

export interface ShellCarry {
  read: () => CarriedShell;
  write: (patch: Partial<CarriedShell>) => void;
}

export interface FederationView {
  initialPath: string;
  /** Writes this instance's view into the fragment (`push` adds a history entry); false while the route is unresolved. */
  onRoute: (path: string, push: boolean) => boolean;
  onAuth: (authenticated: boolean) => void;
  onEvent?: (action: Action) => void;
  onTerminal: (open: boolean) => void;
  connectionsSettings: ReactNode;
  sourceControl: ReactNode;
  otherAttention?: number;
  onHomeVersion: (version: string) => void;
  homeLoadedVersion?: string;
  homeVersion?: string;
  pendingRoute: boolean;
  /** Present only with connected instances: every enabled machine, the active one included. */
  machines?: Machine[];
  choices?: MachineChoice[];
  /** Shows `path` on the machine `id` ('' for this instance). */
  go?: (id: string, path: string) => void;
  filter?: MachineFilter | null;
  onFilter?: (filter: MachineFilter | null) => void;
  intent?: MachineIntent | null;
  /** Switches to the intent's machine; its view runs the intent once loaded. */
  request?: (intent: MachineIntent) => void;
  consumeIntent?: () => void;
  /** The known state of the machine on screen, to start its view from instead of a skeleton. */
  seed?: State;
  authenticated?: boolean;
  metaCache?: Map<string, Meta>;
  carry?: ShellCarry;
}
export const FederationContext = createContext<FederationView | null>(null);
export const useFederation = () => useContext(FederationContext);
