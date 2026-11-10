import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { BACKGROUND_OUTPUT_LATEST, describeError, isStatus } from '../api';
import { useApi } from '../ApiContext';
import { Loading } from './common';
import { Button } from './ui/button';

/** How often a running shell is read again once its output is caught up. */
const FOLLOW_MS = 2000;

/**
 * A background shell's output in the command output panel, read from its start in chunks of 8 KB
 * at most: the first on open, the waiting ones on Load more, and once caught up with a running
 * shell, what it prints next every two seconds while the panel is open and the page visible.
 * A follow read keeps the panel at its end only when the reader was already there. Jump to latest
 * skips what is still waiting and shows the newest chunk instead, then follows as before.
 */
export function BackgroundOutput({ sessionId, taskId }: Readonly<{ sessionId: string; taskId: string }>) {
  const api = useApi();
  // shortcut: everything read stays in one pre; window it if multi-megabyte logs become common.
  const [text, setText] = useState('');
  const [state, setState] = useState<'reading' | 'more' | 'following' | 'done' | 'unreadable' | 'cut' | 'failed'>('reading');
  const [error, setError] = useState('');
  /** Whether Jump to latest left out output between what was read and the newest chunk. */
  const [skipped, setSkipped] = useState(false);
  /** Bumped to read the next chunk: Load more, Try again, the follow timer. */
  const [reads, setReads] = useState(0);
  const cursor = useRef('');
  const box = useRef<HTMLDivElement>(null);
  /** Whether the read in flight is a follow read, and whether its text should keep the view at the end. */
  const following = useRef(false);
  const stick = useRef(false);
  /** Whether the read in flight skips to the newest chunk; it stays set for Try again until one succeeds. */
  const jumping = useRef(false);

  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const follow = () => {
      if (document.hidden) timer = setTimeout(follow, FOLLOW_MS);
      else {
        following.current = true;
        setReads((n) => n + 1);
      }
    };
    const jump = jumping.current;
    api.backgroundOutput(sessionId, taskId, (jump ? BACKGROUND_OUTPUT_LATEST : '') + cursor.current).then((out) => {
      if (!live) return;
      const scroller = box.current?.parentElement;
      // Only a follow read moves the view, and only for a reader already at the end; a jump always does.
      stick.current = jump || (following.current && !!scroller && scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 24);
      cursor.current = out.cursor;
      if (jump) {
        jumping.current = false;
        setSkipped(true);
        setText(out.text);
      } else if (out.text) setText((shown) => shown + out.text);
      if (out.done) setState('done');
      else if (out.more) setState('more');
      else {
        setState('following');
        timer = setTimeout(follow, FOLLOW_MS);
      }
    }, (e: unknown) => {
      if (!live) return;
      setError(describeError(e));
      if (!isStatus(e, 404)) setState('failed');
      else setState(cursor.current ? 'cut' : 'unreadable');
    });
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [api, sessionId, taskId, reads]);

  useLayoutEffect(() => {
    const scroller = box.current?.parentElement;
    if (stick.current) scroller?.scrollTo({ top: scroller.scrollHeight });
  }, [text]);

  const again = (jump = jumping.current) => {
    jumping.current = jump;
    following.current = false;
    setState('reading');
    setReads((n) => n + 1);
  };
  return (
    <div ref={box}>
      {skipped && <p className="pb-2 text-caption text-muted">Earlier output was skipped.</p>}
      {text && <pre translate="no" className="font-mono text-code-sm whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{text}</pre>}
      {state === 'reading' && <Loading label="Reading output…" />}
      {state === 'more' && (
        <div className="mt-2 flex gap-2">
          <Button size="sm" onClick={() => again()}>Load more</Button>
          <Button size="sm" onClick={() => again(true)}>Jump to latest</Button>
        </div>
      )}
      <p role="status" className="pt-2 text-caption text-muted empty:hidden">
        {state === 'following' && 'Running. New output appears here.'}
        {state === 'done' && (text ? 'End of output.' : 'No output. The command printed nothing, or its output is no longer kept.')}
        {state === 'cut' && 'The rest of this output is no longer kept.'}
        {state === 'unreadable' && 'This command’s output cannot be read here.'}
      </p>
      {state === 'failed' && <p role="alert" className="pt-2 text-caption text-error">{error} <Button size="sm" variant="subtle" onClick={() => again()}>Try again</Button></p>}
    </div>
  );
}
