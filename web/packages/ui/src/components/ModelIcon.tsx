import { useId, type ReactNode } from 'react';
import { modelBrand } from '../lib/models';
import { MARKS } from '../lib/modelMarks';

/** The model's brand mark in its own colours; `fallback` where no brand matches its ID. */
export function ModelIcon({ model, fallback = null, className }: Readonly<{ model: string; fallback?: ReactNode; className?: string }>) {
  const u = useId();
  const brand = modelBrand(model);
  if (!brand) return fallback;
  const mark = MARKS[brand];
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" fill={mark.fill} fillRule={mark.fillRule} className={className}>
      {mark.draw(u)}
    </svg>
  );
}
