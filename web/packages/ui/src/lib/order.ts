/** Code-unit order, which a bare `sort()` uses too, named: IDs and ISO timestamps sort by it in every locale. */
export function byCodeUnit(a: string, b: string): number {
  if (a < b) return -1;
  if (a > b) return 1;
  return 0;
}
