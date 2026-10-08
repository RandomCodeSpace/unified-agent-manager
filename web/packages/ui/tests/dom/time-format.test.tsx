// Clock and date labels (components/common): one formatter per format, the same text as the Date methods.
import { expect, test, vi } from 'vitest';
import { clockTime, dateTime } from '../../src/components/common';

const times = ['2026-10-05T14:05:09Z', '2026-01-31T23:59:59.999Z', '1999-12-31T00:00:00Z', new Date(2026, 6, 4, 9, 3, 7)];

test('clock and date labels read as the browser formats them', () => {
  for (const at of times) {
    expect(clockTime(at)).toBe(new Date(at).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }));
    expect(clockTime(at, true)).toBe(new Date(at).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: '2-digit' }));
    expect(dateTime(at)).toBe(new Date(at).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }));
  }
  // An unreadable time is named, as the Date methods do, never thrown.
  expect(clockTime('not a time')).toBe('Invalid Date');
  expect(dateTime('not a time')).toBe('Invalid Date');
});

test('each format is built once, not per label', () => {
  for (const at of times) { clockTime(at); clockTime(at, true); dateTime(at); }
  // The Date methods build a formatter on every call too.
  const made = [vi.spyOn(Intl, 'DateTimeFormat'), vi.spyOn(Date.prototype, 'toLocaleTimeString'), vi.spyOn(Date.prototype, 'toLocaleString')];
  for (const at of times) { clockTime(at); clockTime(at, true); dateTime(at); }
  for (const spy of made) expect(spy).not.toHaveBeenCalled();
  vi.restoreAllMocks();
});
