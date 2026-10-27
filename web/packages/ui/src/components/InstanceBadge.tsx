import { BADGE_COLORS } from '../api';
import { cn } from '../lib/cn';
import { BADGE_TEXT } from './common';

/** Machine initials in a stable text colour, hidden from screen readers; the caller names the machine (its row's description, its button's tip). */
export function InstanceBadge({ id, label, className }: Readonly<{ id: string; label: string; className?: string }>) {
  const words = label.trim().split(/\s+/).filter(Boolean);
  const first = Array.from(words[0] ?? '?');
  const text = (words.length > 1 ? first[0] + Array.from(words.at(-1)!)[0] : first.slice(0, 2).join('')).toUpperCase();
  let hash = 0;
  for (const character of id || 'home') hash = (Math.imul(hash, 31) + character.codePointAt(0)!) >>> 0;
  return <span aria-hidden="true" className={cn('inline-block shrink-0 text-meta font-normal', BADGE_TEXT[BADGE_COLORS[hash % BADGE_COLORS.length]], className)}>{text}</span>;
}
