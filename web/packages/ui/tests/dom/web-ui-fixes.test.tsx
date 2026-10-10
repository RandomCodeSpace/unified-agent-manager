import { act, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { api } from '../../src/api';
import { Lightbox } from '../../src/components/Attachments';
import * as data from '../../src/mock/data';
import { log, openTask, renderApp, sidebar } from './render';

// The sandboxed diagram frame does not run here: every block draws as Mermaid draws A --> B, 110 by 160.
vi.mock('../../src/lib/diagram', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../src/lib/diagram')>()),
  renderDiagram: async () => ({ svg: '<svg xmlns="http://www.w3.org/2000/svg" width="110" height="160"></svg>', width: 110, height: 160 }),
}));

afterEach(() => vi.restoreAllMocks());

const PNG = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0];

describe('sidebar shelves', () => {
  test('the pinned section headers sit flush with the foot of the list, past its bottom padding', async () => {
    renderApp();
    const side = await sidebar();
    const settled = side.getByRole('button', { name: /^Settled/ });
    const done = side.getByRole('button', { name: /^Done/ });
    // The scroller's 12px bottom padding insets the sticky edge; the headers reach past it, so no row shows under them.
    expect(settled.parentElement!.parentElement!.className).toContain('pb-3');
    expect(settled.className).toContain('-bottom-3');
    expect(done.className).toContain('bottom-4');
    expect(done.className).toContain('pointer-coarse:bottom-8');
  });
});

describe('log out', () => {
  test('the sign-in screen has the plain title and no Settings fragment', async () => {
    vi.spyOn(api, 'auth').mockResolvedValue({ authenticated: true, required: true });
    vi.spyOn(api, 'logout').mockResolvedValue(undefined);
    const { user } = renderApp('#settings');
    await screen.findByRole('heading', { level: 1, name: 'Settings' });
    await waitFor(() => expect(document.title).toMatch(/UAM - Settings$/));
    await user.click(await screen.findByRole('button', { name: 'Log out' }));
    expect(await screen.findByRole('heading', { name: 'Sign in to UAM' })).toBeTruthy();
    await waitFor(() => expect(document.title).toBe('UAM'));
    expect(window.location.hash).toBe('');
  });
});

describe('composer attachments', () => {
  test('a text file named .png is the type the service stored: Text, no thumbnail, and no image slot', async () => {
    const state = data.seed();
    // One image per prompt, as on GPT-6 Luna.
    state.meta.providers[0].models = state.meta.providers[0].models.map((m) => (m.id === 'auto' ? { ...m, media: { images: true, pdf: true, max_images: 1 } } : m));
    vi.spyOn(data, 'seed').mockReturnValue(state);
    const { user } = await openTask('t3');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['not really a png\n'], 'fake.png', { type: 'image/png' }));
    const chip = () => screen.getByRole('button', { name: 'Remove fake.png' }).parentElement!;
    await waitFor(() => expect(within(chip()).getByText('17 B · Text')).toBeTruthy(), { timeout: 3000 });
    expect(chip().querySelector('img')).toBeNull();
    await user.upload(input, new File([new Uint8Array(PNG)], 'pic.png', { type: 'image/png' }));
    expect(await screen.findByRole('button', { name: 'Remove pic.png' })).toBeTruthy();
    expect(screen.queryByText(/accepts at most 1 image/)).toBeNull();
  });

  test('a thumbnail read after the upload came back as text stays off the chip', async () => {
    let thumbnail: (() => void) | undefined;
    vi.spyOn(FileReader.prototype, 'readAsDataURL').mockImplementation(function (this: FileReader) {
      thumbnail = () => {
        Object.defineProperty(this, 'result', { configurable: true, value: 'data:image/png;base64,bm90IHJlYWxseSBhIHBuZwo=' });
        this.onload?.(new ProgressEvent('load') as ProgressEvent<FileReader>);
      };
    });
    const { user } = await openTask('t3');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['not really a png\n'], 'fake.png', { type: 'image/png' }));
    const chip = () => screen.getByRole('button', { name: 'Remove fake.png' }).parentElement!;
    await waitFor(() => expect(within(chip()).getByText('17 B · Text')).toBeTruthy(), { timeout: 3000 });
    act(() => thumbnail!());
    expect(chip().querySelector('img')).toBeNull();
  });

  test('an image the name did not reveal shows its stored copy once the service has it', async () => {
    const { user } = await openTask('t3');
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    // No extension and no browser type: the composer guesses text, the service sniffs a PNG.
    await user.upload(input, new File([new Uint8Array(PNG)], 'screenshot', { type: '' }));
    const chip = () => screen.getByRole('button', { name: 'Remove screenshot' }).parentElement!;
    await waitFor(() => expect(within(chip()).getByText('12 B · Image')).toBeTruthy(), { timeout: 3000 });
    expect(chip().querySelector('img')?.getAttribute('src')).toMatch(/\/api\/sessions\/t3\/attachments\/.+/);
  });

  test("a new Task's first message that fails keeps a text file named .png as Text in its draft", async () => {
    vi.spyOn(api, 'prompt').mockRejectedValue(new Error('the service is busy'));
    const { user } = renderApp();
    const side = await sidebar();
    await user.click(side.getByRole('button', { name: 'New task' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /notes-site/ }));
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('New task'));
    const input = document.querySelector<HTMLInputElement>('form input[type="file"]')!;
    await user.upload(input, new File(['not really a png\n'], 'fake.png', { type: 'image/png' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' }).getAttribute('aria-disabled')).toBeNull());
    await user.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(window.location.hash).toMatch(/^#task=t\d+$/));
    expect(await screen.findByText(/its first message was not sent/)).toBeTruthy();
    const chip = (await screen.findByRole('button', { name: 'Remove fake.png' })).parentElement!;
    expect(within(chip).getByText('17 B · Text')).toBeTruthy();
    expect(chip.querySelector('img')).toBeNull();
  });
});

describe('diagrams', () => {
  test('Open diagram in a Task shows the drawing scaled up to fill the lightbox', async () => {
    const state = data.seed();
    const t3 = state.tasks.find((t) => t.id === 't3')!;
    t3.items = [...t3.items, { id: 'i-diagram', kind: 'assistant', time: new Date().toISOString(), text: '```mermaid\ngraph TD; A[Start]-->B[Done]\n```' }];
    vi.spyOn(data, 'seed').mockReturnValue(state);
    const { user } = await openTask('t3');
    await user.click(await log().findByRole('button', { name: 'Open diagram' }));
    const image = within(await screen.findByRole('dialog')).getByRole('img', { name: 'Diagram' });
    expect(image.style.getPropertyValue('--fit')).toBe('53.625dvh');
  });
});

describe('lightbox', () => {
  const svg = 'data:image/svg+xml,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%20width%3D%22110%22%20height%3D%22160%22%2F%3E';

  test('a drawing with its own size scales up to fill the room, keeping its shape', () => {
    render(<Lightbox open onOpenChange={() => {}} title="Diagram" src={svg} alt="Diagram" size={{ width: 110, height: 160 }} />);
    // As wide as the room allows, and no wider than 78dvh tall at its aspect ratio.
    const image = screen.getByRole('img', { name: 'Diagram' });
    expect(image.style.getPropertyValue('--fit')).toBe('53.625dvh');
    expect(image.className).toContain('w-[min(94vw,1400px,var(--fit))]');
    // A short window squeezes the box's height before its width: the drawing keeps its shape inside it.
    expect(image.className).toContain('object-contain');
  });

  test('a picture without a size shows at most at its own size, so it never blurs', () => {
    render(<Lightbox open onOpenChange={() => {}} title="shot.png" src="/shot.png" alt="shot.png" />);
    const image = screen.getByRole('img', { name: 'shot.png' });
    expect(image.style.getPropertyValue('--fit')).toBe('');
    expect(image.className).toContain('w-auto');
  });
});
