import { clsx, type ClassValue } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';

// tailwind-merge must know the DESIGN.md type scale and shadows, or `text-ui` would be
// treated as a colour and dropped when merged with `text-ink`.
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      'font-size': [{ text: ['eyebrow', 'keycap', 'caption', 'code-sm', 'ui', 'code', 'title', 'chat', 'chat-lg', 'display-sm', 'display-md'] }],
      shadow: [{ shadow: ['float', 'modal'] }],
    },
  },
});

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
