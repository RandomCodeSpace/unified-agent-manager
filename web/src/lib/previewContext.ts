import { createContext, useContext } from 'react';

interface PreviewDetails {
  name: string;
  description?: string;
  /** Only the server's file-view route permits framing; attachments retain external open. */
  frameable?: boolean;
  /** Diagrams retain their existing image-only enlargement. */
  original?: boolean;
  temporary?: boolean;
}

export type PreviewTarget = PreviewDetails & (
  /** Already displayed images and sandbox-rendered diagrams need no metadata request. */
  { url: string; image?: boolean; tempPath?: never; hash?: never }
  | { tempPath: string; hash?: string; url?: never; image?: never }
);

export type OpenPreview = (target: PreviewTarget, opener: HTMLElement) => void;
export const PreviewContext = createContext<OpenPreview | null>(null);
export const usePreview = () => useContext(PreviewContext);
export const TempRootContext = createContext<{ temp_root?: string; temp_root_aliases?: readonly string[] } | null>(null);
