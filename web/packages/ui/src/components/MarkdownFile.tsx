import { useState, type ReactNode } from 'react';
import { Markdown, MdBaseContext, SessionContext, WorkdirContext } from './common';
import { Segmented } from './ui/segmented';

/** Whether a path names a Markdown file, which renders rather than showing its source. */
export const isMarkdown = (path: string | undefined): boolean => !!path && /\.(md|markdown)$/i.test(path);

/**
 * A Markdown file of the Task's folder, rendered as a reply is, with a switch to its source
 * (`children`). Relative links and images resolve from the file's own folder.
 */
export function MarkdownFile({ sessionId, workdir, path, text, children }: Readonly<{
  sessionId: string;
  workdir: string;
  path: string;
  text: string;
  children: ReactNode;
}>) {
  const [view, setView] = useState('preview');
  const base = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  return (
    <>
      <Segmented
        size="sm"
        aria-label="Show the file as"
        className="self-start"
        value={view}
        onValueChange={setView}
        items={[{ value: 'preview', label: 'Preview' }, { value: 'source', label: 'Source' }]}
      />
      {view === 'source' ? children : (
        <SessionContext.Provider value={sessionId}>
          <WorkdirContext.Provider value={workdir}>
            <MdBaseContext.Provider value={base}>
              {/* A document, not a reply: its headings step down in size (a reply's stay at the body size). */}
              <Markdown text={text} className="shrink-0 text-chat text-ink [&_h1]:text-display-md [&_h2]:text-display-sm [&_h3]:text-chat-lg" />
            </MdBaseContext.Provider>
          </WorkdirContext.Provider>
        </SessionContext.Provider>
      )}
    </>
  );
}
