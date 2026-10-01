// PROTOTYPE (throwaway, branch prototype/ui-redesign): three complete redesign directions for
// uam web, switchable via `?variant=A|B|C` under `vite dev`, fed by the mock seed. `#task=<id>`
// opens a Task. `&noswitch` hides the switcher for screenshots. Dev only (see main.tsx).

import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { VariantA, name as nameA } from './VariantA';
import { VariantB, name as nameB } from './VariantB';
import { VariantC, name as nameC } from './VariantC';
import { VariantD, name as nameD } from './VariantD';
import { VariantE, name as nameE } from './VariantE';
import { VariantF, name as nameF } from './VariantF';
import { VariantG, name as nameG } from './VariantG';
import { VariantH, name as nameH } from './VariantH';
import { VariantI, name as nameI } from './VariantI';
import { VariantJ, name as nameJ } from './VariantJ';
import { VariantK, name as nameK } from './VariantK';

const VARIANTS = [
  { key: 'A', name: nameA, View: VariantA },
  { key: 'B', name: nameB, View: VariantB },
  { key: 'C', name: nameC, View: VariantC },
  { key: 'D', name: nameD, View: VariantD },
  { key: 'E', name: nameE, View: VariantE },
  { key: 'F', name: nameF, View: VariantF },
  { key: 'G', name: nameG, View: VariantG },
  { key: 'H', name: nameH, View: VariantH },
  { key: 'I', name: nameI, View: VariantI },
  { key: 'J', name: nameJ, View: VariantJ },
  { key: 'K', name: nameK, View: VariantK },
];

const readTask = () => (window.location.hash.startsWith('#task=') ? window.location.hash.slice(6) : null);

export default function PrototypeApp() {
  const params = new URLSearchParams(window.location.search);
  const [key, setKey] = useState(params.get('variant')?.toUpperCase() ?? 'A');
  const [openId, setOpenId] = useState(readTask);
  const index = Math.max(0, VARIANTS.findIndex((v) => v.key === key));
  const { View } = VARIANTS[index];

  const go = (step: number) => {
    const next = VARIANTS[(index + step + VARIANTS.length) % VARIANTS.length].key;
    const url = new URL(window.location.href);
    url.searchParams.set('variant', next);
    history.replaceState(null, '', url);
    setKey(next);
  };
  const open = (id: string | null) => {
    history.replaceState(null, '', id ? `#task=${id}` : window.location.pathname + window.location.search);
    setOpenId(id);
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      if (el.closest('input, textarea, [contenteditable]')) return;
      if (e.key === 'ArrowLeft') go(-1);
      if (e.key === 'ArrowRight') go(1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  return (
    <>
      <View openId={openId} open={open} />
      {!params.has('noswitch') && (
        <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-1 rounded-full bg-[#111] px-1.5 py-1 text-ui text-[#fff] shadow-modal">
          <button aria-label="Previous variant" onClick={() => go(-1)} className="rounded-full p-1.5 hover:bg-[#ffffff26]">
            <ChevronLeft className="size-4" />
          </button>
          <span className="px-2 font-medium">
            {VARIANTS[index].key} · {VARIANTS[index].name}
          </span>
          <button aria-label="Next variant" onClick={() => go(1)} className="rounded-full p-1.5 hover:bg-[#ffffff26]">
            <ChevronRight className="size-4" />
          </button>
        </div>
      )}
    </>
  );
}
