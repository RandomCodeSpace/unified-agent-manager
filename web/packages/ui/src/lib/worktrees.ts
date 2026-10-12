import type { DiskInfo, SessionSummary } from '../api';

const GB = 1024 ** 3;
const MB = 1024 ** 2;

/** 64 GB, 1.9 GB, 412 MB, 0 MB. */
export function sizeText(bytes: number): string {
  if (bytes >= 10 * GB) return `${Math.round(bytes / GB)} GB`;
  if (bytes >= GB) return `${(bytes / GB).toFixed(1)} GB`;
  return `${Math.round(bytes / MB)} MB`;
}

/** The free share of the disk in whole percent, rounded down so 9.9% reads as under the 10% line. */
export function percentFree(disk: DiskInfo): number {
  return disk.total_bytes ? Math.floor((disk.free_bytes / disk.total_bytes) * 100) : 0;
}

/** "2 not committed · 3 not pushed": words and counts, never a colour alone; '' when nothing is pending. */
export function pendingWords(pending: SessionSummary['git_pending']): string {
  if (!pending) return '';
  return [pending.uncommitted > 0 && `${pending.uncommitted} not committed`, pending.unpushed > 0 && `${pending.unpushed} not pushed`].filter(Boolean).join(' · ');
}

/** The low-space sentence for tips and screen readers, or '' while there is room. */
export function diskLowText(disk: DiskInfo | undefined): string {
  return disk?.low ? `Disk space low: ${sizeText(disk.free_bytes)} free (${percentFree(disk)}%)` : '';
}
