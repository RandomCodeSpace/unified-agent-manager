// The live line at the foot of a running turn names the work with one calm gerund, chosen
// once per turn from the id that starts it, so it neither cycles nor changes on reload.

/** Calm, work-flavoured gerunds. Never "Working", "Thinking" or "Running": those mean specific things elsewhere. */
export const VERBS: readonly string[] = [
  'Tinkering',
  'Untangling',
  'Brewing',
  'Sketching',
  'Assembling',
  'Weaving',
  'Polishing',
  'Wrangling',
  'Plotting',
  'Mulling',
  'Sifting',
  'Stitching',
  'Tuning',
  'Charting',
  'Shaping',
  'Threading',
  'Distilling',
  'Composing',
  'Kneading',
  'Sorting',
  'Mapping',
  'Forging',
  'Calibrating',
  'Piecing together',
];

/** The verb for a turn: the same key gives the same verb, and different keys spread over the list. */
export function turnVerb(key: string): string {
  // FNV-1a over the UTF-16 code units: tiny, and stable across builds and browsers.
  let hash = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    hash ^= key.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return VERBS[hash % VERBS.length];
}
