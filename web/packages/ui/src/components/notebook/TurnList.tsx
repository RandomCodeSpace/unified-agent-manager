import { memo, useLayoutEffect, useRef } from 'react';
import { cn } from '../../lib/cn';
import type { Turn, TurnWord } from '../../lib/turns';
import { Dot, TONE_TEXT, type Tone } from '../common';
import { useTurnJump } from './slots';

const WORD_TONE: Record<TurnWord, Tone> = { Done: 'muted', Running: 'accent', 'Asked you': 'attention', Failed: 'error', Stopped: 'muted', Queued: 'muted' };

const ROW = 'flex h-8 w-full items-center gap-2 rounded-sm px-2 text-left text-ui pointer-coarse:h-11';

/**
 * The open Thread's turns (lib/turns), in the Contents column: number, the first words of the
 * prompt and how the turn stands. A row scrolls the conversation to its turn; a queued message is
 * not in the conversation yet, so its row is no button.
 */
export const TurnList = memo(function TurnList({ turns }: Readonly<{ turns: readonly Turn[] }>) {
  const jump = useTurnJump();
  const list = useRef<HTMLOListElement>(null);
  // A long Thread's list opens on its latest turns, as its conversation does, and follows a new one.
  useLayoutEffect(() => {
    if (list.current) list.current.scrollTop = list.current.scrollHeight;
  }, [turns.length]);
  if (!turns.length) return <p className="px-2 py-1 text-caption text-muted">No turns yet.</p>;
  return (
    <ol ref={list} className="flex min-h-0 flex-col gap-0.5 overflow-y-auto overscroll-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
      {turns.map((turn) => {
        const current = turn.word === 'Running' || turn.word === 'Asked you';
        const row = (
          <>
            <span className="w-5 shrink-0 text-right text-meta text-muted tabular-nums">{turn.number}</span>
            <span className="min-w-0 flex-1 truncate">{turn.text || (turn.key === 'start' ? 'Before the first message' : 'Message not loaded')}</span>
            {turn.word && (
              <span className={cn('flex shrink-0 items-center gap-1 text-caption font-normal', TONE_TEXT[WORD_TONE[turn.word]])}>
                {turn.word === 'Running' && <Dot tone="accent" />}
                {turn.word}
              </span>
            )}
          </>
        );
        return (
          <li key={turn.key}>
            {turn.queued ? (
              <div className={cn(ROW, 'text-muted')}>{row}</div>
            ) : (
              <button
                type="button"
                aria-current={current ? 'step' : undefined}
                className={cn(ROW, 'transition-[background-color] duration-100 focus-visible:-outline-offset-2', current ? 'bg-tint-selected font-medium text-ink' : 'text-body hover:bg-tint-hover')}
                onClick={() => jump(turn.key)}
              >
                {row}
              </button>
            )}
          </li>
        );
      })}
    </ol>
  );
});
