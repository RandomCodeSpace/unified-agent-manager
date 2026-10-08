// The environment removes `window` after a file's last test, while a mock reply may still be on its way; the
// reply's next step would then throw "window is not defined". Its timers do nothing once `window` is gone.
import { expect, test, vi } from 'vitest';
import { install } from '../../src/mock/install';

test('a mock reply or stream still on its way does nothing once window is gone', async () => {
  install();
  const heard: string[] = [];
  void fetch('/api/meta').then(() => heard.push('reply'));
  new EventSource('/api/events').onopen = () => heard.push('open');
  vi.stubGlobal('window', undefined);
  try {
    await new Promise((resolve) => setTimeout(resolve, 200));
  } finally {
    vi.unstubAllGlobals();
  }
  expect(heard).toEqual([]);
});
