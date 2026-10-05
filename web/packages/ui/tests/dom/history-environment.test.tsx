import { expect, test } from 'vitest';

test('replaceState updates the URL without dispatching hashchange', async () => {
  const events: HashChangeEvent[] = [];
  const listener = (event: HashChangeEvent) => events.push(event);
  window.addEventListener('hashchange', listener);
  try {
    history.replaceState({ task: 'example' }, '', '/#task=example');
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(window.location.hash).toBe('#task=example');
    expect(history.state).toEqual({ task: 'example' });
    expect(events).toHaveLength(0);
  } finally {
    window.removeEventListener('hashchange', listener);
  }
});

test('real hash navigation, including returning home, dispatches hashchange', async () => {
  const events: HashChangeEvent[] = [];
  const listener = (event: HashChangeEvent) => events.push(event);
  window.addEventListener('hashchange', listener);
  try {
    window.location.hash = '#task=example';
    await new Promise(resolve => setTimeout(resolve, 0));
    window.location.hash = '';
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(events.map(event => new URL(event.newURL).hash)).toEqual(['#task=example', '']);
  } finally {
    window.removeEventListener('hashchange', listener);
  }
});
