import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterAll, beforeAll, describe, expect, test, vi } from 'vitest';
import type { ApiClient } from '../../src/api';
import { ApiContext } from '../../src/ApiContext';
import { SessionContext } from '../../src/components/common';
import { DiagramCard } from '../../src/components/Diagram';
import type { FlowGraph } from '../../src/lib/flowGraph';
import { layoutFlow } from '../../src/lib/flowLayout';

// ELK lays out in this thread here: happy-dom has no Worker for its worker script.
vi.mock('elkjs/lib/elk-api.js', async () => {
  const { default: ELK } = await import('elkjs/lib/elk.bundled.js');
  return { default: class { layout = (graph: object) => new ELK().layout(graph) } };
});
// Watched so a test can tell which directions a chart was laid out in.
vi.mock('../../src/lib/flowLayout', async (real) => {
  const m = await real<typeof import('../../src/lib/flowLayout')>();
  return { ...m, layoutFlow: vi.fn(m.layoutFlow) };
});
// The Mermaid view's frame does not draw here; these tests are about the React Flow view.
vi.mock('../../src/lib/diagram', async (real) => ({ ...(await real<object>()), renderDiagram: () => new Promise(() => {}) }));

// React Flow measures its nodes before it draws edges; happy-dom lays nothing out, so a node
// measures as its style says and a mouse counts as a fine pointer (React Flow's testing guide).
const sizes = ['offsetWidth', 'offsetHeight'] as const;
const real = sizes.map((k) => Object.getOwnPropertyDescriptor(HTMLElement.prototype, k));
beforeAll(() => {
  for (const k of sizes) {
    Object.defineProperty(HTMLElement.prototype, k, {
      configurable: true,
      get(this: HTMLElement) {
        return Number.parseFloat(k === 'offsetWidth' ? this.style.width : this.style.height) || 1;
      },
    });
  }
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(private readonly cb: ResizeObserverCallback) {}
      // Later, as a real one reports: React Flow registers its pane after its nodes mount.
      observe(target: Element) {
        setTimeout(() => this.cb([{ target } as ResizeObserverEntry], this as unknown as ResizeObserver));
      }
      unobserve() {}
      disconnect() {}
    },
  );
  vi.stubGlobal(
    'DOMMatrixReadOnly',
    class {
      m22: number;
      constructor(transform?: string) {
        this.m22 = Number(/scale\(([\d.]+)\)/.exec(transform ?? '')?.[1] ?? 1);
      }
    },
  );
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: query === '(pointer: fine)', media: query, addEventListener() {}, removeEventListener() {} }));
});
afterAll(() => {
  sizes.forEach((k, i) => real[i] && Object.defineProperty(HTMLElement.prototype, k, real[i]));
  vi.unstubAllGlobals();
});

const card = (source: string, api: Partial<ApiClient> = {}, sessionId?: string) =>
  render(
    <ApiContext.Provider value={api as ApiClient}>
      <SessionContext.Provider value={sessionId}>
        <DiagramCard source={source} ready>
          <pre>{source}</pre>
        </DiagramCard>
      </SessionContext.Provider>
    </ApiContext.Provider>,
  );

const pressed = () => screen.getAllByRole('button', { pressed: true }).map((b) => b.textContent);

describe('React Flow view', () => {
  test('a flowchart opens in React Flow, laid out with its kinds, groups and labels', async () => {
    card('flowchart LR\n  a([Start]) -->|go| b{Ok?}\n  subgraph s[Store]\n    c[(Orders)]\n  end\n  b -.-> c\n  b <==> a\n  u["User<br>id PK<br>name"]');
    expect(pressed()).toEqual(['React Flow']);
    const figure = await screen.findByRole('figure', { name: 'Diagram' });
    await waitFor(() => expect(within(figure).getByText('Orders')).toBeTruthy());
    expect(figure.querySelector('.flow-terminal')?.textContent).toBe('Start');
    expect(figure.querySelector('.flow-decision')?.textContent).toBe('Ok?');
    expect(figure.querySelector('.flow-store .flow-icon')).toBeTruthy();
    expect(figure.querySelector('.flow-group-title')?.textContent).toBe('Store');
    // Pele's line breaks only wrap a label.
    expect(figure.querySelector('.flow-step .flow-detail')?.textContent).toBe('id PK\nname');
    expect(figure.classList.contains('flow-details')).toBe(false);
    // Edges follow ELK's route, dashed and thick as the source says, with arrow heads and labels.
    await waitFor(() => expect(figure.querySelectorAll('.flow-edge')).toHaveLength(3));
    expect(figure.querySelector('.flow-edge.is-dashed')).toBeTruthy();
    expect(figure.querySelector('.flow-edge.is-thick')?.querySelectorAll('.flow-edge-head')).toHaveLength(2);
    expect(within(figure).getByText('go')).toBeTruthy();

    // Selecting a node lights its edges and dims the rest.
    const start = figure.querySelector<HTMLElement>('.react-flow__node:has(.flow-terminal)')!;
    fireEvent.click(start);
    await waitFor(() => expect(figure.querySelectorAll('.flow-edge.is-hot')).toHaveLength(2));
    expect(figure.querySelectorAll('.flow-edge.is-dim')).toHaveLength(1);

    // A dragged node frees its edges from ELK's route until Reset layout.
    const at = { clientX: 10, clientY: 10, button: 0, view: window };
    fireEvent.mouseDown(start, at);
    fireEvent.mouseMove(window, { ...at, clientX: 60, clientY: 40 });
    fireEvent.mouseUp(window, { ...at, clientX: 60, clientY: 40 });
    fireEvent.click(await within(figure).findByRole('button', { name: 'Reset layout' }));
    await waitFor(() => expect(within(figure).queryByRole('button', { name: 'Reset layout' })).toBeNull());
  });

  test('a chart too wide to read top to bottom is laid out left to right; a small one is laid out once', async () => {
    vi.mocked(layoutFlow).mockClear();
    // A mindmap's shape: a root, 5 branches and 15 leaves, all leaves in one row top to bottom.
    const branches = [1, 2, 3, 4, 5].map((b) => `  r --> b${b}\n` + [1, 2, 3].map((l) => `  b${b} --> l${b}${l}\n`).join('')).join('');
    card(`flowchart TB\n${branches}`);
    let figure = await screen.findByRole('figure', { name: 'Diagram' });
    await waitFor(() => expect(figure.querySelector('.react-flow__node')).toBeTruthy());
    expect(vi.mocked(layoutFlow).mock.calls.map(([, dir]) => dir)).toEqual(['TB', 'LR']);
    expect(figure.querySelector('.react-flow__handle-left')).toBeTruthy();
    expect(figure.querySelector('.react-flow__handle-top')).toBeNull();
    // The card grows toward its tallest for the taller layout.
    expect(Number.parseFloat(figure.style.height)).toBe(560);
    cleanup();

    vi.mocked(layoutFlow).mockClear();
    card('flowchart TB\n  a --> b\n  a --> c');
    figure = await screen.findByRole('figure', { name: 'Diagram' });
    await waitFor(() => expect(figure.querySelector('.react-flow__node')).toBeTruthy());
    expect(vi.mocked(layoutFlow).mock.calls.map(([, dir]) => dir)).toEqual(['TB']);
    expect(figure.querySelector('.react-flow__handle-top')).toBeTruthy();
  });

  test('another diagram opens in Mermaid, with no React Flow tab outside a Thread', () => {
    card('sequenceDiagram\n  A->>B: hi');
    card('stateDiagram-v2\n  [*] --> Idle');
    expect(screen.getAllByRole('group', { name: 'View' }).map((g) => g.textContent)).toEqual(['MermaidCode', 'MermaidCode']);
  });

  test('in a Thread, another box-and-arrow diagram opens in React Flow: the Utility model draws it, once per source', async () => {
    const graph: FlowGraph = {
      direction: 'TB',
      groups: [],
      nodes: [
        { id: 'idle', label: 'Idle\nwaits for a prompt', kind: 'step' },
        { id: 'busy', label: 'Busy', kind: 'step' },
      ],
      edges: [{ from: 'idle', to: 'busy', label: 'start', dashed: false, thick: false, head: 'end' }],
    };
    const diagramGraph = vi.fn(async () => graph);
    const user = userEvent.setup();
    card('stateDiagram-v2\n  Idle --> Busy: start', { diagramGraph }, 'thread-1');
    expect(pressed()).toEqual(['React Flow']);
    const figure = await screen.findByRole('figure', { name: 'Diagram' });
    await waitFor(() => expect(within(figure).getByText('Busy')).toBeTruthy());
    // The model's further lines are details under the name.
    expect(figure.classList.contains('flow-details')).toBe(true);
    expect(figure.querySelector('.flow-detail')?.textContent).toBe('waits for a prompt');
    expect(diagramGraph).toHaveBeenCalledWith('thread-1', 'stateDiagram-v2\n  Idle --> Busy: start');
    await user.click(screen.getByRole('button', { name: 'Code' }));
    await user.click(screen.getByRole('button', { name: 'React Flow' }));
    await waitFor(() => expect(within(screen.getByRole('figure', { name: 'Diagram' })).getByText('Busy')).toBeTruthy());
    expect(diagramGraph).toHaveBeenCalledTimes(1);
  });

  test('a failed conversion goes back to Mermaid and says why', async () => {
    const diagramGraph = vi.fn(async () => {
      throw new Error('the model could not turn this diagram into a graph');
    });
    const user = userEvent.setup();
    card('classDiagram\n  Animal <|-- Duck', { diagramGraph }, 'thread-2');
    expect(await screen.findByText('React Flow view unavailable: the model could not turn this diagram into a graph')).toBeTruthy();
    expect(pressed()).toEqual(['Mermaid']);
    // React Flow stays a click away, and asks again.
    await user.click(screen.getByRole('button', { name: 'React Flow' }));
    await waitFor(() => expect(diagramGraph).toHaveBeenCalledTimes(2));
  });
});
