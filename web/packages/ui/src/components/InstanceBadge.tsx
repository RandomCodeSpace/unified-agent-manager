import { BADGE_COLORS } from '../api';
import { cn } from '../lib/cn';

/** Machine initials use a stable text colour; the full name remains available on hover. */
export function InstanceBadge({ id, label, className }: Readonly<{ id: string; label: string; className?: string }>) {
  const words = label.trim().split(/\s+/).filter(Boolean);
  const first = Array.from(words[0] ?? '?');
  const text = (words.length > 1 ? first[0] + Array.from(words.at(-1)!)[0] : first.slice(0, 2).join('')).toUpperCase();
  let hash = 0;
  for (const character of id || 'home') hash = (Math.imul(hash, 31) + character.codePointAt(0)!) >>> 0;
  return <span title={label} aria-hidden="true" className={cn('inline-block shrink-0 text-meta font-normal', className)} style={{ color: `var(--color-badge-${BADGE_COLORS[hash % BADGE_COLORS.length]})` }}>{text}</span>;
}
