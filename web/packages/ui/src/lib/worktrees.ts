import type { DiskInfo } from '../api';

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

/** The branch chip's low-disk slot: free and total while space is low, otherwise nothing. */
export function lowDisk(disk: DiskInfo | undefined): { free: number; total: number } | undefined {
  return disk?.low ? { free: disk.free_bytes, total: disk.total_bytes } : undefined;
}

/** The low-space sentence for tips and screen readers, or '' while there is room. */
export function diskLowText(disk: DiskInfo | undefined): string {
  return disk?.low ? `Disk space low: ${sizeText(disk.free_bytes)} free (${percentFree(disk)}%)` : '';
}
