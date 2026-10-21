import { createContext } from 'react';
import type { ModelCalls } from '../api';

/**
 * The service's model calls in flight across its Threads (`model_calls`); null until a snapshot carries them, as from
 * an older service. Its own context, so the counts changing re-render the sidebar's Usage and AI button alone.
 */
export const ModelCallsContext = createContext<ModelCalls | null>(null);

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

/** The Usage and AI button's words for the work in flight, line by line: whether a model works now, for what, and what runs locally. */
export function modelCallsLines(calls: ModelCalls): string[] {
  const count = calls.threads + calls.subagents + calls.background;
  const lines = [count ? 'Model working now' : 'No model calls now'];
  if (count) {
    const parts = [calls.threads && plural(calls.threads, 'Thread'), calls.subagents && plural(calls.subagents, 'subagent')].filter(Boolean);
    parts.push(calls.background ? `Background AI: ${plural(calls.background, 'call')}` : 'Background AI idle');
    lines.push(parts.join(' · '));
  }
  if (calls.local) lines.push(`Local: ${plural(calls.local, 'Thread')} running a tool`);
  return lines;
}
