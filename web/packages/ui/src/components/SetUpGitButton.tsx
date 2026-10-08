import { useApi } from '../ApiContext';
import { FolderGit2 } from 'lucide-react';
import { useState } from 'react';
import { describeError, type SessionSummary } from '../api';
import { Note } from './common';
import { Button } from './ui/button';

/**
 * "Set up git here": `git init` in the Project folder, offered only where git says the folder
 * is in no repository. The Project's frame then drops `no_git`, so Changes and Files appear.
 */
export function SetUpGitButton({ session, busy, onDone }: Readonly<{ session: SessionSummary; busy?: string; onDone?: () => void }>) {
  const api = useApi();
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="flex flex-col gap-1.5">
      <Button
        variant="secondary"
        className="self-start"
        disabled={!!busy}
        loading={running}
        onClick={() => {
          setRunning(true);
          setError(null);
          api.gitInit(session.id).then(() => onDone?.(), (e: unknown) => setError(describeError(e))).finally(() => setRunning(false));
        }}
      >
        <FolderGit2 />
        Set up git here
      </Button>
      {busy && <Note tone="warn" role="status">{busy}</Note>}
      {error && <Note tone="error" role="alert">{error}</Note>}
    </div>
  );
}
