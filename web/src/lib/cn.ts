import { clsx, type ClassValue } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';

// tailwind-merge must know the DESIGN.md type scale, shadows and layout sizes, or `text-ui`
// would be treated as a colour and dropped when merged with `text-ink`, and `max-w-sheet`
// would survive a `max-w-[...]` override (both stay, and the later rule in the CSS wins).
const twMerge = extendTailwindMerge({
  extend: {
    theme: { spacing: ['rail', 'drawer', 'header', 'sheet', 'panel'] },
    classGroups: {
      'font-size': [{ text: ['eyebrow', 'keycap', 'caption', 'code-sm', 'ui', 'code', 'title', 'chat', 'chat-lg', 'display-sm', 'display-md'] }],
      shadow: [{ shadow: ['float', 'modal'] }],
    },
  },
});

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
