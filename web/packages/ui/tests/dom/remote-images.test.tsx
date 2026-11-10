// Web images in Markdown: trusted origins load at once, any other waits for a click.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { Markdown } from '../../src/components/common';
import { GitLabItemsContext, type GitLabItems } from '../../src/components/GitLabStrip';
import { FederationContext, type FederationView } from '../../src/FederationContext';

const happy = (window as unknown as { happyDOM: { setURL: (url: string) => void } }).happyDOM;
let before = '';
beforeEach(() => {
  before = window.location.href;
  happy.setURL('https://uam.test/');
});
afterEach(() => happy.setURL(before));

const gitlab = { origin: { host: 'gitlab.test', path: 'group/project' } } as GitLabItems;
const federation = { machines: [{ id: 'peer', connection: { base_url: 'https://peer.test:8443/' } }] } as unknown as FederationView;

function Trusted({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <FederationContext.Provider value={federation}>
      <GitLabItemsContext.Provider value={gitlab}>{children}</GitLabItemsContext.Provider>
    </FederationContext.Provider>
  );
}

const image = (alt: string, src: string) => `![${alt}](${src})`;

describe('remote images', () => {
  test.each([
    ['this server', 'https://uam.test/api/sessions/t1/view/a.png'],
    ['a connected uam server', 'https://peer.test:8443/b.png'],
    ['the GitLab host', 'https://gitlab.test/group/project/uploads/c.png'],
  ])('an image from %s loads at once with no referrer', (_, src) => {
    render(<Trusted><Markdown text={image('shot', src)} /></Trusted>);
    const img = screen.getByRole('img', { name: 'shot' });
    expect(img.getAttribute('src')).toBe(src);
    expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(screen.queryByRole('button', { name: /Load image/ })).toBeNull();
  });

  test('an image from another origin is a card with its host until Load image is used', async () => {
    const user = userEvent.setup();
    const src = 'https://other.example/track.png?q=1';
    const view = render(<Trusted><Markdown text={image('chart', src)} /></Trusted>);
    expect(view.container.querySelector('img')).toBeNull();
    expect(view.container.textContent).toContain('chart');
    expect(screen.getByRole('link', { name: 'other.example' }).getAttribute('href')).toBe(src);
    await user.click(screen.getByRole('button', { name: 'Load image from other.example' }));
    const img = screen.getByRole('img', { name: 'chart' });
    expect(img.getAttribute('src')).toBe(src);
    expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  test.each([
    'https://gitlab.test.other.example/x.png',
    'https://evilgitlab.test/x.png',
    'https://gitlab.test:8443/x.png',
    'https://peer.test/x.png',
    'https://peer.test:8444/x.png',
    'https://peer.test.other.example:8443/x.png',
    'https://uam.test:444/x.png',
    'https://sub.uam.test/x.png',
  ])('a look-alike address is not trusted: %s', (src) => {
    const view = render(<Trusted><Markdown text={image('look', src)} /></Trusted>);
    expect(view.container.querySelector('img')).toBeNull();
    expect(screen.getByRole('button', { name: `Load image from ${new URL(src).host}` })).toBeTruthy();
  });

  test('an http image from a trusted host stays a link card', () => {
    const view = render(<Trusted><Markdown text={image('plain', 'http://gitlab.test/x.png')} /></Trusted>);
    expect(view.container.querySelector('img')).toBeNull();
    expect(screen.queryByRole('button', { name: /Load image/ })).toBeNull();
    expect(screen.getByRole('link', { name: /plain/ }).getAttribute('href')).toBe('http://gitlab.test/x.png');
  });

  test('a loaded image stays loaded when its row mounts again', async () => {
    const user = userEvent.setup();
    const text = image('kept', 'https://kept.example/a.png');
    const first = render(<Markdown text={text} />);
    await user.click(screen.getByRole('button', { name: 'Load image from kept.example' }));
    first.unmount();
    render(<Markdown text={text} />);
    expect(screen.getByRole('img', { name: 'kept' }).getAttribute('src')).toBe('https://kept.example/a.png');
  });

  test('an address that does not parse is neither trusted nor loadable', () => {
    const view = render(<Trusted><Markdown text={image('broken', 'https://exa%20mple.com/a.png')} /></Trusted>);
    expect(view.container.querySelector('img')).toBeNull();
    expect(screen.queryByRole('button', { name: /Load image/ })).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
    expect(view.container.textContent).toContain('broken');
  });
});
