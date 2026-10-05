/** Only item identities and measured heights survive page eviction. */
export interface HistoryBand { ids: string[]; height: number }

export function bandHeight(bands: HistoryBand[]): number {
  return bands.reduce((height, band) => height + band.height, 0);
}

/** Partial reloads estimate the remaining band; the visible DOM anchor stays exact. */
export function retainBands(bands: HistoryBand[], keep: (id: string) => boolean): HistoryBand[] {
  return bands.flatMap(band => {
    const ids = band.ids.filter(keep);
    return ids.length ? [{ ids, height: band.height * ids.length / band.ids.length }] : [];
  });
}

export function windowEdges(previous: string[], next: string[]) {
  const oldSet = new Set(previous), nextSet = new Set(next);
  const first = previous.findIndex(id => nextSet.has(id));
  let last = -1, nextLast = -1;
  previous.forEach((id, i) => { if (nextSet.has(id)) last = i; });
  next.forEach((id, i) => { if (oldSet.has(id)) nextLast = i; });
  return {
    first: first < 0 ? undefined : previous[first],
    last: last < 0 ? undefined : previous[last],
    droppedBefore: first < 0 ? [] : previous.slice(0, first),
    droppedAfter: last < 0 ? [] : previous.slice(last + 1),
    addedBefore: next.slice(0, Math.max(0, next.findIndex(id => oldSet.has(id)))),
    addedAfter: next.slice(nextLast + 1),
  };
}
