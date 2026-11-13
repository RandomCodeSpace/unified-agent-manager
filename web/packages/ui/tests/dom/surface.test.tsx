import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test, vi } from 'vitest';
import { ApiContext } from '../../src/ApiContext';
import { ApiError, type ApiClient, type Item, type Surface, type SurfaceComponent } from '../../src/api';
import { SurfaceCard, SurfaceView } from '../../src/components/Surface';
import { SurfaceActionChip } from '../../src/components/SurfaceActionChip';
import { applyMessage, cardTitle, drawnCount, filled, isSurfaceCall, parseActionLine, rememberSurface, set, SurfaceVersions } from '../../src/lib/surface';
import board from '../../src/mock/surfaces/board.json';
import params_ from '../../src/mock/surfaces/params.json';
import realBoard from '../../src/mock/surfaces/real-board.json';
import realRelease from '../../src/mock/surfaces/real-release.json';
import realReview from '../../src/mock/surfaces/real-review.json';
import release from '../../src/mock/surfaces/release.json';
import review from '../../src/mock/surfaces/review.json';
import { openTask } from './render';

const media = (path: string) => `/api/sessions/t/files/raw?path=${encodeURIComponent(path)}`;
const build = (payload: { messages: object[] }, from: Surface | null = null) => payload.messages.reduce<Surface | null>((s, m) => applyMessage(s, m, 'call'), from)!;
const draw = (surface: Surface, onAction = vi.fn().mockResolvedValue(undefined)) => ({ onAction, ...render(<SurfaceView surface={surface} onAction={onAction} media={media} />) });

describe('uam_surface', () => {
  test('a list template binds each row relatively, and a data patch updates it in place', () => {
    const first = build(board.create);
    const { rerender } = render(<SurfaceView surface={first} onAction={vi.fn()} media={media} />);
    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(8);
    expect(within(rows[3]).getByText('0044_receipts_backfill')).toBeTruthy();
    expect(within(rows[3]).getByText('Running')).toBeTruthy();
    const patched = build(board.patches[0], first);
    expect(patched.version).toBe(first.version + 3);
    rerender(<SurfaceView surface={patched} onAction={vi.fn()} media={media} />);
    expect(within(screen.getAllByRole('listitem')[3]).getByText('Done')).toBeTruthy();
    expect(screen.getByText('4 of 8 done, 1 running, 3 queued')).toBeTruthy();
  });

  test('a press with a required choice missing marks the empty rows instead of sending; filled, it posts every choice once', async () => {
    const user = userEvent.setup();
    const { onAction } = draw(build(review.create));
    const send = screen.getByRole('button', { name: 'Send decisions' });
    const groups = screen.getAllByRole('radiogroup');
    expect(groups).toHaveLength(6);
    // Unlabelled pickers are named by their row's words.
    expect(groups[0].getAttribute('aria-label')).toBe('Action context is posted without checking the surface still exists');
    expect(groups.some((g) => g.getAttribute('aria-invalid'))).toBe(false);
    await user.click(send);
    expect(onAction).not.toHaveBeenCalled();
    expect(groups.every((g) => g.getAttribute('aria-invalid') === 'true')).toBe(true);
    // In table rows the ring and the Button's message say it; the word is read, not shown.
    expect(screen.getAllByText('Required').map((n) => n.className)).toEqual(Array(6).fill('sr-only'));
    expect(screen.getByRole('alert').textContent).toMatch(/\S/);
    // Blank text is empty: a labelled field shows the word under itself.
    const params = draw(build(params_.create));
    const endpoint = within(params.container).getByLabelText('Target endpoint');
    await user.clear(endpoint);
    await user.type(endpoint, '   ');
    await user.click(within(params.container).getByRole('button', { name: 'Run' }));
    expect(params.onAction).not.toHaveBeenCalled();
    expect(endpoint.getAttribute('aria-invalid')).toBe('true');
    expect(document.getElementById(endpoint.getAttribute('aria-describedby')!)!.className).toContain('text-error');
    params.unmount();
    for (const [i, g] of groups.entries()) await user.click(within(g).getByRole('radio', { name: i % 2 ? 'Skip' : 'Fix' }));
    expect(screen.queryByText('Required')).toBeNull();
    await user.click(send);
    expect(onAction).toHaveBeenCalledWith({
      surface_id: 'review-surface-pr', name: 'review_decisions', source_component_id: 'send',
      context: { F1: ['fix'], F2: ['skip'], F3: ['fix'], F4: ['skip'], F5: ['fix'], F6: ['skip'] },
      request_id: expect.stringMatching(/^[0-9a-f-]{36}$/),
    });
  });

  test('media load only through the raw file route, icons draw, the date writes back, and the changelog dialog keeps its Button', async () => {
    const user = userEvent.setup();
    const { container, onAction } = draw(build(release.create));
    expect(screen.getByRole('img', { name: 'A migration board drawn as a card in a Thread' }).getAttribute('src')).toBe('/api/sessions/t/files/raw?path=release%2Fscreenshot.jpg');
    const video = container.querySelector('video')!;
    expect([video.getAttribute('src'), video.getAttribute('poster')]).toEqual(['/api/sessions/t/files/raw?path=release%2Fdemo.mp4', '/api/sessions/t/files/raw?path=release%2Fdemo-poster.jpg']);
    expect(container.querySelector('audio')!.getAttribute('src')).toBe('/api/sessions/t/files/raw?path=release%2Fvoice-note.mp3');
    expect(container.querySelectorAll('svg.lucide')).toHaveLength(4);
    const date = screen.getByLabelText('Release date') as HTMLInputElement;
    expect([date.type, date.value, date.min]).toEqual(['date', '2026-11-18', '2026-11-14']);
    fireEvent.change(date, { target: { value: '2026-11-20' } });
    await user.click(screen.getByRole('button', { name: 'Show changelog' }));
    const dialog = await screen.findByRole('dialog', { name: 'Show changelog' });
    // The trigger has no action: opening the dialog sends nothing.
    expect(onAction).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole('button', { name: 'Approve release' }));
    expect(onAction).toHaveBeenCalledWith({ surface_id: 'release-notes', name: 'approve_release', source_component_id: 'approve', context: { date: '2026-11-20' }, request_id: expect.any(String) });
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(within(dialog).getByText('Card from the agent')).toBeTruthy();
    expect(within(dialog).getByText('Sent to the agent')).toBeTruthy();
  });
});

describe('the card keeps its design whatever the model sends', () => {
  test('a root Card and nested Cards draw flat inside the one house card', () => {
    const { container } = draw(build(realRelease.create));
    const card = screen.getByRole('region', { name: 'Release sign-off · v0.23.0' });
    expect(card.classList.contains('shadow-raised')).toBe(true);
    // No box in a box: nothing inside carries a fill or a shadow of its own but the media and the inputs.
    expect(container.querySelectorAll('section div.bg-sunken, section div[class*="shadow"], section div.rounded-md')).toHaveLength(0);
  });

  test('a short lead line titles the card, a marked title too; a long instruction stays body text', () => {
    draw(build(realBoard.create));
    expect(screen.getByText('Staging database migrations').classList.contains('text-title')).toBe(true);
    expect(screen.getByText('8 migrations · simulated progress').classList.contains('-mt-2')).toBe(true);
    draw(build(release.create));
    expect(screen.getByText('uam 0.23.0 is ready to ship').classList.contains('text-title')).toBe(true);
    const { container } = draw(build(realReview.create));
    expect(screen.getByText(/^Choose Fix, Skip, or Later/).classList.contains('text-title')).toBe(false);
    expect(cardTitle(build(realReview.create))).toBeNull();
    expect(container.querySelector('section')!.hasAttribute('aria-label')).toBe(false);
  });

  test('a row of icons or buttons packs to the start though the agent spread it', () => {
    draw(build(realRelease.create));
    const icons = screen.getByText('Settings').closest('div.flex-row')!;
    const actions = screen.getByRole('button', { name: 'Approve' }).closest('div.flex-row')!;
    for (const row of [icons, actions]) expect(row.className).not.toContain('justify-between');
  });

  test('an image without a variant fills its column, contained and rounded', () => {
    draw(build(realRelease.create));
    const image = screen.getByRole('img', { name: 'Settings page screenshot' });
    expect(image.getAttribute('src')).toBe('/api/sessions/t/files/raw?path=shots%2Fsettings-page.png');
    for (const name of ['w-full', 'max-h-[28rem]', 'object-contain', 'rounded-md']) expect(image.classList.contains(name)).toBe(true);
  });

  test('template rows share one grid, a missing note keeps its cell, and Progress shows its share instead of its own name', () => {
    draw(build(realBoard.create, null));
    const list = screen.getByRole('list');
    expect((list as HTMLElement).style.gridTemplateColumns).toBe('fit-content(24rem) auto minmax(6rem,1fr) fit-content(16rem)');
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(8);
    for (const row of rows) expect(row.children).toHaveLength(4);
    expect(screen.queryByText('Progress')).toBeNull();
    expect(within(rows[0]).getByRole('progressbar').getAttribute('aria-valuetext')).toBe('100%');
    expect(within(rows[0]).getByText('100%')).toBeTruthy();
  });

  test('beside its row text a picker reads its own label but does not show it', () => {
    draw(build(realReview.create));
    const group = screen.getByRole('radiogroup', { name: 'Choice 1' });
    expect(group).toBeTruthy();
    expect(screen.getByText('Choice 1').classList.contains('sr-only')).toBe(true);
    expect(screen.getByText(/^1\. High/).classList.contains('flex-1')).toBe(true);
  });

  test('the real release Modal is titled by its content and sends nothing when opened', async () => {
    const user = userEvent.setup();
    const { onAction } = draw(build(realRelease.create));
    await user.click(screen.getByRole('button', { name: 'Show changelog' }));
    const dialog = await screen.findByRole('dialog', { name: "What's new in v0.23.0" });
    expect(within(dialog).getByText('Faster pickers')).toBeTruthy();
    expect(within(dialog).queryByText("What's new in v0.23.0", { selector: 'p' })).toBeNull();
    expect(onAction).not.toHaveBeenCalled();
  });
});

describe('the transcript', () => {
  const call = (tool: Partial<NonNullable<Item['tool']>>): Item => ({ id: 'c', kind: 'tool', time: '', tool: { name: 'uam_surface', status: 'completed', ...tool } });

  test('only a call that created a surface shows a card', () => {
    expect(isSurfaceCall(call({ surface_id: 'board' }))).toBe(true);
    expect(isSurfaceCall(call({}))).toBe(false);
    expect(isSurfaceCall(call({ name: 'uam_chart', surface_id: 'board' }))).toBe(false);
  });

  test('an action line reads as the chip, named from the card, its values on demand', async () => {
    const user = userEvent.setup();
    const line = '[uam_surface action] {"version":"1.0","action":{"name":"approve","surfaceId":"release-signoff-v0230","sourceComponentId":"approve-button","timestamp":"2026-11-13T18:00:00Z","context":{"releaseAt":"2026-11-20T10:00:00"}}}';
    const action = parseActionLine(line)!;
    expect(parseActionLine('[uam_surface action] not json')).toBeNull();
    const { rerender } = render(<SurfaceActionChip action={action} sessionId="s" />);
    expect(screen.getByRole('button', { name: 'You: Pressed approve' })).toBeTruthy();
    rememberSurface('s', build(realRelease.create));
    rerender(<SurfaceActionChip action={action} sessionId="s" />);
    const chip = await screen.findByRole('button', { name: 'You: Pressed Approve · Release sign-off · v0.23.0' });
    await user.click(chip);
    const values = await screen.findByRole('dialog', { name: 'Sent to the agent' });
    expect(within(values).getByText('releaseAt').nextElementSibling!.textContent).toBe('2026-11-20T10:00:00');
  });

  test('the words uam froze in the action line at the press win over the card as it reads now', () => {
    const line = '[uam_surface action] {"version":"1.0","action":{"name":"approve","surfaceId":"release-signoff-v0230","sourceComponentId":"approve-button","label":"Ship it","title":"Release v0.23.0","context":{}}}';
    rememberSurface('s2', build(realRelease.create));
    render(<SurfaceActionChip action={parseActionLine(line)!} sessionId="s2" />);
    expect(screen.getByRole('button', { name: 'You: Pressed Ship it · Release v0.23.0' })).toBeTruthy();
  });

  test('the Cards Thread: cards for creating calls only, a deleted card says so, and the action line copies as sent', async () => {
    const { user } = await openTask('t-cards');
    await waitFor(() => expect(document.querySelectorAll('[data-surface]')).toHaveLength(7), { timeout: 5000 });
    expect(screen.getAllByText('Removed by the agent')).toHaveLength(1);
    expect(screen.queryByText(/no longer kept/)).toBeNull();
    const chip = await screen.findByRole('button', { name: 'You: Pressed Send decisions · Review of #863: 6 findings' }, { timeout: 5000 });
    expect(screen.queryByText(/\[uam_surface action\]/)).toBeNull();
    const copy = vi.spyOn(navigator.clipboard, 'writeText');
    await user.click(within(chip.closest<HTMLElement>('[data-history-anchor]')!).getByRole('button', { name: 'Copy message' }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith(expect.stringMatching(/^\[uam_surface action\] \{"version":"1\.0","action":\{"name":"review_decisions"/)));
    copy.mockRestore();
  }, 20000);
});

const text = (id: string, words: string): SurfaceComponent => ({ id, component: 'Text', text: words });
const surfaceOf = (components: SurfaceComponent[], data_model: Record<string, unknown> = {}, version = 1): Surface => ({ surface_id: 's', call_id: 'c', catalog_id: 'urn:uam:catalog:v1', version, deleted: false, components, data_model });

describe('the review fixes', () => {
  test('a shared child doubling 24 levels deep stops at the budget with one quiet line', () => {
    const components: SurfaceComponent[] = [{ id: 'root', component: 'Column', children: ['n0', 'n0'] }];
    for (let i = 0; i < 24; i++) components.push({ id: `n${i}`, component: 'Column', children: [`n${i + 1}`, `n${i + 1}`] });
    components.push(text('n24', 'leaf'));
    const started = performance.now();
    expect(drawnCount(new Map(components.map((c) => [c.id, c])), {}, 2000)).toBe(2001);
    expect(performance.now() - started).toBeLessThan(200);
    draw(surfaceOf(components));
    expect(screen.getByText('This card is too large to show')).toBeTruthy();
    expect(screen.queryByText('leaf')).toBeNull();
  });

  test('a template counts once per item: 2,000 rows are too many, 100 are drawn', () => {
    const components = [{ id: 'root', component: 'List', children: { componentId: 'row', path: '/rows' } }, { id: 'row', component: 'Text', text: { '@path': 'name' } }];
    const rows = (n: number) => ({ rows: Array.from({ length: n }, (_, i) => ({ name: `row ${i}` })) });
    const { unmount } = draw(surfaceOf(components, rows(2000)));
    expect(screen.getByText('This card is too large to show')).toBeTruthy();
    unmount();
    draw(surfaceOf(components, rows(100)));
    expect(screen.getByText('row 99')).toBeTruthy();
  });

  test('every card and its dialogs carry uam\'s own line first, and a row of buttons never sits at the end like uam\'s prompts', () => {
    const { container } = draw(build(review.create));
    expect(container.querySelector('section')!.firstElementChild!.textContent).toBe('Card from the agent');
    const foot = screen.getByRole('button', { name: 'Send decisions' }).closest('div.flex-row')!;
    expect(foot.className).not.toMatch(/justify-(end|between)/);
  });

  test('a Card directly inside the root Card draws flat; one inside that is a hairline group', () => {
    const { container } = draw(surfaceOf([
      { id: 'root', component: 'Card', child: 'col' }, { id: 'col', component: 'Column', children: ['outer'] },
      { id: 'outer', component: 'Card', child: 'inner' }, { id: 'inner', component: 'Card', child: 'words' }, text('words', 'grouped'),
    ]));
    const groups = container.querySelectorAll('section div.border-hairline');
    expect(groups).toHaveLength(1);
    expect(groups[0].textContent).toBe('grouped');
  });

  test('an unsent edit survives an update to another value; where the agent changed it too, the agent wins and says so; after a send the edit is no longer kept', async () => {
    const user = userEvent.setup();
    const components = [
      { id: 'root', component: 'Column', children: ['name', 'note', 'go'] },
      { id: 'name', component: 'TextField', label: 'Name', value: { '@path': '/name' } },
      { id: 'note', component: 'TextField', label: 'Note', value: { '@path': '/note' } },
      { id: 'go', component: 'Button', child: 'go-label', action: { event: { name: 'go', context: { name: { '@path': '/name' } } } } }, text('go-label', 'Go'),
    ];
    const onAction = vi.fn().mockResolvedValue(undefined);
    const view = (model: Record<string, unknown>, version: number) => <SurfaceView surface={surfaceOf(components, model, version)} onAction={onAction} media={media} />;
    const { rerender } = render(view({ name: 'a', note: 'x', status: 1 }, 1));
    await user.type(screen.getByLabelText('Name'), 'b');
    await user.type(screen.getByLabelText('Note'), 'y');
    rerender(view({ name: 'a', note: 'agent', status: 2 }, 2));
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('ab');
    const note = screen.getByLabelText('Note') as HTMLInputElement;
    expect(note.value).toBe('agent');
    expect(note.getAttribute('aria-describedby')).toBeTruthy();
    expect(screen.getByText('Updated by the agent')).toBeTruthy();
    await user.type(note, '!');
    expect(screen.queryByText('Updated by the agent')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Go' }));
    expect(onAction.mock.calls[0][0].context).toEqual({ name: 'ab' });
    await screen.findByText('Sent to the agent');
    rerender(view({ name: 'a', note: 'agent', status: 3 }, 3));
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('a');
  });

  test('one action in flight per card: rows from one template are separate Buttons, and a second press waits', async () => {
    let finish = () => {};
    const onAction = vi.fn(() => new Promise<void>((r) => { finish = r; }));
    draw(surfaceOf([
      { id: 'root', component: 'List', children: { componentId: 'row', path: '/rows' } },
      { id: 'row', component: 'Row', children: ['label', 'run'] }, { id: 'label', component: 'Text', text: { '@path': 'name' } },
      { id: 'run', component: 'Button', child: 'run-label', action: { event: { name: 'run', context: { name: { '@path': 'name' } } } } }, text('run-label', 'Run'),
    ], { rows: [{ name: 'first' }, { name: 'second' }] }), onAction);
    const [first, second] = screen.getAllByRole('button', { name: 'Run' }) as HTMLButtonElement[];
    fireEvent.click(first);
    fireEvent.click(first);
    fireEvent.click(second);
    expect(onAction).toHaveBeenCalledTimes(1);
    expect(onAction.mock.calls[0][0]).toMatchObject({ context: { name: 'first' } });
    await waitFor(() => expect(first.getAttribute('aria-busy')).toBe('true'));
    expect(second.disabled).toBe(true);
    await act(async () => finish());
    expect(second.disabled).toBe(false);
    fireEvent.click(second);
    expect(onAction).toHaveBeenCalledTimes(2);
    expect(onAction.mock.calls[1][0].request_id).not.toBe(onAction.mock.calls[0][0].request_id);
  });

  test('typing in one template row redraws that row, not the others', async () => {
    const user = userEvent.setup();
    const seen = vi.fn(media);
    render(<SurfaceView surface={surfaceOf([
      { id: 'root', component: 'List', children: { componentId: 'row', path: '/rows' } },
      { id: 'row', component: 'Row', children: ['pic', 'note'] }, { id: 'pic', component: 'Image', url: { '@path': 'pic' }, variant: 'icon' },
      { id: 'note', component: 'TextField', value: { '@path': 'note' } },
    ], { rows: [{ pic: 'a.png', note: '' }, { pic: 'b.png', note: '' }, { pic: 'c.png', note: '' }], title: 't' })} onAction={vi.fn()} media={seen} />);
    seen.mockClear();
    await user.type(screen.getAllByRole('textbox')[1], 'x');
    expect(seen.mock.calls.map(([p]) => p)).toEqual(['b.png']);
    // The image keeps the catalog's small size.
    expect(document.querySelector('section img')!.classList.contains('size-6')).toBe(true);
  });

  test('icons are looked up by own name only, required reads blanks and false as empty and 0 as filled, and Progress clamps its value', () => {
    expect(() => draw(surfaceOf([{ id: 'root', component: 'Row', children: ['a', 'b'] }, { id: 'a', component: 'Icon', name: '__proto__' }, { id: 'b', component: 'Icon', name: 'toString' }]))).not.toThrow();
    expect(document.querySelectorAll('section svg')).toHaveLength(0);
    expect([filled('  '), filled(false), filled(0), filled([]), filled('x')]).toEqual([false, false, true, false, true]);
    draw(surfaceOf([{ id: 'root', component: 'Column', children: ['p', 'q'] }, { id: 'p', component: 'Progress', value: 150, max: 100 }, { id: 'q', component: 'Progress', value: -5 }]));
    const [over, under] = screen.getAllByRole('progressbar');
    expect([over.getAttribute('aria-valuenow'), under.getAttribute('aria-valuenow')]).toEqual(['100', '0']);
  });

  test('array writes never turn sparse, and "/" or no path replaces the whole model', () => {
    const model = { list: ['a'] };
    expect(set(model, '/list/5', 'x')).toEqual(model);
    expect(set(model, '/list/1', 'b')).toEqual({ list: ['a', 'b'] });
    expect(set(model, '/list/-', 'b')).toEqual({ list: ['a', 'b'] });
    const s = surfaceOf([], { old: 1 });
    for (const path of ['/', undefined]) expect(applyMessage(s, { updateDataModel: { surfaceId: 's', ...(path ? { path } : {}), value: { fresh: 2 } } })!.data_model).toEqual({ fresh: 2 });
  });
});

describe('the card in the transcript', () => {
  const client = (surface: (signal?: AbortSignal) => Promise<Surface>) => ({ surface: (_: string, __: string, signal?: AbortSignal) => surface(signal), surfaceAction: vi.fn(), rawFileUrl: (_: string, p: string) => p }) as unknown as ApiClient;
  const mount = (api: ApiClient, version: number) => <ApiContext.Provider value={api}><SurfaceVersions value={{ c: version }}><SurfaceCard sessionId="t" callId="c" /></SurfaceVersions></ApiContext.Provider>;

  test('a failed refresh keeps the card and says it may be out of date, with Retry', async () => {
    const user = userEvent.setup();
    const answers = [() => Promise.resolve(build(review.create)), () => Promise.reject(new Error('offline')), () => Promise.resolve(build(review.create))];
    const api = client(() => answers.shift()!());
    const { rerender } = render(mount(api, 1));
    await screen.findByRole('region', { name: 'Review of #863: 6 findings' });
    rerender(mount(api, 2));
    const stale = await screen.findByText(/May be out of date/);
    expect(screen.getByRole('region', { name: 'Review of #863: 6 findings' })).toBeTruthy();
    await user.click(within(stale).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByText(/May be out of date/)).toBeNull());
  });

  test('a surface the server does not know draws nothing; only a tombstone says the agent removed it', async () => {
    const { container, unmount } = render(mount(client(() => Promise.reject(new ApiError(404, 'no surface'))), 1));
    await waitFor(() => expect(container.querySelector('[aria-busy], [role="status"]')).toBeNull());
    expect(container.textContent).toBe('');
    unmount();
    render(mount(client(() => Promise.resolve({ ...build(review.create), deleted: true })), 1));
    expect(await screen.findByText('Removed by the agent')).toBeTruthy();
  });
});
