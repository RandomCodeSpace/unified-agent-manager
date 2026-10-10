import { Check, CircleDashed, X } from 'lucide-react';
import { cn } from '../lib/cn';
import { allUnseen, allVerified, type Stamp } from '../lib/receipts';
import { useLocateItem } from './Subagents';

// Receipts (DESIGN.md Receipts): under a turn's last reply, each path, command and test claim
// it makes, stamped with what the record shows. Quiet when every claim holds: one line that says
// so. A stamp with evidence opens the call that is the evidence.

const MARK = {
  verified: <Check aria-hidden="true" className="size-3 shrink-0 text-success" strokeWidth={2.5} />,
  unseen: <CircleDashed aria-hidden="true" className="size-3 shrink-0 text-warning" strokeWidth={2.25} />,
  contradicted: <X aria-hidden="true" className="size-3 shrink-0 text-error" strokeWidth={2.5} />,
} as const;

const WORD = { verified: 'matches the record', unseen: 'not seen in the record', contradicted: 'contradicted by the record' } as const;

export function Receipts({ stamps }: Readonly<{ stamps: readonly Stamp[] }>) {
  const locate = useLocateItem();
  if (!stamps.length) return null;
  if (allVerified(stamps)) {
    return (
      <p className="mt-2 flex items-center gap-1.5 text-meta text-muted" title={stamps.map((s) => `${s.claim}: ${s.note}`).join('\n')}>
        {MARK.verified}
        {stamps.length === 1 ? 'Its one claim matches the record' : `Its ${stamps.length} claims match the record`}
      </p>
    );
  }
  if (allUnseen(stamps)) {
    const aside = stamps[0].note.split(' · ')[1];
    return (
      <p className="mt-2 flex items-center gap-1.5 text-meta text-muted" title={stamps.map((s) => `${s.claim}: ${s.note}`).join('\n')}>
        {MARK.unseen}
        {`${stamps.length} ${stamps.length === 1 ? 'claim' : 'claims'} not seen in the main agent’s work${aside ? ` · ${aside}` : ''}`}
      </p>
    );
  }
  return (
    <ul aria-label="Receipts" className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1">
      {stamps.map((s) => {
        const body = (
          <>
            {MARK[s.verdict]}
            <span className={cn('min-w-0 truncate', s.kind === 'tests' ? '' : 'font-mono text-code-sm')}>{s.claim}</span>
            <span className="shrink-0 text-muted">{s.note}</span>
          </>
        );
        const className = cn('flex h-6 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-meta tabular-nums transition-colors duration-100', s.verdict === 'contradicted' ? 'bg-error-wash text-error' : 'bg-tint-well text-body');
        return (
          <li key={`${s.kind}:${s.claim}`} className="min-w-0 max-w-full">
            {s.itemId && locate ? (
              <button type="button" className={cn(className, 'hover:bg-tint-hover pointer-coarse:min-h-11')} title={`${WORD[s.verdict]}. Show the call.`} onClick={() => void locate(s.itemId!)}>
                {body}
              </button>
            ) : (
              <span className={className} title={WORD[s.verdict]}>{body}</span>
            )}
          </li>
        );
      })}
    </ul>
  );
}
