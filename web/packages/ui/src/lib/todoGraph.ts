// The todo dependencies as a Mermaid flowchart, the other way to draw them beside the rails
// (`todoLinks`). Which one a reader shows is a setting kept per browser (`uam.todoGraph`). The
// flowchart renders in the sandboxed diagram frame like any Mermaid fence (lib/diagram.ts).

import type { Todo, TodoStatus } from '../api';
import { chartToken } from './chart.ts';
import type { TodoLink } from './todos.ts';

export type TodoGraphStyle = 'rails' | 'graph';

export const TODO_GRAPH_KEY = 'uam.todoGraph';

/** A stored value as a setting: "graph" as chosen; nothing stored, or garbage, is the rails. */
export const parseTodoGraph = (raw: string | null): TodoGraphStyle => (raw === 'graph' ? 'graph' : 'rails');

export function loadTodoGraph(): TodoGraphStyle {
  try {
    return parseTodoGraph(localStorage.getItem(TODO_GRAPH_KEY));
  } catch {
    return 'rails';
  }
}

/** Stores the choice; storage that refuses keeps the rails after a reload. */
export function saveTodoGraph(style: TodoGraphStyle): void {
  try {
    localStorage.setItem(TODO_GRAPH_KEY, style);
  } catch {
    // Private mode or a full quota.
  }
}

/** Each stage's glyph, so no node's state is colour alone, as on the readers' rows. */
const GLYPH: Record<TodoStatus, string> = { done: '✓', in_progress: '◉', blocked: '⊘', pending: '○' };

/**
 * A title as a quoted Mermaid label: Mermaid's entity codes for the characters its syntax reads
 * (`#` first, as the codes start with it), one line. The agents write the titles; the frame
 * renders them as text in a sandbox either way, so this only keeps the source parsing.
 */
function label(text: string, max = Infinity): string {
  const chars = [...text.replace(/\s+/g, ' ').trim()];
  const cut = chars.length > max ? chars.slice(0, max).join('') + '…' : chars.join('');
  return cut.replaceAll('#', '#35;').replaceAll('"', '#quot;').replaceAll('<', '#lt;').replaceAll('>', '#gt;');
}

/** Where a row sits in the graph: the turn that added it (a tag on the row), and the subagent that wrote it (its box). */
export interface TodoGroup { turn?: number; agent?: string }

/**
 * The flowchart for `todos` and their `links`, top to bottom: a box per row (its stage's glyph,
 * "T3" for the turn that added it when `group` knows it, its title, "↻ 2" when reopened) in its
 * stage's colours, the row the work is at outlined heavier; an arrow from each row to each row
 * that waits on it; a loop's links dashed in the accent, a blocked row's wait in the warning tone.
 * With `group`, each subagent's rows sit in a box of their own named for it, the main agent's
 * outside them. Turns are tags rather than boxes: Mermaid's layout cannot stack boxes that links
 * cross. Node ids are the rows' places, never their agent-written ids.
 */
export function todoMermaid(todos: readonly Todo[], links: readonly TodoLink[], now?: string, group?: (todo: Todo) => TodoGroup): string {
  const colour = (name: string, fallback: string) => chartToken(name, fallback);
  // Narrow labels and tight spacing: the reader is 440px wide, and the image shrinks to fit it.
  const lines = ['%%{init: {"flowchart": {"wrappingWidth": 120, "nodeSpacing": 16, "rankSpacing": 28, "padding": 6}}}%%', 'flowchart TB'];
  const groups = todos.map((t) => group?.(t) ?? {});
  const node = (i: number, indent: string) => {
    const t = todos[i], turn = groups[i].turn;
    const kind = t.id === now && t.status === 'in_progress' ? 'now' : t.status;
    const tag = turn ? 'T' + turn + ' · ' : '';
    const reopened = t.reopened ? ' ↻' + t.reopened : '';
    return `${indent}n${i}["${GLYPH[t.status]} ${tag}${label(t.title)}${reopened}"]:::${kind}`;
  };
  todos.forEach((_, i) => !groups[i].agent && lines.push(node(i, '  ')));
  const agents = [...new Set(groups.flatMap((g) => (g.agent ? [g.agent] : [])))];
  agents.forEach((agent, a) => {
    lines.push(`  subgraph g${a}["${label(agent, 22)}"]`, '    direction TB');
    todos.forEach((_, i) => groups[i].agent === agent && lines.push(node(i, '    ')));
    lines.push('  end');
  });
  const styles: string[] = [];
  links.forEach((l, i) => {
    lines.push(l.kind === 'loop' ? `  n${l.from} -.-> n${l.to}` : `  n${l.from} --> n${l.to}`);
    if (l.kind === 'loop') styles.push(`  linkStyle ${i} stroke:${colour('accent', '#2f5fd0')},stroke-width:1.5px`);
    else if (l.kind === 'wait') styles.push(`  linkStyle ${i} stroke:${colour('warning', '#8a5a00')},stroke-width:1.5px`);
  });
  const raised = colour('raised', '#ffffff');
  lines.push(
    ...styles,
    `  classDef done fill:${colour('success-wash', '#e3f3e8')},stroke:${colour('success', '#1d7a3a')},color:${colour('body', '#3a3a3a')}`,
    `  classDef in_progress fill:${colour('accent-wash', '#e6edfb')},stroke:${colour('accent', '#2f5fd0')},color:${colour('ink', '#1a1a1a')}`,
    `  classDef now fill:${colour('accent-wash', '#e6edfb')},stroke:${colour('accent', '#2f5fd0')},stroke-width:2.5px,color:${colour('ink', '#1a1a1a')}`,
    `  classDef blocked fill:${colour('warning-wash', '#fbf0d9')},stroke:${colour('warning', '#8a5a00')},color:${colour('ink', '#1a1a1a')}`,
    `  classDef pending fill:${raised},stroke:${colour('hairline-strong', '#c4c4c4')},color:${colour('body', '#3a3a3a')}`,
  );
  return lines.join('\n');
}
